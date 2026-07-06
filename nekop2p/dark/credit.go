package dark

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/nekop2p/nekop2p/crypto"
)

// CreditNote 是暗域中可花费的信用单位。
// 类似于 Zcash 的 UTXO：每张票据只能花费一次。
type CreditNote struct {
	Value      uint64  // 信用金额
	OwnerKey   [32]byte // 从 master_secret 派生的公钥
	Serial     [32]byte // 唯一序列号（随机）
	Commitment [32]byte // SHA256(Value || OwnerKey || Serial)
}

// CreateNote 生成新的信用票据。
func (dk *Keys) CreateNote(value uint64, noteIndex uint64) (*CreditNote, error) {
	// 从 master_secret 派生所有者密钥
	ownerKey := crypto.DeriveKey(dk.MasterSecret[:], uint64ToBytes(noteIndex))

	// 生成随机序列号
	serial, err := crypto.RandomBytes(32)
	if err != nil {
		return nil, err
	}

	note := &CreditNote{
		Value:    value,
		OwnerKey: ownerKey,
	}
	copy(note.Serial[:], serial)

	// Commitment = SHA256(Value || OwnerKey || Serial)
	note.Commitment = note.computeCommitment()
	return note, nil
}

// Nullifier 生成用于花费此票据的 nullifier。
// nullifier = PRF(master_secret, serial)
// 记录 nullifier 可以防止双重花费。
func (dk *Keys) Nullifier(note *CreditNote) [32]byte {
	return crypto.PRF(dk.MasterSecret[:], note.Serial[:])
}

func (n *CreditNote) computeCommitment() [32]byte {
	buf := make([]byte, 8+32+32)
	binary.BigEndian.PutUint64(buf[:8], n.Value)
	copy(buf[8:40], n.OwnerKey[:])
	copy(buf[40:72], n.Serial[:])
	return sha256.Sum256(buf)
}

// VerifyCommitment 检查票据的承诺是否有效。
func (n *CreditNote) VerifyCommitment() bool {
	return n.Commitment == n.computeCommitment()
}

// SpendProofInput 是信用花费证明的输入。
type SpendProofInput struct {
	Note     *CreditNote
	Nullifier [32]byte // PRF(master_secret, serial)
}

// PrepareSpend 创建花费票据所需的 nullifier。
// 返回需要提交到暗链的 nullifier。
func (dk *Keys) PrepareSpend(note *CreditNote) *SpendProofInput {
	return &SpendProofInput{
		Note:      note,
		Nullifier: dk.Nullifier(note),
	}
}

// TotalValue 计算多张信用票据的总价值。
func TotalValue(notes []*CreditNote) uint64 {
	var sum uint64
	for _, n := range notes {
		sum += n.Value
	}
	return sum
}

// SelectNotes 选择票据以覆盖所需金额。
// 返回选中的票据和找零金额。
func SelectNotes(notes []*CreditNote, required uint64) ([]*CreditNote, uint64, error) {
	var selected []*CreditNote
	var total uint64

	for _, n := range notes {
		selected = append(selected, n)
		total += n.Value
		if total >= required {
			change := total - required
			return selected, change, nil
		}
	}

	return nil, 0, fmt.Errorf("insufficient credit: have %d, need %d", total, required)
}

func uint64ToBytes(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}
