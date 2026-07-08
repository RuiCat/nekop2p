// Package store Merkle 信用树实现。
//
// 信用树存储所有未花费信用票据的 commitment，用于：
//   1. 链上验证信用票据的花费（Merkle 包含性证明）
//   2. ZK 信用电路的公开输入（CreditTreeRoot）
//   3. 防双花（配合 nullifier 集合）
//
// 使用 BLAKE3 作为哈希函数（256-bit 输出），
// 与项目的密码学原语选择保持一致。
package store

import (
	"bytes"
	"crypto/sha256"
	"sort"
	"sync"

	"lukechampine.com/blake3"
)

// CreditMerkleTree 存储所有未花费信用票据的承诺。
// 支持并发安全的承诺添加和 Merkle 证明生成。
type CreditMerkleTree struct {
	mu          sync.RWMutex
	commitments [][]byte // 排序后的承诺列表（按字典序）
	root        [32]byte // 缓存的 Merkle 根
	dirty       bool     // 承诺列表是否已更改
}

// NewCreditMerkleTree 创建新的信用 Merkle 树。
func NewCreditMerkleTree() *CreditMerkleTree {
	return &CreditMerkleTree{
		commitments: make([][]byte, 0),
		dirty:       true,
	}
}

// AddCommitment 向树中添加一个信用票据承诺。
// 自动维护承诺列表的排序，并标记根为脏。
func (t *CreditMerkleTree) AddCommitment(commitment []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// 使用二分查找插入位置，保持排序
	idx := sort.Search(len(t.commitments), func(i int) bool {
		return bytes.Compare(t.commitments[i], commitment) >= 0
	})

	// 检查重复
	if idx < len(t.commitments) && bytes.Equal(t.commitments[idx], commitment) {
		return // 已存在
	}

	// 插入
	t.commitments = append(t.commitments, nil)
	copy(t.commitments[idx+1:], t.commitments[idx:])
	t.commitments[idx] = make([]byte, len(commitment))
	copy(t.commitments[idx], commitment)
	t.dirty = true
}

// RemoveCommitment 从树中移除一个信用票据承诺（票据被花费后）。
func (t *CreditMerkleTree) RemoveCommitment(commitment []byte) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	idx := sort.Search(len(t.commitments), func(i int) bool {
		return bytes.Compare(t.commitments[i], commitment) >= 0
	})

	if idx >= len(t.commitments) || !bytes.Equal(t.commitments[idx], commitment) {
		return false
	}

	t.commitments = append(t.commitments[:idx], t.commitments[idx+1:]...)
	t.dirty = true
	return true
}

// Root 返回当前的 Merkle 树根。
// 如果树为空，返回零哈希。
// 结果被缓存直到承诺列表变更。
func (t *CreditMerkleTree) Root() [32]byte {
	t.mu.RLock()
	if !t.dirty {
		root := t.root
		t.mu.RUnlock()
		return root
	}
	t.mu.RUnlock()

	// 需要重新计算
	t.mu.Lock()
	defer t.mu.Unlock()

	// 双重检查（可能在等待锁期间已计算）
	if !t.dirty {
		return t.root
	}

	t.root = computeMerkleRoot(t.commitments)
	t.dirty = false
	return t.root
}

// Count 返回树中的承诺数量。
func (t *CreditMerkleTree) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.commitments)
}

// GenerateProof 为指定承诺生成 Merkle 包含性证明。
// 返回从叶子到根的路径（兄弟节点列表），
// 以及叶子在树中的索引。
//
// ⚠️ 此方法在持有 RLock 的情况下计算根哈希，避免调用 Root() 导致的死锁。
func (t *CreditMerkleTree) GenerateProof(commitment []byte) (*MerkleProof, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// 查找承诺在排序列表中的位置
	idx := sort.Search(len(t.commitments), func(i int) bool {
		return bytes.Compare(t.commitments[i], commitment) >= 0
	})

	if idx >= len(t.commitments) || !bytes.Equal(t.commitments[idx], commitment) {
		return nil, &MerkleError{msg: "commitment not found in tree"}
	}

	// 计算 Merkle 证明（内联计算根以避免调用 Root() 导致的死锁）
	proof := &MerkleProof{
		LeafIndex: idx,
		Siblings:  make([][32]byte, 0),
	}

	leaves := make([][32]byte, len(t.commitments))
	for i, c := range t.commitments {
		leaves[i] = hashLeaf(c)
	}

	currentLevel := leaves
	currentIdx := idx
	var computedRoot [32]byte

	// 单叶节点：根 = 叶子哈希
	if len(currentLevel) == 1 {
		computedRoot = currentLevel[0]
		proof.Root = computedRoot
		return proof, nil
	}

	for len(currentLevel) > 1 {
		nextLevel := make([][32]byte, (len(currentLevel)+1)/2)

		// 找到兄弟节点
		siblingIdx := currentIdx ^ 1 // 翻转最后一位
		if siblingIdx < len(currentLevel) {
			proof.Siblings = append(proof.Siblings, currentLevel[siblingIdx])
		} else {
			// 如果兄弟不存在（奇数层最后一个），用自身作为兄弟
			proof.Siblings = append(proof.Siblings, currentLevel[currentIdx])
		}

		for i := 0; i < len(currentLevel); i += 2 {
			if i+1 < len(currentLevel) {
				nextLevel[i/2] = hashPair(currentLevel[i], currentLevel[i+1])
			} else {
				nextLevel[i/2] = hashPair(currentLevel[i], currentLevel[i])
			}
		}

		currentLevel = nextLevel
		currentIdx /= 2

		// 记录最终根
		if len(currentLevel) == 1 {
			computedRoot = currentLevel[0]
		}
	}

	proof.Root = computedRoot
	return proof, nil
}

// VerifyProof 验证 Merkle 包含性证明。
// 如果承诺确实在树中（root 匹配），返回 true。
func VerifyProof(root [32]byte, proof *MerkleProof, commitment []byte) bool {
	if proof == nil {
		return false
	}

	// 从叶子开始逐层哈希到根
	currentHash := hashLeaf(commitment)
	currentIdx := proof.LeafIndex

	for _, sibling := range proof.Siblings {
		if currentIdx%2 == 0 {
			// 当前是左节点，兄弟是右节点
			currentHash = hashPair(currentHash, sibling)
		} else {
			// 当前是右节点，兄弟是左节点
			currentHash = hashPair(sibling, currentHash)
		}
		currentIdx /= 2
	}

	return currentHash == root
}

// MerkleProof 包含在信用树中的包含性证明。
type MerkleProof struct {
	LeafIndex int        // 叶子在排序承诺列表中的索引
	Siblings  [][32]byte // 从叶子到根的兄弟节点哈希列表
	Root      [32]byte   // 预期的 Merkle 根
}

// MerkleError 表示 Merkle 树操作错误。
type MerkleError struct {
	msg string
}

func (e *MerkleError) Error() string {
	return "merkle: " + e.msg
}

// ===== 内部哈希函数 =====

// hashLeaf 对单个叶子的内容进行哈希。
// leafHash = BLAKE3("credit_leaf" || data)
func hashLeaf(data []byte) [32]byte {
	h := blake3.New(32, nil)
	h.Write([]byte("credit_leaf"))
	h.Write(data)
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

// hashPair 对一对子节点进行哈希。
// pairHash = SHA256(left || right)（使用 SHA256 以与 ZK 电路兼容）
func hashPair(left, right [32]byte) [32]byte {
	h := sha256.New()
	h.Write(left[:])
	h.Write(right[:])
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

// computeMerkleRoot 从承诺列表计算 Merkle 根。
func computeMerkleRoot(commitments [][]byte) [32]byte {
	if len(commitments) == 0 {
		return [32]byte{} // 空树根为零哈希
	}

	// 叶节点哈希
	leaves := make([][32]byte, len(commitments))
	for i, c := range commitments {
		leaves[i] = hashLeaf(c)
	}

	// 逐层向上哈希
	currentLevel := leaves
	for len(currentLevel) > 1 {
		nextLevel := make([][32]byte, (len(currentLevel)+1)/2)
		for i := 0; i < len(currentLevel); i += 2 {
			if i+1 < len(currentLevel) {
				nextLevel[i/2] = hashPair(currentLevel[i], currentLevel[i+1])
			} else {
				// 奇数个节点：最后一个与自己配对
				nextLevel[i/2] = hashPair(currentLevel[i], currentLevel[i])
			}
		}
		currentLevel = nextLevel
	}

	return currentLevel[0]
}

// ===== 序列化 =====

// Commitments 返回当前所有承诺的快照（线程安全）。
func (t *CreditMerkleTree) Commitments() [][]byte {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([][]byte, len(t.commitments))
	for i, c := range t.commitments {
		result[i] = make([]byte, len(c))
		copy(result[i], c)
	}
	return result
}

// Restore 从持久化的承诺列表恢复树状态。
func (t *CreditMerkleTree) Restore(commitments [][]byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.commitments = make([][]byte, len(commitments))
	for i, c := range commitments {
		t.commitments[i] = make([]byte, len(c))
		copy(t.commitments[i], c)
	}
	sort.Slice(t.commitments, func(i, j int) bool {
		return bytes.Compare(t.commitments[i], t.commitments[j]) < 0
	})
	t.dirty = true
}
