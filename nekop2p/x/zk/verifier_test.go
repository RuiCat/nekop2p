package zk_test

import (
	"bytes"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"

	"github.com/nekop2p/nekop2p/x/zk"
	"github.com/nekop2p/nekop2p/zkcircuits/marker"
)

func TestNewVerifier(t *testing.T) {
	v := zk.NewVerifier()
	if v == nil {
		t.Fatal("verifier is nil")
	}
}

func TestLoadKey(t *testing.T) {
	v := zk.NewVerifier()

	// 编译一个简单的标记电路来生成真实的 vk
	ccs, err := frontend.Compile(ecc.BN254.ScalarField(), r1cs.NewBuilder, &marker.Circuit{})
	if err != nil {
		t.Skipf("circuit compile failed (may need gnark setup): %v", err)
	}
	_, vk, err := groth16.Setup(ccs)
	if err != nil {
		t.Skipf("setup failed: %v", err)
	}

	// 序列化 vk
	var buf bytes.Buffer
	vk.WriteTo(&buf)

	// 加载到验证器
	err = v.LoadKey(zk.ProofMarker, buf.Bytes())
	if err != nil {
		t.Fatalf("load key: %v", err)
	}
}

func TestVerifyEmptyProof(t *testing.T) {
	v := zk.NewVerifier()
	err := v.VerifyIdentityProof(nil, &marker.Circuit{})
	if err == nil {
		t.Error("empty proof should fail")
	}
}

func TestVerifyNoKey(t *testing.T) {
	v := zk.NewVerifier()
	err := v.VerifyCreditProof([]byte("fake-proof"), &marker.Circuit{})
	if err == nil {
		t.Error("no verifying key should fail")
	}
}

func TestProofTypeString(t *testing.T) {
	tests := []struct {
		pt   zk.ProofType
		want string
	}{
		{zk.ProofIdentity, "identity"},
		{zk.ProofCredit, "credit"},
		{zk.ProofRepay, "repay"},
		{zk.ProofWork, "work"},
		{zk.ProofMarker, "marker"},
	}
	for _, tt := range tests {
		if tt.pt.String() != tt.want {
			t.Errorf("ProofType(%d): got %s, want %s", tt.pt, tt.pt.String(), tt.want)
		}
	}
}

func TestUnknownProofType(t *testing.T) {
	if zk.ProofType(999).String() != "unknown" {
		t.Error("unknown type should return 'unknown'")
	}
}

func TestRegisterKeyCompat(t *testing.T) {
	v := zk.NewVerifier()

	ccs, err := frontend.Compile(ecc.BN254.ScalarField(), r1cs.NewBuilder, &marker.Circuit{})
	if err != nil {
		t.Skipf("compile: %v", err)
	}
	_, vk, err := groth16.Setup(ccs)
	if err != nil {
		t.Skipf("setup: %v", err)
	}
	var buf bytes.Buffer
	vk.WriteTo(&buf)

	err = v.RegisterKey(zk.ProofMarker, buf.Bytes())
	if err != nil {
		t.Fatalf("RegisterKey: %v", err)
	}
}
