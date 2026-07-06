// Package peer 管理节点之间的直连。
package peer

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/nekop2p/nekop2p/frame"
)

// ChainID 是一个 32 字节的链上节点标识符。
type ChainID [32]byte

// State 是对等连接状态。
type State int

const (
	StateConnecting State = iota
	StateHandshaking
	StateActive
	StateClosing
	StateClosed
)

// Info 保存关于已连接对等方的元数据。
type Info struct {
	ChainID   ChainID
	IPv6      [16]byte
	Port      uint16
	State     State
	IsPadding bool // 如果这是填充连接则为 true
	Conn      net.Conn
	Framer    *frame.SessionKeys
	StartedAt time.Time
	LastSeen  time.Time
}

// Manager 管理所有直连对等方。
type Manager struct {
	mu     sync.RWMutex
	peers  map[ChainID]*Info
	ctx    context.Context
	cancel context.CancelFunc

	// 回调
	onMessage func(from ChainID, f *frame.Frame)
	onConnect func(peer *Info)
	onDisconnect func(peer *Info)
}

// NewManager 创建新的对等连接管理器。
func NewManager(ctx context.Context) *Manager {
	ctx, cancel := context.WithCancel(ctx)
	return &Manager{
		peers: make(map[ChainID]*Info),
		ctx:   ctx,
		cancel: cancel,
	}
}

// SetCallbacks 设置事件处理器。
func (m *Manager) SetCallbacks(onMsg func(ChainID, *frame.Frame), onConn func(*Info), onDisc func(*Info)) {
	m.onMessage = onMsg
	m.onConnect = onConn
	m.onDisconnect = onDisc
}

// Add 向管理器添加对等方并启动其读写循环。
func (m *Manager) Add(chainID ChainID, conn net.Conn, framer *frame.SessionKeys, isPadding bool) *Info {
	pi := &Info{
		ChainID:   chainID,
		Conn:      conn,
		Framer:    framer,
		IsPadding: isPadding,
		State:     StateActive,
		StartedAt: time.Now(),
		LastSeen:  time.Now(),
	}

	// 获取远程地址
	if tcpAddr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		copy(pi.IPv6[:], tcpAddr.IP.To16())
		pi.Port = uint16(tcpAddr.Port)
	} else {
		log.Printf("peer.Add: failed to extract IP from RemoteAddr %T", conn.RemoteAddr())
	}

	m.mu.Lock()
	m.peers[chainID] = pi
	m.mu.Unlock()

	// 启动读取循环
	go m.readLoop(pi)

	if m.onConnect != nil {
		m.onConnect(pi)
	}

	return pi
}

// Remove 移除并关闭对等连接。
func (m *Manager) Remove(chainID ChainID) {
	m.mu.Lock()
	pi, ok := m.peers[chainID]
	if ok {
		delete(m.peers, chainID)
	}
	m.mu.Unlock()

	if ok && pi != nil {
		pi.State = StateClosing
		pi.Conn.Close()
		pi.State = StateClosed
		if m.onDisconnect != nil {
			m.onDisconnect(pi)
		}
	}
}

// Get 按链 ID 返回对等方。
func (m *Manager) Get(chainID ChainID) *Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.peers[chainID]
}

// Count 返回活跃对等方数量。
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.peers)
}

// RealCount 返回排除填充连接后的数量。
func (m *Manager) RealCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, p := range m.peers {
		if !p.IsPadding {
			count++
		}
	}
	return count
}

// All 返回所有对等方信息的副本。
func (m *Manager) All() []*Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Info, 0, len(m.peers))
	for _, p := range m.peers {
		result = append(result, p)
	}
	return result
}

// SendFrame 向指定对等方发送一帧。
func (m *Manager) SendFrame(chainID ChainID, f *frame.Frame) error {
	m.mu.RLock()
	pi, ok := m.peers[chainID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("peer not found")
	}
	return pi.Framer.WriteEncryptedFrame(pi.Conn, f)
}

// BroadcastFrame 向所有活跃对等方广播一帧。
func (m *Manager) BroadcastFrame(f *frame.Frame, exclude ChainID) {
	for _, pi := range m.All() {
		if pi.ChainID == exclude {
			continue
		}
		pi.Framer.WriteEncryptedFrame(pi.Conn, f)
	}
}

// Shutdown 优雅地关闭所有连接。
func (m *Manager) Shutdown() {
	m.cancel()
	for _, pi := range m.All() {
		// 发送关闭帧
		closeFrame := frame.NewFrame(frame.FrameClose, frame.CloseFlagReconnect, nil)
		pi.Framer.WriteEncryptedFrame(pi.Conn, closeFrame)
		pi.Conn.Close()
	}
}

func (m *Manager) readLoop(pi *Info) {
	defer m.Remove(pi.ChainID)

	for {
		select {
		case <-m.ctx.Done():
			return
		default:
		}

		pi.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		f, err := pi.Framer.ReadEncryptedFrame(pi.Conn)
		if err != nil {
			return // 连接已关闭或出错
		}

		pi.LastSeen = time.Now()

		// 在本地处理控制帧
		switch f.Type {
		case frame.FramePing:
			pong := frame.NewFrame(frame.FramePong, 0, nil)
			pi.Framer.WriteEncryptedFrame(pi.Conn, pong)
			continue
		case frame.FrameClose:
			return
		}

		// 转发到应用处理器
		if m.onMessage != nil {
			m.onMessage(pi.ChainID, f)
		}
	}
}
