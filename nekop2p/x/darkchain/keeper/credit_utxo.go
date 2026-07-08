// Package keeper 信用票据 UTXO 系统。
//
// 实现完整的信用票据生命周期：
//   1. 创建票据 → 计算 commitment → 加入 Merkle 树
//   2. 花费票据 → Merkle 证明 → nullifier → 防双花
//   3. 票据选择 → 覆盖借款金额 → 生成找零票据
//
// 安全性：
//   - 每张票据只能花费一次（nullifier 集合防双花）
//   - 花费证明需要 Merkle 路径（票据确实在树中）
//   - 票据总值 = 借款额 + 找零（守恒定律）
package keeper

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/nekop2p/nekop2p/crypto"
	"github.com/nekop2p/nekop2p/store"
)

// CreditNote 暗域中可花费的信用票据（UTXO 模型）。
// 与 dark/credit.go 中的定义一致，添加了链上操作所需的字段。
type CreditNote struct {
	Value      uint64    // 信用金额
	OwnerKey   [32]byte  // 从 master_secret 派生的公钥
	Serial     [32]byte  // 唯一序列号（随机）
	Commitment [32]byte  // SHA256(Value || OwnerKey || Serial)
	BlockCreated int64   // 创建时的区块高度
}

// ComputeCommitment 计算信用票据的承诺。
// commitment = SHA256(Value || OwnerKey || Serial)
func (n *CreditNote) ComputeCommitment() [32]byte {
	buf := make([]byte, 8+32+32)
	binary.BigEndian.PutUint64(buf[:8], n.Value)
	copy(buf[8:40], n.OwnerKey[:])
	copy(buf[40:72], n.Serial[:])
	return sha256.Sum256(buf)
}

// VerifyCommitment 验证票据承诺是否有效。
func (n *CreditNote) VerifyCommitment() bool {
	return n.Commitment == n.ComputeCommitment()
}

// ============================================================
// 票据创建
// ============================================================

// CreateCreditNote 创建一张新的信用票据并加入 Merkle 树。
// 返回创建后的票据（commitment 已计算并持久化）。
func (k *Keeper) CreateCreditNote(value uint64, ownerKey [32]byte, blockHeight int64) (*CreditNote, error) {
	if value == 0 {
		return nil, fmt.Errorf("credit note value must be > 0")
	}

	// 生成随机序列号
	serialBytes, err := crypto.RandomBytes(32)
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}

	note := &CreditNote{
		Value:        value,
		OwnerKey:     ownerKey,
		BlockCreated: blockHeight,
	}
	copy(note.Serial[:], serialBytes)
	note.Commitment = note.ComputeCommitment()

	// 将承诺加入 Merkle 树并持久化
	if err := k.AddCreditCommitment(note.Commitment[:]); err != nil {
		return nil, fmt.Errorf("add credit commitment: %w", err)
	}

	return note, nil
}

// CreateCreditNotes 批量创建多张信用票据。
// 用于初始信用分配或还款后的找零。
func (k *Keeper) CreateCreditNotes(values []uint64, ownerKey [32]byte, blockHeight int64) ([]*CreditNote, error) {
	notes := make([]*CreditNote, 0, len(values))
	for _, v := range values {
		note, err := k.CreateCreditNote(v, ownerKey, blockHeight)
		if err != nil {
			return notes, fmt.Errorf("create note %d: %w", v, err)
		}
		notes = append(notes, note)
	}
	return notes, nil
}

// ============================================================
// 票据花费
// ============================================================

// SpendInput 票据花费的输入。
type SpendInput struct {
	Note      *CreditNote // 被花费的票据
	MerkleProof *store.MerkleProof // 票据在信用树中的证明
}

// SpendResult 票据花费的结果。
type SpendResult struct {
	SpentNotes   []*CreditNote  // 已花费的票据
	Nullifiers   [][32]byte      // 记录到链上的 nullifier
	ChangeNotes  []*CreditNote   // 找零票据（如果总输入 > 借款额）
	TotalSpent   uint64          // 总花费金额
	ChangeAmount uint64          // 找零金额
}

// SpendCreditNotes 花费信用票据以覆盖所需金额。
//
// 流程:
//   1. 验证每张票据的承诺
//   2. 生成 Merkle 包含性证明
//   3. 验证票据在信用树中
//   4. 生成并记录 nullifier（防双花）
//   5. 从信用树中移除已花费票据
//   6. 生成找零票据（如果输入 > 需求）
//
// 参数:
//   - notes: 要花费的票据列表
//   - masterSecret: 票据所有者的 master_secret（用于生成 nullifier）
//   - required: 需要的信用额度
func (k *Keeper) SpendCreditNotes(notes []*CreditNote, masterSecret [32]byte, required uint64) (*SpendResult, error) {
	if len(notes) == 0 {
		return nil, fmt.Errorf("no notes provided")
	}
	if required == 0 {
		return nil, fmt.Errorf("required amount must be > 0")
	}

	result := &SpendResult{}

	// 1. 选择和验证票据
	var totalInput uint64
	for _, note := range notes {
		if !note.VerifyCommitment() {
			return nil, fmt.Errorf("invalid commitment for note with serial %x", note.Serial[:8])
		}
		totalInput += note.Value
	}

	if totalInput < required {
		return nil, fmt.Errorf("insufficient credit: have %d, need %d", totalInput, required)
	}

	// 2. 预创建找零票据（在修改全局状态之前）
	//    这样如果找零创建失败，不会丢失已花费的票据
	if totalInput > required {
		changeAmount := totalInput - required
		changeNote, err := k.CreateCreditNote(changeAmount, notes[0].OwnerKey, 0)
		if err != nil {
			return nil, fmt.Errorf("create change note: %w", err)
		}
		result.ChangeNotes = append(result.ChangeNotes, changeNote)
		result.ChangeAmount = changeAmount
	}

	// 3. 花费票据：生成 Merkle 证明、标记 nullifier、移除承诺
	for _, note := range notes {
		proof, err := k.GenerateCreditProof(note.Commitment[:])
		if err != nil {
			// ⚠️ 回滚：移除已创建的任何找零票据
			k.rollbackChangeNotes(result.ChangeNotes)
			return nil, fmt.Errorf("merkle proof for note %x: %w", note.Serial[:8], err)
		}

		root := k.CreditTreeRoot()
		if !store.VerifyProof(root, proof, note.Commitment[:]) {
			k.rollbackChangeNotes(result.ChangeNotes)
			return nil, fmt.Errorf("merkle proof verification failed for note %x", note.Serial[:8])
		}

		nullifier := crypto.PRF(masterSecret[:], note.Serial[:])
		nullifierKey := fmt.Sprintf("%x", nullifier[:])
		if k.IsNullifierSpent(nullifierKey) {
			k.rollbackChangeNotes(result.ChangeNotes)
			return nil, fmt.Errorf("double spend detected: nullifier %x already spent", nullifier[:8])
		}

		if !k.MarkNullifier(nullifierKey) {
			k.rollbackChangeNotes(result.ChangeNotes)
			return nil, fmt.Errorf("failed to mark nullifier %x (concurrent spend?)", nullifier[:8])
		}

		if err := k.SpendCreditCommitment(note.Commitment[:]); err != nil {
			k.rollbackChangeNotes(result.ChangeNotes)
			return nil, fmt.Errorf("remove commitment: %w", err)
		}

		result.SpentNotes = append(result.SpentNotes, note)
		result.Nullifiers = append(result.Nullifiers, nullifier)
		result.TotalSpent += note.Value
	}

	return result, nil
}

// ============================================================
// 查询方法
// ============================================================

// GetCreditNote 从信树中查询票据是否未花费。
// 返回票据的 Merkle 证明（如果存在）。
func (k *Keeper) GetCreditNote(commitment []byte) (*store.MerkleProof, error) {
	return k.GenerateCreditProof(commitment)
}

// HasCreditNote 检查票据承诺是否在信用树中。
func (k *Keeper) HasCreditNote(commitment []byte) bool {
	_, err := k.GenerateCreditProof(commitment)
	return err == nil
}

// ============================================================
// 批量初始化（创世）
// ============================================================

// InitializeGenesisCredits 为种子用户创建初始信用票据。
// 创世阶段：每个种子用户获得初始信用额度。
func (k *Keeper) InitializeGenesisCredits(ownerKey [32]byte, creditAmount uint64, blockHeight int64) ([]*CreditNote, error) {
	// 将初始信用拆分为多张票据（方便后续花费）
	numNotes := 3
	if creditAmount < 300 {
		numNotes = 1
	}
	values := splitEvenly(creditAmount, numNotes)
	return k.CreateCreditNotes(values, ownerKey, blockHeight)
}

// splitEvenly 将总额均匀分割为 n 份。
func splitEvenly(total uint64, n int) []uint64 {
	if n <= 0 {
		return nil
	}
	base := total / uint64(n)
	remainder := total % uint64(n)

	result := make([]uint64, n)
	for i := range result {
		result[i] = base
		if uint64(i) < remainder {
			result[i]++
		}
	}
	return result
}

// ============================================================
// 序列化
// ============================================================

// serializeCreditNote 将信用票据序列化为字节。
// 格式: value(8) + ownerKey(32) + serial(32) + commitment(32) = 104 bytes
func serializeCreditNote(note *CreditNote) []byte {
	buf := make([]byte, 104)
	binary.BigEndian.PutUint64(buf[0:8], note.Value)
	copy(buf[8:40], note.OwnerKey[:])
	copy(buf[40:72], note.Serial[:])
	copy(buf[72:104], note.Commitment[:])
	return buf
}

// deserializeCreditNote 从字节反序列化信用票据。
func deserializeCreditNote(data []byte) (*CreditNote, error) {
	if len(data) < 104 {
		return nil, fmt.Errorf("credit note data too short: %d bytes (need 104)", len(data))
	}
	note := &CreditNote{
		Value: binary.BigEndian.Uint64(data[0:8]),
	}
	copy(note.OwnerKey[:], data[8:40])
	copy(note.Serial[:], data[40:72])
	copy(note.Commitment[:], data[72:104])
	return note, nil
}

// SerializeCreditNotes 批量序列化信用票据。
func SerializeCreditNotes(notes []*CreditNote) [][]byte {
	result := make([][]byte, len(notes))
	for i, n := range notes {
		result[i] = serializeCreditNote(n)
	}
	return result
}

// DeserializeCreditNotes 批量反序列化信用票据。
func DeserializeCreditNotes(data [][]byte) ([]*CreditNote, error) {
	notes := make([]*CreditNote, len(data))
	for i, d := range data {
		n, err := deserializeCreditNote(d)
		if err != nil {
			return nil, fmt.Errorf("note %d: %w", i, err)
		}
		notes[i] = n
	}
	return notes, nil
}

// rollbackChangeNotes 回滚已创建的找零票据（从 Merkle 树中移除）。
// 仅在 SpendCreditNotes 中支出现错误时调用。
func (k *Keeper) rollbackChangeNotes(notes []*CreditNote) {
	for _, note := range notes {
		k.creditTree.RemoveCommitment(note.Commitment[:])
		// 从持久化存储中删除
		key := fmt.Sprintf("credit-%x", note.Commitment[:16])
		k.store.Write(func(tx *store.Tx) error {
			return tx.DeleteCreditCommitment(key)
		})
	}
}
