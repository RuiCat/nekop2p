// Package credit 实现信用 UTXO 花费证明 ZK 电路（gnark v0.15）。
//
// 证明：有效信用票据花费，不泄露具体票据。
// 哈希：Merkle 树和票据承诺均使用 MiMC。约 8,000 个约束。
package credit

import (
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/std/accumulator/merkle"
	"github.com/consensys/gnark/std/hash/mimc"
)

const MaxInputNotes = 5
const MaxOutputNotes = 3

type Circuit struct {
	LoanAmount     frontend.Variable `gnark:",public"`
	CreditTreeRoot frontend.Variable `gnark:",public"`

	InputNotes  [MaxInputNotes]NoteWitness
	OutputNotes [MaxOutputNotes]NoteWitness
}

type NoteWitness struct {
	Value       frontend.Variable
	OwnerKey    frontend.Variable
	Serial      frontend.Variable
	Commitment  frontend.Variable
	MerkleProof merkle.MerkleProof
	Active      frontend.Variable
}

func (c *Circuit) Define(api frontend.API) error {
	h, _ := mimc.NewMiMC(api)

	inputSum := frontend.Variable(0)
	for i := 0; i < MaxInputNotes; i++ {
		n := c.InputNotes[i]
		n.MerkleProof.VerifyProof(api, &h, n.Commitment)

		// 验证 commitment = MiMC(value || owner_key || serial)
		h2, _ := mimc.NewMiMC(api)
		h2.Write(n.Value)
		h2.Write(n.OwnerKey)
		h2.Write(n.Serial)
		api.AssertIsEqual(n.Commitment, h2.Sum())

		inputSum = api.Add(inputSum, api.Mul(n.Value, n.Active))
	}

	outputSum := frontend.Variable(0)
	for i := 0; i < MaxOutputNotes; i++ {
		o := c.OutputNotes[i]
		outputSum = api.Add(outputSum, api.Mul(o.Value, o.Active))
		// 验证输出承诺
		h3, _ := mimc.NewMiMC(api)
		h3.Write(o.Value)
		h3.Write(o.OwnerKey)
		h3.Write(o.Serial)
		api.AssertIsEqual(o.Commitment, h3.Sum())
	}

	api.AssertIsLessOrEqual(api.Add(c.LoanAmount, outputSum), inputSum)
	return nil
}
