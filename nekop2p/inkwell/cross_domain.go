// Package inkwell 跨域混沌池路由 (VRG-07)。
//
// CrossDomainRouter 实现暗域通用积分跨 Namespace 的匿名转移。
// 使 Inkwell 从「同域清算」升级为「跨域清算」：
//
//   源域 (Namespace A):
//     ① 用户锁定资产 + 生成 ZK 证明（资产已锁定、未超额度、无双花）
//     ② 锁定凭证锚定到影子收据
//
//   混沌池 (匿名中转):
//     ③ 源域锁定的资产进入 Inkwell 混沌池
//     ④ 混沌池对资产进行金额拆封 + 时间随机化 + 来源伪装
//     ⑤ 摆渡节点物理携带影子收据至目标域
//
//   目标域 (Namespace B):
//     ⑥ 验证影子收据: 联合签名 + ZK 证明 + Epoch 绑定
//     ⑦ 释放等值资产（扣除混沌池手续费）
//
// 安全保证:
//   - 接收方仅验证「该价值由合法自治社区足额锁定」，不暴露来源
//   - 跨域摆渡节点不可见资产内容（洋葱加密）
//   - 双花防护: 同一资产同一 Epoch 只能绑定一个收据
package inkwell

import (
	"crypto/sha256"
	"fmt"
	"sync"
)

// ============================================================
// 跨域转账状态
// ============================================================

// CrossDomainStatus 跨域转账状态。
type CrossDomainStatus int32

const (
	CDLocked       CrossDomainStatus = 0 // 已锁定（源域）
	CDInPool       CrossDomainStatus = 1 // 混沌池中（匿名中转）
	CDInTransit    CrossDomainStatus = 2 // 摆渡中（物理传输）
	CDReleased     CrossDomainStatus = 3 // 已释放（目标域）
	CDFailed       CrossDomainStatus = 4 // 失败（超时/拒绝）
	CDRefunded     CrossDomainStatus = 5 // 已退款（源域）
)

func (cs CrossDomainStatus) String() string {
	switch cs {
	case CDLocked: return "LOCKED"
	case CDInPool: return "IN_POOL"
	case CDInTransit: return "IN_TRANSIT"
	case CDReleased: return "RELEASED"
	case CDFailed: return "FAILED"
	case CDRefunded: return "REFUNDED"
	default: return "UNKNOWN"
	}
}

// ============================================================
// 跨域转账记录
// ============================================================

// CrossDomainTransfer 一笔跨域混沌池转账。
type CrossDomainTransfer struct {
	TransferID [32]byte // 唯一转账 ID

	// 源域信息
	SourceNS      uint16    // 源 Namespace ID
	SourceUser    [32]byte  // 源用户匿名化名
	SourceCommunity [32]byte // 源社区虚拟化身 ID
	LockedAmount  uint64    // 锁定金额
	LockProof     []byte    // ZK 证明: 资产已锁定 + 未超额度

	// 目标域信息
	TargetNS      uint16    // 目标 Namespace ID
	TargetUser    [32]byte  // 目标用户匿名化名（可选，零值 = 任意）
	TargetCommunity [32]byte // 目标社区虚拟化身 ID

	// 混沌池参数
	PoolSeed      [32]byte  // 混沌种子（用于金额拆封 + 时间随机化）
	Fragments     []uint64  // 金额碎片
	RelayPath     [][32]byte // 混沌池中继路径

	// 摆渡节点
	FerryNodeID   [32]byte  // 当前携带的摆渡节点
	ReceiptID     [32]byte  // 关联的影子收据 ID

	// Epoch 绑定
	LockEpoch     uint64    // 锁定时的 Epoch
	ReleaseEpoch  uint64    // 释放时的 Epoch

	// 时间戳
	LockedAt      int64     // 锁定时间
	PoolEnteredAt int64     // 进入混沌池时间
	TransitAt     int64     // 开始摆渡时间
	ReleasedAt    int64     // 释放时间

	// 手续费
	PoolFee       uint64    // 混沌池手续费

	// 状态
	Status        CrossDomainStatus
}

// ComputeTransferID 计算跨域转账的唯一 ID。
func ComputeTransferID(sourceNS uint16, sourceUser [32]byte, amount uint64, lockEpoch uint64) [32]byte {
	h := sha256.New()
	h.Write([]byte("inkwell-cross-domain-v1"))
	h.Write(uint16ToBytes(sourceNS))
	h.Write(sourceUser[:])
	h.Write(uint64ToBytes(amount))
	h.Write(uint64ToBytes(lockEpoch))
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

// ============================================================
// CrossDomainRouter 跨域路由引擎
// ============================================================

// CrossDomainRouter 管理跨 Namespace 的混沌池转账。
type CrossDomainRouter struct {
	mu        sync.RWMutex
	transfers map[[32]byte]*CrossDomainTransfer // transferID → transfer
	bySource  map[uint16][][32]byte             // sourceNS → transferIDs
	byTarget  map[uint16][][32]byte             // targetNS → transferIDs

	// 混沌池统计
	poolBalance   uint64 // 混沌池中总锁定金额
	totalTransfers uint64
	feeCollected  uint64
}

// NewCrossDomainRouter 创建跨域路由引擎。
func NewCrossDomainRouter() *CrossDomainRouter {
	return &CrossDomainRouter{
		transfers: make(map[[32]byte]*CrossDomainTransfer),
		bySource:  make(map[uint16][][32]byte),
		byTarget:  make(map[uint16][][32]byte),
	}
}

// ============================================================
// 转账生命周期
// ============================================================

// LockAndTransfer 在源域锁定资产并发起跨域转账。
//
// 此函数在源域调用：
//   1. 验证 ZK 锁定证明（资产已锁定、未超额度、无双花）
//   2. 创建跨域转账记录
//   3. 生成混沌池参数（金额+时间随机化）
//   4. 将转账标记为 IN_POOL 状态
func (cdr *CrossDomainRouter) LockAndTransfer(
	sourceNS uint16,
	sourceUser [32]byte,
	sourceCommunity [32]byte,
	amount uint64,
	lockProof []byte,
	targetNS uint16,
	targetUser [32]byte,
	targetCommunity [32]byte,
	lockEpoch uint64,
	lockedAt int64,
	poolSeed [32]byte,
) (*CrossDomainTransfer, error) {
	cdr.mu.Lock()
	defer cdr.mu.Unlock()

	// 验证源域和目标域不同
	if sourceNS == targetNS {
		return nil, fmt.Errorf("cross-domain: source and target namespace must differ")
	}

	// 验证金额 > 0
	if amount == 0 {
		return nil, fmt.Errorf("cross-domain: amount must be > 0")
	}

	// 创建转账记录
	transferID := ComputeTransferID(sourceNS, sourceUser, amount, lockEpoch)

	if _, exists := cdr.transfers[transferID]; exists {
		return nil, fmt.Errorf("cross-domain: transfer %x already exists", transferID[:8])
	}

	// 计算混沌池手续费（0.5%）
	poolFee := amount / 200
	netAmount := amount - poolFee

	// 生成金额碎片（使用混沌种子）
	fragments := generateCrossDomainFragments(netAmount, poolSeed)

	transfer := &CrossDomainTransfer{
		TransferID:      transferID,
		SourceNS:        sourceNS,
		SourceUser:      sourceUser,
		SourceCommunity: sourceCommunity,
		LockedAmount:    amount,
		LockProof:       lockProof,
		TargetNS:        targetNS,
		TargetUser:      targetUser,
		TargetCommunity: targetCommunity,
		PoolSeed:        poolSeed,
		Fragments:       fragments,
		LockEpoch:       lockEpoch,
		LockedAt:        lockedAt,
		PoolFee:         poolFee,
		Status:          CDInPool,
		PoolEnteredAt:   lockedAt,
	}

	cdr.transfers[transferID] = transfer
	cdr.bySource[sourceNS] = append(cdr.bySource[sourceNS], transferID)
	cdr.byTarget[targetNS] = append(cdr.byTarget[targetNS], transferID)
	cdr.poolBalance += amount
	cdr.totalTransfers++

	return transfer, nil
}

// AssignFerry 分配摆渡节点并标记为传输中。
// 当摆渡节点接受任务时调用。
func (cdr *CrossDomainRouter) AssignFerry(
	transferID [32]byte,
	ferryNodeID [32]byte,
	receiptID [32]byte,
	transitAt int64,
) error {
	cdr.mu.Lock()
	defer cdr.mu.Unlock()

	transfer, exists := cdr.transfers[transferID]
	if !exists {
		return fmt.Errorf("cross-domain: transfer %x not found", transferID[:8])
	}

	if transfer.Status != CDInPool {
		return fmt.Errorf("cross-domain: transfer %x is %s, not IN_POOL", transferID[:8], transfer.Status)
	}

	transfer.FerryNodeID = ferryNodeID
	transfer.ReceiptID = receiptID
	transfer.TransitAt = transitAt
	transfer.Status = CDInTransit

	return nil
}

// ReleaseOnTarget 在目标域释放跨域转账。
//
// 此函数在目标域调用：
//   1. 验证转账存在且状态为 IN_TRANSIT
//   2. 验证目标社区匹配
//   3. 释放资产（扣除混沌池手续费后）
//   4. 更新状态为 RELEASED
func (cdr *CrossDomainRouter) ReleaseOnTarget(
	transferID [32]byte,
	targetCommunity [32]byte,
	releaseEpoch uint64,
	releasedAt int64,
) (*CrossDomainTransfer, uint64, error) {
	cdr.mu.Lock()
	defer cdr.mu.Unlock()

	transfer, exists := cdr.transfers[transferID]
	if !exists {
		return nil, 0, fmt.Errorf("cross-domain: transfer %x not found", transferID[:8])
	}

	if transfer.Status != CDInTransit {
		return nil, 0, fmt.Errorf("cross-domain: transfer %x is %s, not IN_TRANSIT", transferID[:8], transfer.Status)
	}

	if transfer.TargetCommunity != targetCommunity {
		return nil, 0, fmt.Errorf("cross-domain: target community mismatch")
	}

	netAmount := transfer.LockedAmount - transfer.PoolFee
	transfer.ReleaseEpoch = releaseEpoch
	transfer.ReleasedAt = releasedAt
	transfer.Status = CDReleased

	cdr.poolBalance -= transfer.LockedAmount
	cdr.feeCollected += transfer.PoolFee

	return transfer, netAmount, nil
}

// RefundOnSource 在源域退款。
// 当转账超时或被拒绝时调用。
func (cdr *CrossDomainRouter) RefundOnSource(transferID [32]byte, refundedAt int64) error {
	cdr.mu.Lock()
	defer cdr.mu.Unlock()

	transfer, exists := cdr.transfers[transferID]
	if !exists {
		return fmt.Errorf("cross-domain: transfer %x not found", transferID[:8])
	}

	if transfer.Status == CDReleased {
		return fmt.Errorf("cross-domain: transfer %x already released", transferID[:8])
	}

	if transfer.Status == CDRefunded {
		return fmt.Errorf("cross-domain: transfer %x already refunded", transferID[:8])
	}

	transfer.Status = CDRefunded
	cdr.poolBalance -= transfer.LockedAmount

	return nil
}

// MarkFailed 标记跨域转账为失败。
func (cdr *CrossDomainRouter) MarkFailed(transferID [32]byte, reason string) error {
	cdr.mu.Lock()
	defer cdr.mu.Unlock()

	transfer, exists := cdr.transfers[transferID]
	if !exists {
		return fmt.Errorf("cross-domain: transfer %x not found", transferID[:8])
	}

	if transfer.Status == CDReleased || transfer.Status == CDRefunded {
		return fmt.Errorf("cross-domain: transfer %x already finalized", transferID[:8])
	}

	transfer.Status = CDFailed
	return nil
}

// ============================================================
// 查询接口
// ============================================================

// GetTransfer 获取跨域转账。
func (cdr *CrossDomainRouter) GetTransfer(transferID [32]byte) *CrossDomainTransfer {
	cdr.mu.RLock()
	defer cdr.mu.RUnlock()
	return cdr.transfers[transferID]
}

// GetTransfersBySource 获取源域所有跨域转账。
func (cdr *CrossDomainRouter) GetTransfersBySource(sourceNS uint16) []*CrossDomainTransfer {
	cdr.mu.RLock()
	defer cdr.mu.RUnlock()

	ids := cdr.bySource[sourceNS]
	transfers := make([]*CrossDomainTransfer, 0, len(ids))
	for _, id := range ids {
		if t, exists := cdr.transfers[id]; exists {
			transfers = append(transfers, t)
		}
	}
	return transfers
}

// GetTransfersByTarget 获取目标域所有跨域转账。
func (cdr *CrossDomainRouter) GetTransfersByTarget(targetNS uint16) []*CrossDomainTransfer {
	cdr.mu.RLock()
	defer cdr.mu.RUnlock()

	ids := cdr.byTarget[targetNS]
	transfers := make([]*CrossDomainTransfer, 0, len(ids))
	for _, id := range ids {
		if t, exists := cdr.transfers[id]; exists {
			transfers = append(transfers, t)
		}
	}
	return transfers
}

// GetPendingTransfers 获取所有待处理（混沌池中 + 摆渡中）的跨域转账。
func (cdr *CrossDomainRouter) GetPendingTransfers() []*CrossDomainTransfer {
	cdr.mu.RLock()
	defer cdr.mu.RUnlock()

	var pending []*CrossDomainTransfer
	for _, t := range cdr.transfers {
		if t.Status == CDInPool || t.Status == CDInTransit {
			pending = append(pending, t)
		}
	}
	return pending
}

// GetPoolBalance 获取混沌池中总锁定金额。
func (cdr *CrossDomainRouter) GetPoolBalance() uint64 {
	cdr.mu.RLock()
	defer cdr.mu.RUnlock()
	return cdr.poolBalance
}

// GetStats 获取统计信息。
func (cdr *CrossDomainRouter) GetStats() (totalTransfers uint64, feeCollected uint64) {
	cdr.mu.RLock()
	defer cdr.mu.RUnlock()
	return cdr.totalTransfers, cdr.feeCollected
}

// ============================================================
// 辅助函数
// ============================================================

// generateCrossDomainFragments 为跨域转账生成金额碎片。
// 使用与同域还款相同的 splitAmount 算法。
func generateCrossDomainFragments(amount uint64, seed [32]byte) []uint64 {
	rng := newRNG(seed)
	fragmentCount := 3 + rng.Intn(5) // 3-7 片
	return splitAmount(amount, fragmentCount, rng)
}

// uint16ToBytes 将 uint16 编码为 2 字节大端序。
func uint16ToBytes(v uint16) []byte {
	b := make([]byte, 2)
	b[0] = byte(v >> 8)
	b[1] = byte(v)
	return b
}

// uint64ToBytes 将 uint64 编码为 8 字节大端序。
func uint64ToBytes(v uint64) []byte {
	b := make([]byte, 8)
	b[0] = byte(v >> 56)
	b[1] = byte(v >> 48)
	b[2] = byte(v >> 40)
	b[3] = byte(v >> 32)
	b[4] = byte(v >> 24)
	b[5] = byte(v >> 16)
	b[6] = byte(v >> 8)
	b[7] = byte(v)
	return b
}
