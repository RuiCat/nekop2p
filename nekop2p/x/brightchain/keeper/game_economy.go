// Package keeper 动态经济参数治理。
//
// 实现掉落算法全局上限 P 值的自适应调节机制：
//   1. 基于活跃用户数 N 的自适应: P = P_base × sqrt(N / N_base)
//   2. 链上治理提案 + 投票
//   3. 变更后 30 天渐变过渡期
//
// 缓解「生产力陷阱」：越多人参与，单个人产出越低的问题。
package keeper

import (
	"fmt"
	"log"
	"math"
	"sync"

	"github.com/nekop2p/nekop2p/x/brightchain/types"
)

// ============================================================
// 动态经济参数
// ============================================================

// EconomyParams 链上可治理的经济参数。
type EconomyParams struct {
	// 掉落算法参数
	PBase    uint64 // 基础 P 值（静态部分）
	NBase    uint64 // 基础用户数（基准点）
	PValue   uint64 // 当前有效 P 值 = PBase × sqrt(N / NBase)

	// 过渡期管理
	TargetPValue   uint64 // 目标 P 值（治理投票通过后设定）
	TransitionStart int64  // 过渡开始区块高度
	TransitionEnd   int64  // 过渡结束区块高度
	TransitionBlocks int64 // 过渡持续区块数（默认 30 天 = 432000 blocks）

	// 自适应参数
	AdaptiveEnabled bool   // 是否启用自适应调节
	UpdateInterval  int64  // 更新间隔（区块数）
	LastUpdateBlock int64  // 上次更新区块高度
	ActiveUserCount uint64 // 当前活跃用户数（缓存）
}

// DefaultEconomyParams 返回默认经济参数。
func DefaultEconomyParams() *EconomyParams {
	return &EconomyParams{
		PBase:            1000,
		NBase:            100,
		PValue:           1000,
		TransitionBlocks: 432000, // 30 天 (假设 6秒/区块)
		AdaptiveEnabled:  true,
		UpdateInterval:   14400, // 每天更新一次
	}
}

// ============================================================
// P 值治理
// ============================================================

// EconomyGovernor 经济参数治理器。
type EconomyGovernor struct {
	mu      sync.RWMutex
	params  *EconomyParams
	proposals map[string]*GovernanceProposal
}

// NewEconomyGovernor 创建经济治理器。
func NewEconomyGovernor(params *EconomyParams) *EconomyGovernor {
	if params == nil {
		params = DefaultEconomyParams()
	}
	return &EconomyGovernor{
		params:    params,
		proposals: make(map[string]*GovernanceProposal),
	}
}

// ============================================================
// 自适应 P 值计算
// ============================================================

// ComputeAdaptivePValue 根据活跃用户数计算自适应 P 值。
//
// 公式: P = P_base × sqrt(N / N_base)
//
// 当用户数增长时，P 值适度增长以维持单用户产出。
// 当用户数减少时，P 值相应收缩以防止过度通胀。
func (eg *EconomyGovernor) ComputeAdaptivePValue(activeUserCount uint64) uint64 {
	eg.mu.RLock()
	params := eg.params
	eg.mu.RUnlock()

	if activeUserCount == 0 || params.NBase == 0 || !params.AdaptiveEnabled {
		return params.PBase
	}

	// P = P_base × sqrt(N / N_base)
	ratio := float64(activeUserCount) / float64(params.NBase)
	pValue := float64(params.PBase) * math.Sqrt(ratio)

	// 约束: P ∈ [P_base/10, P_base×10]
	minP := params.PBase / 10
	maxP := params.PBase * 10
	result := uint64(pValue)

	if result < minP {
		result = minP
	}
	if result > maxP {
		result = maxP
	}

	return result
}

// UpdateActiveUserCount 更新活跃用户数并重新计算 P 值。
// 应在 EndBlocker 中定期调用。
func (eg *EconomyGovernor) UpdateActiveUserCount(count uint64, blockHeight int64) (newPValue uint64) {
	eg.mu.Lock()
	defer eg.mu.Unlock()

	eg.params.ActiveUserCount = count

	// 检查是否需要更新
	if blockHeight-eg.params.LastUpdateBlock < eg.params.UpdateInterval {
		return eg.getCurrentPValueLocked()
	}
	eg.params.LastUpdateBlock = blockHeight

	// 计算新的自适应 P 值
	targetP := eg.ComputeAdaptivePValue(count)

	// 检查是否处于过渡期
	if blockHeight >= eg.params.TransitionStart && blockHeight < eg.params.TransitionEnd {
		// 在过渡期内：线性插值
		elapsed := blockHeight - eg.params.TransitionStart
		total := eg.params.TransitionBlocks
		progress := float64(elapsed) / float64(total)

		oldP := eg.params.PValue
		newTargetP := eg.params.TargetPValue

		interpolated := uint64(float64(oldP) + (float64(newTargetP)-float64(oldP))*progress)
		eg.params.PValue = interpolated

		if blockHeight >= eg.params.TransitionEnd-1 {
			eg.params.PValue = newTargetP
			eg.params.TransitionStart = 0
			eg.params.TransitionEnd = 0
		}

		log.Printf("[economy] P-value transition: %.1f%% complete, P=%d → %d",
			progress*100, oldP, eg.params.PValue)
	} else {
		// 正常自适应更新
		oldP := eg.params.PValue
		eg.params.PValue = targetP
		if oldP != targetP {
			log.Printf("[economy] P-value adapted: %d → %d (users=%d, ratio=%.2f)",
				oldP, targetP, count, float64(count)/float64(eg.params.NBase))
		}
	}

	return eg.params.PValue
}

// getCurrentPValueLocked 获取当前 P 值（调用方必须持有锁）。
func (eg *EconomyGovernor) getCurrentPValueLocked() uint64 {
	return eg.params.PValue
}

// CurrentPValue 返回当前有效的 P 值。
func (eg *EconomyGovernor) CurrentPValue() uint64 {
	eg.mu.RLock()
	defer eg.mu.RUnlock()
	return eg.params.PValue
}

// ActiveUserCount 返回缓存的活跃用户数。
func (eg *EconomyGovernor) ActiveUserCount() uint64 {
	eg.mu.RLock()
	defer eg.mu.RUnlock()
	return eg.params.ActiveUserCount
}

// ============================================================
// 链上治理
// ============================================================

// GovernanceProposal 治理提案。
type GovernanceProposal struct {
	ProposalID    string
	Title         string
	Description   string
	Proposer      string // 提案人 chain_id
	ProposedAt    int64  // 提案区块高度

	// 参数变更
	NewPBase      uint64 // 新 P_base 值（0 表示不变）
	NewNBase      uint64 // 新 N_base 值
	ToggleAdaptive *bool  // 开关自适应

	// 投票
	VotingStart   int64  // 投票开始区块
	VotingEnd     int64  // 投票结束区块
	YesVotes      uint64 // 赞成票（按 trust_weight 加权）
	NoVotes       uint64 // 反对票
	AbstainVotes  uint64 // 弃权票

	// 阈值
	Quorum        uint64 // 法定人数（参与投票的最小 trust_weight）
	PassThreshold float64 // 通过阈值（赞成票比例，默认 0.5）

	// 状态
	Status        ProposalStatus
	ExecutedAt    int64 // 执行区块高度
}

// ProposalStatus 提案状态。
type ProposalStatus int

const (
	ProposalVoting    ProposalStatus = 0 // 投票中
	ProposalPassed    ProposalStatus = 1 // 已通过
	ProposalRejected  ProposalStatus = 2 // 已拒绝
	ProposalExecuted  ProposalStatus = 3 // 已执行
	ProposalExpired   ProposalStatus = 4 // 已过期
)

// SubmitProposal 提交治理提案。
func (eg *EconomyGovernor) SubmitProposal(
	proposerID, title, description string,
	newPBase, newNBase uint64,
	toggleAdaptive *bool,
	blockHeight int64,
) *GovernanceProposal {
	eg.mu.Lock()
	defer eg.mu.Unlock()

	proposalID := fmt.Sprintf("gov-%s-%d", proposerID[:8], blockHeight)

	// 投票期：7 天 = 100800 blocks
	votingBlocks := int64(100800)

	prop := &GovernanceProposal{
		ProposalID:     proposalID,
		Title:          title,
		Description:    description,
		Proposer:       proposerID,
		ProposedAt:     blockHeight,
		NewPBase:       newPBase,
		NewNBase:       newNBase,
		ToggleAdaptive: toggleAdaptive,
		VotingStart:    blockHeight + 14400, // 1 天后开始投票
		VotingEnd:      blockHeight + 14400 + votingBlocks,
		Quorum:         500, // 参与投票的最小 trust_weight
		PassThreshold:  0.5,
		Status:         ProposalVoting,
	}

	eg.proposals[proposalID] = prop
	log.Printf("[economy] governance proposal %s: %s by %s (P_base:%d→%d)",
		proposalID, title, proposerID[:8], eg.params.PBase, newPBase)
	return prop
}

// Vote 对提案投票。
func (eg *EconomyGovernor) Vote(proposalID, voterID string, support bool, trustWeight uint64, blockHeight int64) error {
	eg.mu.Lock()
	defer eg.mu.Unlock()

	prop, exists := eg.proposals[proposalID]
	if !exists {
		return fmt.Errorf("economy: proposal not found: %s", proposalID)
	}
	if prop.Status != ProposalVoting {
		return fmt.Errorf("economy: proposal %s not in voting phase", proposalID)
	}
	if blockHeight < prop.VotingStart || blockHeight > prop.VotingEnd {
		return fmt.Errorf("economy: voting not open for proposal %s", proposalID)
	}

	if support {
		prop.YesVotes += trustWeight
	} else {
		prop.NoVotes += trustWeight
	}

	return nil
}

// TallyProposal 计票并确定提案是否通过。
func (eg *EconomyGovernor) TallyProposal(proposalID string, blockHeight int64) bool {
	eg.mu.Lock()
	defer eg.mu.Unlock()

	prop, exists := eg.proposals[proposalID]
	if !exists {
		return false
	}

	if blockHeight <= prop.VotingEnd {
		return false // 投票尚未结束
	}

	totalVotes := prop.YesVotes + prop.NoVotes
	if totalVotes < prop.Quorum {
		prop.Status = ProposalRejected
		log.Printf("[economy] proposal %s REJECTED: quorum not met (%d < %d)",
			proposalID, totalVotes, prop.Quorum)
		return false
	}

	ratio := float64(prop.YesVotes) / float64(totalVotes)
	if ratio >= prop.PassThreshold {
		prop.Status = ProposalPassed
		log.Printf("[economy] proposal %s PASSED: yes=%d no=%d ratio=%.1f%%",
			proposalID, prop.YesVotes, prop.NoVotes, ratio*100)
		return true
	}

	prop.Status = ProposalRejected
	log.Printf("[economy] proposal %s REJECTED: yes=%d no=%d ratio=%.1f%%",
		proposalID, prop.YesVotes, prop.NoVotes, ratio*100)
	return false
}

// ExecuteProposal 执行已通过的提案。
// 启动 30 天渐变过渡期。
func (eg *EconomyGovernor) ExecuteProposal(proposalID string, blockHeight int64) error {
	eg.mu.Lock()
	defer eg.mu.Unlock()

	prop, exists := eg.proposals[proposalID]
	if !exists {
		return fmt.Errorf("economy: proposal not found: %s", proposalID)
	}
	if prop.Status != ProposalPassed {
		return fmt.Errorf("economy: proposal %s not passed", proposalID)
	}

	// 设定过渡期
	eg.params.TransitionStart = blockHeight
	eg.params.TransitionEnd = blockHeight + eg.params.TransitionBlocks

	// 记录目标值
	if prop.NewPBase > 0 {
		eg.params.TargetPValue = prop.NewPBase
	} else {
		eg.params.TargetPValue = eg.params.PValue // 不变
	}

	if prop.ToggleAdaptive != nil {
		eg.params.AdaptiveEnabled = *prop.ToggleAdaptive
	}

	prop.Status = ProposalExecuted
	prop.ExecutedAt = blockHeight

	log.Printf("[economy] proposal %s EXECUTED: transition over %d blocks (P_target=%d, adaptive=%v)",
		proposalID, eg.params.TransitionBlocks, eg.params.TargetPValue, eg.params.AdaptiveEnabled)
	return nil
}

// GetProposal 获取提案状态。
func (eg *EconomyGovernor) GetProposal(proposalID string) *GovernanceProposal {
	eg.mu.RLock()
	defer eg.mu.RUnlock()
	return eg.proposals[proposalID]
}

// ActiveProposals 返回所有活跃提案。
func (eg *EconomyGovernor) ActiveProposals() []*GovernanceProposal {
	eg.mu.RLock()
	defer eg.mu.RUnlock()
	var result []*GovernanceProposal
	for _, p := range eg.proposals {
		if p.Status == ProposalVoting {
			result = append(result, p)
		}
	}
	return result
}

// GetParams 返回经济参数快照。
func (eg *EconomyGovernor) GetParams() *EconomyParams {
	eg.mu.RLock()
	defer eg.mu.RUnlock()
	cp := *eg.params
	return &cp
}

// ============================================================
// Keeper 集成
// ============================================================

// EconomyGovernor 返回经济治理器。
func (k *Keeper) EconomyGovernor() *EconomyGovernor {
	k.initEconomyGovernor()
	return k.economy
}

var _ = types.ModuleName
