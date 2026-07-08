// Package inkwell 实现混沌结算池。
//
// Inkwell 对还款随机化三个维度，以防止
// 将暗链贷款与明链还款关联起来：
//
//   锁 1（时间）：   还款分布在 1-90 天内，3-7 个分片
//   锁 2（金额）：   总债务拆分为随机不等额的分片
//   锁 3（来源）：   分片通过其他用户中继（可选）
//
// 安全性：即使完全访问明链和暗链，
// 将还款与贷款关联也需要猜测 1/N 的概率。
package inkwell

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/big"
	"sync"
	"time"
)

// RelayPathProvider 提供中继路径选择的接口。
// 实现者应从暗链近期交易参与者中随机选择活跃的 anon_id。
type RelayPathProvider interface {
	// GetRecentAnonIDs 返回暗链近期活跃的匿名化名列表。
	// limit 指定最多返回多少个候选。
	GetRecentAnonIDs(limit int) [][32]byte
}

var (
	relayProviderMu sync.RWMutex
	relayProvider   RelayPathProvider
)

// SetRelayPathProvider 设置全局中继路径提供者。
// 应在初始化时由暗链模块调用一次。
func SetRelayPathProvider(provider RelayPathProvider) {
	relayProviderMu.Lock()
	defer relayProviderMu.Unlock()
	relayProvider = provider
}

// selectRelayPath 从暗链近期活跃 anon_id 中随机选择中继路径。
// maxHops 指定最大中继跳数（0-3），实际选择的跳数是随机的。
// rng 用于确定性随机选择，确保借贷双方计算出相同的中继路径。
func selectRelayPath(maxHops int, excludeAnonID [32]byte, rng *rngSource) [][32]byte {
	if maxHops <= 0 {
		return nil
	}

	relayProviderMu.RLock()
	provider := relayProvider
	relayProviderMu.RUnlock()

	if provider == nil {
		return nil // 没有提供者，直接还款
	}

	// 获取近期活跃 anon_id 候选列表
	candidates := provider.GetRecentAnonIDs(50)
	if len(candidates) == 0 {
		return nil
	}

	// 过滤掉借款人自身
	filtered := make([][32]byte, 0, len(candidates))
	for _, c := range candidates {
		if c != excludeAnonID {
			filtered = append(filtered, c)
		}
	}
	if len(filtered) == 0 {
		return nil
	}

	// 实际跳数：随机选择 1 到 maxHops
	hops := 1
	if maxHops > 1 {
		hops = 1 + rng.Intn(maxHops)
	}
	if hops > len(filtered) {
		hops = len(filtered)
	}

	// Fisher-Yates 洗牌选择 hops 个不重复的中继节点
	shuffled := make([][32]byte, len(filtered))
	copy(shuffled, filtered)
	for i := len(shuffled) - 1; i > 0; i-- {
		j := rng.Intn(i + 1)
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}

	return shuffled[:hops]
}

// Params 保存一笔贷款的混沌结算参数。
type Params struct {
	LoanID     [32]byte // 暗链贷款标识符
	TotalAmount uint64  // 总债务金额
	Seed       [32]byte // 双方商定的种子（借款方 + 贷款方贡献）

	// 锁 1：时间随机化
	WindowStart  time.Time // 还款窗口开始（贷款创建 + 1 天）
	WindowEnd    time.Time // 还款窗口结束
	FragmentCount int      // 还款分片数量（3-7）

	// 锁 2：金额分片
	Fragments []uint64 // 还款分片金额（总和 = TotalAmount）

	// 锁 3：来源中继（可选）
	RelayEnabled bool
	RelayPath    [][32]byte // 中继节点的 anon_id（0 到 3 个）
}

// FragmentPlan 保存一个还款分片的计划。
type FragmentPlan struct {
	Index      int       // 分片索引
	Amount     uint64    // 应还款金额
	DueAfter   time.Time // 该分片可支付的时间
	RelayVia   [32]byte  // 中继节点的 anon_id（零值 = 直接还款）
	IsPaid     bool
	PaidAt     time.Time
}

// GenerateParams 为新贷款创建混沌结算参数。
//
// seed = SHA256(borrower_seed || lender_seed)
// 这确保双方都贡献了随机性，任何一方都不能单方面预测分片计划。
//
// ⚠️ 安全要求: 借贷双方必须在调用此函数之前通过 commit-reveal 协议
// (SeedCommit/SeedReveal) 安全交换种子。直接交换种子存在最后一动者优势——
// 后出手方可以看到对方种子后选择自己的种子以操纵还款计划。
//
// 注意：now 应该是双方都认可的时间戳（如贷款创建时的区块时间戳），
// 而非本地时钟。使用本地 time.Now() 会导致双方计算出的还款窗口不一致。
//
// borrowerAnonID 用于中继路径选择时排除借款人自身。
func GenerateParams(loanID [32]byte, totalAmount uint64, borrowerSeed, lenderSeed [32]byte, now time.Time, borrowerAnonID [32]byte) (*Params, error) {
	// 合并种子
	combined := make([]byte, 64)
	copy(combined[:32], borrowerSeed[:])
	copy(combined[32:], lenderSeed[:])
	seed := sha256.Sum256(combined)

	// 从种子派生确定性随机数
	rng := newRNG(seed)

	// 锁 1：时间随机化
	windowStart := now.Add(24 * time.Hour)
	windowDays := 1 + rng.Intn(90) // 1 到 90 天
	windowEnd := windowStart.Add(time.Duration(windowDays) * 24 * time.Hour)

	fragmentCount := 3 + rng.Intn(5) // 3 到 7

	// 锁 2：金额分片
	fragments := splitAmount(totalAmount, fragmentCount, rng)

	// 锁 3：来源中继（30% 概率）
	relayEnabled := rng.Intn(100) < 30
	var relayPath [][32]byte
	if relayEnabled {
		// 自动选择中继路径（最多3跳，排除借款人自身）
		relayPath = selectRelayPath(3, borrowerAnonID, rng)
	}

	params := &Params{
		LoanID:        loanID,
		TotalAmount:   totalAmount,
		Seed:          seed,
		WindowStart:   windowStart,
		WindowEnd:     windowEnd,
		FragmentCount: fragmentCount,
		Fragments:     fragments,
		RelayEnabled:  relayEnabled,
		RelayPath:     relayPath,
	}

	return params, nil
}

// GeneratePlan 从参数创建完整的还款计划。
func (p *Params) GeneratePlan() []FragmentPlan {
	plan := make([]FragmentPlan, len(p.Fragments))
	rng := newRNG(p.Seed)

	windowDuration := p.WindowEnd.Sub(p.WindowStart)
	interval := windowDuration / time.Duration(len(p.Fragments))

	for i := range p.Fragments {
		// 在窗口内散布分片并添加抖动
		baseDelay := interval * time.Duration(i)
		jitter := time.Duration(rng.Int63n(int64(interval) / 2))
		dueAfter := p.WindowStart.Add(baseDelay + jitter)

		plan[i] = FragmentPlan{
			Index:    i,
			Amount:   p.Fragments[i],
			DueAfter: dueAfter,
		}

		// 如果启用了中继，从 GenerateParams 自动选择的中继路径中选取节点
		if p.RelayEnabled && i < len(p.RelayPath) {
			plan[i].RelayVia = p.RelayPath[i]
		}
	}

	return plan
}

// GetNextFragment 返回下一个到期未支付的分片。
func (p *Params) GetNextFragment(paidIndices []int) (*FragmentPlan, error) {
	plan := p.GeneratePlan()
	paidSet := make(map[int]bool)
	for _, idx := range paidIndices {
		paidSet[idx] = true
	}

	now := time.Now()
	var candidates []FragmentPlan
	for _, fp := range plan {
		if paidSet[fp.Index] {
			continue
		}
		if now.After(fp.DueAfter) {
			candidates = append(candidates, fp)
		}
	}

	if len(candidates) == 0 {
		// 检查是否全部付清
		if len(paidSet) == len(p.Fragments) {
			return nil, fmt.Errorf("all fragments paid")
		}
		// 最早即将到期的
		earliest := plan[0]
		for _, fp := range plan {
			if !paidSet[fp.Index] && fp.DueAfter.Before(earliest.DueAfter) {
				earliest = fp
			}
		}
		return &earliest, fmt.Errorf("no fragments due yet; next: %s", earliest.DueAfter.Format(time.RFC3339))
	}

	// 返回随机候选（使时机更不可预测）
	rng := newRNG(p.Seed)
	idx := rng.Intn(len(candidates))
	return &candidates[idx], nil
}

// RemainingAmount 返回尚未支付的总金额。
func (p *Params) RemainingAmount(paidIndices []int) uint64 {
	paidSet := make(map[int]bool)
	for _, idx := range paidIndices {
		paidSet[idx] = true
	}

	var remaining uint64
	for i, amount := range p.Fragments {
		if !paidSet[i] {
			remaining += amount
		}
	}
	return remaining
}

// splitAmount 将总金额拆分为 count 个总和等于 total 的随机分片。
// 使用基于随机权重的归一化方法，确保总和精确且无零分片。
func splitAmount(total uint64, count int, rng *rngSource) []uint64 {
	if count <= 0 {
		return nil
	}
	if count == 1 {
		return []uint64{total}
	}
	if total == 0 {
		fragments := make([]uint64, count)
		return fragments
	}

	// 为每个分片生成随机权重 (1~1000)
	weights := make([]uint64, count)
	var sumWeights uint64
	for i := range weights {
		weights[i] = uint64(rng.Int63n(1000)) + 1 // 避免零权重
		sumWeights += weights[i]
	}

	// 按权重分配
	fragments := make([]uint64, count)
	var allocated uint64
	for i := 0; i < count; i++ {
		fragments[i] = total * weights[i] / sumWeights
		allocated += fragments[i]
	}

	// 将舍入误差加入最后一个分片 (保持总和精确)
	if total > allocated {
		fragments[count-1] += total - allocated
	}

	// 确保没有零分片 (极小金额情况)
	for i := range fragments {
		if fragments[i] == 0 && total > uint64(count) {
			fragments[i] = 1
			// 从最大分片借1
			maxIdx := count - 1
			for j := range fragments {
				if fragments[j] > fragments[maxIdx] {
					maxIdx = j
				}
			}
			if maxIdx != i && fragments[maxIdx] > 1 {
				fragments[maxIdx]--
			}
		}
	}

	return fragments
}

// rngSource 是用 SHA256 作为种子的确定性随机数生成器。
type rngSource struct {
	state [32]byte
	counter uint64
}

func newRNG(seed [32]byte) *rngSource {
	return &rngSource{state: seed}
}

func (r *rngSource) next() []byte {
	r.counter++
	input := make([]byte, 40)
	copy(input[:32], r.state[:])
	binary.BigEndian.PutUint64(input[32:], r.counter)
	hash := sha256.Sum256(input)
	return hash[:]
}

func (r *rngSource) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	b := r.next()
	bi := new(big.Int).SetBytes(b)
	bi.Mod(bi, big.NewInt(int64(n)))
	return int(bi.Int64())
}

func (r *rngSource) Int63n(n int64) int64 {
	if n <= 0 {
		return 0
	}
	b := r.next()
	bi := new(big.Int).SetBytes(b)
	bi.Mod(bi, big.NewInt(n))
	return bi.Int64()
}

// --- 确定性随机数生成器（基于 SHA256）---
// 种子由借贷双方协商，确保混沌参数可复现。

// ============================================================
// 种子协商协议 (Commit-Reveal)
//
// 借贷双方通过 commit-reveal 安全交换种子，防止最后一动者优势：
//   1. 双方各生成 (seed, nonce)
//   2. 双方交换 commit = SHA256(seed || nonce)
//   3. 收到对方 commit 后，双方揭示 (seed, nonce)
//   4. 验证对方揭示与承诺匹配
//   5. 合并种子 = SHA256(b_seed || l_seed)
// ============================================================

// SeedCommit 是种子协商的承诺阶段。
// commit = SHA256(seed || party_id || nonce) 绑定到特定参与者。
func SeedCommit(seed, nonce [32]byte, partyID string) [32]byte {
	h := sha256.New()
	h.Write(seed[:])
	h.Write([]byte(partyID))
	h.Write(nonce[:])
	return [32]byte(h.Sum(nil))
}

// SeedReveal 验证揭示的种子是否匹配之前的承诺。
// 返回 true 如果 SHA256(seed || party_id || nonce) == commit。
func SeedReveal(commit, seed, nonce [32]byte, partyID string) bool {
	computed := SeedCommit(seed, nonce, partyID)
	return computed == commit
}

// CombineSeeds 合并双方种子生成最终混沌参数种子。
// combined = SHA256(borrower_seed || lender_seed)
// 此函数应在双方完成 commit-reveal 后调用。
func CombineSeeds(borrowerSeed, lenderSeed [32]byte) [32]byte {
	combined := make([]byte, 64)
	copy(combined[:32], borrowerSeed[:])
	copy(combined[32:], lenderSeed[:])
	return sha256.Sum256(combined)
}
