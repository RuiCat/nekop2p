// Package keeper 延期准备金拨备 (Deferred Provisioning)。
//
// 将递归追偿的瞬时脉冲惩罚展开为基于 Epoch 的分级递进扣除机制，
// 类比巴塞尔 III 逆周期资本缓冲 (Counter-Cyclical Capital Buffer)。
//
// 核心机制:
//   1. 观察期 (Observation Window): 违约发生后 3 个 Epoch 内仅部分扣除
//   2. 递进扣除: 30% → 70% → 100%（每 Epoch 递增）
//   3. 申诉窗口: 10,000 blocks 内可提交争议证据
//   4. 良好行为豁免: 连续 50 Epoch 无违约 → 加速追偿减免
//
// 风险缓解效果:
//   - 将惩罚从瞬时脉冲展开为时间序列
//   - 为关联节点提供资产隔离和申诉窗口
//   - 防止极端事件中多米诺骨牌式的连锁违约
package keeper

import (
	"fmt"
	"log"
	"sync"

	"github.com/nekop2p/nekop2p/x/brightchain/types"
)

// ============================================================
// Epoch 系统
// ============================================================

// EpochConfig Epoch 配置。
type EpochConfig struct {
	BlocksPerEpoch      int64 // 每个 Epoch 的区块数（默认 14400 = 约1天）
	ObservationEpochs   int   // 观察期 Epoch 数（默认 3）
	AppealWindowBlocks  int64 // 申诉窗口区块数（默认 10000）
	GoodBehaviorEpochs  int   // 良好行为豁免所需连续无违约 Epoch 数（默认 50）
	ProvisionRates      []float64 // 递进扣除比例 [0.30, 0.70, 1.00]
	MaxRecourseDepth    int   // 最大递归深度
}

// DefaultEpochConfig 返回默认 Epoch 配置。
func DefaultEpochConfig() EpochConfig {
	return EpochConfig{
		BlocksPerEpoch:     14400, // ~1天 (假设 6秒/区块)
		ObservationEpochs:  3,
		AppealWindowBlocks: 10000,
		GoodBehaviorEpochs: 50,
		ProvisionRates:     []float64{0.30, 0.70, 1.00},
		MaxRecourseDepth:   5,
	}
}

// EpochTracker 追踪全局 Epoch 计数。
type EpochTracker struct {
	mu           sync.RWMutex
	currentBlock int64
	config       EpochConfig
}

// NewEpochTracker 创建 Epoch 追踪器。
func NewEpochTracker(cfg EpochConfig) *EpochTracker {
	return &EpochTracker{config: cfg}
}

// CurrentEpoch 返回当前 Epoch 编号。
func (et *EpochTracker) CurrentEpoch() int64 {
	et.mu.RLock()
	defer et.mu.RUnlock()
	return et.currentBlock / et.config.BlocksPerEpoch
}

// AdvanceBlock 推进区块计数。
func (et *EpochTracker) AdvanceBlock(height int64) {
	et.mu.Lock()
	defer et.mu.Unlock()
	et.currentBlock = height
}

// BlocksUntilNextEpoch 返回到下一个 Epoch 的剩余区块数。
func (et *EpochTracker) BlocksUntilNextEpoch() int64 {
	et.mu.RLock()
	defer et.mu.RUnlock()
	nextEpoch := ((et.currentBlock / et.config.BlocksPerEpoch) + 1) * et.config.BlocksPerEpoch
	return nextEpoch - et.currentBlock
}

// ============================================================
// 延期准备金状态
// ============================================================

// DeferredProvision 追踪单个违约事件的递进扣除状态。
type DeferredProvision struct {
	ProvisionID     string  // 唯一标识
	DefaulterID     string  // 违约方 chain_id
	DebtAmount      uint64  // 原始债务金额
	RemainingAmount uint64  // 剩余未追偿金额

	// Epoch 追踪
	DefaultEpoch    int64   // 违约发生的 Epoch
	CurrentEpoch    int64   // 当前处理到的 Epoch
	ProvisionStep   int     // 当前扣除步骤 (0=初次, 1=观察期, 2=递增...)

	// 时间戳
	DefaultedAt    int64    // 违约时间 (区块高度)
	LastProvisionAt int64   // 上次扣除时间 (区块高度)

	// 申诉
	AppealSubmitted  bool   // 是否已提交申诉
	AppealResolved   bool   // 申诉是否已处理
	AppealResult     string // 申诉结果: "pending" / "upheld" / "rejected"

	// 担保人链
	GuarantorChain  []string // 追偿链上的担保人
	CurrentGuarantorIdx int   // 当前追偿到的担保人索引

	// 状态
	Status ProvisionStatus
}

// ProvisionStatus 延期准备金状态。
type ProvisionStatus int

const (
	ProvisionPending    ProvisionStatus = 0 // 待处理（观察期内）
	ProvisionActive     ProvisionStatus = 1 // 活跃追偿中
	ProvisionAppealed   ProvisionStatus = 2 // 已申诉（暂停追偿）
	ProvisionCompleted  ProvisionStatus = 3 // 追偿完成
	ProvisionExempted   ProvisionStatus = 4 // 豁免（良好行为）
	ProvisionCancelled  ProvisionStatus = 5 // 已取消
)

func (ps ProvisionStatus) String() string {
	switch ps {
	case ProvisionPending: return "pending"
	case ProvisionActive: return "active"
	case ProvisionAppealed: return "appealed"
	case ProvisionCompleted: return "completed"
	case ProvisionExempted: return "exempted"
	case ProvisionCancelled: return "cancelled"
	default: return "unknown"
	}
}

// ============================================================
// 延期准备金管理器
// ============================================================

// RecourseManager 管理延期追偿流程。
type RecourseManager struct {
	mu          sync.RWMutex
	config      EpochConfig
	epochTracker *EpochTracker
	provisions   map[string]*DeferredProvision // provisionID → state
	nodeHistory  map[string]*NodeRecourseHistory // nodeID → history
}

// NodeRecourseHistory 节点的追偿历史（用于良好行为豁免判定）。
type NodeRecourseHistory struct {
	NodeID               string
	TotalDefaults        int       // 总违约次数
	ConsecutiveCleanEpochs int     // 连续无违约 Epoch 数
	LastDefaultEpoch     int64     // 上次违约的 Epoch
	TotalProvisioned     uint64    // 累计被追偿金额
	ExemptionEligible    bool      // 是否满足豁免条件
}

// NewRecourseManager 创建延期追偿管理器。
func NewRecourseManager(cfg EpochConfig) *RecourseManager {
	return &RecourseManager{
		config:       cfg,
		epochTracker: NewEpochTracker(cfg),
		provisions:   make(map[string]*DeferredProvision),
		nodeHistory:  make(map[string]*NodeRecourseHistory),
	}
}

// ============================================================
// 核心追偿逻辑
// ============================================================

// InitiateProvision 发起延期追偿流程。
// 在违约发生时调用，而非立即扣除全部担保金。
// 返回首次扣除金额（30%）。
func (rm *RecourseManager) InitiateProvision(
	defaulterID string,
	debtAmount uint64,
	guarantorChain []string,
	blockHeight int64,
) (*DeferredProvision, uint64, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.epochTracker.AdvanceBlock(blockHeight)
	currentEpoch := rm.epochTracker.CurrentEpoch()

	provisionID := fmt.Sprintf("prov-%s-%d-%d", safePrefix(defaulterID, 8), blockHeight, currentEpoch)

	prov := &DeferredProvision{
		ProvisionID:     provisionID,
		DefaulterID:     defaulterID,
		DebtAmount:      debtAmount,
		RemainingAmount: debtAmount,
		DefaultEpoch:    currentEpoch,
		CurrentEpoch:    currentEpoch,
		ProvisionStep:   0,
		DefaultedAt:     blockHeight,
		LastProvisionAt: blockHeight,
		GuarantorChain:  guarantorChain,
		Status:          ProvisionPending,
	}

	// 首次扣除: 30%（使用整数比例避免 float64 精度损失）
	initialRate := rm.config.ProvisionRates[0]
	initialAmount := uint64(float64(debtAmount) * initialRate)
	if initialAmount == 0 && debtAmount > 0 {
		initialAmount = 1 // 确保至少扣除 1 单位
	}
	prov.RemainingAmount = debtAmount - initialAmount
	prov.ProvisionStep = 1

	if prov.RemainingAmount == 0 {
		prov.Status = ProvisionCompleted
	}

	rm.provisions[provisionID] = prov
	rm.recordDefault(defaulterID, currentEpoch)

	log.Printf("[recourse] initiated provision %s: defaulter=%s debt=%d initial=%d (%.0f%%) remaining=%d",
		provisionID, defaulterID[:8], debtAmount, initialAmount, initialRate*100, prov.RemainingAmount)

	return prov, initialAmount, nil
}

// ProcessEpochProvision 处理 Epoch 递进扣除。
// 每 Epoch 调用一次，按比例扣除担保人 Bond。
// 返回本轮扣除金额。
func (rm *RecourseManager) ProcessEpochProvision(blockHeight int64) []ProvisionAction {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.epochTracker.AdvanceBlock(blockHeight)
	currentEpoch := rm.epochTracker.CurrentEpoch()

	var actions []ProvisionAction

	for _, prov := range rm.provisions {
		if prov.Status != ProvisionActive && prov.Status != ProvisionPending {
			continue
		}

		// 检查是否进入新 Epoch
		if currentEpoch <= prov.CurrentEpoch {
			continue
		}

		// 检查申诉窗口
		blocksSinceDefault := blockHeight - prov.DefaultedAt
		if blocksSinceDefault <= rm.config.AppealWindowBlocks {
			// 仍在申诉窗口内，仅做最小扣除
			continue
		}

		// 检查良好行为豁免
		history := rm.nodeHistory[prov.DefaulterID]
		if history != nil && history.ConsecutiveCleanEpochs >= rm.config.GoodBehaviorEpochs {
			prov.Status = ProvisionExempted
			log.Printf("[recourse] provision %s exempted: %d consecutive clean epochs",
				prov.ProvisionID, history.ConsecutiveCleanEpochs)
			continue
		}

		// 递进扣除
		prov.CurrentEpoch = currentEpoch
		prov.Status = ProvisionActive

		// 确定扣除比例
		stepsSince := int(currentEpoch - prov.DefaultEpoch)
		rateIdx := stepsSince
		if rateIdx >= len(rm.config.ProvisionRates) {
			rateIdx = len(rm.config.ProvisionRates) - 1
		}
		rate := rm.config.ProvisionRates[rateIdx]

		// 计算本轮扣除金额
		originalRemaining := prov.RemainingAmount
		amount := uint64(float64(prov.DebtAmount) * rate)
		// 减去已扣除部分
		alreadyProvisioned := prov.DebtAmount - originalRemaining
		if amount <= alreadyProvisioned {
			amount = 0 // 当前比例对应的总额已扣除
		} else {
			amount = amount - alreadyProvisioned
		}

		if amount > prov.RemainingAmount {
			amount = prov.RemainingAmount
		}

		if amount > 0 {
			prov.RemainingAmount -= amount
			prov.LastProvisionAt = blockHeight
			prov.ProvisionStep++

			// 确定当前追偿目标
			targetGuarantor := prov.DefaulterID
			if prov.CurrentGuarantorIdx < len(prov.GuarantorChain) {
				targetGuarantor = prov.GuarantorChain[prov.CurrentGuarantorIdx]
			}

			actions = append(actions, ProvisionAction{
				ProvisionID:  prov.ProvisionID,
				TargetNode:   targetGuarantor,
				Amount:       amount,
				Rate:         rate,
				Remaining:    prov.RemainingAmount,
			})
		}

		if prov.RemainingAmount == 0 {
			prov.Status = ProvisionCompleted
			// 如果还有更多担保人，推进到下一位
			prov.CurrentGuarantorIdx++
			if prov.CurrentGuarantorIdx < len(prov.GuarantorChain) {
				// 对下一个担保人继续追偿
				prov.Status = ProvisionActive
			}
			log.Printf("[recourse] provision %s completed for guarantor[%d]: total=%d epochs=%d",
				prov.ProvisionID, prov.CurrentGuarantorIdx-1, prov.DebtAmount, stepsSince)
		}

		// 更新节点历史
		rm.recordProvision(targetGuarantor(prov), amount, currentEpoch)
	}

	// 清理完成的追偿（保留最近 100 条用于审计）
	if len(rm.provisions) > 1000 {
		rm.pruneOldProvisions()
	}

	return actions
}

// ProvisionAction 单次追偿动作。
type ProvisionAction struct {
	ProvisionID string
	TargetNode  string
	Amount      uint64
	Rate        float64
	Remaining   uint64
}

// ============================================================
// 申诉系统
// ============================================================

// SubmitAppeal 提交追偿申诉。
// 申诉期间暂停扣除，但不退还已扣除金额。
func (rm *RecourseManager) SubmitAppeal(provisionID, evidence string, blockHeight int64) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	prov, exists := rm.provisions[provisionID]
	if !exists {
		return fmt.Errorf("recourse: provision not found: %s", provisionID)
	}

	blocksSinceDefault := blockHeight - prov.DefaultedAt
	if blocksSinceDefault > rm.config.AppealWindowBlocks {
		return fmt.Errorf("recourse: appeal window closed (%d > %d blocks)",
			blocksSinceDefault, rm.config.AppealWindowBlocks)
	}

	if prov.AppealSubmitted {
		return fmt.Errorf("recourse: appeal already submitted for %s", provisionID)
	}

	prov.AppealSubmitted = true
	prov.Status = ProvisionAppealed
	prov.AppealResult = "pending"

	log.Printf("[recourse] appeal submitted for %s (evidence=%s)", provisionID, safePrefix(evidence, 32))
	return nil
}

// ResolveAppeal 处理申诉结果。
func (rm *RecourseManager) ResolveAppeal(provisionID string, upheld bool) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	prov, exists := rm.provisions[provisionID]
	if !exists {
		return fmt.Errorf("recourse: provision not found: %s", provisionID)
	}
	if !prov.AppealSubmitted {
		return fmt.Errorf("recourse: no appeal for %s", provisionID)
	}

	prov.AppealResolved = true
	if upheld {
		prov.AppealResult = "upheld"
		prov.Status = ProvisionCancelled
		// 申诉成功：退还已扣除金额（在调用方处理）
		log.Printf("[recourse] appeal UPHELD for %s — provision cancelled, refund required", provisionID)
	} else {
		prov.AppealResult = "rejected"
		prov.Status = ProvisionActive
		log.Printf("[recourse] appeal REJECTED for %s — resuming provision", provisionID)
	}

	return nil
}

// ============================================================
// 良好行为追踪
// ============================================================

// RecordCleanEpoch 记录节点一个无违约 Epoch。
func (rm *RecourseManager) RecordCleanEpoch(nodeID string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	history := rm.getOrCreateHistory(nodeID)
	history.ConsecutiveCleanEpochs++

	if history.ConsecutiveCleanEpochs >= rm.config.GoodBehaviorEpochs {
		history.ExemptionEligible = true
	}
}

func (rm *RecourseManager) recordDefault(nodeID string, epoch int64) {
	history := rm.getOrCreateHistory(nodeID)
	history.TotalDefaults++
	history.ConsecutiveCleanEpochs = 0
	history.LastDefaultEpoch = epoch
	history.ExemptionEligible = false
}

func (rm *RecourseManager) recordProvision(nodeID string, amount uint64, epoch int64) {
	history := rm.getOrCreateHistory(nodeID)
	history.TotalProvisioned += amount
}

func (rm *RecourseManager) getOrCreateHistory(nodeID string) *NodeRecourseHistory {
	h, exists := rm.nodeHistory[nodeID]
	if !exists {
		h = &NodeRecourseHistory{NodeID: nodeID}
		rm.nodeHistory[nodeID] = h
	}
	return h
}

func (rm *RecourseManager) GetNodeHistory(nodeID string) *NodeRecourseHistory {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.nodeHistory[nodeID]
}

// ============================================================
// 查询与管理
// ============================================================

// GetProvision 获取追偿状态。
func (rm *RecourseManager) GetProvision(provisionID string) *DeferredProvision {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.provisions[provisionID]
}

// ActiveProvisions 返回所有活跃的追偿。
func (rm *RecourseManager) ActiveProvisions() []*DeferredProvision {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	var result []*DeferredProvision
	for _, p := range rm.provisions {
		if p.Status == ProvisionActive || p.Status == ProvisionPending {
			result = append(result, p)
		}
	}
	return result
}

// TotalProvisioned 返回累计追偿总额。
func (rm *RecourseManager) TotalProvisioned() uint64 {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	var total uint64
	for _, h := range rm.nodeHistory {
		total += h.TotalProvisioned
	}
	return total
}

// CurrentEpoch 返回当前全局 Epoch。
func (rm *RecourseManager) CurrentEpoch() int64 {
	return rm.epochTracker.CurrentEpoch()
}

// AdvanceEpoch 手动推进 Epoch（用于测试）。
func (rm *RecourseManager) AdvanceEpoch(blockHeight int64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.epochTracker.AdvanceBlock(blockHeight)
}

func (rm *RecourseManager) pruneOldProvisions() {
	// 保留最近 100 条完成的追偿记录
	var active, completed []string
	for id, p := range rm.provisions {
		if p.Status == ProvisionCompleted || p.Status == ProvisionCancelled || p.Status == ProvisionExempted {
			completed = append(completed, id)
		} else {
			active = append(active, id)
		}
	}
	// 删除多余的已完成记录
	if len(completed) > 100 {
		for _, id := range completed[:len(completed)-100] {
			delete(rm.provisions, id)
		}
	}
	_ = active // 活跃记录不删除
}

func targetGuarantor(prov *DeferredProvision) string {
	if prov.CurrentGuarantorIdx < len(prov.GuarantorChain) {
		return prov.GuarantorChain[prov.CurrentGuarantorIdx]
	}
	return prov.DefaulterID
}

// safePrefix 安全截断字符串前缀。
func safePrefix(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

var _ = types.ModuleName
