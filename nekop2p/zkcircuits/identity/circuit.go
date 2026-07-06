// Package identity 实现身份证明 ZK 电路（gnark v0.15）。
//
// 证明：用户拥有 ≥3 个注册的担保人，且信任权重足够。
// 哈希：MiMC（ZK 原生，约束少）。生产环境：对承诺改用 SHA256。
package identity

import (
	twed "github.com/consensys/gnark-crypto/ecc/twistededwards"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/std/accumulator/merkle"
	"github.com/consensys/gnark/std/algebra/native/twistededwards"
	"github.com/consensys/gnark/std/hash/mimc"
	"github.com/consensys/gnark/std/signature/eddsa"
)

const MaxGuarantors = 5
const MinGuarantors = 3

type Circuit struct {
	MySendPK       frontend.Variable `gnark:",public"`
	MerkleRoot     frontend.Variable `gnark:",public"`
	TrustThreshold frontend.Variable `gnark:",public"`
	Guarantors     [MaxGuarantors]GuarantorWitness
}

type GuarantorWitness struct {
	Signature   eddsa.Signature
	PublicKey   eddsa.PublicKey
	MerkleProof merkle.MerkleProof
	TrustWeight frontend.Variable
	Active      frontend.Variable
}

func (c *Circuit) Define(api frontend.API) error {
	curve, err := twistededwards.NewEdCurve(api, twed.BN254)
	if err != nil {
		return err
	}
	h, _ := mimc.NewMiMC(api)

	validCount := frontend.Variable(0)
	for i := 0; i < MaxGuarantors; i++ {
		g := c.Guarantors[i]

		// EdDSA 验证：担保人对 MySendPK 签名
		eddsa.Verify(curve, g.Signature, c.MySendPK, g.PublicKey, &h)

		// Merkle 验证：PublicKey 已在链上注册
		g.MerkleProof.VerifyProof(api, &h, g.PublicKey.A.X)

		// 信任权重 ≥ 阈值（Cmp 返回 1 当且仅当 a > b，因此用 a+1 > b 实现 ≥）
		pass := api.Cmp(api.Add(g.TrustWeight, 1), c.TrustThreshold)
		validCount = api.Add(validCount, api.Mul(g.Active, pass))
	}

	api.AssertIsLessOrEqual(frontend.Variable(MinGuarantors), validCount)
	return nil
}
