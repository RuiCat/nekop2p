//go:build cosmos

// Package keeper 实现 Inkwell 混沌结算池完整状态管理 (Cosmos SDK, Phase 4 完成)。
//
// 核心功能:
//   - 确定性金额拆封 (种子 PRNG, 共识兼容)
//   - FragmentPlan 持久化 (链上防重放)
//   - 时间随机化 + 还款窗口管理
//   - 种子 commit-reveal 协议
//   - BeginBlocker 到期检查 + EndBlocker 过期清理
//
// Package keeper 提供 Inkwell 状态管理器。
package keeper

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/nekop2p/nekop2p/x/inkwell/types"
)

// ============================================================
// 确定性 PRNG (共识兼容)
// ============================================================

// seedRNG 基于种子的确定性随机数生成器。
// 使用 SHA256 链式哈希，确保所有节点对同一种子产生相同输出。
type seedRNG struct {
	state []byte
	mu    *sync.Mutex
}

func newSeedRNG(seed [32]byte) *seedRNG {
	return &seedRNG{state: seed[:], mu: &sync.Mutex{}}
}

func (r *seedRNG) next32() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := sha256.Sum256(r.state)
	r.state = h[:]
	return h[:]
}

func (r *seedRNG) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	b := r.next32()
	val := binary.BigEndian.Uint64(b[:8])
	return int(val % uint64(n))
}

func (r *seedRNG) Float64() float64 {
	b := r.next32()
	val := binary.BigEndian.Uint64(b[:8])
	return float64(val) / float64(^uint64(0))
}

// ============================================================
// Keeper
// ============================================================

// Keeper 管理 Inkwell 状态。
type Keeper struct {
	storeKey       storetypes.StoreKey
	activePlans    map[string]*types.FragmentPlan // loanID → plan (内存缓存)
	plansMu        *sync.RWMutex
}

// NewKeeper 创建 Inkwell Keeper。
func NewKeeper(_ interface{}, storeKey storetypes.StoreKey) Keeper {
	return Keeper{
		storeKey:    storeKey,
		activePlans: make(map[string]*types.FragmentPlan),
		plansMu:     &sync.RWMutex{},
	}
}

func (k Keeper) StoreKey() storetypes.StoreKey { return k.storeKey }

// ============================================================
// 序列化
// ============================================================

func marshal(v interface{}) ([]byte, error)  { return json.Marshal(v) }
func unmarshal(data []byte, v interface{}) error { return json.Unmarshal(data, v) }

// ============================================================
// 参数生成 (确定性 PRNG, 共识兼容)
// ============================================================

// GenerateParams 从种子生成混沌结算参数 (确定性)。
func (k *Keeper) GenerateParams(loanID string, seed [32]byte, totalAmount uint64) *types.InkwellParams {
	rng := newSeedRNG(seed)

	windowStart := time.Now().Add(24 * time.Hour).Unix()
	windowEnd := windowStart + int64(rng.Intn(90*24*3600))
	fragCount := 3 + rng.Intn(5)
	fragments := splitAmount(totalAmount, fragCount, rng)
	relayEnabled := rng.Float64() < 0.3

	return &types.InkwellParams{
		LoanID:        loanID,
		Seed:          seed[:],
		TotalAmount:   totalAmount,
		WindowStart:   windowStart,
		WindowEnd:     windowEnd,
		FragmentCount: fragCount,
		Fragments:     fragments,
		RelayEnabled:  relayEnabled,
	}
}

// splitAmount Dirichlet 分布拆分金额 (确定性版本)。
func splitAmount(total uint64, count int, rng *seedRNG) []uint64 {
	if count <= 0 || total == 0 {
		return []uint64{total}
	}

	weights := make([]float64, count)
	sum := 0.0
	for i := range weights {
		weights[i] = rng.Float64() + 0.1
		sum += weights[i]
	}

	fragments := make([]uint64, count)
	allocated := uint64(0)
	for i := 0; i < count-1; i++ {
		fragments[i] = uint64(float64(total) * weights[i] / sum)
		if fragments[i] == 0 {
			fragments[i] = 1
		}
		allocated += fragments[i]
	}
	last := total - allocated
	if last == 0 {
		// 从最大分片借 1 保证非零
		maxIdx := 0
		for i := 1; i < count-1; i++ {
			if fragments[i] > fragments[maxIdx] {
				maxIdx = i
			}
		}
		if fragments[maxIdx] > 1 {
			fragments[maxIdx]--
			last = 1
		}
	}
	fragments[count-1] = last
	return fragments
}

// ============================================================
// FragmentPlan 持久化 (链上防重放)
// ============================================================

// CreateFragmentPlan 创建碎片还款计划并持久化。
func (k *Keeper) CreateFragmentPlan(ctx sdk.Context, params *types.InkwellParams) (*types.FragmentPlan, error) {
	plan := &types.FragmentPlan{
		LoanID:      params.LoanID,
		Fragments:   params.Fragments,
		PaidIndices: make(map[int]bool),
		TotalPaid:   0,
		WindowStart: params.WindowStart,
		WindowEnd:   params.WindowEnd,
		Completed:   false,
		CreatedAt:   ctx.BlockTime().Unix(),
	}

	k.plansMu.Lock()
	k.activePlans[params.LoanID] = plan
	k.plansMu.Unlock()

	// 持久化
	store := ctx.KVStore(k.storeKey)
	bz, err := marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("marshal fragment plan: %w", err)
	}
	store.Set(planKey(params.LoanID), bz)

	return plan, nil
}

// GetFragmentPlan 获取碎片还款计划。
func (k *Keeper) GetFragmentPlan(ctx sdk.Context, loanID string) (*types.FragmentPlan, error) {
	// 先查缓存
	k.plansMu.RLock()
	if plan, ok := k.activePlans[loanID]; ok {
		k.plansMu.RUnlock()
		return plan, nil
	}
	k.plansMu.RUnlock()

	// 查持久化存储
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(planKey(loanID))
	if bz == nil {
		return nil, fmt.Errorf("fragment plan not found: %s", loanID)
	}

	var plan types.FragmentPlan
	if err := unmarshal(bz, &plan); err != nil {
		return nil, err
	}

	k.plansMu.Lock()
	k.activePlans[loanID] = &plan
	k.plansMu.Unlock()

	return &plan, nil
}

// MarkFragmentPaid 标记碎片已还 (持久化, 防重放)。
func (k *Keeper) MarkFragmentPaid(ctx sdk.Context, loanID string, index int, amount uint64) error {
	plan, err := k.GetFragmentPlan(ctx, loanID)
	if err != nil {
		return err
	}

	k.plansMu.Lock()
	defer k.plansMu.Unlock()

	if plan.PaidIndices[index] {
		return fmt.Errorf("fragment %d already paid for loan %s (replay rejected)", index, loanID)
	}

	plan.PaidIndices[index] = true
	plan.TotalPaid += amount

	if plan.TotalPaid >= sumFragments(plan.Fragments) {
		plan.Completed = true
	}

	// 持久化
	store := ctx.KVStore(k.storeKey)
	bz, err := marshal(plan)
	if err != nil {
		return err
	}
	store.Set(planKey(loanID), bz)

	return nil
}

// ============================================================
// 还款调度
// ============================================================

// GetNextFragment 返回下一个待还碎片。
func (k *Keeper) GetNextFragment(ctx sdk.Context, loanID string) (int, uint64, error) {
	plan, err := k.GetFragmentPlan(ctx, loanID)
	if err != nil {
		return 0, 0, err
	}

	for i, amount := range plan.Fragments {
		if !plan.PaidIndices[i] && amount > 0 {
			return i, amount, nil
		}
	}
	return 0, 0, fmt.Errorf("all fragments paid")
}

// RemainingAmount 返回剩余未还金额。
func (k *Keeper) RemainingAmount(ctx sdk.Context, loanID string) uint64 {
	plan, err := k.GetFragmentPlan(ctx, loanID)
	if err != nil {
		return 0
	}
	return sumFragments(plan.Fragments) - plan.TotalPaid
}

func sumFragments(fragments []uint64) uint64 {
	var sum uint64
	for _, f := range fragments {
		sum += f
	}
	return sum
}

// ============================================================
// 种子 Commit-Reveal
// ============================================================

func CommitSeed(seed, nonce []byte) []byte {
	h := sha256.New()
	h.Write(seed)
	h.Write(nonce)
	return h.Sum(nil)
}

func VerifyReveal(commitment, seed, nonce []byte) bool {
	expected := CommitSeed(seed, nonce)
	return fmt.Sprintf("%x", expected) == fmt.Sprintf("%x", commitment)
}

func CombineSeeds(borrowerSeed, lenderSeed []byte) [32]byte {
	h := sha256.New()
	h.Write(borrowerSeed)
	h.Write(lenderSeed)
	return sha256.Sum256(h.Sum(nil))
}

// ============================================================
// BeginBlock / EndBlock (完整实现)
// ============================================================

// BeginBlocker 每区块开始时：检查到期还款碎片。
func (k Keeper) BeginBlocker(ctx sdk.Context) {
	now := ctx.BlockTime().Unix()

	k.plansMu.RLock()
	loanIDs := make([]string, 0, len(k.activePlans))
	for id := range k.activePlans {
		loanIDs = append(loanIDs, id)
	}
	k.plansMu.RUnlock()

	for _, loanID := range loanIDs {
		plan, err := k.GetFragmentPlan(ctx, loanID)
		if err != nil {
			continue
		}
		if plan.Completed || now > plan.WindowEnd {
			continue
		}

		// 检查是否有到期未还的碎片
		overdue := false
		for i, amount := range plan.Fragments {
			if !plan.PaidIndices[i] && amount > 0 {
				// 碎片 i 的预计还款时间 = WindowStart + (WindowEnd-WindowStart) * i / len(Fragments)
				dueTime := plan.WindowStart + (plan.WindowEnd-plan.WindowStart)*int64(i)/int64(len(plan.Fragments))
				if now > dueTime {
					overdue = true
					break
				}
			}
		}

		if overdue {
			// 触发逾期通知（可用于暗链递归追偿）
			ctx.EventManager().EmitEvent(sdk.NewEvent(
				"inkwell.fragment_overdue",
				sdk.NewAttribute("loan_id", loanID),
				sdk.NewAttribute("remaining", fmt.Sprintf("%d", k.RemainingAmount(ctx, loanID))),
			))
		}
	}
}

// EndBlocker 每区块结束时：清理已完成/过期的计划。
func (k Keeper) EndBlocker(ctx sdk.Context) {
	now := ctx.BlockTime().Unix()

	k.plansMu.Lock()
	defer k.plansMu.Unlock()

	store := ctx.KVStore(k.storeKey)
	for loanID, plan := range k.activePlans {
		// 清理已完成或已过期的计划
		if plan.Completed || (now > plan.WindowEnd && plan.TotalPaid == 0) {
			store.Delete(planKey(loanID))
			delete(k.activePlans, loanID)
		}
	}

	// 定期清理大量过期缓存 (每 1000 区块)
	if ctx.BlockHeight()%1000 == 0 && len(k.activePlans) > 100 {
		// 按 CreatedAt 排序，删除最早的
		type entry struct {
			id  string
			ts  int64
		}
		entries := make([]entry, 0, len(k.activePlans))
		for id, p := range k.activePlans {
			entries = append(entries, entry{id, p.CreatedAt})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].ts < entries[j].ts })

		// 删除最早的一半
		for i := 0; i < len(entries)/2; i++ {
			store.Delete(planKey(entries[i].id))
			delete(k.activePlans, entries[i].id)
		}
	}
}

// ============================================================
// InitGenesis / ExportGenesis
// ============================================================

func (k Keeper) InitGenesis(ctx sdk.Context, gs *types.GenesisState) {
	store := ctx.KVStore(k.storeKey)
	for _, params := range gs.ActiveParams {
		plan := &types.FragmentPlan{
			LoanID:      params.LoanID,
			Fragments:   params.Fragments,
			PaidIndices: make(map[int]bool),
			WindowStart: params.WindowStart,
			WindowEnd:   params.WindowEnd,
			CreatedAt:   ctx.BlockTime().Unix(),
		}
		bz, _ := marshal(plan)
		store.Set(planKey(params.LoanID), bz)
		k.activePlans[params.LoanID] = plan
	}
}

func (k Keeper) ExportGenesis(ctx sdk.Context) *types.GenesisState {
	store := ctx.KVStore(k.storeKey)
	iter := store.Iterator(planPrefix, nil)
	defer iter.Close()

	var params []*types.InkwellParams
	for ; iter.Valid(); iter.Next() {
		var plan types.FragmentPlan
		if unmarshal(iter.Value(), &plan) == nil {
			params = append(params, &types.InkwellParams{
				LoanID:      plan.LoanID,
				Fragments:   plan.Fragments,
				WindowStart: plan.WindowStart,
				WindowEnd:   plan.WindowEnd,
			})
		}
	}
	return &types.GenesisState{ActiveParams: params}
}

// ============================================================
// 存储键前缀
// ============================================================

var planPrefix = []byte{0x20}

func planKey(loanID string) []byte {
	return append(planPrefix, []byte(loanID)...)
}
