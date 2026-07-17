//go:build !cosmos

// Package keeper Shadow Claims（影子凭证）二级市场实现。
//
// 允许 Inkwell 锁仓期间的远期债权作为可转让凭证在明域流通，
// 释放锁仓资金效率，买卖双方各取所需。
package keeper

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nekop2p/nekop2p/x/brightchain/types"
)

// ShadowClaimManager 管理影子凭证的生命周期。
type ShadowClaimManager struct {
	mu        sync.RWMutex
	claims    map[string]*types.ShadowClaim // claimID → claim
	byOwner   map[string][]string           // ownerID → claimIDs (索引)
	nullifiers map[string]bool               // nullifier → spent
}

// NewShadowClaimManager 创建影子凭证管理器。
func NewShadowClaimManager() *ShadowClaimManager {
	return &ShadowClaimManager{
		claims:     make(map[string]*types.ShadowClaim),
		byOwner:    make(map[string][]string),
		nullifiers: make(map[string]bool),
	}
}

// ============================================================
// 发行
// ============================================================

// IssueShadowClaim 发行一张新的影子凭证。
// 约束:
//   - 同一锁仓引用最多 1 张活跃凭证
//   - ZK 绑定证明验证（由调用方在发行前完成）
//   - 释放日期必须在未来
func (scm *ShadowClaimManager) IssueShadowClaim(
	issuerID string,
	faceValue uint64,
	releaseDate int64,
	zkProof []byte,
	lockRef []byte,
	blockHeight int64,
) (*types.ShadowClaim, error) {
	scm.mu.Lock()
	defer scm.mu.Unlock()

	// 检查：同一锁仓是否已有活跃凭证
	for _, claim := range scm.claims {
		if claim.Status == types.ShadowClaimActive &&
			string(claim.LockReference) == string(lockRef) {
			return nil, fmt.Errorf("shadow claim: lock already has active claim %s", claim.ClaimID)
		}
	}

	// 检查：释放日期必须在未来
	now := time.Now().Unix()
	if releaseDate <= now {
		return nil, fmt.Errorf("shadow claim: release date %d is in the past", releaseDate)
	}

	// 检查：面值必须 > 0
	if faceValue == 0 {
		return nil, fmt.Errorf("shadow claim: face value must be > 0")
	}

	claimID := fmt.Sprintf("sc-%s-%d", safePrefix(issuerID, 8), blockHeight)
	claim := &types.ShadowClaim{
		ClaimID:        claimID,
		IssuerID:       issuerID,
		OwnerID:        issuerID, // 初始持有者 = 发行者
		FaceValue:      faceValue,
		ReleaseDate:    releaseDate,
		IssuedAt:       blockHeight,
		TransferCount:  0,
		ZkBindingProof: zkProof,
		LockReference:  lockRef,
		Status:         types.ShadowClaimActive,
	}

	scm.claims[claimID] = claim
	scm.byOwner[issuerID] = append(scm.byOwner[issuerID], claimID)

	log.Printf("[shadow-claim] issued %s: issuer=%s face=%d release=%s",
		claimID, safePrefix(issuerID, 8), faceValue,
		time.Unix(releaseDate, 0).Format("2006-01-02"))
	return claim, nil
}

// ============================================================
// 转让
// ============================================================

// TransferShadowClaim 转让影子凭证所有权。
// 约束:
//   - 凭证必须为活跃状态
//   - 转出方必须是当前持有者
//   - 转入方不能是当前持有者
func (scm *ShadowClaimManager) TransferShadowClaim(
	claimID, fromOwner, toOwner string,
	transferPrice uint64,
	blockHeight int64,
) (*types.ShadowClaim, error) {
	scm.mu.Lock()
	defer scm.mu.Unlock()

	claim, exists := scm.claims[claimID]
	if !exists {
		return nil, fmt.Errorf("shadow claim: claim not found: %s", claimID)
	}
	if claim.Status != types.ShadowClaimActive {
		return nil, fmt.Errorf("shadow claim: claim %s is %s, not active", claimID, claim.Status)
	}
	if claim.OwnerID != fromOwner {
		return nil, fmt.Errorf("shadow claim: %s not owner of %s", fromOwner, claimID)
	}
	if fromOwner == toOwner {
		return nil, fmt.Errorf("shadow claim: cannot transfer to self")
	}

	// 更新所有者
	oldOwner := claim.OwnerID
	claim.OwnerID = toOwner
	claim.TransferredAt = blockHeight
	claim.TransferCount++

	// 更新索引
	scm.removeOwnerClaim(oldOwner, claimID)
	scm.byOwner[toOwner] = append(scm.byOwner[toOwner], claimID)

	log.Printf("[shadow-claim] transferred %s: %s→%s price=%d (transfer #%d)",
		claimID, safePrefix(oldOwner, 8), safePrefix(toOwner, 8),
		transferPrice, claim.TransferCount)
	return claim, nil
}

// ============================================================
// 赎回
// ============================================================

// RedeemShadowClaim 赎回影子凭证（释放日期到达后）。
// 约束:
//   - 凭证必须为活跃状态
//   - 当前时间 >= 释放日期
//   - nullifier 未被使用
func (scm *ShadowClaimManager) RedeemShadowClaim(
	claimID, redeemerID string,
	nullifier [32]byte,
) (*types.ShadowClaim, error) {
	scm.mu.Lock()
	defer scm.mu.Unlock()

	claim, exists := scm.claims[claimID]
	if !exists {
		return nil, fmt.Errorf("shadow claim: claim not found: %s", claimID)
	}
	if claim.Status != types.ShadowClaimActive {
		return nil, fmt.Errorf("shadow claim: claim %s is %s", claimID, claim.Status)
	}
	if claim.OwnerID != redeemerID {
		return nil, fmt.Errorf("shadow claim: %s not owner of %s", redeemerID, claimID)
	}

	// 检查释放日期
	now := time.Now().Unix()
	if now < claim.ReleaseDate {
		remaining := time.Duration(claim.ReleaseDate-now) * time.Second
		return nil, fmt.Errorf("shadow claim: cannot redeem before release date (%s remaining)",
			remaining.String())
	}

	// 检查 nullifier
	nullifierKey := fmt.Sprintf("%x", nullifier[:])
	if scm.nullifiers[nullifierKey] {
		return nil, fmt.Errorf("shadow claim: nullifier already spent for %s", claimID)
	}

	scm.nullifiers[nullifierKey] = true
	claim.Status = types.ShadowClaimRedeemed

	log.Printf("[shadow-claim] redeemed %s: owner=%s face=%d",
		claimID, safePrefix(redeemerID, 8), claim.FaceValue)
	return claim, nil
}

// ============================================================
// 查询
// ============================================================

// GetClaim 获取凭证。
func (scm *ShadowClaimManager) GetClaim(claimID string) *types.ShadowClaim {
	scm.mu.RLock()
	defer scm.mu.RUnlock()
	return scm.claims[claimID]
}

// GetClaimsByOwner 获取持有者的所有凭证。
func (scm *ShadowClaimManager) GetClaimsByOwner(ownerID string) []*types.ShadowClaim {
	scm.mu.RLock()
	defer scm.mu.RUnlock()
	var result []*types.ShadowClaim
	for _, claimID := range scm.byOwner[ownerID] {
		if claim, ok := scm.claims[claimID]; ok {
			result = append(result, claim)
		}
	}
	return result
}

// ActiveClaims 返回所有活跃凭证。
func (scm *ShadowClaimManager) ActiveClaims() []*types.ShadowClaim {
	scm.mu.RLock()
	defer scm.mu.RUnlock()
	var result []*types.ShadowClaim
	for _, claim := range scm.claims {
		if claim.Status == types.ShadowClaimActive {
			result = append(result, claim)
		}
	}
	return result
}

// ExpireClaims 将过期的活跃凭证标记为过期。
// 应在每个区块的 BeginBlocker 中调用。
func (scm *ShadowClaimManager) ExpireClaims() int {
	scm.mu.Lock()
	defer scm.mu.Unlock()
	now := time.Now().Unix()
	expired := 0
	for _, claim := range scm.claims {
		if claim.Status == types.ShadowClaimActive && now > claim.ReleaseDate+86400*7 {
			// 释放日期后 7 天未赎回 → 过期
			claim.Status = types.ShadowClaimExpired
			expired++
		}
	}
	if expired > 0 {
		log.Printf("[shadow-claim] expired %d claims", expired)
	}
	return expired
}

// IsNullifierSpent 检查 nullifier 是否已被使用。
func (scm *ShadowClaimManager) IsNullifierSpent(nullifier [32]byte) bool {
	scm.mu.RLock()
	defer scm.mu.RUnlock()
	return scm.nullifiers[fmt.Sprintf("%x", nullifier[:])]
}

// TotalActive 返回活跃凭证总数。
func (scm *ShadowClaimManager) TotalActive() int {
	scm.mu.RLock()
	defer scm.mu.RUnlock()
	count := 0
	for _, c := range scm.claims {
		if c.Status == types.ShadowClaimActive { count++ }
	}
	return count
}

// ===== 索引维护 =====

func (scm *ShadowClaimManager) removeOwnerClaim(ownerID, claimID string) {
	claims := scm.byOwner[ownerID]
	for i, id := range claims {
		if id == claimID {
			scm.byOwner[ownerID] = append(claims[:i], claims[i+1:]...)
			return
		}
	}
}

var _ = types.ModuleName
