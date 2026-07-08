// Package vrg 虚拟根网图 — 影子收据格式与摆渡节点协议。
//
// 影子收据 (Shadow Receipt):
//   社区间离线交易的标准化凭证，包含:
//   - 社区联合签名（门限签名）
//   - ZK 证明（资产已锁定、未超额度、无本纪元双花）
//   - Epoch 绑定
//   - 状态机: PENDING_OUTBOUND → IN_TRANSIT → ACCEPTED_INBOUND / REJECTED
package vrg

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/nekop2p/nekop2p/crypto"
)

// ============================================================
// 影子收据
// ============================================================

// ReceiptStatus 影子收据状态。
type ReceiptStatus int32

const (
	ReceiptPendingOutbound ReceiptStatus = 0 // 待发出（源社区已签署）
	ReceiptInTransit       ReceiptStatus = 1 // 传输中（摆渡节点携带）
	ReceiptAcceptedInbound ReceiptStatus = 2 // 已接收（目标社区验证通过）
	ReceiptRejected        ReceiptStatus = 3 // 已拒绝（验证失败或过期）
	ReceiptSettled         ReceiptStatus = 4 // 已结算（资产已释放）
)

func (rs ReceiptStatus) String() string {
	switch rs {
	case ReceiptPendingOutbound: return "PENDING_OUTBOUND"
	case ReceiptInTransit: return "IN_TRANSIT"
	case ReceiptAcceptedInbound: return "ACCEPTED_INBOUND"
	case ReceiptRejected: return "REJECTED"
	case ReceiptSettled: return "SETTLED"
	default: return "UNKNOWN"
	}
}

// ShadowReceipt 影子收据——跨社区交易的标准链上凭证。
type ShadowReceipt struct {
	ReceiptID    [32]byte // 唯一收据 ID = SHA256(源社区 || Epoch || 序列号)
	
	// 源社区信息
	SourceCommunityID [32]byte          // 源社区虚拟化身 ID
	SourceEpoch       uint64            // 源社区 Epoch 编号
	SourceSignature   *crypto.ThresholdSignature // 源社区门限签名
	
	// 目标社区信息
	TargetCommunityID [32]byte // 目标社区虚拟化身 ID
	
	// 资产信息
	AssetType    string // "credit" / "token" / "data"
	AssetAmount  uint64 // 资产数量
	AssetProof   []byte // ZK 证明: 资产已锁定 + 未超额度 + 无双花
	
	// Epoch 绑定
	EpochNumber  uint64 // 全局 Epoch 编号
	EpochBinding []byte // Epoch 绑定证明（防跨纪元重放）
	
	// 时间戳
	CreatedAt    int64  // 创建时间
	ExpiresAt    int64  // 过期时间
	AcceptedAt   int64  // 接收时间
	SettledAt    int64  // 结算时间
	
	// 摆渡节点
	FerryNodeID  [32]byte // 当前携带收据的摆渡节点 ID
	FerryHistory [][32]byte // 摆渡节点路径历史
	
	// 状态
	Status ReceiptStatus
}

// NewShadowReceipt 创建新的影子收据。
func NewShadowReceipt(
	sourceCommunityID, targetCommunityID [32]byte,
	assetType string, assetAmount uint64,
	assetProof []byte,
	epochNumber uint64,
	sourceSig *crypto.ThresholdSignature,
) *ShadowReceipt {
	receiptID := generateReceiptID(sourceCommunityID, targetCommunityID, epochNumber)
	
	return &ShadowReceipt{
		ReceiptID:          receiptID,
		SourceCommunityID:  sourceCommunityID,
		TargetCommunityID:  targetCommunityID,
		AssetType:          assetType,
		AssetAmount:        assetAmount,
		AssetProof:         assetProof,
		EpochNumber:        epochNumber,
		SourceSignature:    sourceSig,
		CreatedAt:          time.Now().Unix(),
		ExpiresAt:          time.Now().Add(30 * 24 * time.Hour).Unix(), // 30 天过期
		Status:             ReceiptPendingOutbound,
	}
}

// VerifySourceSignature 验证源社区的门限签名。
func (sr *ShadowReceipt) VerifySourceSignature(avatar *crypto.CommunityAvatar) (bool, error) {
	if sr.SourceSignature == nil {
		return false, fmt.Errorf("receipt %x: no source signature", sr.ReceiptID[:8])
	}
	// 签名数据 = ReceiptID || AssetType || AssetAmount || EpochNumber
	signedData := sr.signingData()
	if !bytesEqual(signedData, sr.SourceSignature.SignedData) {
		return false, fmt.Errorf("receipt %x: signed data mismatch", sr.ReceiptID[:8])
	}
	return avatar.VerifyThresholdSignature(sr.SourceSignature)
}

func (sr *ShadowReceipt) signingData() []byte {
	h := sha256.New()
	h.Write(sr.ReceiptID[:])
	h.Write([]byte(sr.AssetType))
	h.Write(uint64ToBytes(sr.AssetAmount))
	h.Write(uint64ToBytes(sr.EpochNumber))
	return h.Sum(nil)
}

// ============================================================
// 状态转换
// ============================================================

// MarkInTransit 标记收据为传输中。
func (sr *ShadowReceipt) MarkInTransit(ferryNodeID [32]byte) error {
	if sr.Status != ReceiptPendingOutbound {
		return fmt.Errorf("receipt %x: cannot transit from %s", sr.ReceiptID[:8], sr.Status)
	}
	sr.Status = ReceiptInTransit
	sr.FerryNodeID = ferryNodeID
	sr.FerryHistory = append(sr.FerryHistory, ferryNodeID)
	return nil
}

// MarkAccepted 标记收据为已接收。
func (sr *ShadowReceipt) MarkAccepted() error {
	if sr.Status != ReceiptInTransit {
		return fmt.Errorf("receipt %x: cannot accept from %s", sr.ReceiptID[:8], sr.Status)
	}
	// 检查是否过期
	if time.Now().Unix() > sr.ExpiresAt {
		sr.Status = ReceiptRejected
		return fmt.Errorf("receipt %x: expired", sr.ReceiptID[:8])
	}
	sr.Status = ReceiptAcceptedInbound
	sr.AcceptedAt = time.Now().Unix()
	return nil
}

// MarkRejected 标记收据为已拒绝。
func (sr *ShadowReceipt) MarkRejected(reason string) error {
	if sr.Status != ReceiptInTransit {
		return fmt.Errorf("receipt %x: cannot reject from %s", sr.ReceiptID[:8], sr.Status)
	}
	sr.Status = ReceiptRejected
	return nil
}

// MarkSettled 标记收据为已结算。
func (sr *ShadowReceipt) MarkSettled() error {
	if sr.Status != ReceiptAcceptedInbound {
		return fmt.Errorf("receipt %x: cannot settle from %s", sr.ReceiptID[:8], sr.Status)
	}
	sr.Status = ReceiptSettled
	sr.SettledAt = time.Now().Unix()
	return nil
}

// ============================================================
// 摆渡节点协议
// ============================================================

// FerryNode 摆渡节点——在孤立社区间物理传递影子收据。
type FerryNode struct {
	NodeID       [32]byte
	CommunityID  [32]byte // 所属社区
	PublicKey    [32]byte // Curve25519 公钥
	
	// 携带的收据
	CarryingReceipts map[[32]byte]*ShadowReceipt // receiptID → receipt
	
	// 统计
	TotalDeliveries   uint64
	TotalEarnings     uint64
	OnlineSince       int64
}

// NewFerryNode 创建新的摆渡节点。
func NewFerryNode(nodeID, communityID, publicKey [32]byte) *FerryNode {
	return &FerryNode{
		NodeID:           nodeID,
		CommunityID:      communityID,
		PublicKey:        publicKey,
		CarryingReceipts: make(map[[32]byte]*ShadowReceipt),
		OnlineSince:      time.Now().Unix(),
	}
}

// PickupReceipt 摆渡节点拾取影子收据。
func (fn *FerryNode) PickupReceipt(receipt *ShadowReceipt) error {
	if receipt.Status != ReceiptPendingOutbound {
		return fmt.Errorf("ferry: receipt %x not pending outbound", receipt.ReceiptID[:8])
	}
	fn.CarryingReceipts[receipt.ReceiptID] = receipt
	return receipt.MarkInTransit(fn.NodeID)
}

// DeliverReceipt 摆渡节点投递影子收据到目标社区。
func (fn *FerryNode) DeliverReceipt(receiptID [32]byte, accepted bool) error {
	receipt, exists := fn.CarryingReceipts[receiptID]
	if !exists {
		return fmt.Errorf("ferry: receipt %x not carried", receiptID[:8])
	}
	
	if accepted {
		if err := receipt.MarkAccepted(); err != nil {
			return err
		}
		fn.TotalDeliveries++
	} else {
		receipt.MarkRejected("target community verification failed")
	}
	
	delete(fn.CarryingReceipts, receiptID)
	return nil
}

// ============================================================
// 辅助函数
// ============================================================

func generateReceiptID(source, target [32]byte, epoch uint64) [32]byte {
	h := sha256.New()
	h.Write(source[:])
	h.Write(target[:])
	h.Write(uint64ToBytes(epoch))
	// 添加时间戳纳秒保证唯一性
	h.Write(uint64ToBytes(uint64(time.Now().UnixNano())))
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

func uint64ToBytes(v uint64) []byte {
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = byte(v)
		v >>= 8
	}
	return b
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
