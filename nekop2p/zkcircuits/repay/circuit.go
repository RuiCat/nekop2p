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
	LoanID           frontend.Variable // 必须等于公开输入 c.LoanID
	BrightMerkleProof merkle.MerkleProof
	Active            frontend.Variable
}

func (c *Circuit) Define(api frontend.API) error {
	h, _ := mimc.NewMiMC(api)
	api.AssertIsLessOrEqual(c.LoanAmount, c.TotalRepaid)
	computed := frontend.Variable(0)
	for i := 0; i < MaxFragments; i++ {
		f := c.Fragments[i]
		// 活跃分片必须绑定到正确的贷款
		// 约束: Active * (LoanID - c.LoanID) == 0
		// 含义: 活跃分片的 LoanID 必须等于电路公开输入 LoanID
		api.AssertIsEqual(
			api.Mul(f.Active, api.Sub(f.LoanID, c.LoanID)),
			frontend.Variable(0),
		)
		// Merkle 验证 BrightTxID 存在性
		f.BrightMerkleProof.VerifyProof(api, &h, f.BrightTxID)
		// 累加活跃分片金额
		computed = api.Add(computed, api.Mul(f.Amount, f.Active))
	}
	api.AssertIsEqual(computed, c.TotalRepaid)
	return nil
}
