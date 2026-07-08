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
	// SerialInverse[j] 用于约束活跃票据必须具有不同的序列号 (防双花)
	SerialInverse [MaxInputNotes]frontend.Variable
}

func (c *Circuit) Define(api frontend.API) error {
	h, _ := mimc.NewMiMC(api)

	inputSum := frontend.Variable(0)
	for i := 0; i < MaxInputNotes; i++ {
		n := c.InputNotes[i]
		// Constrain Active to be 0 or 1: Active * (Active - 1) == 0
		api.AssertIsEqual(
			api.Mul(n.Active, api.Sub(n.Active, 1)),
			frontend.Variable(0),
		)
		n.MerkleProof.VerifyProof(api, &h, n.Commitment)

		// 验证 commitment = MiMC(value || owner_key || serial)
		h2, _ := mimc.NewMiMC(api)
		h2.Write(n.Value)
		h2.Write(n.OwnerKey)
		h2.Write(n.Serial)
		api.AssertIsEqual(n.Commitment, h2.Sum())

		inputSum = api.Add(inputSum, api.Mul(n.Value, n.Active))
	}

	// 输入票据序列号唯一性约束
	one := frontend.Variable(1)
	zero := frontend.Variable(0)
	for i := 0; i < MaxInputNotes; i++ {
		for j := i + 1; j < MaxInputNotes; j++ {
			bothActive := api.Mul(c.InputNotes[i].Active, c.InputNotes[j].Active)
			diff := api.Sub(c.InputNotes[i].Serial, c.InputNotes[j].Serial)
			api.AssertIsEqual(
				api.Mul(bothActive, api.Sub(api.Mul(diff, c.InputNotes[i].SerialInverse[j]), one)),
				zero,
			)
		}
	}

	// 输入票据承诺唯一性约束
	for i := 0; i < MaxInputNotes; i++ {
		for j := i + 1; j < MaxInputNotes; j++ {
			bothActive := api.Mul(c.InputNotes[i].Active, c.InputNotes[j].Active)
			diff := api.Sub(c.InputNotes[i].Commitment, c.InputNotes[j].Commitment)
			diffIsZero := api.IsZero(diff)
			api.AssertIsEqual(api.Mul(bothActive, diffIsZero), zero)
		}
	}

	outputSum := frontend.Variable(0)
	for i := 0; i < MaxOutputNotes; i++ {
		o := c.OutputNotes[i]
		// Constrain Active to be 0 or 1: Active * (Active - 1) == 0
		api.AssertIsEqual(
			api.Mul(o.Active, api.Sub(o.Active, 1)),
			frontend.Variable(0),
		)
		outputSum = api.Add(outputSum, api.Mul(o.Value, o.Active))
		// 验证输出承诺
		h3, _ := mimc.NewMiMC(api)
		h3.Write(o.Value)
		h3.Write(o.OwnerKey)
		h3.Write(o.Serial)
		api.AssertIsEqual(o.Commitment, h3.Sum())
	}

	// 输出票据序列号唯一性约束: 活跃票据的 Serial 必须互不相同
	// 使用逆元技巧: 若 bothActive=1 则 diff*diff_inv=1 → diff≠0
	for i := 0; i < MaxOutputNotes; i++ {
		for j := i + 1; j < MaxOutputNotes; j++ {
			bothActive := api.Mul(c.OutputNotes[i].Active, c.OutputNotes[j].Active)
			diff := api.Sub(c.OutputNotes[i].Serial, c.OutputNotes[j].Serial)
			// 约束: bothActive * (diff * serial_inv - 1) == 0
			api.AssertIsEqual(
				api.Mul(bothActive, api.Sub(api.Mul(diff, c.OutputNotes[i].SerialInverse[j]), one)),
				zero,
			)
		}
	}

	api.AssertIsLessOrEqual(api.Add(c.LoanAmount, outputSum), inputSum)
	return nil
}
