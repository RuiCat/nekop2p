//go:build cosmos

// Package keeper 代币 Staking 机制 (Cosmos SDK 版本)。
//
// Staking 系统:
//   - 质押 uneko 获得分红权
//   - 分红来源: 节点工资池 + 利息收益
//   - 锁仓期: 最短30天, 最长365天 (更长=更高分红比例)
//   - 年化收益: ~5-15% (取决于总质押量和网络收入)
//
// Package keeper 提供 Staking 管理。
package keeper

import (
	"fmt"
	"sync"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// ============================================================
// Staking 参数
// ============================================================

const (
	MinStakeAmount    = 100   // 最低质押金额
	MinLockDays       = 30    // 最短锁仓天数
	MaxLockDays       = 365   // 最长锁仓天数
	StakingRewardPool = "staking_reward" // 分红池标识
)

// StakeRecord 质押记录。
type StakeRecord struct {
	Staker      string // 质押者 chain_id
	Amount      uint64 // 质押金额
	LockedUntil int64  // 锁仓到期时间 (unix)
	Share       uint64 // 分红权重 (基于 金额×锁仓系数)
	CreatedAt   int64  // 创建时间
}

// StakingState 全局 Staking 状态。
type StakingState struct {
	mu             sync.RWMutex
	Stakes         map[string]*StakeRecord // staker → record
	TotalStaked    uint64                  // 总质押量
	TotalShares    uint64                  // 总分红权重
	RewardPool     uint64                  // 待分配分红池
	LastRewardCalc int64                   // 上次分红计算时间
}

// NewStakingState 创建 Staking 状态。
func NewStakingState() *StakingState {
	return &StakingState{
		Stakes: make(map[string]*StakeRecord),
	}
}

// ============================================================
// Staking 操作
// ============================================================

// Stake 质押代币。
func (ss *StakingState) Stake(staker string, amount uint64, lockDays int64, now int64) error {
	if amount < MinStakeAmount {
		return fmt.Errorf("stake: minimum amount is %d, got %d", MinStakeAmount, amount)
	}
	if lockDays < MinLockDays || lockDays > MaxLockDays {
		return fmt.Errorf("stake: lock days must be %d-%d, got %d", MinLockDays, MaxLockDays, lockDays)
	}

	ss.mu.Lock()
	defer ss.mu.Unlock()

	// 如果已有质押，追加
	if existing, ok := ss.Stakes[staker]; ok {
		existing.Amount += amount
		existing.LockedUntil = max(existing.LockedUntil, now+lockDays*86400)
		newShare := amount * uint64(lockDays) / MinLockDays
		existing.Share += newShare
		ss.TotalStaked += amount
		ss.TotalShares += newShare
		return nil
	}

	// 新质押: 分红权重 = 金额 × 锁仓系数 (锁仓天数/30)
	share := amount * uint64(lockDays) / MinLockDays
	ss.Stakes[staker] = &StakeRecord{
		Staker:      staker,
		Amount:      amount,
		LockedUntil: now + lockDays*86400,
		Share:       share,
		CreatedAt:   now,
	}
	ss.TotalStaked += amount
	ss.TotalShares += share
	return nil
}

// Unstake 赎回质押。
func (ss *StakingState) Unstake(staker string, amount uint64, now int64) (uint64, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	record, ok := ss.Stakes[staker]
	if !ok {
		return 0, fmt.Errorf("stake: no stake for %s", staker)
	}
	if now < record.LockedUntil {
		return 0, fmt.Errorf("stake: locked until %d (now=%d)", record.LockedUntil, now)
	}
	if amount > record.Amount {
		amount = record.Amount
	}

	shareReduction := amount * record.Share / record.Amount
	record.Amount -= amount
	record.Share -= shareReduction
	ss.TotalStaked -= amount
	ss.TotalShares -= shareReduction

	if record.Amount == 0 {
		delete(ss.Stakes, staker)
	}

	return amount, nil
}

// ClaimReward 领取分红。
func (ss *StakingState) ClaimReward(staker string) (uint64, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	record, ok := ss.Stakes[staker]
	if !ok || record.Share == 0 || ss.TotalShares == 0 {
		return 0, nil
	}

	reward := ss.RewardPool * record.Share / ss.TotalShares
	if reward == 0 {
		return 0, nil
	}

	ss.RewardPool -= reward
	return reward, nil
}

// AddToRewardPool 向分红池添加资金。
func (ss *StakingState) AddToRewardPool(amount uint64) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.RewardPool += amount
}

// GetStakeInfo 获取质押信息。
func (ss *StakingState) GetStakeInfo(staker string) *StakeRecord {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	return ss.Stakes[staker]
}

// ============================================================
// Keeper 集成
// ============================================================

// InitStaking 初始化 Staking 系统。
func (k *Keeper) InitStaking() {
	if k.staking == nil {
		k.staking = NewStakingState()
	}
}

// Stake 代理质押操作。
func (k *Keeper) Stake(ctx sdk.Context, staker string, amount uint64, lockDays int64) error {
	if k.staking == nil {
		k.InitStaking()
	}
	// 从用户余额中扣除质押金额
	user, err := k.GetUser(ctx, []byte(staker))
	if err != nil {
		return err
	}
	if user.CreditLimit < amount {
		return fmt.Errorf("stake: insufficient credit limit (%d < %d)", user.CreditLimit, amount)
	}
	user.CreditLimit -= amount
	if err := k.SetUser(ctx, user); err != nil {
		return err
	}
	return k.staking.Stake(staker, amount, lockDays, ctx.BlockTime().Unix())
}

// Unstake 代理赎回操作。
func (k *Keeper) Unstake(ctx sdk.Context, staker string, amount uint64) (uint64, error) {
	if k.staking == nil {
		return 0, fmt.Errorf("stake: staking not initialized")
	}
	unstaked, err := k.staking.Unstake(staker, amount, ctx.BlockTime().Unix())
	if err != nil {
		return 0, err
	}
	// 返还到用户余额
	user, _ := k.GetUser(ctx, []byte(staker))
	if user != nil {
		user.CreditLimit += unstaked
		k.SetUser(ctx, user)
	}
	return unstaked, nil
}

// ProcessStakingRewards 处理 Staking 分红 (EndBlocker 中每月调用)。
func (k *Keeper) ProcessStakingRewards(ctx sdk.Context) {
	if k.staking == nil || k.staking.TotalStaked == 0 {
		return
	}

	// 分红来源: 节点工资池的 10%
	pool := k.GetPool(ctx)
	rewardBudget := (pool.SalaryRelay + pool.SalaryRecord) * 10 / 100
	if rewardBudget == 0 {
		return
	}

	// 从工资池扣除
	if pool.SalaryRelay >= rewardBudget/2 {
		pool.SalaryRelay -= rewardBudget / 2
	}
	if pool.SalaryRecord >= rewardBudget/2 {
		pool.SalaryRecord -= rewardBudget / 2
	}
	k.SetPool(ctx, pool)

	// 注入分红池
	k.staking.AddToRewardPool(rewardBudget)
}

// 辅助
func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
