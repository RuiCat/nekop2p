// Package consensus 全局纪元计数器 — BFT 共识层面的 Epoch 管理。
//
// GlobalEpoch 与 VRG 层的 EpochCounter 职责不同：
//   - GlobalEpoch:    共识层面，每 N 个区块递增，孤网恢复取 max + 1
//   - EpochCounter:   VRG 层面，绑定虚拟根网图中的影子收据和 marker 电路
//
// 孤网恢复规则:
//
//	当孤立的社区重新连接时，取 max(各方 Epoch) + 1 作为新的全局起点。
//	这确保 Epoch 编号在整个网络中单调递增。
package consensus

import (
	"sync"
)

// GlobalEpoch 全局纪元计数器 — 基于 BFT 共识的去中心化 Epoch 递增。
//
// 纪元 (Epoch) 是虚拟根网图中所有影子收据和 marker 电路的时间绑定单位。
// 每 N 个区块自动递增一个 Epoch。
//
// 孤网恢复规则:
//
//	当孤立的社区重新连接时，取 max(各方 Epoch) + 1 作为新的全局起点。
//	这确保 Epoch 编号在整个网络中单调递增。
type GlobalEpoch struct {
	mu sync.RWMutex

	// 当前状态
	currentEpoch   uint64 // 当前 Epoch 编号
	currentBlock   uint64 // 当前区块高度
	blocksPerEpoch uint64 // 每个 Epoch 包含的区块数 (默认 100)

	// 孤网恢复
	isolationCount uint64 // 孤网事件计数
	lastMergeEpoch uint64 // 最后一次合并时的 Epoch

	// Epoch 切换回调
	onEpochChange []func(oldEpoch, newEpoch uint64) // Epoch 切换时的回调函数
}

// NewGlobalEpoch 创建全局纪元计数器。
// blocksPerEpoch: 每个 Epoch 包含的区块数，建议 100。
func NewGlobalEpoch(blocksPerEpoch uint64) *GlobalEpoch {
	if blocksPerEpoch == 0 {
		blocksPerEpoch = 100
	}
	return &GlobalEpoch{
		blocksPerEpoch: blocksPerEpoch,
	}
}

// CurrentEpoch 返回当前 Epoch 编号。
func (ge *GlobalEpoch) CurrentEpoch() uint64 {
	ge.mu.RLock()
	defer ge.mu.RUnlock()
	return ge.currentEpoch
}

// CurrentBlock 返回当前区块高度。
func (ge *GlobalEpoch) CurrentBlock() uint64 {
	ge.mu.RLock()
	defer ge.mu.RUnlock()
	return ge.currentBlock
}

// AdvanceBlock 推进一个区块。
// 当区块数达到 blocksPerEpoch 时自动递增 Epoch。
// 返回 (newEpoch, epochChanged) — epochChanged 为 true 表示发生了 Epoch 切换。
func (ge *GlobalEpoch) AdvanceBlock() (uint64, bool) {
	ge.mu.Lock()
	defer ge.mu.Unlock()

	ge.currentBlock++
	epochChanged := false

	if ge.currentBlock%ge.blocksPerEpoch == 0 {
		oldEpoch := ge.currentEpoch
		ge.currentEpoch++
		epochChanged = true

		for _, cb := range ge.onEpochChange {
			cb(oldEpoch, ge.currentEpoch)
		}
	}

	return ge.currentEpoch, epochChanged
}

// MergeEpoch 合并外部 Epoch（孤网恢复时调用）。
// 取 max(当前 Epoch, 外部 Epoch) + 1 作为新起点。
// 这确保重新连接的孤立社区不会产生 Epoch 冲突。
// 返回合并后的新 Epoch 编号。
func (ge *GlobalEpoch) MergeEpoch(externalEpoch uint64) uint64 {
	ge.mu.Lock()
	defer ge.mu.Unlock()

	maxEpoch := ge.currentEpoch
	if externalEpoch > maxEpoch {
		maxEpoch = externalEpoch
	}

	newEpoch := maxEpoch + 1
	ge.lastMergeEpoch = ge.currentEpoch
	ge.currentEpoch = newEpoch
	ge.isolationCount++

	return newEpoch
}

// OnEpochChange 注册 Epoch 切换回调。
// 当 AdvanceBlock 触发 Epoch 递增时，所有注册的回调按顺序执行。
func (ge *GlobalEpoch) OnEpochChange(callback func(oldEpoch, newEpoch uint64)) {
	ge.mu.Lock()
	defer ge.mu.Unlock()
	ge.onEpochChange = append(ge.onEpochChange, callback)
}

// SetCurrentEpoch 强制设置当前 Epoch（仅用于创世初始化或测试）。
func (ge *GlobalEpoch) SetCurrentEpoch(epoch uint64) {
	ge.mu.Lock()
	defer ge.mu.Unlock()
	ge.currentEpoch = epoch
}

// IsNewEpoch 检查当前区块是否是新 Epoch 的开始。
func (ge *GlobalEpoch) IsNewEpoch() bool {
	ge.mu.RLock()
	defer ge.mu.RUnlock()
	return ge.blocksPerEpoch > 0 && ge.currentBlock%ge.blocksPerEpoch == 0
}
