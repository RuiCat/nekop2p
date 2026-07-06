// Package zk 提供 ZK 证明的链上验证接口。
//
// 该包桥接 gnark 生成的 Groth16 证明与 Cosmos SDK 模块。
// 验证是常数时间操作（~3ms），适合直接在 DeliverTx 中执行。
package zk

import (
	"bytes"
	"fmt"
	"sync"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/frontend"
)

// ProofType 枚举支持的证明类型。
type ProofType int

const (
	ProofIdentity ProofType = iota // 身份证明（≥3 担保人）
	ProofCredit                     // 信用证明（信誉分 > 阈值）
	ProofRepay                      // 还款证明（暗链借条已结清）
	ProofWork                       // 工作量证明（节点转发/存储）
	ProofMarker                     // 身份标记证明（防自我交易）
)

func (pt ProofType) String() string {
	switch pt {
	case ProofIdentity: return "identity"
	case ProofCredit: return "credit"
	case ProofRepay: return "repay"
	case ProofWork: return "work"
	case ProofMarker: return "marker"
	default: return "unknown"
	}
}

// Verifier 验证 ZK 证明。
// 使用 gnark groth16.Verify() 进行实际密码学验证。
type Verifier struct {
	mu   sync.RWMutex
	keys map[ProofType]groth16.VerifyingKey // 已加载的验证密钥
}

// NewVerifier 创建新的 ZK 验证器。
func NewVerifier() *Verifier {
	return &Verifier{
		keys: make(map[ProofType]groth16.VerifyingKey),
	}
}

// LoadKey 从序列化的验证密钥数据加载一个电路的验证密钥。
// keyData 应该是由 gnark groth16.VerifyingKey.WriteTo() 产生的字节。
func (v *Verifier) LoadKey(pt ProofType, keyData []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	vk := groth16.NewVerifyingKey(ecc.BN254)
	if _, err := vk.ReadFrom(bytes.NewReader(keyData)); err != nil {
		return fmt.Errorf("zk: failed to load verifying key for %s: %w", pt, err)
	}
	v.keys[pt] = vk
	return nil
}

// RegisterKey 注册验证密钥（保留旧接口兼容性，推荐使用 LoadKey）。
func (v *Verifier) RegisterKey(pt ProofType, keyData []byte) error {
	return v.LoadKey(pt, keyData)
}

// verify 执行实际的 gnark Groth16 验证。
func (v *Verifier) verify(pt ProofType, proofData []byte, assignment frontend.Circuit) error {
	if len(proofData) == 0 {
		return fmt.Errorf("zk: empty proof")
	}

	v.mu.RLock()
	vk, ok := v.keys[pt]
	v.mu.RUnlock()
	if !ok {
		return fmt.Errorf("zk: no verifying key loaded for %s", pt)
	}

	// 反序列化证明
	gProof := groth16.NewProof(ecc.BN254)
	if _, err := gProof.ReadFrom(bytes.NewReader(proofData)); err != nil {
		return fmt.Errorf("zk: failed to parse proof: %w", err)
	}

	// 从电路赋值创建 witness
	w, err := frontend.NewWitness(assignment, ecc.BN254.ScalarField())
	if err != nil {
		return fmt.Errorf("zk: failed to create witness: %w", err)
	}
	pubWitness, err := w.Public()
	if err != nil {
		return fmt.Errorf("zk: failed to extract public witness: %w", err)
	}

	// 执行 Groth16 验证
	if err := groth16.Verify(gProof, vk, pubWitness); err != nil {
		return fmt.Errorf("zk: proof verification failed for %s: %w", pt, err)
	}
	return nil
}

// VerifyIdentityProof 验证身份证明。
func (v *Verifier) VerifyIdentityProof(proofData []byte, assignment frontend.Circuit) error {
	return v.verify(ProofIdentity, proofData, assignment)
}

// VerifyCreditProof 验证信用证明。
func (v *Verifier) VerifyCreditProof(proofData []byte, assignment frontend.Circuit) error {
	return v.verify(ProofCredit, proofData, assignment)
}

// VerifyRepayProof 验证还款证明。
func (v *Verifier) VerifyRepayProof(proofData []byte, assignment frontend.Circuit) error {
	return v.verify(ProofRepay, proofData, assignment)
}

// VerifyWorkProof 验证工作量证明。
func (v *Verifier) VerifyWorkProof(proofData []byte, assignment frontend.Circuit) error {
	return v.verify(ProofWork, proofData, assignment)
}

// VerifyMarkerProof 验证身份标记证明。
func (v *Verifier) VerifyMarkerProof(proofData []byte, assignment frontend.Circuit) error {
	return v.verify(ProofMarker, proofData, assignment)
}
