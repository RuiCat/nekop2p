//go:build cosmos

// Package keeper 实现 Inkwell 混沌结算池状态管理 (Cosmos SDK 版本)。
//
// 核心功能：
//   - 金额拆封：总金额 → 随机碎片（Dirichlet 分布）
//   - 时间随机化：确定还款窗口和碎片调度
//   - 种子管理：commit-reveal 协议
//
// Package keeper 提供 Inkwell 状态管理器。
package keeper

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/rand"
	"time"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/nekop2p/nekop2p/x/inkwell/types"
)

// Keeper 管理 Inkwell 状态。
type Keeper struct {
	storeKey storetypes.StoreKey
	rng      *rand.Rand
}

// NewKeeper 创建 Inkwell Keeper。
func NewKeeper(_ interface{}, storeKey storetypes.StoreKey) Keeper {
	return Keeper{
		storeKey: storeKey,
		rng:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (k Keeper) StoreKey() storetypes.StoreKey { return k.storeKey }

// ============================================================
// 参数生成
// ============================================================

// GenerateParams 从种子生成混沌结算参数。
func (k *Keeper) GenerateParams(loanID string, seed [32]byte, totalAmount uint64) *types.InkwellParams {
	rng := rand.New(rand.NewSource(int64(binary.BigEndian.Uint64(seed[:8]))))

	// 锁1: 时间窗口 1-90 天
	windowStart := time.Now().Add(24 * time.Hour).Unix()
	windowEnd := windowStart + int64(rng.Intn(90*24*3600))

	// 锁2: 碎片数 3-7
	fragCount := 3 + rng.Intn(5)

	// 锁3: 金额拆封
	fragments := splitAmount(totalAmount, fragCount, rng)

	// 锁4: 来源伪装（30% 概率启用）
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

// splitAmount Dirichlet 分布拆分金额。
func splitAmount(total uint64, count int, rng *rand.Rand) []uint64 {
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
	fragments[count-1] = total - allocated
	return fragments
}

// ============================================================
// 种子 Commit-Reveal
// ============================================================

// CommitSeed 生成种子承诺。
func CommitSeed(seed, nonce []byte) []byte {
	h := sha256.New()
	h.Write(seed)
	h.Write(nonce)
	return h.Sum(nil)
}

// VerifyReveal 验证揭示的种子是否匹配承诺。
func VerifyReveal(commitment, seed, nonce []byte) bool {
	expected := CommitSeed(seed, nonce)
	return fmt.Sprintf("%x", expected) == fmt.Sprintf("%x", commitment)
}

// CombineSeeds 合并借贷双方种子。
func CombineSeeds(borrowerSeed, lenderSeed []byte) [32]byte {
	h := sha256.New()
	h.Write(borrowerSeed)
	h.Write(lenderSeed)
	return sha256.Sum256(h.Sum(nil))
}

// ============================================================
// 还款调度
// ============================================================

// GetNextFragment 返回下一个待还碎片。
func (k *Keeper) GetNextFragment(params *types.InkwellParams, paidIndexes map[int]bool) (int, uint64, error) {
	if len(params.Fragments) == 0 {
		return 0, 0, fmt.Errorf("no fragments")
	}

	// 找第一个未还碎片
	for i, amount := range params.Fragments {
		if !paidIndexes[i] && amount > 0 {
			return i, amount, nil
		}
	}
	return 0, 0, fmt.Errorf("all fragments paid")
}

// ============================================================
// BeginBlock / EndBlock
// ============================================================

// BeginBlocker 每区块开始时：检查到期还款碎片。
func (k Keeper) BeginBlocker(ctx sdk.Context) {
	// 检查活跃结算参数中的到期碎片
	height := ctx.BlockHeight()
	_ = height
	// Phase 4: 完整实现到期碎片检查和通知
}

// EndBlocker 每区块结束时：清理过期参数。
func (k Keeper) EndBlocker(ctx sdk.Context) {
	// 清理超过窗口期的结算参数
	now := ctx.BlockTime().Unix()
	_ = now
	// Phase 4: 完整实现过期参数清理
}

// InitGenesis 从创世状态初始化活跃结算参数。
func (k Keeper) InitGenesis(ctx sdk.Context, gs *types.GenesisState) {
	for _, params := range gs.ActiveParams {
		// Phase 4: 持久化活跃的结算参数
		_ = params
	}
}

// ExportGenesis 导出当前活跃结算参数。
func (k Keeper) ExportGenesis(ctx sdk.Context) *types.GenesisState {
	// Phase 4: 从存储中读取活跃参数
	return &types.GenesisState{
		ActiveParams: []*types.InkwellParams{},
	}
}
