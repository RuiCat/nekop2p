// Package node 节点治理完善 — 黑名单、罚没、任期管理。
//
// 本轮实现:
//   1. 黑名单 (Blacklist) — 严重违规节点永久或定期封禁
//   2. 自动罚没 (Auto-Slashing) — 持续低表现节点的质押金罚没
//   3. 任期限制 (Term Limits) — 正式节点固定任期 + 强制轮换
package node

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ============================================================
// 黑名单
// ============================================================

// BlacklistEntry 黑名单条目。
type BlacklistEntry struct {
	NodeID    string    // 被封禁的节点 chain_id
	Reason    string    // 封禁原因
	BannedAt  time.Time // 封禁时间
	ExpiresAt time.Time // 过期时间（零值 = 永久封禁）
	BannedBy  string    // 执行封禁的节点 chain_id
}

// Blacklist 管理被封禁的节点。
type Blacklist struct {
	mu      sync.RWMutex
	entries map[string]*BlacklistEntry // nodeID → entry
}

// NewBlacklist 创建新的黑名单。
func NewBlacklist() *Blacklist {
	return &Blacklist{
		entries: make(map[string]*BlacklistEntry),
	}
}

// Ban 将节点加入黑名单。
func (b *Blacklist) Ban(nodeID, reason, bannedBy string, duration time.Duration) *BlacklistEntry {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry := &BlacklistEntry{
		NodeID:   nodeID,
		Reason:   reason,
		BannedAt: time.Now(),
		BannedBy: bannedBy,
	}
	if duration > 0 {
		entry.ExpiresAt = time.Now().Add(duration)
	}
	b.entries[nodeID] = entry
	return entry
}

// Unban 从黑名单中移除节点。
func (b *Blacklist) Unban(nodeID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, exists := b.entries[nodeID]
	delete(b.entries, nodeID)
	return exists
}

// IsBanned 检查节点是否在黑名单中（纯读操作，不过期清理）。
func (b *Blacklist) IsBanned(nodeID string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	entry, exists := b.entries[nodeID]
	if !exists {
		return false
	}
	// 检查是否已过期（不在此处删除，由 CleanExpired 统一清理）
	if !entry.ExpiresAt.IsZero() && time.Now().After(entry.ExpiresAt) {
		return false
	}
	return true
}

// GetEntry 获取黑名单条目。
func (b *Blacklist) GetEntry(nodeID string) *BlacklistEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.entries[nodeID]
}

// List 返回所有黑名单条目。
func (b *Blacklist) List() []*BlacklistEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]*BlacklistEntry, 0, len(b.entries))
	for _, entry := range b.entries {
		result = append(result, entry)
	}
	return result
}

// CleanExpired 清理过期的黑名单条目。
func (b *Blacklist) CleanExpired() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	cleaned := 0
	for id, entry := range b.entries {
		if !entry.ExpiresAt.IsZero() && time.Now().After(entry.ExpiresAt) {
			delete(b.entries, id)
			cleaned++
		}
	}
	return cleaned
}

// Count 返回黑名单节点数。
func (b *Blacklist) Count() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.entries)
}

// ============================================================
// 自动罚没 (Auto-Slashing)
// ============================================================

// SlashingConfig 罚没配置。
type SlashingConfig struct {
	// 连续低表现季度数的阈值（超过此值触发罚没）
	ConsecutiveLowPerfQuarters int
	// 罚没比例（占 Bond 质押金的百分比）
	SlashingFraction float64
	// 低表现阈值（评分低于此值视为低表现）
	LowPerfScoreThreshold uint64
	// 最高罚没上限（累计罚没不超过此比例）
	MaxCumulativeSlash float64
}

// DefaultSlashingConfig 返回默认罚没配置。
func DefaultSlashingConfig() SlashingConfig {
	return SlashingConfig{
		ConsecutiveLowPerfQuarters: 3,    // 连续3季度低表现触发罚没
		SlashingFraction:           0.05,  // 每次罚没 5%
		LowPerfScoreThreshold:      30,    // 评分低于 30 视为低表现
		MaxCumulativeSlash:         0.30,  // 累计最多罚没 30%
	}
}

// NodeSlashingState 追踪单个节点的罚没状态。
type NodeSlashingState struct {
	NodeID                   string
	ConsecutiveLowPerfCount  int       // 连续低表现季度数
	TotalSlashed             uint64    // 累计罚没金额
	LastSlashAt              time.Time // 上次罚没时间
	MissedBlocksCount        int64     // 漏块计数（共识层的缺席次数）
}

// SlashingManager 管理节点罚没逻辑。
type SlashingManager struct {
	config SlashingConfig
	states map[string]*NodeSlashingState
}

// NewSlashingManager 创建罚没管理器。
func NewSlashingManager(cfg SlashingConfig) *SlashingManager {
	return &SlashingManager{
		config: cfg,
		states: make(map[string]*NodeSlashingState),
	}
}

// RecordPerformance 记录节点本季度表现。
// 返回是否应该触发罚没。
func (sm *SlashingManager) RecordPerformance(nodeID string, score uint64, bondAmount uint64) (shouldSlash bool, slashAmount uint64) {
	state, exists := sm.states[nodeID]
	if !exists {
		state = &NodeSlashingState{NodeID: nodeID}
		sm.states[nodeID] = state
	}

	if score < sm.config.LowPerfScoreThreshold {
		state.ConsecutiveLowPerfCount++
	} else {
		// 表现回升，重置连续计数器
		state.ConsecutiveLowPerfCount = 0
	}

	if state.ConsecutiveLowPerfCount >= sm.config.ConsecutiveLowPerfQuarters {
		// 触发罚没
		slashAmount = uint64(float64(bondAmount) * sm.config.SlashingFraction)
		if state.TotalSlashed+slashAmount > uint64(float64(bondAmount)*sm.config.MaxCumulativeSlash) {
			slashAmount = uint64(float64(bondAmount)*sm.config.MaxCumulativeSlash) - state.TotalSlashed
		}
		state.TotalSlashed += slashAmount
		state.LastSlashAt = time.Now()
		state.ConsecutiveLowPerfCount = 0 // 罚没后重置计数器

		return true, slashAmount
	}

	return false, 0
}

// GetState 获取节点的罚没状态。
func (sm *SlashingManager) GetState(nodeID string) *NodeSlashingState {
	return sm.states[nodeID]
}

// ResetState 重置节点罚没状态（节点升级或降级时调用）。
func (sm *SlashingManager) ResetState(nodeID string) {
	delete(sm.states, nodeID)
}

// ============================================================
// 任期限制 (Term Limits)
// ============================================================

// TermConfig 任期配置。
type TermConfig struct {
	OfficialTermDays  int // 正式节点任期（天）
	MaxConsecutiveTerms int // 最多连任次数
	CoolDownDays      int // 轮换冷却期（天）
}

// DefaultTermConfig 返回默认任期配置。
func DefaultTermConfig() TermConfig {
	return TermConfig{
		OfficialTermDays:     90,  // 3 个月任期
		MaxConsecutiveTerms:  3,    // 最多连续 3 任期（9个月）
		CoolDownDays:         30,   // 1 个月冷却期
	}
}

// NodeTerm 追踪节点的任期信息。
type NodeTerm struct {
	NodeID       string
	TermStart    int64     // 任期开始时间（Unix 时间戳）
	TermEnd      int64     // 任期结束时间
	TermNumber   int       // 当前是第几个任期
	Role         string    // relay / record
}

// TermManager 管理节点任期。
type TermManager struct {
	mu      sync.RWMutex
	config  TermConfig
	terms   map[string]*NodeTerm    // nodeID → current term
	history map[string][]*NodeTerm  // nodeID → term history
}

// NewTermManager 创建任期管理器。
func NewTermManager(cfg TermConfig) *TermManager {
	return &TermManager{
		config:  cfg,
		terms:   make(map[string]*NodeTerm),
		history: make(map[string][]*NodeTerm),
	}
}

// StartTerm 为节点开始一个新任期。
func (tm *TermManager) StartTerm(nodeID, role string) *NodeTerm {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	pastTerms := tm.history[nodeID]

	term := &NodeTerm{
		NodeID:    nodeID,
		TermStart: time.Now().Unix(),
		TermEnd:   time.Now().Add(time.Duration(tm.config.OfficialTermDays) * 24 * time.Hour).Unix(),
		TermNumber: len(pastTerms) + 1,
		Role:      role,
	}
	tm.terms[nodeID] = term
	return term
}

// EndTerm 结束节点的当前任期。
func (tm *TermManager) EndTerm(nodeID string) *NodeTerm {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	term, exists := tm.terms[nodeID]
	if !exists {
		return nil
	}
	delete(tm.terms, nodeID)
	tm.history[nodeID] = append(tm.history[nodeID], term)
	return term
}

// IsTermExpired 检查当前任期是否已过期。
func (tm *TermManager) IsTermExpired(nodeID string) bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	term, exists := tm.terms[nodeID]
	if !exists {
		return false
	}
	return time.Now().Unix() >= term.TermEnd
}

// CanRenew 检查节点是否可以连任。
// 同时计入已完成的任期(history)和当前活跃任期(terms)。
func (tm *TermManager) CanRenew(nodeID string) bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	pastTerms := tm.history[nodeID]
	currentTerm := tm.terms[nodeID]

	// 总任期数 = 已完成 + 当前活跃（如有）
	totalTerms := len(pastTerms)
	if currentTerm != nil {
		totalTerms++
	}
	if totalTerms == 0 {
		return true
	}

	// 统计连续任期数（从最近向历史回溯）
	// 构建有序的任期列表：已完成任期 + 当前活跃任期
	type orderedTerm struct {
		start, end int64
	}
	var allTerms []orderedTerm
	for _, t := range pastTerms {
		allTerms = append(allTerms, orderedTerm{t.TermStart, t.TermEnd})
	}
	if currentTerm != nil {
		allTerms = append(allTerms, orderedTerm{currentTerm.TermStart, currentTerm.TermEnd})
	}

	// 按开始时间排序
	sort.Slice(allTerms, func(i, j int) bool {
		return allTerms[i].start > allTerms[j].start // 降序：最近的在前面
	})

	consecutive := 1
	for i := 1; i < len(allTerms); i++ {
		gap := allTerms[i-1].start - allTerms[i].end
		if gap < int64(tm.config.CoolDownDays)*86400 {
			consecutive++
		} else {
			break
		}
	}

	return consecutive < tm.config.MaxConsecutiveTerms
}

// GetCurrentTerm 获取节点的当前任期。
func (tm *TermManager) GetCurrentTerm(nodeID string) *NodeTerm {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.terms[nodeID]
}

// GetTermHistory 获取节点的任期历史。
func (tm *TermManager) GetTermHistory(nodeID string) []*NodeTerm {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.history[nodeID]
}

// ExpiredTerms 返回所有已过期的任期。
func (tm *TermManager) ExpiredTerms() []*NodeTerm {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	var expired []*NodeTerm
	for _, term := range tm.terms {
		if time.Now().Unix() >= term.TermEnd {
			expired = append(expired, term)
		}
	}
	return expired
}

// ============================================================
// 综合节点治理器
// ============================================================

// NodeGovernor 综合节点治理器。
// 统一管理黑名单、罚没和任期。
type NodeGovernor struct {
	Blacklist       *Blacklist
	Slashing        *SlashingManager
	TermManager     *TermManager
	Examiner        *Examiner
}

// NewNodeGovernor 创建综合节点治理器。
func NewNodeGovernor() *NodeGovernor {
	return &NodeGovernor{
		Blacklist:    NewBlacklist(),
		Slashing:     NewSlashingManager(DefaultSlashingConfig()),
		TermManager:  NewTermManager(DefaultTermConfig()),
		Examiner:     NewExaminer(DefaultRelayExamConfig(), DefaultRecordExamConfig()),
	}
}

// BanReason 预定义的封禁原因。
const (
	BanReasonDoubleSpend      = "double_spend"        // 双花攻击
	BanReasonInvalidBlock     = "invalid_block"       // 产生无效区块
	BanReasonDDoSAttack       = "ddos_attack"         // DDoS 攻击
	BanReasonSybilAttack      = "sybil_attack"         // 女巫攻击
	BanReasonDataCorruption   = "data_corruption"      // 数据损坏
	BanReasonProlongedOffline = "prolonged_offline"    // 长期离线
	BanReasonGovernanceAbuse  = "governance_abuse"     // 治理滥用
)

// BanDuration 预定义的封禁时长。
var (
	BanPermanent = time.Duration(0)               // 永久封禁
	BanMonth     = 30 * 24 * time.Hour            // 1 个月
	BanQuarter   = 90 * 24 * time.Hour            // 3 个月
	BanYear      = 365 * 24 * time.Hour           // 1 年
)

// computeHash 计算字节数据的 SHA256 哈希。
func computeHash(data []byte) [32]byte {
	return sha256.Sum256(data)
}

// FormatNodeID 格式化节点 ID 用于显示。
func FormatNodeID(nodeID string) string {
	if len(nodeID) > 16 {
		return fmt.Sprintf("%x...", []byte(nodeID[:8]))
	}
	return nodeID
}
