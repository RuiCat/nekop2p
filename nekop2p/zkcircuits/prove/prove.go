// Package prove 提供证明生成与验证功能（gnark v0.15）。
package prove

import (
	"fmt"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
)

// GenerateProof 为电路生成 ZK 证明。
func GenerateProof(circuit frontend.Circuit, pk groth16.ProvingKey) ([]byte, error) {
	ccs, err := frontend.Compile(ecc.BN254.ScalarField(), r1cs.NewBuilder, circuit)
	if err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}

	witness, err := frontend.NewWitness(circuit, ecc.BN254.ScalarField())
	if err != nil {
		return nil, fmt.Errorf("witness: %w", err)
	}

	proof, err := groth16.Prove(ccs, pk, witness)
	if err != nil {
		return nil, fmt.Errorf("prove: %w", err)
	}

	// 将证明序列化为字节
	var buf bytesWriter
	if _, err := proof.WriteTo(&buf); err != nil {
		return nil, fmt.Errorf("serialize proof: %w", err)
	}
	return buf.data, nil
}

// VerifyProof 根据公开输入验证 ZK 证明。
func VerifyProof(proofBytes []byte, vk groth16.VerifyingKey, assignment frontend.Circuit) error {
	proof := groth16.NewProof(ecc.BN254)
	if _, err := proof.ReadFrom(&bytesReader{buf: proofBytes}); err != nil {
		return fmt.Errorf("read proof: %w", err)
	}

	publicWitness, err := frontend.NewWitness(assignment, ecc.BN254.ScalarField(), frontend.PublicOnly())
	if err != nil {
		return fmt.Errorf("witness: %w", err)
	}

	return groth16.Verify(proof, vk, publicWitness)
}

type bytesWriter struct {
	data []byte
}

func (w *bytesWriter) Write(p []byte) (int, error) {
	w.data = append(w.data, p...)
	return len(p), nil
}

type bytesReader struct {
	buf []byte
	pos int
}

func (r *bytesReader) Read(p []byte) (int, error) {
	n := copy(p, r.buf[r.pos:])
	r.pos += n
	if n == 0 && len(p) > 0 {
		return 0, fmt.Errorf("EOF")
	}
	return n, nil
}
