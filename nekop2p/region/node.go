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
// 故障转移
// ============================================================

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
