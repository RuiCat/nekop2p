//go:build cosmos

// Package keeper 游戏经济参数管理 — 自适应汇率系统。
//
// 汇率模型:
//   - 储备率 = TotalGoldReserve / TotalGoldInCirculation
//   - 汇率 = 基础汇率 × (0.8 + 0.4 × 储备率)
//   - 储备率高 → 汇率稳定 (买入价≈卖出价)
//   - 储备率低 → 提现汇率降低 (防止挤兑)
//
// Package keeper 提供汇率管理。
package keeper

// ============================================================
// 汇率参数
// ============================================================

const (
	DefaultGoldPerRecharge  = 100.0  // 默认买入汇率: 1 uneko = 100 金币
	DefaultGoldPerWithdraw  = 100.0  // 默认卖出汇率: 100 金币 = 1 uneko
	DefaultSpreadRate       = 0.005  // 买卖价差: 0.5%
	DefaultReserveRatio     = 1.0    // 初始储备率: 100% 背书
	MinReserveRatio         = 0.5    // 最低储备率: 50% (触发汇率调整)
)

// ExchangeParams 汇率参数 (治理可调)。
type ExchangeParams struct {
	GoldPerRecharge float64 `json:"gold_per_recharge"` // 买入汇率
	GoldPerWithdraw float64 `json:"gold_per_withdraw"` // 卖出汇率
	SpreadRate      float64 `json:"spread_rate"`       // 买卖价差
	ReserveRatio    float64 `json:"reserve_ratio"`     // 目标储备率
	AdaptiveEnabled bool    `json:"adaptive_enabled"`  // 启用自适应汇率
}

// DefaultExchangeParams 默认汇率参数。
func DefaultExchangeParams() ExchangeParams {
	return ExchangeParams{
		GoldPerRecharge: DefaultGoldPerRecharge,
		GoldPerWithdraw: DefaultGoldPerWithdraw,
		SpreadRate:      DefaultSpreadRate,
		ReserveRatio:    DefaultReserveRatio,
		AdaptiveEnabled: false,
	}
}

// ============================================================
// 自适应汇率计算
// ============================================================

// CalcAdaptiveRechargeRate 计算自适应买入汇率。
// 储备率越高，买入汇率越接近基准值。
func CalcAdaptiveRechargeRate(reserve, circulation uint64, params ExchangeParams) float64 {
	if !params.AdaptiveEnabled || circulation == 0 {
		return params.GoldPerRecharge
	}
	reserveRatio := float64(reserve) / float64(circulation)
	// 买入汇率: 储备率低时略降 (吸引充值)
	return params.GoldPerRecharge * (0.9 + 0.2*reserveRatio)
}

// CalcAdaptiveWithdrawRate 计算自适应卖出汇率。
// 储备率低时降低汇率防止挤兑。
func CalcAdaptiveWithdrawRate(reserve, circulation uint64, params ExchangeParams) float64 {
	if !params.AdaptiveEnabled || circulation == 0 {
		return params.GoldPerWithdraw
	}
	reserveRatio := float64(reserve) / float64(circulation)
	// 卖出汇率: 储备率低时显著降低
	rate := params.GoldPerWithdraw * (0.8 + 0.4*reserveRatio)
	if rate < params.GoldPerWithdraw*0.5 {
		rate = params.GoldPerWithdraw * 0.5 // 最低不低于50%
	}
	return rate
}

// ============================================================
// 汇率操作
// ============================================================

// RechargeGold 充值: uneko → 金币。
func RechargeGold(unekoAmount uint64, params ExchangeParams, reserve, circulation uint64) uint64 {
	rate := CalcAdaptiveRechargeRate(reserve, circulation, params)
	return uint64(float64(unekoAmount) * rate)
}

// WithdrawGold 提现: 金币 → uneko。
func WithdrawGold(goldAmount uint64, params ExchangeParams, reserve, circulation uint64) uint64 {
	rate := CalcAdaptiveWithdrawRate(reserve, circulation, params)
	// 扣除买卖价差
	effectiveRate := rate * (1.0 - params.SpreadRate)
	return uint64(float64(goldAmount) / effectiveRate)
}
