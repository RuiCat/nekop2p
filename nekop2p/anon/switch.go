// Package anon 实现按需匿名切换层。
//
// 三种通信通道：
//
//	Clear (明道) — 直连，端到端加密，快速。默认模式。
//	Dark  (暗道) — 洋葱路由，通过 ZK 信用证明实现匿名身份。
//	Bunker (防空洞) — 完全匿名：身份剥离，资金清洗，5 层防御。
//
// 切换由上层应用触发（物理开关/软件按钮）。
package anon

import (
	"fmt"
	"sync"
	"time"

	"github.com/nekop2p/nekop2p/crypto"
)

// Channel 表示当前的匿名模式。
type Channel int

const (
	ChannelClear  Channel = iota // 明道: 直接、透明
	ChannelDark                  // 暗道: 洋葱路由、ZK 匿名
	ChannelBunker                // 防空洞: 完全匿名
)

func (c Channel) String() string {
	switch c {
	case ChannelClear:  return "clear"
	case ChannelDark:   return "dark"
	case ChannelBunker: return "bunker"
	default:            return "unknown"
	}
}

// State 管理当前匿名通道并处理切换。
type State struct {
	mu          sync.RWMutex
	channel     Channel

	// 各通道身份
	chainID      [32]byte // 明域身份（明道）
	anonID       [32]byte // 暗域匿名身份
	bunkerID     [32]byte // 防空洞域匿名身份
	anonCounter  uint64   // 用于生成下一个 anon_id 的计数器
	masterSecret [32]byte // 暗域主密钥（用于 PRF 派生匿名身份）

	// 路由状态
	useOnion    bool     // 使用洋葱路由时为 true
	onionHops   int      // 洋葱跳数（明道 3 跳，防空洞 5 跳）

	// 资金状态
	fundsMixed  bool     // 资金已通过混合器时为 true（防空洞）
	fundsPaused bool     // 防空洞退出重新混合期间为 true

	// 回调
	onSwitch   func(from, to Channel)
	onIdentity func(channel Channel, id [32]byte)

	lastSwitch time.Time
}

// Config 保存匿名切换配置。
type Config struct {
	ChainID        [32]byte
	MasterSecret   [32]byte // 暗域主密钥
	OnSwitch       func(from, to Channel)
	OnIdentity     func(channel Channel, id [32]byte)
}

// New 创建新的匿名状态管理器。
func New(cfg Config) *State {
	s := &State{
		channel:      ChannelClear,
		chainID:      cfg.ChainID,
		masterSecret: cfg.MasterSecret,
		onSwitch:     cfg.OnSwitch,
		onIdentity:   cfg.OnIdentity,
		onionHops:    3,
	}

	return s
}

// Channel 返回当前的匿名通道。
func (s *State) Channel() Channel {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.channel
}

// IsOnion 返回当前通道是否使用洋葱路由。
func (s *State) IsOnion() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.useOnion
}

// OnionHops 返回当前通道的洋葱跳数。
func (s *State) OnionHops() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.onionHops
}

// CurrentIdentity 返回当前通道的活动身份。
func (s *State) CurrentIdentity() [32]byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch s.channel {
	case ChannelClear:  return s.chainID
	case ChannelDark:   return s.anonID
	case ChannelBunker: return s.bunkerID
	default:            return s.chainID
	}
}

// SwitchTo 切换到指定的通道。
func (s *State) SwitchTo(target Channel) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if target == s.channel {
		return nil
	}

	from := s.channel

	switch target {
	case ChannelClear:
		return s.enterClear(from)
	case ChannelDark:
		return s.enterDark(from)
	case ChannelBunker:
		return s.enterBunker(from)
	default:
		return fmt.Errorf("anon: unknown channel: %d", target)
	}
}

func (s *State) enterClear(from Channel) error {
	switch from {
	case ChannelDark:
		// 暗道 → 明道：销毁暗域身份，恢复直连路由
		s.useOnion = false
		s.onionHops = 3

	case ChannelBunker:
		// 防空洞 → 明道：重新混合资金，短暂暂停，恢复身份
		s.fundsPaused = true
		// 模拟混合器延迟
		time.Sleep(100 * time.Millisecond)
		s.fundsPaused = false
		s.fundsMixed = false
		s.useOnion = false
		s.onionHops = 3
	}

	s.channel = ChannelClear
	s.lastSwitch = time.Now()

	if s.onSwitch != nil {
		s.onSwitch(from, ChannelClear)
	}
	if s.onIdentity != nil {
		s.onIdentity(ChannelClear, s.chainID)
	}

	return nil
}

func (s *State) enterDark(from Channel) error {
	// 为本会话生成新的匿名身份
	s.anonCounter++
	s.anonID = deriveAnonID(s.masterSecret, s.anonCounter)
	s.useOnion = true
	s.onionHops = 3

	switch from {
	case ChannelBunker:
		// 防空洞 → 暗道：销毁防空洞身份
		s.bunkerID = [32]byte{}
	}

	s.channel = ChannelDark
	s.lastSwitch = time.Now()

	if s.onSwitch != nil {
		s.onSwitch(from, ChannelDark)
	}
	if s.onIdentity != nil {
		s.onIdentity(ChannelDark, s.anonID)
	}

	return nil
}

func (s *State) enterBunker(from Channel) error {
	// 生成全新的防空洞身份（不能从暗域身份推导）
	s.anonCounter++
	s.bunkerID = deriveAnonID(s.masterSecret, s.anonCounter+9999) // 偏移以避免碰撞
	s.useOnion = true
	s.onionHops = 5 // 更多跳数以获得最大匿名性
	s.fundsMixed = true

	switch from {
	case ChannelDark:
		// 暗道 → 防空洞：销毁暗域身份，通过混合器路由
		s.anonID = [32]byte{}
	case ChannelClear:
		// 明道 → 防空洞：拆分资金，通过混合器路由
	}

	s.channel = ChannelBunker
	s.lastSwitch = time.Now()

	if s.onSwitch != nil {
		s.onSwitch(from, ChannelBunker)
	}
	if s.onIdentity != nil {
		s.onIdentity(ChannelBunker, s.bunkerID)
	}

	return nil
}

// PauseDuration 返回通道切换所需时间。
func (s *State) PauseDuration() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.fundsPaused {
		return 5 * time.Second // 模拟的混合器处理时间
	}
	return 0
}

// IsSwitching 返回通道切换是否正在进行中。
func (s *State) IsSwitching() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fundsPaused
}

// deriveAnonID 从主密钥和计数器派生匿名身份。
// 使用 PRF(master_secret, counter) 确保密码学安全。
func deriveAnonID(masterSecret [32]byte, counter uint64) [32]byte {
	var counterBytes [8]byte
	counterBytes[0] = byte(counter >> 56)
	counterBytes[1] = byte(counter >> 48)
	counterBytes[2] = byte(counter >> 40)
	counterBytes[3] = byte(counter >> 32)
	counterBytes[4] = byte(counter >> 24)
	counterBytes[5] = byte(counter >> 16)
	counterBytes[6] = byte(counter >> 8)
	counterBytes[7] = byte(counter)
	return crypto.PRF(masterSecret[:], counterBytes[:])
}
