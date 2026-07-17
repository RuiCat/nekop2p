// Package region 区域节点管理 (Phase B)。
//
// 区域节点职责:
//   - 承载本区域所有用户的交易流量
//   - 串行处理交易 (每次一笔)
//   - 维护区域链 + 状态快照
//   - 跨区协调
//
// Package region 提供区域节点管理。
package region

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

// ============================================================
// 区域节点
// ============================================================

// RegionNode 区域节点 (微型共识引擎 + 流量枢纽)。
type RegionNode struct {
	RegionID   string                 // 区域标识
	GridIndex  int                    // 0=网格1, 1=网格2
	Members    map[string]*MemberState // ChainID → 成员状态
	mu         sync.RWMutex

	// 区域链
	Chain      []*RegionBlock
	chainMu    sync.RWMutex
	StateRoot  [32]byte

	// 交易处理
	TxSeq      uint64       // 交易序号 (单调递增)
	TxQueue    chan *Transaction // 待处理交易队列
	batchSize  int           // 批量提交大小

	// 故障转移
	Partner    *RegionNode  // 同区域另一节点 (热备)
	Shadow     *RegionNode  // 影子候补
	lastBeat   time.Time
	isActive   bool

	// 收入追踪
	Earnings       uint64  // 累计交易处理费收入
	CrossEarnings  uint64  // 跨区协调费收入
	BatchFeesPaid  uint64  // 支付给全局链的记录费
}

// MemberState 区域成员状态。
type MemberState struct {
	ChainID    string
	Balance    uint64
	LockedAmt  uint64    // 交易中锁定金额
	Coord      SpatialCoord
	OtherNode  string    // 用户在另一个网格的区域节点 ID
}

// ============================================================
// 区域区块
// ============================================================

// RegionBlock 区域链区块。
type RegionBlock struct {
	Height     uint64
	PrevHash   [32]byte
	Tx         *Transaction
	Timestamp  int64
	RegionSig  []byte     // 区域节点 Ed25519 签名
}

// Hash 计算区块哈希。
func (b *RegionBlock) Hash() [32]byte {
	h := sha256.New()
	fmt.Fprintf(h, "%d%x%s%d", b.Height, b.PrevHash, b.Tx.ID, b.Timestamp)
	return sha256.Sum256(h.Sum(nil))
}

// ============================================================
// 批量提交 (到全局链)
// ============================================================

// RegionBatch 批量提交到全局链的数据结构。
type RegionBatch struct {
	RegionID   string          `json:"region"`
	StartSeq   uint64          `json:"start_seq"`
	EndSeq     uint64          `json:"end_seq"`
	TxsMerkle  [32]byte        `json:"txs_merkle"`   // 交易 Merkle 根
	StateRoot  [32]byte        `json:"state_root"`    // 余额状态根
	CrossRefs  []CrossRegionRef `json:"cross_refs"`   // 跨区引用
	Signature  []byte          `json:"signature"`
}

// CrossRegionRef 跨区交易引用。
type CrossRegionRef struct {
	LocalSeq      uint64 `json:"local_seq"`
	RemoteRegion  string `json:"remote_region"`
	RemoteSeq     uint64 `json:"remote_seq"`
}

// ============================================================
// 节点操作
// ============================================================

// NewRegionNode 创建区域节点。
func NewRegionNode(regionID string, gridIndex, batchSize int) *RegionNode {
	if batchSize <= 0 {
		batchSize = 100
	}
	return &RegionNode{
		RegionID:  regionID,
		GridIndex: gridIndex,
		Members:   make(map[string]*MemberState),
		TxQueue:   make(chan *Transaction, 1000),
		batchSize: batchSize,
		isActive:  true,
		lastBeat:  time.Now(),
	}
}

// AddMember 添加区域成员。
func (rn *RegionNode) AddMember(chainID string, balance uint64, coord SpatialCoord, otherNode string) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	rn.Members[chainID] = &MemberState{
		ChainID:   chainID,
		Balance:   balance,
		Coord:     coord,
		OtherNode: otherNode,
	}
}

// GetBalance 获取成员余额。
func (rn *RegionNode) GetBalance(chainID string) (uint64, bool) {
	rn.mu.RLock()
	defer rn.mu.RUnlock()
	m, ok := rn.Members[chainID]
	if !ok {
		return 0, false
	}
	return m.Balance, true
}

// LockBalance 锁定成员余额 (交易中)。
func (rn *RegionNode) LockBalance(chainID string, amount uint64) error {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	m, ok := rn.Members[chainID]
	if !ok {
		return fmt.Errorf("member %s not in region %s", chainID, rn.RegionID)
	}
	if m.Balance < amount {
		return fmt.Errorf("insufficient balance: %d < %d", m.Balance, amount)
	}
	m.Balance -= amount
	m.LockedAmt += amount
	return nil
}

// UnlockBalance 解锁成员余额 (交易完成或取消)。
func (rn *RegionNode) UnlockBalance(chainID string, amount uint64) error {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	m, ok := rn.Members[chainID]
	if !ok {
		return fmt.Errorf("member %s not in region", chainID)
	}
	if m.LockedAmt < amount {
		return fmt.Errorf("locked amount insufficient: %d < %d", m.LockedAmt, amount)
	}
	m.LockedAmt -= amount
	return nil
}

// CreditBalance 增加成员余额。
func (rn *RegionNode) CreditBalance(chainID string, amount uint64) error {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	m, ok := rn.Members[chainID]
	if !ok {
		return fmt.Errorf("member %s not in region", chainID)
	}
	m.Balance += amount
	return nil
}

// ============================================================
// 费用参数
// ============================================================

// FeeParams 区域节点费用参数 (治理可调)。
type FeeParams struct {
	LocalTxRate      uint64 // 同区域交易费率 (基点, 默认 10 = 0.1%)
	CrossTxRate      uint64 // 跨区域交易费率 (基点, 默认 50 = 0.5%)
	BatchSubmitFee   uint64 // 批量提交到全局链的费用 (默认 100)
	GlobalChainShare uint64 // 全局链记录费占比 (%, 默认 20)
}

// DefaultFeeParams 默认费用参数。
func DefaultFeeParams() FeeParams {
	return FeeParams{
		LocalTxRate:      10,   // 0.1%
		CrossTxRate:      50,   // 0.5%
		BatchSubmitFee:   100,  // 100 uneko/批次
		GlobalChainShare: 20,   // 20% 归全局链
	}
}

// CalcLocalFee 计算同区域交易处理费。
func (fp FeeParams) CalcLocalFee(amount uint64) uint64 {
	return amount * fp.LocalTxRate / 10000
}

// CalcCrossFee 计算跨区域交易处理费。
func (fp FeeParams) CalcCrossFee(amount uint64) uint64 {
	return amount * fp.CrossTxRate / 10000
}

// ============================================================
// 收入操作
// ============================================================

// AddEarnings 增加交易处理费收入。
func (rn *RegionNode) AddEarnings(amount uint64) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	rn.Earnings += amount
}

// AddCrossEarnings 增加跨区协调费收入。
func (rn *RegionNode) AddCrossEarnings(amount uint64) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	rn.CrossEarnings += amount
}

// PayBatchFee 支付批量提交费给全局链。
func (rn *RegionNode) PayBatchFee(amount uint64) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	rn.BatchFeesPaid += amount
}

// GetRevenue 获取净收入 = 处理费 + 跨区费 - 记录费。
func (rn *RegionNode) GetRevenue() uint64 {
	rn.mu.RLock()
	defer rn.mu.RUnlock()
	total := rn.Earnings + rn.CrossEarnings
	if total > rn.BatchFeesPaid {
		return total - rn.BatchFeesPaid
	}
	return 0
}


// ============================================================
// 状态快照 — 持久化与恢复
// ============================================================

// SnapshotData 可序列化的快照数据。
type SnapshotData struct {
	RegionID     string            `json:"region_id"`
	GridIndex    int               `json:"grid_index"`
	Members      map[string]uint64 `json:"members"`
	TxSeq        uint64            `json:"tx_seq"`
	Earnings     uint64            `json:"earnings"`
	CrossEarnings uint64           `json:"cross_earnings"`
	BatchFeesPaid uint64           `json:"batch_fees_paid"`
}

// SaveSnapshot 导出可持久化的快照。
func (rn *RegionNode) SaveSnapshot() *SnapshotData {
	rn.mu.RLock()
	defer rn.mu.RUnlock()
	rn.chainMu.RLock()
	defer rn.chainMu.RUnlock()
	members := make(map[string]uint64, len(rn.Members))
	for id, m := range rn.Members { members[id] = m.Balance }
	return &SnapshotData{
		RegionID: rn.RegionID, GridIndex: rn.GridIndex,
		Members: members, TxSeq: rn.TxSeq,
		Earnings: rn.Earnings, CrossEarnings: rn.CrossEarnings,
		BatchFeesPaid: rn.BatchFeesPaid,
	}
}

// LoadSnapshot 从快照恢复状态。
func (rn *RegionNode) LoadSnapshot(snap *SnapshotData) {
	rn.mu.Lock(); defer rn.mu.Unlock()
	rn.chainMu.Lock(); defer rn.chainMu.Unlock()
	rn.TxSeq = snap.TxSeq
	rn.Earnings = snap.Earnings
	rn.CrossEarnings = snap.CrossEarnings
	rn.BatchFeesPaid = snap.BatchFeesPaid
	for id, bal := range snap.Members {
		rn.Members[id] = &MemberState{ChainID: id, Balance: bal}
	}
}

// Pause 暂停区域节点服务 (节点离线/降级时调用)。
func (rn *RegionNode) Pause() {
	rn.mu.Lock(); defer rn.mu.Unlock()
	rn.isActive = false
}

// Resume 恢复区域节点服务 (节点重新上线时调用)。
func (rn *RegionNode) Resume() {
	rn.mu.Lock(); defer rn.mu.Unlock()
	rn.isActive = true; rn.lastBeat = time.Now()
}

// MonthlySettle 月度结算: 返回净收入并重置计数器。
func (rn *RegionNode) MonthlySettle() uint64 {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	revenue := rn.Earnings + rn.CrossEarnings
	if revenue > rn.BatchFeesPaid {
		revenue -= rn.BatchFeesPaid
	} else {
		revenue = 0
	}
	rn.Earnings = 0
	rn.CrossEarnings = 0
	rn.BatchFeesPaid = 0
	return revenue
}

// Heartbeat 发送心跳。
func (rn *RegionNode) Heartbeat() {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	rn.lastBeat = time.Now()
}

// IsAlive 检查节点是否存活。
func (rn *RegionNode) IsAlive(timeout time.Duration) bool {
	rn.mu.RLock()
	defer rn.mu.RUnlock()
	return rn.isActive && time.Since(rn.lastBeat) < timeout
}

// Failover 故障转移到影子节点。
func (rn *RegionNode) Failover(shadow *RegionNode) error {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	if shadow == nil {
		return fmt.Errorf("no shadow node for failover")
	}
	rn.isActive = false
	// 影子节点接管状态
	shadow.Members = rn.Members
	shadow.Chain = rn.Chain
	shadow.TxSeq = rn.TxSeq
	shadow.StateRoot = rn.StateRoot
	shadow.isActive = true
	return nil
}

// ============================================================
// 交易处理 (串行)
// ============================================================

// ProcessLoop 交易处理循环。
func (rn *RegionNode) ProcessLoop() {
	for tx := range rn.TxQueue {
		rn.processTx(tx)
	}
}

// processTx 处理单笔交易 (串行, 无并发)。
func (rn *RegionNode) processTx(tx *Transaction) {
	rn.chainMu.Lock()
	defer rn.chainMu.Unlock()

	// 1. 分配序号
	rn.TxSeq++
	tx.Seq = rn.TxSeq
	tx.RegionID = rn.RegionID

	// 2. 写入区域链
	prevHash := rn.StateRoot
	if len(rn.Chain) > 0 {
		prevHash = rn.Chain[len(rn.Chain)-1].Hash()
	}
	block := &RegionBlock{
		Height:    uint64(len(rn.Chain)) + 1,
		PrevHash:  prevHash,
		Tx:        tx,
		Timestamp: time.Now().Unix(),
	}
	rn.Chain = append(rn.Chain, block)
	rn.StateRoot = block.Hash()
}

// GetTxCount 返回已处理交易总数。
func (rn *RegionNode) GetTxCount() uint64 {
	rn.chainMu.RLock()
	defer rn.chainMu.RUnlock()
	return rn.TxSeq
}

// GetChainBlocks 返回区域链区块列表。
func (rn *RegionNode) GetChainBlocks() []*RegionBlock {
	rn.chainMu.RLock()
	defer rn.chainMu.RUnlock()
	blocks := make([]*RegionBlock, len(rn.Chain))
	copy(blocks, rn.Chain)
	return blocks
}
func (rn *RegionNode) GetBatch(startSeq, endSeq uint64, crossRefs []CrossRegionRef) *RegionBatch {
	rn.chainMu.RLock()
	defer rn.chainMu.RUnlock()

	// 计算交易 Merkle 根
	txsMerkle := rn.StateRoot // 简化: 用最后一个区块哈希

	return &RegionBatch{
		RegionID:  rn.RegionID,
		StartSeq:  startSeq,
		EndSeq:    endSeq,
		TxsMerkle: txsMerkle,
		StateRoot: rn.StateRoot,
		CrossRefs: crossRefs,
	}
}
