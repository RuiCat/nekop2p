package setup

import (
	"fmt"
	"os"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"

	"github.com/nekop2p/nekop2p/zkcircuits/credit"
	"github.com/nekop2p/nekop2p/zkcircuits/identity"
	"github.com/nekop2p/nekop2p/zkcircuits/marker"
	"github.com/nekop2p/nekop2p/zkcircuits/repay"
	"github.com/nekop2p/nekop2p/zkcircuits/work"
)

type Result struct {
	Name            string
	ProvingKey      groth16.ProvingKey
	VerifyingKey    groth16.VerifyingKey
	ConstraintCount int
}

func SetupAll() ([]*Result, error) {
	circuits := []struct {
		name string
		c    frontend.Circuit
	}{
		{"identity", &identity.Circuit{}},
		{"credit", &credit.Circuit{}},
		{"marker", &marker.Circuit{}},
		{"repay", &repay.Circuit{}},
		{"work", &work.Circuit{}},
	}

	var results []*Result
	for _, circ := range circuits {
		r, err := setupCircuit(circ.name, circ.c)
		if err != nil {
			return nil, fmt.Errorf("setup %s: %w", circ.name, err)
		}
		results = append(results, r)
	}
	return results, nil
}

func setupCircuit(name string, circuit frontend.Circuit) (*Result, error) {
	ccs, err := frontend.Compile(ecc.BN254.ScalarField(), r1cs.NewBuilder, circuit)
	if err != nil {
		return nil, fmt.Errorf("%s compile: %w", name, err)
	}

	pk, vk, err := groth16.Setup(ccs)
	if err != nil {
		return nil, fmt.Errorf("%s setup: %w", name, err)
	}

	return &Result{
		Name:            name,
		ProvingKey:      pk,
		VerifyingKey:    vk,
		ConstraintCount: ccs.GetNbConstraints(),
	}, nil
}

func SaveProvingKey(r *Result, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = r.ProvingKey.WriteTo(f)
	return err
}

func SaveVerifyingKey(r *Result, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = r.VerifyingKey.WriteTo(f)
	return err
}
