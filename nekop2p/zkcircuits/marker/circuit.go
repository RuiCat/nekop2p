// Package marker 实现身份标记 ZK 电路（gnark v0.15）。
//
// 证明：identity_marker = MiMC(master_secret || cycle_marker)
// 约 500 个约束。
package marker

import (
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/std/hash/mimc"
)

type Circuit struct {
	CycleMarker    frontend.Variable `gnark:",public"`
	IdentityMarker frontend.Variable `gnark:",public"`
	MasterSecret   frontend.Variable
}

func (c *Circuit) Define(api frontend.API) error {
	h, _ := mimc.NewMiMC(api)
	h.Write(c.MasterSecret)
	h.Write(c.CycleMarker)
	api.AssertIsEqual(h.Sum(), c.IdentityMarker)
	return nil
}
