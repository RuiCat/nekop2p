package dark

import (
	"crypto/sha256"
	"fmt"
	"sync"

	"github.com/nekop2p/nekop2p/crypto"
)

// Transaction 表示两个匿名方之间的一笔完整暗域交易。
type Transaction struct {
	ID          [32]byte // 唯一交易 ID
	CycleMarker [32]byte // 区块周期标记（用于防自我交易）
	
	PartyA      *Party  // 发送方/卖方
	PartyB      *Party  // 接收方/买方
	
	InputNotes  []*SpendProofInput // 正在花费的信用票据
	ChangeNotes []*CreditNote     // 创建的找零票据
	
	Status      TxStatus
	mu          sync.RWMutex
}

// Party 是暗域交易中的一方。
type Party struct {
	AnonID         [32]byte // 本笔交易的匿名身份
	IdentityMarker [32]byte // PRF(master_secret, cycle_marker) — 防自我交易
	RecvPK         [32]byte // 本笔交易的临时 Curve25519 公钥
}

// TxStatus 表示交易生命周期。
type TxStatus int

const (
	TxPending   TxStatus = iota
	TxVerified          // 身份标记已验证（防自我交易检查通过）
	TxCommitted         // nullifier 已记录，信用票据已花费
	TxSettled           // 交割已确认，找零票据可用
	TxFailed
)

// TxConfig 保存交易创建参数。
type TxConfig struct {
	CycleMarker  [32]byte
	PartyAKeys   *Keys // 发送方的暗域密钥
	PartyBKeys   *Keys // 接收方的暗域密钥
	InputNotes   []*CreditNote // 要花费的票据（来自 PartyA）
	Amount       uint64        // 交易金额
}

// NewTransaction 创建新的暗域交易。
func NewTransaction(cfg *TxConfig) (*Transaction, error) {
	if len(cfg.InputNotes) == 0 {
		return nil, ErrNoInputNotes
	}

	totalInput := TotalValue(cfg.InputNotes)
	if totalInput < cfg.Amount {
		return nil, fmt.Errorf("dark tx: insufficient credit: have %d, need %d: %w", totalInput, cfg.Amount, ErrInsufficientCredit)
	}

	tx := &Transaction{
		CycleMarker: cfg.CycleMarker,
		Status:      TxPending,
	}
	tx.ID = computeTxID(cfg.CycleMarker, cfg.PartyAKeys.AnonID(0), cfg.PartyBKeys.AnonID(0))

	// 创建带有身份标记的交易双方
	tx.PartyA = &Party{
		AnonID:         cfg.PartyAKeys.AnonID(cfg.PartyAKeys.counter()),
		IdentityMarker: cfg.PartyAKeys.IdentityMarker(cfg.CycleMarker),
	}
	tx.PartyB = &Party{
		AnonID:         cfg.PartyBKeys.AnonID(cfg.PartyBKeys.counter()),
		IdentityMarker: cfg.PartyBKeys.IdentityMarker(cfg.CycleMarker),
	}

	// 为本笔交易生成接收密钥
	epk, err := generateEphemeralRecvPK()
	if err != nil {
		return nil, fmt.Errorf("dark tx: gen recv key: %w", err)
	}
	tx.PartyB.RecvPK = epk

	// 选择并准备输入票据
	selected, change, err := SelectNotes(cfg.InputNotes, cfg.Amount)
	if err != nil {
		return nil, err
	}

	tx.InputNotes = make([]*SpendProofInput, len(selected))
	for i, note := range selected {
		tx.InputNotes[i] = cfg.PartyAKeys.PrepareSpend(note)
	}

	// 如果需要则创建找零票据
	if change > 0 {
		changeNote, err := cfg.PartyAKeys.CreateNote(change, cfg.PartyAKeys.counter()+uint64(len(selected)))
		if err != nil {
			return nil, fmt.Errorf("dark tx: create change: %w", err)
		}
		tx.ChangeNotes = append(tx.ChangeNotes, changeNote)
	}

	// 为 PartyB 创建输出票据
	outputNote, err := cfg.PartyBKeys.CreateNote(cfg.Amount, cfg.PartyBKeys.counter())
	if err != nil {
		return nil, fmt.Errorf("dark tx: create output: %w", err)
	}
	tx.ChangeNotes = append(tx.ChangeNotes, outputNote)

	return tx, nil
}

// Verify 执行防自我交易检查。
// 如果双方具有相同的身份标记（同一个人），则返回错误。
func (tx *Transaction) Verify() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.Status != TxPending {
		return fmt.Errorf("%w: cannot verify in status %v", ErrInvalidStatus, tx.Status)
	}

	// 防自我交易检查：相同身份标记 = 同一个人
	if tx.PartyA.IdentityMarker == tx.PartyB.IdentityMarker {
		tx.Status = TxFailed
		return ErrSelfDealing
	}

	tx.Status = TxVerified
	return nil
}

// Commit 将交易标记为已提交（nullifier 已记录到链上）。
func (tx *Transaction) Commit() ([]SpendProofInput, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.Status != TxVerified {
		return nil, fmt.Errorf("%w: cannot commit in status %v", ErrInvalidStatus, tx.Status)
	}

	nullifiers := make([]SpendProofInput, len(tx.InputNotes))
	for i, inp := range tx.InputNotes {
		nullifiers[i] = *inp
	}

	tx.Status = TxCommitted
	return nullifiers, nil
}

// Settle 将交易标记为已结算（交割已确认）。
func (tx *Transaction) Settle() ([]*CreditNote, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.Status != TxCommitted {
		return nil, fmt.Errorf("%w: cannot settle in status %v", ErrInvalidStatus, tx.Status)
	}

	tx.Status = TxSettled
	return tx.ChangeNotes, nil
}

// IsValid 返回交易是否处于有效状态。
func (tx *Transaction) IsValid() bool {
	tx.mu.RLock()
	defer tx.mu.RUnlock()
	return tx.Status == TxVerified || tx.Status == TxCommitted || tx.Status == TxSettled
}

// Nullifiers 返回所有必须记录到链上的 nullifier。
func (tx *Transaction) Nullifiers() [][32]byte {
	tx.mu.RLock()
	defer tx.mu.RUnlock()
	result := make([][32]byte, len(tx.InputNotes))
	for i, inp := range tx.InputNotes {
		result[i] = inp.Nullifier
	}
	return result
}

// computeTxID 生成确定性交易 ID。
func computeTxID(cycleMarker [32]byte, anonA, anonB [32]byte) [32]byte {
	data := make([]byte, 32+32+32)
	copy(data[0:32], cycleMarker[:])
	copy(data[32:64], anonA[:])
	copy(data[64:96], anonB[:])
	return sha256Sum(data)
}

func (dk *Keys) counter() uint64 {
	return dk.ctr.Add(1)
}

func generateEphemeralRecvPK() ([32]byte, error) {
	kp, err := crypto.GenerateEphemeralKey()
	if err != nil {
		return [32]byte{}, err
	}
	return kp.Public, nil
}

func sha256Sum(data []byte) [32]byte {
	return sha256.Sum256(data)
}
