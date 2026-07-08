// Package keeper Shadow Claim ↔ Inkwell 集成桥接。
//
// 当 Inkwell 锁仓发生时自动发行影子凭证，
// 允许锁仓期间的资金以远期凭证形式在二级市场流通。
package keeper

import (
	"fmt"
	"log"

	"github.com/nekop2p/nekop2p/x/brightchain/types"
	darktypes "github.com/nekop2p/nekop2p/x/darkchain/types"
)

// ============================================================
// 集成点：贷款批准 → 影子凭证发行
// ============================================================

// IssueShadowClaimOnLoanApproval 在贷款批准时自动发行影子凭证。
//
// 调用时机: darkchain keeper 的 ApproveLoan 成功后
// 参数:
//   - loan: 已批准的贷款记录
//   - issuerID: 借款人的 chain_id（从匿名化名映射而来）
//   - lockPeriod: Inkwell 锁仓天数
func (k *Keeper) IssueShadowClaimOnLoanApproval(
	loan *darktypes.LoanRecord,
	issuerID string,
	lockPeriodDays int64,
	blockHeight int64,
) (*types.ShadowClaim, error) {
	if k.shadowClaims == nil {
		return nil, fmt.Errorf("shadow claim manager not initialized")
	}
	if loan == nil {
		return nil, fmt.Errorf("loan is nil")
	}

	// 计算释放日期
	releaseDate := loan.CreatedAt + lockPeriodDays*86400

	// 锁仓引用 = SHA256(loanID || issuerID)
	lockRef := generateLockRef(loan.LoanID, issuerID)

	// 发行影子凭证（ZK 绑定证明在完整实现中由外部提供）
	claim, err := k.shadowClaims.IssueShadowClaim(
		issuerID,
		loan.Amount,
		releaseDate,
		nil, // ZK 证明（生产环境由电路生成）
		lockRef,
		blockHeight,
	)
	if err != nil {
		return nil, fmt.Errorf("issue shadow claim on loan approval: %w", err)
	}

	log.Printf("[shadow-claim] auto-issued claim %s for loan %s: amount=%d release=%d lock=%ddays",
		claim.ClaimID, loan.LoanID[:16], claim.FaceValue,
		claim.ReleaseDate, lockPeriodDays)

	return claim, nil
}

// ============================================================
// 集成点：还款 → 影子凭证赎回
// ============================================================

// RedeemShadowClaimOnRepay 在还款时赎回到期的影子凭证。
//
// 调用时机: brightchain keeper 的 Repay 处理中
func (k *Keeper) RedeemShadowClaimOnRepay(
	claimID string,
	redeemerID string,
	nullifier [32]byte,
) (*types.ShadowClaim, error) {
	if k.shadowClaims == nil {
		return nil, fmt.Errorf("shadow claim manager not initialized")
	}

	claim, err := k.shadowClaims.RedeemShadowClaim(claimID, redeemerID, nullifier)
	if err != nil {
		return nil, fmt.Errorf("redeem shadow claim on repay: %w", err)
	}

	log.Printf("[shadow-claim] redeemed claim %s: owner=%s face=%d",
		claimID, redeemerID[:8], claim.FaceValue)
	return claim, nil
}

// ============================================================
// 查询方法
// ============================================================

// GetClaimsByOwner 获取持有者的所有影子凭证。
func (k *Keeper) GetClaimsByOwner(ownerID string) []*types.ShadowClaim {
	if k.shadowClaims == nil {
		return nil
	}
	return k.shadowClaims.GetClaimsByOwner(ownerID)
}

// GetShadowClaim 获取指定影子凭证。
func (k *Keeper) GetShadowClaim(claimID string) *types.ShadowClaim {
	if k.shadowClaims == nil {
		return nil
	}
	return k.shadowClaims.GetClaim(claimID)
}

// TransferShadowClaim 转让影子凭证。
func (k *Keeper) TransferShadowClaim(
	claimID, fromOwner, toOwner string,
	transferPrice uint64,
	blockHeight int64,
) (*types.ShadowClaim, error) {
	if k.shadowClaims == nil {
		return nil, fmt.Errorf("shadow claim manager not initialized")
	}
	return k.shadowClaims.TransferShadowClaim(claimID, fromOwner, toOwner, transferPrice, blockHeight)
}

// ============================================================
// 辅助
// ============================================================

func generateLockRef(loanID, issuerID string) []byte {
	ref := fmt.Sprintf("lock:%s:%s", loanID, issuerID)
	return []byte(ref)
}

// ShadowClaimManager 返回影子凭证管理器。
func (k *Keeper) ShadowClaimManager() *ShadowClaimManager {
	return k.shadowClaims
}
