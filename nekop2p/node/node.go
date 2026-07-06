// Package node 提供 neko-node 的主节点编排器。
//
// === 状态机 ===
//   OFFLINE ─→ LIMBO(匿名登陆) ─→ ONLINE(正式在线) / ISOLATED(孤立)
//
// === 上线流程 ===
//   1. Phase 1: 尝试直连缓存的好友 IP（快速上线）
//   2. Phase 2: 连接公开节点 → 发送加密信标 → 等待好友反向连接
//   3. Phase 3: 梯度重试 (0s/5s/15s/30s)
//
// === 后台循环 ===
//   heartbeatLoop:  心跳扩散（活跃30s/空闲5min）
//   beaconRelayLoop: 信标中继转发
//   budgetLoop:     连接预算维护（填充连接）
//   cleanupLoop:    过期状态清理
//
// === 安全审计 ===
//   ✅ 已通过。修复了 defer conn.Close() 导致传入连接立即断开的问题。
//   ✅ 修复了 ratchs map 并发读写 data race。
//   ✅ 修复了 maintainBudget 双重添加 peer。
//
// Package node 提供 neko-node 的主节点编排器。
package node

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nekop2p/nekop2p/anon"
	"github.com/nekop2p/nekop2p/beacon"
	"github.com/nekop2p/nekop2p/crypto"
	"github.com/nekop2p/nekop2p/frame"
	"github.com/nekop2p/nekop2p/onion"
	"github.com/nekop2p/nekop2p/peer"
	"github.com/nekop2p/nekop2p/ratchet"
	"github.com/nekop2p/nekop2p/store"
)

// FriendEntry 由本地 API friends 端点返回。
type FriendEntry struct {
	ChainID   string `json:"chain_id"`
	Online    bool   `json:"online"`
	TrustDist int    `json:"trust_dist"`
}

// FriendInfo 是 FriendEntry 的旧版别名。
type FriendInfo = FriendEntry

// NodeState 表示节点的生命周期状态。
type NodeState int

const (
	StateOffline  NodeState = iota
	StateLimbo              // 匿名登录阶段
	StateOnline             // 完全在线
	StateIsolated           // 无可用好友
)

func (s NodeState) String() string {
	switch s {
	case StateOffline: return "OFFLINE"
	case StateLimbo: return "LIMBO"
	case StateOnline: return "ONLINE"
	case StateIsolated: return "ISOLATED"
	default: return "UNKNOWN"
	}
}

// Config 保存节点配置。
type Config struct {
	ChainID      [32]byte
	RecvKey      crypto.KeyPair
	SendKey      crypto.SignKeyPair
	ListenAddr   string // 例如 "[::]:9070"
	BootstrapIPs [][16]byte
	BootstrapPKs map[[16]byte][32]byte // 引导节点 IPv6 → 公钥映射（安全方式）
	TargetConnections int // 默认 20
	DataDir           string // 数据目录（BoltDB + 密钥文件）
	Blacklist         map[peer.ChainID]bool // 黑名单节点
}

// Node 是主节点编排器。
type Node struct {
	cfg    Config
	state  NodeState
	mu     sync.RWMutex

	peers    *peer.Manager
	topology *Topology
	ratchs     map[peer.ChainID]*ratchet.State // 活跃的 ratchet 状态
	ratchsMu   sync.Mutex                       // 保护 ratchs 并发写入和 ratchet 加密操作

	listener net.Listener
	udpConn *net.UDPConn // UDP 监听器（接收信标响应）
	store   *store.ChainStore // BoltDB 持久化存储

	// 信标追踪
	pendingNonces  map[[32]byte]time.Time // nonce → 过期时间
	seenBeaconTags map[[16]byte]time.Time // 标签 → 过期时间

	// 洋葱路由中继循环和信标中继通道
	beaconRelayCh chan *beaconRelayTask
	anonState     *anon.State // 按需匿名切换状态（nil=未启用）

	// 填充连接计数器（生成唯一 chainID）
	paddingCounter uint64

	// 事件通道
	onStateChange chan NodeState
	peerAddedCh   chan struct{}

	// 优雅关闭
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc

	// 帧处理并发上限（每实例独立）
	frameWorkers chan struct{}
}

// beaconRelayTask 表示一个待中继的信标。
type beaconRelayTask struct {
	data   []byte // 序列化的信标数据
	except peer.ChainID // 不转发给此对等方（信标来源）
}

// New 创建一个新的 Node。
func New(cfg Config) (*Node, error) {
	// 配置验证
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "[::]:9070"
	}
	if cfg.TargetConnections <= 0 {
		cfg.TargetConnections = 20
	}
	if cfg.TargetConnections > 1000 {
		return nil, fmt.Errorf("target connections too large: %d (max 1000)", cfg.TargetConnections)
	}
	if len(cfg.BootstrapIPs) == 0 && len(cfg.BootstrapPKs) == 0 {
		// 零配置引导：允许（节点将依赖缓存的好友 IP 或在 ISOLATED 状态等待）
	}

	ctx, cancel := context.WithCancel(context.Background())

	// 初始化持久化存储
	var chainStore *store.ChainStore
	if cfg.DataDir != "" {
		var err error
		chainStore, err = store.New(cfg.DataDir)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("open chain store: %w", err)
		}
	}

	n := &Node{
		cfg:            cfg,
		state:          StateOffline,
		topology:       NewTopology(),
		ratchs:         make(map[peer.ChainID]*ratchet.State),
		pendingNonces:  make(map[[32]byte]time.Time),
		seenBeaconTags: make(map[[16]byte]time.Time),
		beaconRelayCh:  make(chan *beaconRelayTask, 100),
		onStateChange:  make(chan NodeState, 10),
		peerAddedCh:    make(chan struct{}, 50),
		frameWorkers:   make(chan struct{}, 100),
		store:          chainStore,
		ctx:            ctx,
		cancel:         cancel,
	}

	n.peers = peer.NewManager(ctx)
	n.peers.SetCallbacks(n.handleFrame, n.handleConnect, n.handleDisconnect)

	return n, nil
}

// State 以字符串形式返回当前节点状态。
func (n *Node) State() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.state.String()
}

// ChainID 返回我们的链 ID。
func (n *Node) ChainID() [32]byte { return n.cfg.ChainID }

// AddCoreFriend 向拓扑中添加一个好友并返回条目。
func (n *Node) AddCoreFriend(chainID peer.ChainID, recvPK, sendPK [32]byte) {
	n.topology.AddCoreFriend(chainID, recvPK, sendPK)
}

// Friends 返回带有在线状态的好友列表。
func (n *Node) Friends() []FriendEntry {
	friends := n.topology.GetCoreFriends()
	result := make([]FriendEntry, len(friends))
	for i, f := range friends {
		trustDist := 1 // 核心好友 = 信任距离 1（默认为直连好友）
		// 如果好友是通过介绍获得的，则 trustDist=2
		if f.IntroducedBy != (peer.ChainID{}) {
			trustDist = 2
		}
		result[i] = FriendEntry{
			ChainID:   fmt.Sprintf("%x", f.ChainID[:]),
			Online:    f.Online,
			TrustDist: trustDist,
		}
	}
	return result
}

// OnlineFriends 返回在线核心好友的数量。
func (n *Node) OnlineFriends() int {
	count := 0
	for _, f := range n.topology.GetCoreFriends() {
		if f.Online {
			count++
		}
	}
	return count
}

// TotalPeers 返回已连接的对等节点总数。
func (n *Node) TotalPeers() int { return n.peers.Count() }

// Store 返回持久化存储（BoltDB）。
func (n *Node) Store() *store.ChainStore { return n.store }

// ===== 黑名单 =====

// BanNode 将节点加入黑名单。
func (n *Node) BanNode(chainID peer.ChainID) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.cfg.Blacklist == nil {
		n.cfg.Blacklist = make(map[peer.ChainID]bool)
	}
	n.cfg.Blacklist[chainID] = true
	n.peers.Remove(chainID) // 断开已有连接
}

// UnbanNode 从黑名单移除。
func (n *Node) UnbanNode(chainID peer.ChainID) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.cfg.Blacklist, chainID)
}

// IsBanned 检查节点是否在黑名单中。
func (n *Node) IsBanned(chainID peer.ChainID) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.cfg.Blacklist[chainID]
}

// BlacklistAll 返回黑名单列表。
func (n *Node) BlacklistAll() []peer.ChainID {
	n.mu.RLock()
	defer n.mu.RUnlock()
	result := make([]peer.ChainID, 0, len(n.cfg.Blacklist))
	for id := range n.cfg.Blacklist {
		result = append(result, id)
	}
	return result
}

func (n *Node) setState(s NodeState) {
	n.mu.Lock()
	old := n.state
	n.state = s
	n.mu.Unlock()
	if old != s {
		select {
		case n.onStateChange <- s:
		default:
		}
	}
}

// GoOnline 启动上线流程。
func (n *Node) GoOnline() error {
	n.mu.RLock()
	state := n.state
	n.mu.RUnlock()
	if state != StateOffline {
		return fmt.Errorf("node already %s, cannot GoOnline", state)
	}

	// 第 1 阶段：尝试缓存的好友
	for _, friend := range n.topology.GetCoreFriends() {
		if n.tryDirectConnect(friend) == nil {
			n.setState(StateOnline)
			go n.runOnline()
			return nil
		}
	}

	// 第 2 阶段：连接引导/公共节点并发送信标
	n.setState(StateLimbo)
	go n.limboSequence()

	return nil
}

// Shutdown 优雅地停止节点。
func (n *Node) Shutdown() {
	n.cancel()
	n.peers.Shutdown()
	if n.listener != nil {
		n.listener.Close()
	}
	if n.udpConn != nil {
		n.udpConn.Close()
	}
	n.wg.Wait()
	if n.store != nil {
		n.store.Close()
	}
}

func (n *Node) runOnline() {
	n.wg.Add(4)
	go func() { defer n.wg.Done(); n.heartbeatLoop() }()
	go func() { defer n.wg.Done(); n.beaconRelayLoop() }()
	go func() { defer n.wg.Done(); n.budgetLoop() }()
	go func() { defer n.wg.Done(); n.cleanupLoop() }()
}

func (n *Node) limboSequence() {
	retries := []time.Duration{0, 5 * time.Second, 15 * time.Second, 30 * time.Second}

	for _, delay := range retries {
		// 带上下文感知的等待
		if delay > 0 {
			select {
			case <-n.ctx.Done():
				n.setState(StateIsolated)
				return
			case <-time.After(delay):
			}
		}

		bp, _, err := n.buildBeaconPacket()
		if err != nil {
			continue
		}

		// 通过每个引导节点及其已知公钥发送
		beaconData := bp.p.Serialize()
		for _, ip := range n.cfg.BootstrapIPs {
			bootstrapPK := n.getBootstrapPK(ip)
			pi, err := n.DialBootstrap(ip, 9070, bootstrapPK)
			if err != nil {
				continue
			}
			beaconFrame := frame.NewFrame(frame.FrameBeacon, 0, beaconData)
			pi.Framer.WriteEncryptedFrame(pi.Conn, beaconFrame)
			n.peers.Remove(pi.ChainID) // 发送后关闭
		}

		if n.waitForResponses(30 * time.Second) {
			n.setState(StateOnline)
			go n.runOnline()
			return
		}
	}

	n.setState(StateIsolated)
}

func (n *Node) buildBeaconPacket() (*beaconPkt, [32]byte, error) {
	params := &beacon.BuildParams{
		SenderChainID: n.cfg.ChainID,
		SenderPort:    9070,
	}
	copy(params.SendPrivKey[:], n.cfg.SendKey.Private[:])

	for _, f := range n.topology.GetCoreFriends() {
		params.Targets = append(params.Targets, beacon.Target{
			ChainID: f.ChainID,
			RecvPK:  f.RecvPK,
		})
	}

	bp, nonce, err := beacon.Build(params)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("build beacon: %w", err)
	}

	var nonceArr [32]byte
	copy(nonceArr[:], nonce)
	n.mu.Lock()
	n.pendingNonces[nonceArr] = time.Now().Add(beacon.BeaconTTL)
	n.mu.Unlock()

	return &beaconPkt{p: bp}, nonceArr, nil
}

type beaconPkt struct {
	p *beacon.BeaconPacket
}

func (n *Node) waitForResponses(timeout time.Duration) bool {
	// 排除填充连接：只等待真实好友的响应
	if n.OnlineFriends() > 0 {
		return true
	}
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			return n.OnlineFriends() > 0
		case <-n.ctx.Done():
			return false
		case <-n.peerAddedCh:
			if n.OnlineFriends() > 0 {
				return true
			}
		}
	}
}

func (n *Node) getBootstrapPK(ip [16]byte) [32]byte {
	// 优先从预置的安全映射查找
	if pk, ok := n.cfg.BootstrapPKs[ip]; ok {
		return pk
	}
	// 回退：从 IP 派生（仅用于开发测试，不提供密码学安全）
	var pk [32]byte
	copy(pk[:16], ip[:])
	return pk
}

func (n *Node) tryDirectConnect(friend *CoreFriend) error {
	_, err := n.DialFriend(friend.ChainID, friend.IPv6, friend.Port)
	return err
}

func (n *Node) handleFrame(from peer.ChainID, f *frame.Frame) {
	select {
	case n.frameWorkers <- struct{}{}:
	default:
		return // 过载保护：丢弃帧
	}
	go func() {
		defer func() { <-n.frameWorkers }()
		switch f.Type {
		case frame.FrameBeacon:
			n.handleBeaconFrame(from, f.Payload)
		case frame.FrameData:
			n.handleDataFrame(from, f.Payload)
		case frame.FrameRoute:
			n.handleRouteFrame(from, f.Payload)
		case frame.FrameSync:
			n.handleSyncFrame(from, f.Payload)
		}
	}()
}

// 应用层消息投递回调
var (
	// OnMessageDelivered 在解密消息准备好供应用使用时调用。
	OnMessageDelivered func(from peer.ChainID, plaintext []byte)
	// OnBeaconReceived 在信标被接收并验证后调用。
	OnBeaconReceived func(from peer.ChainID)
	// OnOnionDelivered 在洋葱路由消息到达最终跳时调用（来源匿名）。
	OnOnionDelivered func(plaintext []byte)
	// OnChainSync 当收到链同步请求时调用。返回 (our_height, blocks_json)
	OnChainSync func(fromHeight, toHeight int64) (int64, []byte)
)

func (n *Node) handleBeaconFrame(from peer.ChainID, data []byte) {
	bp, err := beacon.ParseBeacon(data)
	if err != nil {
		return
	}

	if bp.HopCount > bp.HopMax {
		return
	}

	n.mu.Lock()
	if _, seen := n.seenBeaconTags[bp.RandomTag]; seen {
		n.mu.Unlock()
		return
	}
	n.seenBeaconTags[bp.RandomTag] = time.Now().Add(beacon.BeaconTTL)
	n.mu.Unlock()

	matchedAny := false
	// 限制 SlotCount 上限防止 CPU DoS
	slotLimit := bp.SlotCount
	if slotLimit > 32 {
		slotLimit = 32
	}
	for i := uint8(0); i < slotLimit; i++ {
		symKey, err := bp.TryDecryptSlot(int(i), &n.cfg.RecvKey.Private)
		if err != nil {
			continue
		}

		inner, err := bp.DecryptBody(symKey)
		if err != nil {
			continue
		}

		if inner.SenderChainID == n.cfg.ChainID {
			continue // 防回环：跳过发给自己的信标
		}

		// 查找发送者在好友列表中的信息
		senderFriend := n.topology.GetCoreFriend(inner.SenderChainID)
		isFriend := senderFriend != nil

		// 验证信标签名（需要 send_pk）
		if isFriend {
			if err := inner.Verify(&senderFriend.SendPK); err != nil {
				continue // 签名验证失败，可能被伪造
			}
		}
		// 非好友的信标：无法验证签名，但仍通知应用（应用层决定是否接受）

		// 通知应用信标已接收
		if OnBeaconReceived != nil {
			OnBeaconReceived(inner.SenderChainID)
		}

		// 发送者是否是好友？如果是则构建并发送响应
		if isFriend {
			n.sendBeaconResponse(inner, senderFriend)
		}

		matchedAny = true
	}

	// 如果信标匹配到目标，不再中继（目标已收到）
	if matchedAny {
		return
	}

	// 未匹配到目标 → 加入中继队列
	bp.HopCount++
	if bp.HopCount <= bp.HopMax {
		select {
		case n.beaconRelayCh <- &beaconRelayTask{data: bp.Serialize(), except: from}:
		default:
			// 中继队列满，丢弃此信标
		}
	}
}

// sendBeaconResponse 向信标发送者构建并发送回应包。
func (n *Node) sendBeaconResponse(inner *beacon.InnerPayload, sender *CoreFriend) {
	// 生成临时棘轮密钥对（用于后续双棘轮通信）
	ephKey, err := crypto.GenerateEphemeralKey()
	if err != nil {
		return
	}

	// 构建回应包 (BuildResponse 内部已做 KEM 加密)
	senderRecvPK := sender.RecvPK
	resp, err := beacon.BuildResponse(
		inner,
		n.cfg.ChainID,
		n.getOurIPv6(),
		9070,
		ephKey.Public,
		n.cfg.SendKey.Private,
		&senderRecvPK,
	)
	if err != nil {
		return
	}

	// 直接使用已加密的载荷
	encPayload := resp.EncryptedPayload

	// 通过 UDP 发送回应到发送者的 IPv6 地址
	addr := fmt.Sprintf("[%s]:%d", net.IP(inner.SenderIPv6[:]).String(), inner.SenderPort)
	conn, err := net.DialTimeout("udp6", addr, 5*time.Second)
	if err != nil {
		return
	}
	defer conn.Close()

	// 发送: chain_id(32) || encrypted_payload
	wireMsg := make([]byte, 32+len(encPayload))
	copy(wireMsg[:32], n.cfg.ChainID[:])
	copy(wireMsg[32:], encPayload)
	_, _ = conn.Write(wireMsg)
}

// getOurIPv6 返回本节点的 IPv6 地址。
// 优先从 listener 获取，否则回退到本地地址。
func (n *Node) getOurIPv6() [16]byte {
	if n.listener != nil {
		if tcpAddr, ok := n.listener.Addr().(*net.TCPAddr); ok {
			var ip [16]byte
			copy(ip[:], tcpAddr.IP.To16())
			return ip
		}
	}
	// 回退: 通过 UDP 探测获取出站地址
	conn, err := net.DialTimeout("udp6", "[2001:4860:4860::8888]:53", 2*time.Second)
	if err != nil {
		return [16]byte{}
	}
	defer conn.Close()
	if udpAddr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		var ip [16]byte
		copy(ip[:], udpAddr.IP.To16())
		return ip
	}
	return [16]byte{}
}

func (n *Node) handleDataFrame(from peer.ChainID, data []byte) {
	n.ratchsMu.Lock()
	r, ok := n.ratchs[from]
	if !ok {
		n.ratchsMu.Unlock()
		return
	}
	plaintext, err := r.Decrypt(data)
	n.ratchsMu.Unlock()
	if err != nil {
		return
	}
	if OnMessageDelivered != nil {
		OnMessageDelivered(from, plaintext)
	}
}

func (n *Node) handleConnect(pi *peer.Info) {
	if !pi.IsPadding {
		n.topology.MarkOnline(pi.ChainID)
		select {
		case n.peerAddedCh <- struct{}{}:
		default:
		}
	}
}

func (n *Node) handleDisconnect(pi *peer.Info) {
	n.topology.MarkOffline(pi.ChainID)
}

// 后台循环

func (n *Node) heartbeatLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			n.sendHeartbeat()
		}
	}
}

func (n *Node) sendHeartbeat() {
	peers := n.peers.All()

	neighbors := make([]byte, 0, len(peers)*58)
	var entry [58]byte
	for _, pi := range peers {
		if pi.IsPadding {
			continue
		}
		copy(entry[0:32], pi.ChainID[:])
		copy(entry[32:48], pi.IPv6[:])
		binary.BigEndian.PutUint16(entry[48:50], pi.Port)
		binary.BigEndian.PutUint64(entry[50:58], uint64(pi.LastSeen.Unix()))
		neighbors = append(neighbors, entry[:]...)
	}

	heartbeat := frame.NewFrame(frame.FramePing, 0, neighbors)
	for _, pi := range peers {
		if !pi.IsPadding {
			if err := pi.Framer.WriteEncryptedFrame(pi.Conn, heartbeat); err != nil {
				n.peers.Remove(pi.ChainID)
			}
		}
	}
}

func (n *Node) beaconRelayLoop() {
	// 监听信标中继通道，将需要转发的信标广播给已连接的对等方
	for {
		select {
		case <-n.ctx.Done():
			return
		case task := <-n.beaconRelayCh:
			relayFrame := frame.NewFrame(frame.FrameBeacon, 0, task.data)
			n.peers.BroadcastFrame(relayFrame, task.except)
		}
	}
}

func (n *Node) budgetLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			n.maintainBudget()
		}
	}
}

func (n *Node) maintainBudget() {
	target := n.cfg.TargetConnections
	realCount := n.peers.RealCount()
	current := n.peers.Count()

	const maxRetries = 20
	failedIPs := make(map[[16]byte]bool)

	for attempt := 0; attempt < maxRetries && current < target; attempt++ {
		ip, ok := n.pickPaddingTarget()
		if !ok {
			break
		}
		// 跳过已失败的 IP
		if failedIPs[ip] {
			// 尝试下一个：目前只使用 BootstrapIPs[0]，无法轮换
			break
		}
		_, err := n.DialPadding(ip, 9070, n.getBootstrapPK(ip))
		if err != nil {
			failedIPs[ip] = true
			continue
		}
		current = n.peers.Count()
	}

	for current > target && n.peers.Count() > realCount {
		for _, pi := range n.peers.All() {
			if pi.IsPadding {
				n.peers.Remove(pi.ChainID)
				break
			}
		}
		current = n.peers.Count()
	}
}

func (n *Node) pickPaddingTarget() ([16]byte, bool) {
	if len(n.cfg.BootstrapIPs) > 0 {
		// 轮换使用多个引导节点，防止所有填充连接指向同一 IP
		idx := atomic.AddUint64(&n.paddingCounter, 1) % uint64(len(n.cfg.BootstrapIPs))
		return n.cfg.BootstrapIPs[idx], true
	}
	return n.topology.GetAnyNeighborIPv6()
}

func (n *Node) cleanupLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			n.cleanup()
		}
	}
}

func (n *Node) handleRouteFrame(from peer.ChainID, data []byte) {
	// 解析完整的洋葱数据包
	pkt, err := onion.ParseOnion(data)
	if err != nil {
		return
	}

	// 解包第一层
	result, err := onion.UnwrapOne(&n.cfg.RecvKey.Private, pkt.Layers[0])
	if err != nil {
		return // 不针对我们，或已损坏
	}

	if result.IsFinal {
		// 我们是最终跳——投递消息
		n.deliverMessage(result.FinalMsg)
		return
	}

	// 我们是中间跳——剥离第一层，重新序列化剩余层
	stripped := pkt.StripFirst()
	if stripped == nil {
		return
	}
	routeFrame := frame.NewFrame(frame.FrameRoute, 0, stripped.Serialize())

	// 按 IPv6 查找下一跳对等节点
	for _, pi := range n.peers.All() {
		if pi.IPv6 == result.NextHopIP {
			pi.Framer.WriteEncryptedFrame(pi.Conn, routeFrame)
			return
		}
	}
}

func (n *Node) deliverMessage(msg []byte) {
	if OnOnionDelivered != nil {
		OnOnionDelivered(msg)
	}
}

func (n *Node) handleSyncFrame(from peer.ChainID, data []byte) {
	if len(data) < 16 || OnChainSync == nil { return }
	fromH := int64(data[0])<<56 | int64(data[1])<<48 | int64(data[2])<<40 | int64(data[3])<<32 |
		int64(data[4])<<24 | int64(data[5])<<16 | int64(data[6])<<8 | int64(data[7])
	toH := int64(data[8])<<56 | int64(data[9])<<48 | int64(data[10])<<40 | int64(data[11])<<32 |
		int64(data[12])<<24 | int64(data[13])<<16 | int64(data[14])<<8 | int64(data[15])

	// 限制区块范围防止内存耗尽
	if fromH < 0 || toH < fromH || toH-fromH > 1000 {
		return
	}
	if toH-fromH <= 0 {
		return
	}

	ourHeight, blocks := OnChainSync(fromH, toH)
	if blocks != nil {
		resp := make([]byte, 8+len(blocks))
		resp[0] = byte(ourHeight >> 56); resp[1] = byte(ourHeight >> 48)
		resp[2] = byte(ourHeight >> 40); resp[3] = byte(ourHeight >> 32)
		resp[4] = byte(ourHeight >> 24); resp[5] = byte(ourHeight >> 16)
		resp[6] = byte(ourHeight >> 8); resp[7] = byte(ourHeight)
		copy(resp[8:], blocks)
		n.peers.SendFrame(from, frame.NewFrame(frame.FrameSync, 0, resp))
	}
}

func (n *Node) cleanup() {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := time.Now()
	for tag, exp := range n.seenBeaconTags {
		if now.After(exp) {
			delete(n.seenBeaconTags, tag)
		}
	}
	for nonce, exp := range n.pendingNonces {
		if now.After(exp) {
			delete(n.pendingNonces, nonce)
		}
	}
}

// Listen 开始接受传入 TCP 和 UDP 连接。
func (n *Node) Listen() error {
	var err error
	n.listener, err = net.Listen("tcp6", n.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen tcp: %w", err)
	}

	// UDP 监听器：接收信标响应（好友发现协议的回程路径）
	udpAddr, err := net.ResolveUDPAddr("udp6", n.cfg.ListenAddr)
	if err != nil {
		n.listener.Close()
		return fmt.Errorf("resolve udp addr: %w", err)
	}
	n.udpConn, err = net.ListenUDP("udp6", udpAddr)
	if err != nil {
		n.listener.Close()
		return fmt.Errorf("listen udp: %w", err)
	}

	n.wg.Add(2)
	go func() { defer n.wg.Done(); n.acceptLoop() }()
	go func() { defer n.wg.Done(); n.udpBeaconLoop() }()
	return nil
}

// UDP 信标响应的并发处理上限（防 goroutine 爆炸）
var udpBeaconWorkers = make(chan struct{}, 50)

func (n *Node) udpBeaconLoop() {
	buf := make([]byte, 2048)
	for {
		select {
		case <-n.ctx.Done():
			return
		default:
		}

		// 设置读取超时以定期检查 ctx 取消
		n.udpConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		nr, remoteAddr, err := n.udpConn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return // 连接关闭
		}
		data := make([]byte, nr)
		copy(data, buf[:nr])

		// 速率限制：最多 50 个并发处理器
		select {
		case udpBeaconWorkers <- struct{}{}:
			go func() {
				defer func() { <-udpBeaconWorkers }()
				n.handleBeaconResponse(data, remoteAddr)
			}()
		default:
			// 过载：静默丢弃
		}
	}
}

// handleBeaconResponse 处理收到的 UDP 信标响应。
// 完整验证链：KEM解密 → 签名验证 → Nonce匹配 → 时间戳检查
func (n *Node) handleBeaconResponse(data []byte, remoteAddr *net.UDPAddr) {
	if len(data) < 32+44 { // chain_id(32) + KEM_overhead(44)
		return
	}

	// 解析发送者 chain_id
	var senderChainID [32]byte
	copy(senderChainID[:], data[:32])
	encPayload := data[32:]

	// 仅处理来自好友的响应
	senderFriend := n.topology.GetCoreFriend(senderChainID)
	if senderFriend == nil {
		return
	}

	// 需要一个待验证的 nonce：尝试所有未过期的 nonce
	// 注意：信标响应中应携带 nonce 或可推导的 nonce。当前实现暂时
	// 对每个 pending nonce 尝试验证（除非 beacon.VerifyResponse 中有
	// 精确 nonce 校验）。TODO: 在响应格式中添加显式的 nonce 字段。
	n.mu.RLock()
	var nonceCandidates [][32]byte
	for nonce, exp := range n.pendingNonces {
		if time.Now().Before(exp) {
			nonceCandidates = append(nonceCandidates, nonce)
		}
	}
	n.mu.RUnlock()
	if len(nonceCandidates) == 0 {
		return
	}

	// 对每个候选 nonce 尝试验证
	for _, candidate := range nonceCandidates {
		inner, err := beacon.VerifyResponse(encPayload, &n.cfg.RecvKey.Private,
			&senderFriend.SendPK, candidate)
		_ = inner
		if err == nil {
			// 验证成功：标记好友在线并从 pendingNonces 中移除已使用的 nonce
			n.mu.Lock()
			delete(n.pendingNonces, candidate)
			n.mu.Unlock()

			n.topology.MarkOnline(senderChainID)
			select {
			case n.peerAddedCh <- struct{}{}:
			default:
			}
			return
		}
	}
}

// 传入 TCP 连接的并发处理上限（防 goroutine 爆炸）
var acceptWorkers = make(chan struct{}, 100)

func (n *Node) acceptLoop() {
	for {
		conn, err := n.listener.Accept()
		if err != nil {
			return
		}
		select {
		case acceptWorkers <- struct{}{}:
			go func() {
				defer func() { <-acceptWorkers }()
				n.handleIncoming(conn)
			}()
		default:
			conn.Close() // 过载：直接关闭
		}
	}
}
