package work

import (
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/std/accumulator/merkle"
	"github.com/consensys/gnark/std/hash/mimc"
)

type Circuit struct {
	EpochNumber        frontend.Variable `gnark:",public"`
	MinPacketsForwarded frontend.Variable `gnark:",public"`
	MinStorageBytes    frontend.Variable `gnark:",public"`
	MinQueryResponses  frontend.Variable `gnark:",public"`
	DataRoot           frontend.Variable `gnark:",public"`
	PacketsForwarded   frontend.Variable
	StorageBytes       frontend.Variable
	QueryResponses     frontend.Variable
	NodeChainID        frontend.Variable
	StorageProof       merkle.MerkleProof
}

func (c *Circuit) Define(api frontend.API) error {
	h, _ := mimc.NewMiMC(api)
	api.AssertIsLessOrEqual(c.MinPacketsForwarded, c.PacketsForwarded)
	c.StorageProof.VerifyProof(api, &h, c.NodeChainID)
	api.AssertIsLessOrEqual(c.MinStorageBytes, c.StorageBytes)
	api.AssertIsLessOrEqual(c.MinQueryResponses, c.QueryResponses)
	return nil
}
