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
	// DiffInverse[i] 提供与 Guarantors[i] 的公钥差的逆元
	// 用于约束活跃担保人公钥必须互不相同
	DiffInverse [MaxGuarantors]frontend.Variable
}

func (c *Circuit) Define(api frontend.API) error {
	curve, err := twistededwards.NewEdCurve(api, twed.BN254)
	if err != nil {
		return err
	}

	validCount := frontend.Variable(0)
	for i := 0; i < MaxGuarantors; i++ {
		g := c.Guarantors[i]
		// 为每个担保人创建独立的 MiMC 哈希器 (防止状态污染)
		h, _ := mimc.NewMiMC(api)

		// EdDSA 验证：担保人对 MySendPK 签名
		eddsa.Verify(curve, g.Signature, c.MySendPK, g.PublicKey, &h)

		// Merkle 验证：PublicKey 已在链上注册
		g.MerkleProof.VerifyProof(api, &h, g.PublicKey.A.X)

		// 信任权重 ≥ 阈值
		pass := api.Cmp(api.Add(g.TrustWeight, 1), c.TrustThreshold)
		validCount = api.Add(validCount, api.Mul(g.Active, pass))
	}

	// 担保人唯一性约束: 活跃担保人的公钥必须互不相同
	// 使用逆元技巧: 若 bothActive=1 则 diff*diff_inv=1 → diff≠0
	one := frontend.Variable(1)
	zero := frontend.Variable(0)
	for i := 0; i < MaxGuarantors; i++ {
		for j := i + 1; j < MaxGuarantors; j++ {
			bothActive := api.Mul(c.Guarantors[i].Active, c.Guarantors[j].Active)
			diff := api.Sub(c.Guarantors[i].PublicKey.A.X, c.Guarantors[j].PublicKey.A.X)
			// 约束: bothActive * (diff * diff_inv - 1) == 0
			// 含义: 两个都活跃时 diff 必须非零 (diff * diff_inv == 1)
			api.AssertIsEqual(
				api.Mul(bothActive, api.Sub(api.Mul(diff, c.Guarantors[i].DiffInverse[j]), one)),
				zero,
			)
		}
	}

	api.AssertIsLessOrEqual(frontend.Variable(MinGuarantors), validCount)
	return nil
}
