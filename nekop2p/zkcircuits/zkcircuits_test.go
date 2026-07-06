package zkcircuits_test

import (
	"math/big"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
	"github.com/consensys/gnark/std/accumulator/merkle"

	"github.com/nekop2p/nekop2p/zkcircuits/credit"
	"github.com/nekop2p/nekop2p/zkcircuits/identity"
	"github.com/nekop2p/nekop2p/zkcircuits/marker"
	"github.com/nekop2p/nekop2p/zkcircuits/repay"
	"github.com/nekop2p/nekop2p/zkcircuits/work"
)

// dummyMerkleProof 创建一个最小的 MerkleProof 用于编译测试。
// 路径必须至少有 2 个条目（深度 ≥ 1）以避免 gnark 崩溃。
func dummyMerkleProof() merkle.MerkleProof {
	return merkle.MerkleProof{
		Path: []frontend.Variable{big.NewInt(0), big.NewInt(0)},
	}
}

// TestAllCircuitsCompile 验证所有 ZK 电路都能编译为 R1CS
// 并成功完成 Groth16 设置。
func TestAllCircuitsCompile(t *testing.T) {
	circuits := []struct {
		name    string
		circuit frontend.Circuit
		minC    int
	}{
		{"marker", &marker.Circuit{}, 500},
		{"repay", &repay.Circuit{
			Fragments: [repay.MaxFragments]repay.FragmentWitness{
				{BrightMerkleProof: dummyMerkleProof()},
				{BrightMerkleProof: dummyMerkleProof()},
				{BrightMerkleProof: dummyMerkleProof()},
				{BrightMerkleProof: dummyMerkleProof()},
				{BrightMerkleProof: dummyMerkleProof()},
				{BrightMerkleProof: dummyMerkleProof()},
				{BrightMerkleProof: dummyMerkleProof()},
			},
		}, 2000},
		{"work", &work.Circuit{
			StorageProof: dummyMerkleProof(),
		}, 2000},
		{"credit", &credit.Circuit{
			InputNotes: [credit.MaxInputNotes]credit.NoteWitness{
				{MerkleProof: dummyMerkleProof()},
				{MerkleProof: dummyMerkleProof()},
				{MerkleProof: dummyMerkleProof()},
				{MerkleProof: dummyMerkleProof()},
				{MerkleProof: dummyMerkleProof()},
			},
		}, 5000},
		{"identity", &identity.Circuit{
			Guarantors: [identity.MaxGuarantors]identity.GuarantorWitness{
				{MerkleProof: dummyMerkleProof()},
				{MerkleProof: dummyMerkleProof()},
				{MerkleProof: dummyMerkleProof()},
				{MerkleProof: dummyMerkleProof()},
				{MerkleProof: dummyMerkleProof()},
			},
		}, 8000},
	}

	for _, tc := range circuits {
		t.Run(tc.name, func(t *testing.T) {
			ccs, err := frontend.Compile(ecc.BN254.ScalarField(), r1cs.NewBuilder, tc.circuit)
			if err != nil {
				t.Fatalf("compile %s: %v", tc.name, err)
			}

			nb := ccs.GetNbConstraints()
			t.Logf("  %s: %d constraints (min expected: %d)", tc.name, nb, tc.minC)

			if nb < tc.minC {
				t.Errorf("  too few constraints: %d < %d", nb, tc.minC)
			}

			pk, vk, err := groth16.Setup(ccs)
			if err != nil {
				t.Fatalf("setup %s: %v", tc.name, err)
			}
			_ = pk
			_ = vk
		})
	}

	t.Logf("✓ all 5 circuits compile and complete Groth16 setup")
}

func TestCircuitConstraintCounts(t *testing.T) {
	circuits := []struct {
		name    string
		circuit frontend.Circuit
	}{
		{"marker", &marker.Circuit{}},
		{"repay", &repay.Circuit{
			Fragments: [repay.MaxFragments]repay.FragmentWitness{
				{BrightMerkleProof: dummyMerkleProof()},
				{BrightMerkleProof: dummyMerkleProof()},
				{BrightMerkleProof: dummyMerkleProof()},
				{BrightMerkleProof: dummyMerkleProof()},
				{BrightMerkleProof: dummyMerkleProof()},
				{BrightMerkleProof: dummyMerkleProof()},
				{BrightMerkleProof: dummyMerkleProof()},
			},
		}},
		{"work", &work.Circuit{StorageProof: dummyMerkleProof()}},
		{"credit", &credit.Circuit{
			InputNotes: [credit.MaxInputNotes]credit.NoteWitness{
				{MerkleProof: dummyMerkleProof()},
				{MerkleProof: dummyMerkleProof()},
				{MerkleProof: dummyMerkleProof()},
				{MerkleProof: dummyMerkleProof()},
				{MerkleProof: dummyMerkleProof()},
			},
		}},
		{"identity", &identity.Circuit{
			Guarantors: [identity.MaxGuarantors]identity.GuarantorWitness{
				{MerkleProof: dummyMerkleProof()},
				{MerkleProof: dummyMerkleProof()},
				{MerkleProof: dummyMerkleProof()},
				{MerkleProof: dummyMerkleProof()},
				{MerkleProof: dummyMerkleProof()},
			},
		}},
	}

	t.Log("ZK Circuit Constraint Counts:")
	total := 0
	for _, tc := range circuits {
		ccs, err := frontend.Compile(ecc.BN254.ScalarField(), r1cs.NewBuilder, tc.circuit)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		nb := ccs.GetNbConstraints()
		total += nb
		t.Logf("  %-10s %6d constraints", tc.name, nb)
	}
	t.Logf("  %-10s %6d constraints", "TOTAL", total)
}
