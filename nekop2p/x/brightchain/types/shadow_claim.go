// Package types Shadow Claims（影子凭证）类型定义。
//
// Shadow Claims 允许 Inkwell 锁仓期间的资产以远期凭证形式在二级市场流通，
// 类比资产支持商业票据 (ABCP) 和远期利率协议 (FRA)。
//
// 核心机制:
//   1. 锁仓资产 → 发行 ShadowClaim（ZK 绑定所有权+释放日期+面值）
//   2. ShadowClaim 可在明域二级市场转让
//   3. 释放日期到达时，持有者赎回底层资产
//   4. nullifier 防双花（同一锁仓最多 1 张活跃凭证）
package types

// ShadowClaim 影子凭证（可在二级市场交易的远期债权）。
type ShadowClaim struct {
	ClaimID       string   // 唯一凭证 ID
	IssuerID      string   // 原始锁仓者 chain_id
	OwnerID       string   // 当前持有者 chain_id
	FaceValue     uint64   // 面值（锁仓金额）
	ReleaseDate   int64    // 释放日期（Unix 时间戳，Inkwell WindowEnd）
	IssuedAt      int64    // 发行时间
	TransferredAt int64    // 最后转让时间
	TransferCount uint32   // 转让次数

	// ZK 绑定
	ZkBindingProof  []byte // ZK 证明: 所有权 + 释放日期 + 面值 的绑定
	LockReference   []byte // 引用的 Inkwell 锁仓标识

	// 状态
	Status ShadowClaimStatus
}

// ShadowClaimStatus 影子凭证状态。
type ShadowClaimStatus int32

const (
	ShadowClaimActive    ShadowClaimStatus = 0 // 活跃（可交易）
	ShadowClaimRedeemed  ShadowClaimStatus = 1 // 已赎回
	ShadowClaimExpired   ShadowClaimStatus = 2 // 已过期（超过释放日期未赎回）
	ShadowClaimRevoked   ShadowClaimStatus = 3 // 已撤销（发行者提前赎回）
)

func (s ShadowClaimStatus) String() string {
	switch s {
	case ShadowClaimActive: return "active"
	case ShadowClaimRedeemed: return "redeemed"
	case ShadowClaimExpired: return "expired"
	case ShadowClaimRevoked: return "revoked"
	default: return "unknown"
	}
}

// ===== 消息类型 =====

// MsgIssueShadowClaim 发行影子凭证。
type MsgIssueShadowClaim struct {
	IssuerID      string // 锁仓者
	FaceValue     uint64 // 面值
	ReleaseDate   int64  // 释放日期
	ZkBindingProof []byte // ZK 所有权绑定证明
	LockReference  []byte // Inkwell 锁仓引用
}

// MsgTransferShadowClaim 转让影子凭证。
type MsgTransferShadowClaim struct {
	ClaimID       string // 凭证 ID
	FromOwner     string // 转出方
	ToOwner       string // 转入方
	TransferPrice uint64 // 转让价格（记录用）
}

// MsgRedeemShadowClaim 赎回影子凭证（释放日期到达后）。
type MsgRedeemShadowClaim struct {
	ClaimID      string // 凭证 ID
	RedeemerID   string // 赎回者
	Nullifier    [32]byte // nullifier（防双花）
}

// ShadowClaimResponse 通用响应。
type ShadowClaimResponse struct {
	ClaimID string
	Success bool
	Message string
}

// ===== 查询类型 =====

// ShadowClaimQuery 影子凭证查询。
type ShadowClaimQuery struct {
	ClaimID  string
	OwnerID  string // 按持有者过滤
	Status   ShadowClaimStatus
}
