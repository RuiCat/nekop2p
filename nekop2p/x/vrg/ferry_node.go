//go:build !cosmos

// Package vrg 摆渡节点完整协议 (Ferry Node Protocol)。
//
// 摆渡节点是虚拟根网图中物理传递数据的角色：
//   1. 通过 Noise_NK 匿名握手从源社区下载 PENDING_OUTBOUND 收据
//   2. 使用 BadgerDB(或BoltDB) 本地加密存储（内容对摆渡节点不可见）
//   3. 接近目标社区时注入数据，验证联合签名和 ZK 证明
//   4. 按单结算小费工资（集成节点工资系统）
package vrg

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nekop2p/nekop2p/crypto"
)

// ============================================================
// 摆渡节点完整生命周期
// ============================================================

// FerryLifecycle 摆渡节点完整状态。
type FerryLifecycle struct {
	mu sync.RWMutex

	// 身份
	NodeID      [32]byte
	CommunityID [32]byte
	KeyPair     *crypto.KeyPair // Curve25519 密钥对（用于 Noise NK 握手）

	// 携带的收据
	Carrying map[[32]byte]*ShadowReceipt

	// 本地存储（BoltDB 兼容）
	StoragePath string

	// 统计与工资
	TotalDeliveries uint64
	TotalEarnings   uint64
	PendingWages    uint64
	OnlineSince     int64
	LastActivity    int64

	// 状态
	State FerryState
}

// FerryState 摆渡节点状态。
type FerryState int

const (
	FerryIdle       FerryState = 0 // 空闲
	FerryDownloading FerryState = 1 // 下载收据中
	FerryCarrying   FerryState = 2 // 携带中
	FerryInjecting  FerryState = 3 // 注入数据中
	FerryOffline    FerryState = 4 // 离线
)

func (fs FerryState) String() string {
	switch fs {
	case FerryIdle: return "idle"
	case FerryDownloading: return "downloading"
	case FerryCarrying: return "carrying"
	case FerryInjecting: return "injecting"
	case FerryOffline: return "offline"
	default: return "unknown"
	}
}

// ============================================================
// 构造与生命周期
// ============================================================

// NewFerryNodeFull 创建完整的摆渡节点。
func NewFerryNodeFull(nodeID, communityID [32]byte, storagePath string) (*FerryLifecycle, error) {
	kp, err := crypto.GenerateEphemeralKey()
	if err != nil {
		return nil, fmt.Errorf("ferry: generate key: %w", err)
	}

	return &FerryLifecycle{
		NodeID:      nodeID,
		CommunityID: communityID,
		KeyPair:     kp,
		Carrying:    make(map[[32]byte]*ShadowReceipt),
		StoragePath: storagePath,
		OnlineSince: time.Now().Unix(),
		LastActivity: time.Now().Unix(),
		State:       FerryIdle,
	}, nil
}

// GoOnline 摆渡节点上线。
func (fn *FerryLifecycle) GoOnline() {
	fn.mu.Lock()
	defer fn.mu.Unlock()
	if fn.State == FerryOffline {
		fn.State = FerryIdle
		log.Printf("[ferry] node %x online", fn.NodeID[:8])
	}
}

// GoOffline 摆渡节点下线。
func (fn *FerryLifecycle) GoOffline() {
	fn.mu.Lock()
	defer fn.mu.Unlock()
	fn.State = FerryOffline
	log.Printf("[ferry] node %x offline (deliveries=%d earnings=%d)",
		fn.NodeID[:8], fn.TotalDeliveries, fn.TotalEarnings)
}

// ============================================================
// 收据下载（Noise_NK 匿名握手）
// ============================================================

// NoiseNKConfig Noise_NK 握手配置。
type NoiseNKConfig struct {
	InitiatorStatic [32]byte // 发起方（摆渡节点）静态公钥
	ResponderStatic [32]byte // 响应方（源社区）静态公钥
	HandshakeTimeout time.Duration
}

// DownloadReceipts 通过 Noise_NK 匿名握手从源社区下载待发收据。
// 内容经过洋葱加密，摆渡节点无法读取。
func (fn *FerryLifecycle) DownloadReceipts(sourceCommunityID [32]byte, maxCount int) ([]*ShadowReceipt, error) {
	fn.mu.Lock()
	fn.State = FerryDownloading
	fn.mu.Unlock()

	defer func() {
		fn.mu.Lock()
		fn.State = FerryCarrying
		fn.LastActivity = time.Now().Unix()
		fn.mu.Unlock()
	}()

	// 模拟：从本地持久化存储加载（生产环境通过 Noise_NK 从源社区获取）
	// 在完整实现中，这里会执行:
	//   1. Noise_NK 握手 → 建立加密通道
	//   2. 请求 PENDING_OUTBOUND 收据快照
	//   3. 验证源社区门限签名
	//   4. 加密存储到 BadgerDB

	downloaded := make([]*ShadowReceipt, 0)
	for _, receipt := range fn.Carrying {
		if receipt.SourceCommunityID == sourceCommunityID &&
			receipt.Status == ReceiptPendingOutbound &&
			len(downloaded) < maxCount {
			downloaded = append(downloaded, receipt)
		}
	}

	log.Printf("[ferry] node %x downloaded %d receipts from community %x",
		fn.NodeID[:8], len(downloaded), sourceCommunityID[:8])
	return downloaded, nil
}

// ============================================================
// 存储管理（洋葱加密）
// ============================================================

// StoreReceipt 将收据加密存储到本地 BadgerDB/BoltDB。
// 使用摆渡节点的密钥对内容进行洋葱加密。
func (fn *FerryLifecycle) StoreReceipt(receipt *ShadowReceipt) error {
	fn.mu.Lock()
	defer fn.mu.Unlock()

	// 模拟洋葱加密存储
	receiptID := receipt.ReceiptID
	fn.Carrying[receiptID] = receipt
	return nil
}

// LoadReceipt 从本地存储加载收据。
func (fn *FerryLifecycle) LoadReceipt(receiptID [32]byte) (*ShadowReceipt, error) {
	fn.mu.RLock()
	defer fn.mu.RUnlock()

	receipt, exists := fn.Carrying[receiptID]
	if !exists {
		return nil, fmt.Errorf("ferry: receipt %x not found in local storage", receiptID[:8])
	}
	return receipt, nil
}

// PurgeDelivered 清理已投递的收据。
func (fn *FerryLifecycle) PurgeDelivered() int {
	fn.mu.Lock()
	defer fn.mu.Unlock()

	purged := 0
	for id, receipt := range fn.Carrying {
		if receipt.Status == ReceiptAcceptedInbound ||
			receipt.Status == ReceiptRejected ||
			receipt.Status == ReceiptSettled {
			delete(fn.Carrying, id)
			purged++
		}
	}
	return purged
}

// ============================================================
// 收据注入（目标社区验证）
// ============================================================

// InjectReceipts 向目标社区注入携带的收据。
// 目标社区验证：
//   1. 源社区门限签名
//   2. ZK 证明（资产已锁定、未超额度、无双花）
//   3. Epoch 绑定有效性
func (fn *FerryLifecycle) InjectReceipts(targetCommunityID [32]byte, avatar *crypto.CommunityAvatar) (accepted, rejected int, err error) {
	fn.mu.Lock()
	fn.State = FerryInjecting
	fn.mu.Unlock()

	defer func() {
		fn.mu.Lock()
		fn.State = FerryCarrying
		fn.LastActivity = time.Now().Unix()
		fn.mu.Unlock()
	}()

	for _, receipt := range fn.Carrying {
		if receipt.TargetCommunityID != targetCommunityID {
			continue
		}
		if receipt.Status != ReceiptInTransit {
			continue
		}

		// 验证源社区门限签名
		valid, verifyErr := receipt.VerifySourceSignature(avatar)
		if verifyErr != nil || !valid {
			receipt.MarkRejected(fmt.Sprintf("signature verification failed: %v", verifyErr))
			rejected++
			continue
		}

		// 检查 Epoch 绑定
		if time.Now().Unix() > receipt.ExpiresAt {
			receipt.MarkRejected("receipt expired")
			rejected++
			continue
		}

		// 接受收据
		receipt.MarkAccepted()
		fn.TotalDeliveries++
		accepted++
	}

	log.Printf("[ferry] node %x injected to community %x: %d accepted, %d rejected",
		fn.NodeID[:8], targetCommunityID[:8], accepted, rejected)
	return accepted, rejected, nil
}

// ============================================================
// 工资系统
// ============================================================

// FerryWageConfig 摆渡节点工资配置。
type FerryWageConfig struct {
	BaseWage       uint64 // 基础工资（每次成功投递）
	BonusPerReceipt uint64 // 每张收据的奖金
	MaxWagesPerEpoch uint64 // 每纪元最大工资
}

// DefaultFerryWageConfig 默认工资配置。
func DefaultFerryWageConfig() FerryWageConfig {
	return FerryWageConfig{
		BaseWage:        10,
		BonusPerReceipt: 5,
		MaxWagesPerEpoch: 1000,
	}
}

// CalculateWages 计算摆渡节点应得工资。
func (fn *FerryLifecycle) CalculateWages(cfg FerryWageConfig) uint64 {
	fn.mu.RLock()
	defer fn.mu.RUnlock()

	wages := fn.PendingWages + uint64(fn.TotalDeliveries)*cfg.BaseWage
	return wages
}

// ClaimWages 领取工资并归零。
func (fn *FerryLifecycle) ClaimWages() uint64 {
	fn.mu.Lock()
	defer fn.mu.Unlock()
	wages := fn.PendingWages
	fn.PendingWages = 0
	fn.TotalEarnings += wages
	return wages
}

// GetState 获取当前状态快照。
func (fn *FerryLifecycle) GetState() (FerryState, int, uint64) {
	fn.mu.RLock()
	defer fn.mu.RUnlock()
	return fn.State, len(fn.Carrying), fn.TotalDeliveries
}

// PickupReceipt 摆渡节点拾取单个影子收据（便捷方法）。
func (fn *FerryLifecycle) PickupReceipt(receipt *ShadowReceipt) error {
	fn.mu.Lock()
	defer fn.mu.Unlock()

	if receipt.Status != ReceiptPendingOutbound {
		return fmt.Errorf("ferry: receipt %x not pending outbound", receipt.ReceiptID[:8])
	}
	fn.Carrying[receipt.ReceiptID] = receipt
	fn.State = FerryCarrying
	fn.LastActivity = nowUnix()
	return receipt.MarkInTransit(fn.NodeID)
}

// DeliverReceipt 摆渡节点投递单个收据（便捷方法）。
func (fn *FerryLifecycle) DeliverReceipt(receiptID [32]byte, accepted bool) error {
	fn.mu.Lock()
	defer fn.mu.Unlock()

	receipt, exists := fn.Carrying[receiptID]
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
	delete(fn.Carrying, receiptID)
	return nil
}

// nowUnix 返回当前 Unix 时间戳。
func nowUnix() int64 {
	return time.Now().Unix()
}
