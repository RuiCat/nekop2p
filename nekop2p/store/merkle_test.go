//go:build !cosmos

package store_test

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/nekop2p/nekop2p/store"
)

func TestCreditMerkleTreeEmpty(t *testing.T) {
	tree := store.NewCreditMerkleTree()
	root := tree.Root()
	if root != [32]byte{} {
		t.Error("empty tree should have zero root")
	}
	if tree.Count() != 0 {
		t.Error("empty tree count should be 0")
	}
}

func TestCreditMerkleTreeAddAndRoot(t *testing.T) {
	tree := store.NewCreditMerkleTree()

	// 添加承诺
	cm1 := sha256.Sum256([]byte("credit_note_1"))
	cm2 := sha256.Sum256([]byte("credit_note_2"))
	cm3 := sha256.Sum256([]byte("credit_note_3"))

	tree.AddCommitment(cm1[:])
	tree.AddCommitment(cm2[:])
	tree.AddCommitment(cm3[:])

	if tree.Count() != 3 {
		t.Errorf("count: got %d, want 3", tree.Count())
	}

	root := tree.Root()
	if root == [32]byte{} {
		t.Error("non-empty tree should have non-zero root")
	}

	// 相同承诺不应该重复添加
	tree.AddCommitment(cm1[:])
	if tree.Count() != 3 {
		t.Errorf("duplicate add: count got %d, want 3", tree.Count())
	}
}

func TestCreditMerkleTreeProof(t *testing.T) {
	tree := store.NewCreditMerkleTree()

	cm1 := sha256.Sum256([]byte("note_a"))
	cm2 := sha256.Sum256([]byte("note_b"))
	cm3 := sha256.Sum256([]byte("note_c"))

	tree.AddCommitment(cm1[:])
	tree.AddCommitment(cm2[:])
	tree.AddCommitment(cm3[:])

	// 生成证明
	proof, err := tree.GenerateProof(cm1[:])
	if err != nil {
		t.Fatalf("generate proof: %v", err)
	}
	if proof == nil {
		t.Fatal("proof should not be nil")
	}

	// 验证证明
	root := tree.Root()
	if !store.VerifyProof(root, proof, cm1[:]) {
		t.Error("valid proof should verify")
	}

	// 错误的承诺应该验证失败
	fakeCm := sha256.Sum256([]byte("fake_note"))
	if store.VerifyProof(root, proof, fakeCm[:]) {
		t.Error("proof with wrong commitment should fail")
	}
}

func TestCreditMerkleTreeRemove(t *testing.T) {
	tree := store.NewCreditMerkleTree()

	cm1 := sha256.Sum256([]byte("note_1"))
	tree.AddCommitment(cm1[:])
	if tree.Count() != 1 {
		t.Fatal("count should be 1 after add")
	}

	// 移除存在 的承诺
	if !tree.RemoveCommitment(cm1[:]) {
		t.Error("should remove existing commitment")
	}
	if tree.Count() != 0 {
		t.Error("count should be 0 after remove")
	}

	// 移除不存在的承诺
	if tree.RemoveCommitment(cm1[:]) {
		t.Error("should not remove non-existent commitment")
	}

	// 空树根应该是零
	root := tree.Root()
	if root != [32]byte{} {
		t.Error("empty tree should have zero root after all removed")
	}
}

func TestCreditMerkleTreeLarge(t *testing.T) {
	tree := store.NewCreditMerkleTree()

	// 添加 100 个承诺
	commitments := make([][32]byte, 100)
	for i := range commitments {
		commitments[i] = sha256.Sum256([]byte(fmt.Sprintf("note_%d", i)))
		tree.AddCommitment(commitments[i][:])
	}

	if tree.Count() != 100 {
		t.Errorf("count: got %d, want 100", tree.Count())
	}

	// 验证每个承诺的证明
	root := tree.Root()
	for i, cm := range commitments {
		proof, err := tree.GenerateProof(cm[:])
		if err != nil {
			t.Fatalf("note %d: generate proof: %v", i, err)
		}
		if !store.VerifyProof(root, proof, cm[:]) {
			t.Errorf("note %d: proof verification failed", i)
		}
	}
}

func TestCreditMerkleTreeRestore(t *testing.T) {
	// 创建并填充树
	tree1 := store.NewCreditMerkleTree()
	cm1 := sha256.Sum256([]byte("restore_note_1"))
	cm2 := sha256.Sum256([]byte("restore_note_2"))
	tree1.AddCommitment(cm1[:])
	tree1.AddCommitment(cm2[:])
	root1 := tree1.Root()

	// 从快照恢复
	snapshot := tree1.Commitments()
	tree2 := store.NewCreditMerkleTree()
	tree2.Restore(snapshot)
	root2 := tree2.Root()

	if root1 != root2 {
		t.Error("restored tree should have same root")
	}
	if tree2.Count() != 2 {
		t.Errorf("restored count: got %d, want 2", tree2.Count())
	}
}

func TestCreditMerkleTreeProofNotFound(t *testing.T) {
	tree := store.NewCreditMerkleTree()
	cm1 := sha256.Sum256([]byte("exists"))
	tree.AddCommitment(cm1[:])

	fake := sha256.Sum256([]byte("does_not_exist"))
	_, err := tree.GenerateProof(fake[:])
	if err == nil {
		t.Error("should error for non-existent commitment")
	}
}

func TestVerifyProofNilProof(t *testing.T) {
	root := [32]byte{1}
	if store.VerifyProof(root, nil, []byte("anything")) {
		t.Error("nil proof should not verify")
	}
}

func TestMerkleProofLeafIndex(t *testing.T) {
	tree := store.NewCreditMerkleTree()

	// 添加多个承诺并验证索引
	cm1 := sha256.Sum256([]byte("aaa"))
	cm2 := sha256.Sum256([]byte("bbb"))
	cm3 := sha256.Sum256([]byte("ccc"))

	tree.AddCommitment(cm1[:])
	tree.AddCommitment(cm2[:])
	tree.AddCommitment(cm3[:])

	// 所有承诺的索引应该在 [0, 2] 范围内（排序后的位置）
	for _, cm := range [][32]byte{cm1, cm2, cm3} {
		proof, err := tree.GenerateProof(cm[:])
		if err != nil {
			t.Fatalf("generate proof: %v", err)
		}
		if proof.LeafIndex < 0 || proof.LeafIndex >= 3 {
			t.Errorf("leaf index out of range: got %d, want [0,2]", proof.LeafIndex)
		}
		// 验证证明
		root := tree.Root()
		if !store.VerifyProof(root, proof, cm[:]) {
			t.Errorf("proof verification failed for cm at index %d", proof.LeafIndex)
		}
	}

	// 验证每个承诺有唯一的索引
	seen := make(map[int]bool)
	for _, cm := range [][32]byte{cm1, cm2, cm3} {
		proof, _ := tree.GenerateProof(cm[:])
		if seen[proof.LeafIndex] {
			t.Errorf("duplicate leaf index: %d", proof.LeafIndex)
		}
		seen[proof.LeafIndex] = true
	}
	if len(seen) != 3 {
		t.Errorf("expected 3 unique indices, got %d", len(seen))
	}
}
