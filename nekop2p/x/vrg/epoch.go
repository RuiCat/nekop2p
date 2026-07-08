// Package vrg 全局纪元管理与双花冲突检测。
//
// E8: 全局纪元计数器
//   - 基于区块驱动的 Epoch 递增
//   - 孤网期间独立计数，合并时取 max() + 1
//   - Epoch 绑定到所有影子收据
//
// E10: 跨纪元双花碰撞检测
//   - 追踪每个 [资产+Epoch] 的唯一性
//   - 碰撞时自动触发递归 Slashing
//   - 冲突事件广播至全网
package vrg

import (
	"fmt"
	"log"
	"sync"
)

// ============================================================
// E8: 全局纪元计数器
// ============================================================

// EpochCounter 去中心化全局纪元计数器。
type EpochCounter struct {
	mu sync.RWMutex

	currentEpoch  uint64 // 当前纪元编号
	blocksInEpoch uint64 // 当前纪元已产生区块数
	blocksPerEpoch uint64 // 每个纪元的区块数（默认 14400 ≈ 1天）

	// 孤网合并支持
	lastMergeEpoch uint64 // 上次合并时的纪元
	isolationCount uint64 // 孤网期间经历了几次独立递增

	// Epoch 事件
	epochEvents []EpochEvent // 最近的事件日志
}

// EpochEvent 纪元事件。
type EpochEvent struct {
	Epoch     uint64
	EventType string // "increment" / "merge" / "bind"
	BlockHeight int64
	Timestamp int64
	Description string
}

// NewEpochCounter 创建纪元计数器。
func NewEpochCounter(blocksPerEpoch uint64) *EpochCounter {
	return &EpochCounter{
		blocksPerEpoch: blocksPerEpoch,
		epochEvents:    make([]EpochEvent, 0, 100),
	}
}

// CurrentEpoch 返回当前纪元。
func (ec *EpochCounter) CurrentEpoch() uint64 {
	ec.mu.RLock()
	defer ec.mu.RUnlock()
	return ec.currentEpoch
}

// AdvanceBlock 推进一个区块。
// 如果达到纪元边界，自动递增。
func (ec *EpochCounter) AdvanceBlock(blockHeight int64) (epochChanged bool, newEpoch uint64) {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	ec.blocksInEpoch++

	if ec.blocksInEpoch >= ec.blocksPerEpoch {
		ec.currentEpoch++
		ec.blocksInEpoch = 0
		epochChanged = true
		newEpoch = ec.currentEpoch

		ec.recordEvent(EpochEvent{
			Epoch:       newEpoch,
			EventType:   "increment",
			BlockHeight: blockHeight,
			Description: fmt.Sprintf("epoch advanced to %d", newEpoch),
		})

		log.Printf("[epoch] advanced to epoch %d at block %d", newEpoch, blockHeight)
	}

	return epochChanged, ec.currentEpoch
}

// MergeEpoch 孤网合并时更新纪元。
// 新纪元 = max(本地纪元, 对端纪元) + 1
func (ec *EpochCounter) MergeEpoch(remoteEpoch uint64, blockHeight int64) uint64 {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	maxEpoch := ec.currentEpoch
	if remoteEpoch > maxEpoch {
		maxEpoch = remoteEpoch
	}

	newEpoch := maxEpoch + 1
	oldEpoch := ec.currentEpoch
	ec.currentEpoch = newEpoch
	ec.lastMergeEpoch = oldEpoch
	ec.isolationCount++

	ec.recordEvent(EpochEvent{
		Epoch:       newEpoch,
		EventType:   "merge",
		BlockHeight: blockHeight,
		Description: fmt.Sprintf("merged: local=%d remote=%d → new=%d", oldEpoch, remoteEpoch, newEpoch),
	})

	log.Printf("[epoch] merged: local=%d remote=%d → new=%d (isolation #%d)",
		oldEpoch, remoteEpoch, newEpoch, ec.isolationCount)
	return newEpoch
}

// BindReceipt 将收据绑定到当前纪元。
func (ec *EpochCounter) BindReceipt(receiptID [32]byte) uint64 {
	ec.mu.RLock()
	epoch := ec.currentEpoch
	ec.mu.RUnlock()

	ec.mu.Lock()
	ec.recordEvent(EpochEvent{
		Epoch:       epoch,
		EventType:   "bind",
		Description: fmt.Sprintf("receipt %x bound to epoch %d", receiptID[:8], epoch),
	})
	ec.mu.Unlock()

	return epoch
}

func (ec *EpochCounter) recordEvent(e EpochEvent) {
	ec.epochEvents = append(ec.epochEvents, e)
	if len(ec.epochEvents) > 100 {
		ec.epochEvents = ec.epochEvents[len(ec.epochEvents)-100:]
	}
}

// GetEvents 返回最近的纪元事件。
func (ec *EpochCounter) GetEvents(limit int) []EpochEvent {
	ec.mu.RLock()
	defer ec.mu.RUnlock()
	if limit <= 0 || limit >= len(ec.epochEvents) {
		return ec.epochEvents
	}
	return ec.epochEvents[len(ec.epochEvents)-limit:]
}

// IsolationCount 返回孤网合并次数。
func (ec *EpochCounter) IsolationCount() uint64 {
	ec.mu.RLock()
	defer ec.mu.RUnlock()
	return ec.isolationCount
}

// ============================================================
// E10: 跨纪元双花冲突检测
// ============================================================

// DoubleSpendDetector 双花冲突检测器。
// 追踪每个 [资产标识 + Epoch] 的唯一收据绑定。
type DoubleSpendDetector struct {
	mu sync.RWMutex

	// 资产绑定: assetKey → epoch → receiptID
	// 同一资产在同一 Epoch 只能绑定一个收据
	bindings map[string]map[uint64][32]byte

	// 已检测到的冲突
	collisions []CollisionEvent
}

// CollisionEvent 双花冲突事件。
type CollisionEvent struct {
	AssetKey     string   // 资产标识
	Epoch        uint64   // 冲突纪元
	ReceiptA     [32]byte // 第一个收据
	ReceiptB     [32]byte // 第二个收据（冲突的）
	SourceCommunityA [32]byte
	SourceCommunityB [32]byte
	DetectedAt   int64    // 检测时间
	Resolved     bool     // 是否已解决
	SlashApplied bool     // 是否已执行 Slashing
}

// NewDoubleSpendDetector 创建双花检测器。
func NewDoubleSpendDetector() *DoubleSpendDetector {
	return &DoubleSpendDetector{
		bindings:   make(map[string]map[uint64][32]byte),
		collisions: make([]CollisionEvent, 0),
	}
}

// RegisterReceipt 将收据注册到资产绑定表中。
// 如果同一资产+Epoch已有绑定 → 触发双花检测。
func (dsd *DoubleSpendDetector) RegisterReceipt(
	assetKey string,
	epoch uint64,
	receiptID [32]byte,
	sourceCommunity [32]byte,
) (*CollisionEvent, error) {
	dsd.mu.Lock()
	defer dsd.mu.Unlock()

	// 初始化
	if _, exists := dsd.bindings[assetKey]; !exists {
		dsd.bindings[assetKey] = make(map[uint64][32]byte)
	}

	// 检查同一 Epoch 是否已有绑定
	if existingReceipt, exists := dsd.bindings[assetKey][epoch]; exists {
		// 🔴 双花冲突检测！
		collision := CollisionEvent{
			AssetKey:         assetKey,
			Epoch:            epoch,
			ReceiptA:         existingReceipt,
			ReceiptB:         receiptID,
			SourceCommunityA: sourceCommunity,
			DetectedAt:       nowUnix(),
		}

		dsd.collisions = append(dsd.collisions, collision)

		log.Printf("[double-spend] COLLISION DETECTED: asset=%s epoch=%d receipt_a=%x receipt_b=%x",
			assetKey, epoch, existingReceipt[:8], receiptID[:8])

		return &collision, fmt.Errorf("double spend: asset %s already bound to receipt %x in epoch %d",
			assetKey, existingReceipt[:8], epoch)
	}

	// 注册绑定
	dsd.bindings[assetKey][epoch] = receiptID

	return nil, nil
}

// GetBindings 获取指定资产的所有 Epoch 绑定。
func (dsd *DoubleSpendDetector) GetBindings(assetKey string) map[uint64][32]byte {
	dsd.mu.RLock()
	defer dsd.mu.RUnlock()
	return dsd.bindings[assetKey]
}

// IsDoubleSpent 检查资产在某 Epoch 是否已经绑定。
func (dsd *DoubleSpendDetector) IsDoubleSpent(assetKey string, epoch uint64) bool {
	dsd.mu.RLock()
	defer dsd.mu.RUnlock()

	if epochBindings, exists := dsd.bindings[assetKey]; exists {
		_, spent := epochBindings[epoch]
		return spent
	}
	return false
}

// GetCollisions 返回所有已检测的冲突。
func (dsd *DoubleSpendDetector) GetCollisions() []CollisionEvent {
	dsd.mu.RLock()
	defer dsd.mu.RUnlock()
	return dsd.collisions
}

// ResolveCollision 标记冲突为已解决（执行 Slashing 后调用）。
func (dsd *DoubleSpendDetector) ResolveCollision(assetKey string, epoch uint64, slashApplied bool) {
	dsd.mu.Lock()
	defer dsd.mu.Unlock()

	for i := range dsd.collisions {
		c := &dsd.collisions[i]
		if c.AssetKey == assetKey && c.Epoch == epoch {
			c.Resolved = true
			c.SlashApplied = slashApplied
			return
		}
	}
}

// TotalCollisions 返回冲突总数。
func (dsd *DoubleSpendDetector) TotalCollisions() int {
	dsd.mu.RLock()
	defer dsd.mu.RUnlock()
	return len(dsd.collisions)
}

// ============================================================
// 冲突结果广播
// ============================================================

// CollisionBroadcast 冲突广播消息。
type CollisionBroadcast struct {
	Collision CollisionEvent
	Severity  string   // "CRITICAL" / "WARNING"
	Action    string   // "SLASH" / "FREEZE" / "INVESTIGATE"
	AffectedCommunities [][32]byte
}

// GenerateBroadcast 从冲突事件生成广播消息。
func (dsd *DoubleSpendDetector) GenerateBroadcast(collision *CollisionEvent) *CollisionBroadcast {
	bc := &CollisionBroadcast{
		Collision: *collision,
		Severity:  "CRITICAL",
		Action:    "SLASH",
	}

	// 收集受影响社区
	bc.AffectedCommunities = append(bc.AffectedCommunities,
		collision.SourceCommunityA,
		collision.SourceCommunityB,
	)

	return bc
}

// PruneEpochs 清理旧 Epoch 的绑定数据（保留最近 N 个 Epoch）。
func (dsd *DoubleSpendDetector) PruneEpochs(currentEpoch uint64, keepEpochs uint64) int {
	dsd.mu.Lock()
	defer dsd.mu.Unlock()

	// 防御性检查：防止 currentEpoch < keepEpochs 导致 uint64 下溢
	if currentEpoch <= keepEpochs {
		return 0
	}

	pruned := 0
	threshold := currentEpoch - keepEpochs


	for assetKey, epochBindings := range dsd.bindings {
		for epoch := range epochBindings {
			if epoch < threshold {
				delete(epochBindings, epoch)
				pruned++
			}
		}
		if len(epochBindings) == 0 {
			delete(dsd.bindings, assetKey)
		}
	}

	return pruned
}

// ===== 辅助函数 =====
// nowUnix() 在 ferry_node.go 中定义