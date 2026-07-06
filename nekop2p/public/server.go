// Package public 实现公共引导节点服务。
//
// 公共节点接受匿名 Noise_NK 连接，并将加密信标中继到网络中。
// 它们不解密也不检查信标内容。
package public

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nekop2p/nekop2p/beacon"
	"github.com/nekop2p/nekop2p/frame"
	"github.com/nekop2p/nekop2p/noise"
	"github.com/nekop2p/nekop2p/crypto"
	"github.com/nekop2p/nekop2p/peer"
)

// Config 保存公共节点配置。
type Config struct {
	ListenAddr   string // 例如："[::]:9071"
	StaticKey    crypto.KeyPair // 我们的静态 Curve25519 密钥
	MaxConns     int   // 最大同时连接数（默认 10000）
	BeaconRateLimit int // 每个源 IPv6 每分钟最大信标数（默认 5）
}

// Server 是一个公共引导节点。
type Server struct {
	cfg    Config
	peers  *peer.Manager   // 已连接的节点（好友 + 其他公共节点）
	ctx    context.Context
	cancel context.CancelFunc

	listener net.Listener

	// 公共连接追踪
	connCount   int32        // 使用 atomic 操作

	// 速率限制
	mu         sync.Mutex
	rateTrack  map[[16]byte]*rateLimiter // 按源 IPv6 统计

	// 去重
	seenTags   map[[16]byte]time.Time // random_tag → 过期时间
	seenMu     sync.RWMutex
}

type rateLimiter struct {
	tokens   int
	lastRefill time.Time
}

const (
	tagTTL = 60 * time.Second
)

// 公共连接处理的并发上限
var publicConnWorkers = make(chan struct{}, 200)

// New 创建一个新的公共节点服务。
func New(cfg Config) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		cfg:       cfg,
		ctx:       ctx,
		cancel:    cancel,
		rateTrack: make(map[[16]byte]*rateLimiter),
		seenTags:  make(map[[16]byte]time.Time),
	}
	s.peers = peer.NewManager(ctx)
	return s
}

// SetPeers 设置节点管理器（与节点的好友连接共享）。
func (s *Server) SetPeers(pm *peer.Manager) {
	s.peers = pm
}

// Listen 开始接受匿名连接。
func (s *Server) Listen() error {
	s.listener = nil
	l, err := net.Listen("tcp6", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("public listen: %w", err)
	}
	s.listener = l

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			// 并发限制
			select {
			case publicConnWorkers <- struct{}{}:
				go func() {
					defer func() { <-publicConnWorkers }()
					s.handleConnection(conn)
				}()
			default:
				conn.Close() // 过载：直接关闭
			}
		}
	}()

	// 后台清理
	go s.cleanupLoop()

	return nil
}

// Shutdown 优雅地关闭公共节点服务。
func (s *Server) Shutdown() {
	s.cancel()
	if s.listener != nil {
		s.listener.Close()
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	// 原子递增连接计数
	count := atomic.AddInt32(&s.connCount, 1)
	defer atomic.AddInt32(&s.connCount, -1)

	// 连接数限制检查
	if s.cfg.MaxConns > 0 && int(count) > s.cfg.MaxConns {
		return
	}

	// 获取源 IPv6
	var srcIPv6 [16]byte
	if tcpAddr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		copy(srcIPv6[:], tcpAddr.IP.To16())
	}

	// 速率限制检查
	if !s.allowBeacon(srcIPv6) {
		return
	}

	// === Noise_NK 握手（响应方：我们认证，发起方是匿名的）===
	hs := noise.NewResponderNK(&s.cfg.StaticKey, noise.RolePublic)

	// 读取来自发起方的消息1（包含其临时密钥）
	msgBuf := make([]byte, 4096)
	n, err := conn.Read(msgBuf)
	if err != nil {
		return
	}

	_, err = hs.ReadMessage(msgBuf[:n])
	if err != nil {
		return
	}

	// 写入消息2（我们的临时密钥）
	resp, err := hs.WriteMessage(nil)
	if err != nil {
		return
	}
	if _, err := conn.Write(resp); err != nil {
		return
	}

	// 完成握手
	result := hs.Complete()

	// 创建用于帧加密的会话
	sendKey := result.SendCipher.Key
	recvKey := result.RecvCipher.Key
	framer := frame.NewSessionKeys(sendKey[:], recvKey[:])

	// 循环读取帧，查找 BEACON 帧
	for {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		f, err := framer.ReadEncryptedFrame(conn)
		if err != nil {
			return
		}

		switch f.Type {
		case frame.FrameBeacon:
			s.handleBeacon(f.Payload, srcIPv6)
			return // 公共节点：处理完一个信标后关闭连接
		case frame.FramePing:
			pong := frame.NewFrame(frame.FramePong, 0, nil)
			framer.WriteEncryptedFrame(conn, pong)
		default:
			return // 来自匿名客户端的不支持的帧
		}
	}
}

func (s *Server) handleBeacon(data []byte, srcIPv6 [16]byte) {
	// 解析信标
	bp, err := beacon.ParseBeacon(data)
	if err != nil {
		return
	}

	// 去重
	s.seenMu.Lock()
	if _, seen := s.seenTags[bp.RandomTag]; seen {
		s.seenMu.Unlock()
		return
	}
	s.seenTags[bp.RandomTag] = time.Now().Add(tagTTL)
	s.seenMu.Unlock()

	// 转发给已连接的节点（混合策略）
	// 优先级 1：好友（非填充连接）
	beaconFrame := frame.NewFrame(frame.FrameBeacon, 0, bp.Serialize())
	s.peers.BroadcastFrame(beaconFrame, peer.ChainID{})

	// 优先级 2：如果好友较少，也中继到其他公共节点
	// （由节点自身的路由逻辑处理）
}

func (s *Server) allowBeacon(srcIPv6 [16]byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	rl, ok := s.rateTrack[srcIPv6]
	if !ok {
		rl = &rateLimiter{tokens: s.cfg.BeaconRateLimit, lastRefill: time.Now()}
		s.rateTrack[srcIPv6] = rl
	}

	// 补充令牌（每12秒1个 = 每分钟5个）
	elapsed := time.Since(rl.lastRefill)
	refill := int(elapsed.Seconds() / 12.0)
	if refill > 0 {
		rl.tokens += refill
		if rl.tokens > s.cfg.BeaconRateLimit {
			rl.tokens = s.cfg.BeaconRateLimit
		}
		rl.lastRefill = time.Now()
	}

	if rl.tokens <= 0 {
		return false
	}
	rl.tokens--
	return true
}

func (s *Server) cleanupLoop() {
	ticker := time.NewTicker(120 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.cleanup()
		}
	}
}

func (s *Server) cleanup() {
	// 清理旧的已见标签
	s.seenMu.Lock()
	now := time.Now()
	for tag, exp := range s.seenTags {
		if now.After(exp) {
			delete(s.seenTags, tag)
		}
	}
	s.seenMu.Unlock()

	// 清理旧的速率限制器
	s.mu.Lock()
	for ip, rl := range s.rateTrack {
		if time.Since(rl.lastRefill) > 10*time.Minute {
			delete(s.rateTrack, ip)
		}
	}
	s.mu.Unlock()
}
