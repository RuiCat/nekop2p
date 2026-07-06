package node

import (
	"crypto/sha256"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/nekop2p/nekop2p/frame"
	"github.com/nekop2p/nekop2p/noise"
	"github.com/nekop2p/nekop2p/peer"
	"github.com/nekop2p/nekop2p/ratchet"
)

// DialFriend 使用 Noise_IK 连接到已知好友。
func (n *Node) DialFriend(chainID peer.ChainID, ipv6 [16]byte, port uint16) (*peer.Info, error) {
	addr := fmt.Sprintf("[%s]:%d", net.IP(ipv6[:]).String(), port)
	conn, err := net.DialTimeout("tcp6", addr, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial friend: %w", err)
	}

	friend := n.topology.GetCoreFriend(chainID)
	if friend == nil {
		conn.Close()
		return nil, fmt.Errorf("friend not in core table")
	}

	hs := noise.NewInitiatorIK(&n.cfg.RecvKey, &friend.RecvPK, noise.RoleFriend)
	msg1, err := hs.WriteMessage(nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("IK msg1: %w", err)
	}
	if _, err := conn.Write(msg1); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write IK msg1: %w", err)
	}

	msgBuf := make([]byte, 4096)
	nr, err := conn.Read(msgBuf)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read IK msg2: %w", err)
	}
	if _, err := hs.ReadMessage(msgBuf[:nr]); err != nil {
		conn.Close()
		return nil, fmt.Errorf("IK msg2: %w", err)
	}

	result := hs.Complete()
	return n.addPeerSession(conn, chainID, result, false), nil
}

// DialBootstrap 使用 Noise_NK 连接到公共节点。
func (n *Node) DialBootstrap(ipv6 [16]byte, port uint16, bootstrapPK [32]byte) (*peer.Info, error) {
	addr := fmt.Sprintf("[%s]:%d", net.IP(ipv6[:]).String(), port)
	conn, err := net.DialTimeout("tcp6", addr, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial bootstrap: %w", err)
	}

	hs := noise.NewInitiatorNK(&bootstrapPK, noise.RolePublic)
	msg1, err := hs.WriteMessage(nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("NK msg1: %w", err)
	}
	if _, err := conn.Write(msg1); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write NK msg1: %w", err)
	}

	msgBuf := make([]byte, 4096)
	nr, err := conn.Read(msgBuf)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read NK msg2: %w", err)
	}
	if _, err := hs.ReadMessage(msgBuf[:nr]); err != nil {
		conn.Close()
		return nil, fmt.Errorf("NK msg2: %w", err)
	}

	result := hs.Complete()
	// 引导节点连接：使用远程静态公钥的哈希作为唯一标识符
	return n.addPeerSession(conn, hashToChainID(result.RemoteStatic), result, false), nil
}

// DialPadding 连接到填充节点（使用唯一 chainID）。
func (n *Node) DialPadding(ipv6 [16]byte, port uint16, bootstrapPK [32]byte) (*peer.Info, error) {
	addr := fmt.Sprintf("[%s]:%d", net.IP(ipv6[:]).String(), port)
	conn, err := net.DialTimeout("tcp6", addr, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial padding: %w", err)
	}

	hs := noise.NewInitiatorNK(&bootstrapPK, noise.RolePadding)
	msg1, err := hs.WriteMessage(nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("NK msg1: %w", err)
	}
	if _, err := conn.Write(msg1); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write NK msg1: %w", err)
	}

	msgBuf := make([]byte, 4096)
	nr, err := conn.Read(msgBuf)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read NK msg2: %w", err)
	}
	if _, err := hs.ReadMessage(msgBuf[:nr]); err != nil {
		conn.Close()
		return nil, fmt.Errorf("NK msg2: %w", err)
	}

	result := hs.Complete()
	return n.addPeerSession(conn, n.nextPaddingID(), result, true), nil
}

func (n *Node) nextPaddingID() peer.ChainID {
	counter := atomic.AddUint64(&n.paddingCounter, 1)
	var id peer.ChainID
	id[0] = 0xFF // 标记为填充
	id[1] = byte(counter >> 56)
	id[2] = byte(counter >> 48)
	id[3] = byte(counter >> 40)
	id[4] = byte(counter >> 32)
	id[5] = byte(counter >> 24)
	id[6] = byte(counter >> 16)
	id[7] = byte(counter >> 8)
	id[8] = byte(counter)
	return id
}

// handleIncoming 处理已接受的 TCP 连接。
func (n *Node) handleIncoming(conn net.Conn) {
	// 读取第一个 Noise 消息以确定握手类型
	msgBuf := make([]byte, 4096)
	nr, err := conn.Read(msgBuf)
	if err != nil {
		conn.Close()
		return
	}
	data := msgBuf[:nr]

	if len(data) < 32 {
		conn.Close()
		return
	}

	// 先尝试 IK（好友），再尝试 NK（匿名/引导）
	// 注: IK/NK 使用不同 prologue → 不同初始密钥 → 解密互斥，不会误匹配。
	// try-first 模式是故意的设计：允许单端口同时服务好友和匿名连接。
	// 错误解密因 AEAD 认证标签失败 (GCM) 被安全拒绝，不会泄露密钥材料。
	if payload, result, err := n.tryIKResponder(conn, data); err == nil {
		n.handleIKPayload(conn, payload, result)
		return // 连接所有权转交给对等管理器
	}

	// 尝试 NK 响应方（公共节点模式）
	if payload, err := n.tryNKResponder(conn, data); err == nil {
		n.handleAnonymousPayload(conn, payload)
		return // 连接所有权转交给对等管理器
	}

	// 两种握手均未成功
	conn.Close()
}

func (n *Node) tryIKResponder(conn net.Conn, msg1 []byte) ([]byte, *noise.HandshakeResult, error) {
	hs := noise.NewResponderIK(&n.cfg.RecvKey, [32]byte{}, noise.RoleFriend)
	payload, err := hs.ReadMessage(msg1)
	if err != nil {
		return nil, nil, err
	}
	msg2, err := hs.WriteMessage(nil)
	if err != nil {
		return nil, nil, err
	}
	if _, err := conn.Write(msg2); err != nil {
		return nil, nil, err
	}
	result := hs.Complete()
	chainID := hashToChainID(result.RemoteStatic)
	n.addPeerSession(conn, chainID, result, false)
	return payload, result, nil
}

func (n *Node) tryNKResponder(conn net.Conn, msg1 []byte) ([]byte, error) {
	hs := noise.NewResponderNK(&n.cfg.RecvKey, noise.RolePublic)
	payload, err := hs.ReadMessage(msg1)
	if err != nil {
		return nil, err
	}
	msg2, err := hs.WriteMessage(nil)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(msg2); err != nil {
		return nil, err
	}
	result := hs.Complete()
	n.addPeerSession(conn, hashToChainID(result.RemoteStatic), result, true)
	return payload, nil
}

func (n *Node) addPeerSession(conn net.Conn, chainID peer.ChainID, result *noise.HandshakeResult, isPadding bool) *peer.Info {
	sendKey := result.SendCipher.Key
	recvKey := result.RecvCipher.Key
	framer := frame.NewSessionKeys(sendKey[:], recvKey[:])
	return n.peers.Add(chainID, conn, framer, isPadding)
}

func (n *Node) handleIKPayload(conn net.Conn, payload []byte, result *noise.HandshakeResult) {
	// IK 握手载荷包含发起方的身份信息
	// 典型格式: type(1) || chain_id(32) || [additional_data]
	if len(payload) < 33 {
		return
	}

	var chainID peer.ChainID
	copy(chainID[:], payload[1:33])

	// 身份验证: 载荷中的 chain_id 必须匹配经过 Noise IK 密码学认证的远程静态密钥
	authenticatedID := hashToChainID(result.RemoteStatic)
	if chainID != authenticatedID {
		// 载荷声称的 chain_id 与密码学认证的密钥不一致 — 拒绝连接
		n.peers.Remove(authenticatedID)
		conn.Close()
		return
	}

	// 标记好友为在线
	n.topology.MarkOnline(chainID)

	// 如果载荷包含更多数据，尝试解析为帧
	if len(payload) > 33 {
		f, err := frame.ParseRawFrame(payload[33:])
		if err == nil {
			n.handleFrame(chainID, f)
		}
	}
}

func (n *Node) handleAnonymousPayload(conn net.Conn, payload []byte) {
	// 匿名连接（Noise_NK）载荷：尝试解析为帧
	if len(payload) < 2 {
		return
	}

	f, err := frame.ParseRawFrame(payload)
	if err != nil {
		return
	}

	// 使用零值 chainID（匿名连接没有身份）
	var emptyID peer.ChainID
	n.handleFrame(emptyID, f)
}

// InitRatchet 为好友初始化双重 ratchet 状态。
// ourEphemeralSK 是我们在信标响应中生成的临时密钥私钥（发起方）或本地生成的临时密钥私钥（响应方）。
func (n *Node) InitRatchet(chainID peer.ChainID, ourIdentitySK, remoteIdentityPK, ourEphemeralSK, remoteEphemeralPK *[32]byte, isInitiator bool) (*ratchet.State, error) {
	n.ratchsMu.Lock()
	defer n.ratchsMu.Unlock()

	var s *ratchet.State
	var err error
	if isInitiator {
		s, _, err = ratchet.InitAsInitiator(ourIdentitySK, remoteIdentityPK, remoteEphemeralPK)
	} else {
		s, err = ratchet.InitAsResponder(ourIdentitySK, remoteIdentityPK, ourEphemeralSK, remoteEphemeralPK)
	}
	if err != nil {
		return nil, fmt.Errorf("init ratchet: %w", err)
	}
	n.ratchs[chainID] = s
	return s, nil
}

// SendMessage 通过双重 ratchet 加密并发送消息。
func (n *Node) SendMessage(chainID peer.ChainID, plaintext []byte) error {
	n.ratchsMu.Lock()
	r, ok := n.ratchs[chainID]
	if !ok {
		n.ratchsMu.Unlock()
		return fmt.Errorf("no ratchet state for peer %x", chainID)
	}
	wireMsg, err := r.Encrypt(plaintext)
	n.ratchsMu.Unlock()
	if err != nil {
		return fmt.Errorf("ratchet encrypt: %w", err)
	}
	return n.peers.SendFrame(chainID, frame.NewFrame(frame.FrameData, 0, wireMsg))
}

// ReceiveMessage 解密接收到的消息。
func (n *Node) ReceiveMessage(from peer.ChainID, data []byte) ([]byte, error) {
	n.ratchsMu.Lock()
	r, ok := n.ratchs[from]
	if !ok {
		n.ratchsMu.Unlock()
		return nil, fmt.Errorf("no ratchet state for peer %x", from)
	}
	result, err := r.Decrypt(data)
	n.ratchsMu.Unlock()
	return result, err
}

func hashToChainID(pk [32]byte) peer.ChainID {
	h := sha256.Sum256(pk[:])
	var id peer.ChainID
	copy(id[:], h[:])
	return id
}
