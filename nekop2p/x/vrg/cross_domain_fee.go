// Package vrg 跨域消息费系统。
//
// 跨域消息费 (Cross-Domain Message Fee):
//   - 基础费用 + 数据费率 + ZK验证费
//   - 60% → 摆渡节点激励
//   - 25% → 源社区基金
//   - 15% → 目标社区基金
//
// Package vrg 提供跨域计费。
package vrg

import "fmt"

// ============================================================
// 跨域消息费参数
// ============================================================

// CrossDomainFeeParams 跨域消息费参数。
type CrossDomainFeeParams struct {
	BaseFee     uint64 // 基础费用 (per message)
	DataFeeRate uint64 // 数据费率 (per byte)
	VerifyFee   uint64 // ZK 验证费 (per proof)
}

// DefaultCrossDomainFeeParams 默认跨域费用参数。
func DefaultCrossDomainFeeParams() CrossDomainFeeParams {
	return CrossDomainFeeParams{
		BaseFee:     100,  // 100 uneko 基础费
		DataFeeRate: 1,    // 1 uneko/byte
		VerifyFee:   500,  // 500 uneko ZK验证费
	}
}

// ============================================================
// 费用计算
// ============================================================

// CalcCrossDomainFee 计算跨域消息费用。
func CalcCrossDomainFee(dataSize int, hasZKProof bool, params CrossDomainFeeParams) uint64 {
	fee := params.BaseFee
	fee += params.DataFeeRate * uint64(dataSize)
	if hasZKProof {
		fee += params.VerifyFee
	}
	return fee
}

// ============================================================
// 费用分账
// ============================================================

const (
	FerryNodeShare     = 60 // 摆渡节点占比 (%)
	SourceCommunityShare = 25 // 源社区占比 (%)
	TargetCommunityShare = 15 // 目标社区占比 (%)
)

// CrossDomainFeeDistribution 费用分配。
type CrossDomainFeeDistribution struct {
	TotalFee       uint64
	FerryNodeFee   uint64
	SourceCommunityFee uint64
	TargetCommunityFee uint64
}

// DistributeCrossDomainFee 分配跨域费用。
func DistributeCrossDomainFee(totalFee uint64) CrossDomainFeeDistribution {
	return CrossDomainFeeDistribution{
		TotalFee:           totalFee,
		FerryNodeFee:       totalFee * FerryNodeShare / 100,
		SourceCommunityFee: totalFee * SourceCommunityShare / 100,
		TargetCommunityFee: totalFee * TargetCommunityShare / 100,
	}
}

// Validate 验证费用分配合计=100%。
func ValidateShare() error {
	sum := FerryNodeShare + SourceCommunityShare + TargetCommunityShare
	if sum != 100 {
		return fmt.Errorf("cross-domain fee shares must sum to 100, got %d", sum)
	}
	return nil
}
