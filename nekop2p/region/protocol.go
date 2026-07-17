// Package region 交易协议 (Phase C)。
//
// 交易类型:
//   - 同区域交易: A→B 在同一区域, 区域节点直接处理
//   - 跨区域交易: A(区域X)→B(区域Y), 四方协议
//
// Package region 提供交易处理协议。
package region

import (
	"crypto/sha256"
	"fmt"
	"time"
)

// ============================================================
// 交易结构
// ============================================================

// TxStatus 交易状态。
type TxStatus int

const (
	TxPending   TxStatus = 0 // 待处理
	TxLocked    TxStatus = 1 // 已锁定 (发起方余额已扣)
	TxConfirmed TxStatus = 2 // 已确认 (双方完成)
	TxFailed    TxStatus = 3 // 失败
)

// Transaction 交易结构。
type Transaction struct {
	ID         string   // 交易ID = RegionID-Seq
	From       string   // 发起方 ChainID
	To         string   // 接收方 ChainID
	Amount     uint64   // 金额
	Seq        uint64   // 区域序号
	RegionID   string   // 处理区域
	Status     TxStatus
	FromNode   string   // 发起方信任节点
	ToNode     string   // 接收方信任节点
	CrossRef   string   // 跨区引用 (同区域为空)
	CreatedAt  int64
	ConfirmedAt int64
}

// GenerateTxID 生成交易ID。
func GenerateTxID(regionID string, seq uint64) string {
	return fmt.Sprintf("%s-%d", regionID, seq)
}

// ============================================================
// 同区域交易协议 (3步)
// ============================================================

// ProcessLocalTx 处理同区域交易 (事务级锁, 并发安全)。
func ProcessLocalTx(r *RegionNode, from, to string, amount uint64) (*Transaction, error) {
	r.chainMu.Lock()
	defer r.chainMu.Unlock()

	if err := r.LockBalance(from, amount); err != nil {
		return nil, fmt.Errorf("lock balance: %w", err)
	}

	r.TxSeq++
	seq := r.TxSeq

	tx := &Transaction{
		From:      from,
		To:        to,
		Amount:    amount,
		Seq:       seq,
		RegionID:  r.RegionID,
		FromNode:  r.RegionID,
		ToNode:    r.RegionID,
		Status:    TxLocked,
		CreatedAt: time.Now().Unix(),
	}
	tx.ID = GenerateTxID(r.RegionID, seq)
	return tx, nil
}

// ExecuteLocalTx 原子执行同区域交易 (推荐)。
// 在 chainMu 锁内完成锁余额→分配序号→解锁→转账，保证并发安全。
func ExecuteLocalTx(r *RegionNode, from, to string, amount uint64) (*Transaction, error) {
	r.chainMu.Lock()
	defer r.chainMu.Unlock()

	// 1. 锁余额
	if err := r.LockBalance(from, amount); err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}

	// 2. 分配序号
	r.TxSeq++
	seq := r.TxSeq

	// 3. 解锁并转账
	if err := r.UnlockBalance(from, amount); err != nil {
		return nil, fmt.Errorf("unlock: %w", err)
	}
	if err := r.CreditBalance(to, amount); err != nil {
		return nil, fmt.Errorf("credit: %w", err)
	}

	tx := &Transaction{
		ID:         GenerateTxID(r.RegionID, seq),
		From:       from,
		To:         to,
		Amount:     amount,
		Seq:        seq,
		RegionID:   r.RegionID,
		FromNode:   r.RegionID,
		ToNode:     r.RegionID,
		Status:     TxConfirmed,
		CreatedAt:  time.Now().Unix(),
		ConfirmedAt: time.Now().Unix(),
	}

	// 4. 写入区域链
	prevHash := r.StateRoot
	if len(r.Chain) > 0 {
		prevHash = r.Chain[len(r.Chain)-1].Hash()
	}
	block := &RegionBlock{
		Height:    uint64(len(r.Chain)) + 1,
		PrevHash:  prevHash,
		Tx:        tx,
		Timestamp: time.Now().Unix(),
	}
	r.Chain = append(r.Chain, block)
	r.StateRoot = block.Hash()

	return tx, nil
}

// FinalizeLocalTx 完成同区域交易 (与 ProcessLocalTx 配对使用)。
// 注意: 必须在 ProcessLocalTx 的 chainMu 锁内调用以保证原子性。
// 推荐使用 ExecuteLocalTx 进行原子操作。
func FinalizeLocalTx(r *RegionNode, tx *Transaction) error {
	// 解锁发起方
	if err := r.UnlockBalance(tx.From, tx.Amount); err != nil {
		tx.Status = TxFailed
		return err
	}
	// 增加接收方余额
	if err := r.CreditBalance(tx.To, tx.Amount); err != nil {
		tx.Status = TxFailed
		return err
	}
	tx.Status = TxConfirmed
	tx.ConfirmedAt = time.Now().Unix()
	tx.ID = GenerateTxID(r.RegionID, tx.Seq)
	return nil
}

// ============================================================
// 跨区域交易协议 (四方, 4步)
// ============================================================

// CrossRegionRequest 跨区交易请求。
type CrossRegionRequest struct {
	TxID     string `json:"tx_id"`
	From     string `json:"from"`
	To       string `json:"to"`
	Amount   uint64 `json:"amount"`
	FromNode string `json:"from_node"` // 发起方区域节点
	ToNode   string `json:"to_node"`   // 接收方区域节点
	FromSig  []byte `json:"from_sig"`  // 发起方节点签名
}

// CrossRegionResponse 跨区交易响应。
type CrossRegionResponse struct {
	TxID     string `json:"tx_id"`
	Accepted bool   `json:"accepted"`
	ToSig    []byte `json:"to_sig"`    // 接收方节点签名
	Reason   string `json:"reason,omitempty"`
}

// InitiateCrossRegion 发起跨区交易 (Phase 1-2: A→RA₁ 申请)。
func InitiateCrossRegion(fromNode *RegionNode, toNodeID, from, to string, amount uint64) (*Transaction, *CrossRegionRequest, error) {
	// Step 1: 锁定发起方余额
	if err := fromNode.LockBalance(from, amount); err != nil {
		return nil, nil, err
	}

	// Step 2: 分配序号
	fromNode.chainMu.Lock()
	fromNode.TxSeq++
	seq := fromNode.TxSeq
	fromNode.chainMu.Unlock()

	txID := GenerateTxID(fromNode.RegionID, seq)

	req := &CrossRegionRequest{
		TxID:     txID,
		From:     from,
		To:       to,
		Amount:   amount,
		FromNode: fromNode.RegionID,
		ToNode:   toNodeID,
		FromSig:  signData(fromNode.RegionID + txID + from + to),
	}

	tx := &Transaction{
		ID:        txID,
		From:      from,
		To:        to,
		Amount:    amount,
		Seq:       seq,
		RegionID:  fromNode.RegionID,
		Status:    TxLocked,
		FromNode:  fromNode.RegionID,
		ToNode:    toNodeID,
		CreatedAt: time.Now().Unix(),
	}

	// 入队 (在区域链中记录)
	fromNode.TxQueue <- tx

	return tx, req, nil
}

// VerifyCrossRegion 验证跨区交易 (Phase 3: RB₂ 交叉审查)。
func VerifyCrossRegion(toNode *RegionNode, req *CrossRegionRequest) *CrossRegionResponse {
	// 1. 验证接收方在本区域
	_, ok := toNode.Members[req.To]
	if !ok {
		return &CrossRegionResponse{TxID: req.TxID, Accepted: false, Reason: "recipient not in region"}
	}

	// 2. 验证签名
	expectedSig := signData(req.FromNode + req.TxID + req.From + req.To)
	if !verifySig(req.FromSig, expectedSig) {
		return &CrossRegionResponse{TxID: req.TxID, Accepted: false, Reason: "invalid signature"}
	}

	// 3. 交叉审查通过
	return &CrossRegionResponse{
		TxID:     req.TxID,
		Accepted: true,
		ToSig:    signData(req.TxID + req.To + "accepted"),
	}
}

// FinalizeCrossRegion 完成跨区交易 (Phase 4: 双方结算)。
func FinalizeCrossRegion(fromNode, toNode *RegionNode, tx *Transaction) error {
	// 解锁发起方
	if err := fromNode.UnlockBalance(tx.From, tx.Amount); err != nil {
		tx.Status = TxFailed
		return err
	}
	// 增加接收方余额
	if err := toNode.CreditBalance(tx.To, tx.Amount); err != nil {
		tx.Status = TxFailed
		return err
	}
	tx.Status = TxConfirmed
	tx.ConfirmedAt = time.Now().Unix()

	// 生成跨区引用
	tx.CrossRef = fmt.Sprintf("%s:%d↔%s:%d", fromNode.RegionID, tx.Seq, toNode.RegionID, tx.Seq)

	return nil
}

// ============================================================
// 签名辅助 (Phase D 替换为真实 Ed25519)
// ============================================================

func signData(data string) []byte {
	h := sha256.Sum256([]byte(data))
	return h[:]
}

func verifySig(sig, expected []byte) bool {
	if len(sig) != len(expected) {
		return false
	}
	for i := range sig {
		if sig[i] != expected[i] {
			return false
		}
	}
	return true
}
