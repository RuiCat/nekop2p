package repay

import (
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/std/accumulator/merkle"
	"github.com/consensys/gnark/std/hash/mimc"
)

const MaxFragments = 7

type Circuit struct {
	LoanID      frontend.Variable `gnark:",public"`
	TotalRepaid frontend.Variable `gnark:",public"`
	LoanAmount  frontend.Variable `gnark:",public"`
	BrightRoot  frontend.Variable `gnark:",public"`
	Fragments   [MaxFragments]FragmentWitness
}

type FragmentWitness struct {
	Amount            frontend.Variable
	BrightTxID        frontend.Variable
	BrightMerkleProof merkle.MerkleProof
	Active            frontend.Variable
}

func (c *Circuit) Define(api frontend.API) error {
	h, _ := mimc.NewMiMC(api)
	api.AssertIsLessOrEqual(c.LoanAmount, c.TotalRepaid)
	computed := frontend.Variable(0)
	for i := 0; i < MaxFragments; i++ {
		f := c.Fragments[i]
		// 仅对活跃分片验证 Merkle 证明
		// 对于非活跃分片：跳过验证（信任电路构建者提供虚拟有效证明）
		f.BrightMerkleProof.VerifyProof(api, &h, f.BrightTxID)
		computed = api.Add(computed, api.Mul(f.Amount, f.Active))
	}
	api.AssertIsEqual(computed, c.TotalRepaid)
	return nil
}
