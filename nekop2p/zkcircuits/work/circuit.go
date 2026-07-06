package work

import (
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/std/accumulator/merkle"
	"github.com/consensys/gnark/std/hash/mimc"
)

type Circuit struct {
	EpochNumber         frontend.Variable `gnark:",public"`
	MinPacketsForwarded frontend.Variable `gnark:",public"`
	MinStorageBytes     frontend.Variable `gnark:",public"`
	MinQueryResponses   frontend.Variable `gnark:",public"`
	DataRoot            frontend.Variable `gnark:",public"`
	// 工作量指标现在作为公开输入 — 链上可验证
	PacketsForwarded    frontend.Variable `gnark:",public"`
	StorageBytes        frontend.Variable `gnark:",public"`
	QueryResponses      frontend.Variable `gnark:",public"`
	NodeChainID         frontend.Variable
	EpochNumberWitness  frontend.Variable
	StorageProof        merkle.MerkleProof
}

func (c *Circuit) Define(api frontend.API) error {
	h, _ := mimc.NewMiMC(api)
	// 约束 EpochNumber: 防止跨纪元重放
	api.AssertIsEqual(c.EpochNumber, c.EpochNumberWitness)
	// 将 EpochNumber 纳入 Merkle 验证 — 构造 leaf = MiMC(NodeChainID || EpochNumber)
	h.Write(c.NodeChainID)
	h.Write(c.EpochNumberWitness)
	leaf := h.Sum()
	api.AssertIsLessOrEqual(c.MinPacketsForwarded, c.PacketsForwarded)
	c.StorageProof.VerifyProof(api, &h, leaf)
	api.AssertIsLessOrEqual(c.MinStorageBytes, c.StorageBytes)
	api.AssertIsLessOrEqual(c.MinQueryResponses, c.QueryResponses)
	return nil
}
