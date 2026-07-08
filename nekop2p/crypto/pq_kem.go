// Package crypto 后量子密码学集成。
//
// Kyber-1024 KEM (Key Encapsulation Mechanism):
//   - NIST PQC 标准化算法 (FIPS 203 ML-KEM-1024)
//   - IND-CCA2 安全级别
//   - 基于模格 (Module-LWE) 问题的后量子安全
//
// 混合密码学策略:
//   (Curve25519 ECDH) AND (Kyber-1024 KEM) → HKDF → 共享密钥
package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"github.com/cloudflare/circl/kem"
	"github.com/cloudflare/circl/kem/kyber/kyber1024"
	"golang.org/x/crypto/curve25519"
)

// PQKeyPair 表示 Kyber-1024 后量子密钥对。
type PQKeyPair struct {
	Public  kem.PublicKey  // Kyber-1024 公钥
	Private kem.PrivateKey // Kyber-1024 私钥
}

// Zero 清零后量子密钥材料。
func (pq *PQKeyPair) Zero() {
	// circl 的密钥对象由 GC 管理，无显式清零方法。
	// 将引用置 nil 让 GC 回收。
	pq.Public = nil
	pq.Private = nil
}

// GeneratePQKeys 生成全新的 Kyber-1024 后量子密钥对。
// Kyber-1024: NIST 安全级别 5，~256-bit 量子安全。
func GeneratePQKeys() (*PQKeyPair, error) {
	pk, sk, err := kyber1024.GenerateKeyPair(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("pq: generate kyber1024: %w", err)
	}
	return &PQKeyPair{
		Public:  pk,
		Private: sk,
	}, nil
}

// ============================================================
// Kyber KEM 操作
// ============================================================

// PQCiphertextSize 返回 Kyber-1024 密文的字节大小。
func PQCiphertextSize() int {
	return kyber1024.Scheme().CiphertextSize()
}

// PQPublicKeySize 返回 Kyber-1024 公钥的字节大小。
func PQPublicKeySize() int {
	return kyber1024.Scheme().PublicKeySize()
}

// PQPrivateKeySize 返回 Kyber-1024 私钥的字节大小。
func PQPrivateKeySize() int {
	return kyber1024.Scheme().PrivateKeySize()

}

// PQEncapsulate 使用 Kyber-1024 公钥封装共享密钥。
// 返回 (密文, 共享密钥)。
func PQEncapsulate(publicKey kem.PublicKey) (ciphertext, sharedSecret []byte, err error) {
	scheme := kyber1024.Scheme()
	ct, ss, err := scheme.Encapsulate(publicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("pq: encapsulate: %w", err)
	}
	return ct, ss, nil
}

// PQDecapsulate 使用 Kyber-1024 私钥解封装共享密钥。
func PQDecapsulate(privateKey kem.PrivateKey, ciphertext []byte) ([]byte, error) {
	scheme := kyber1024.Scheme()
	ss, err := scheme.Decapsulate(privateKey, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("pq: decapsulate: %w", err)
	}
	return ss, nil
}

// PQMarshalPublicKey 序列化公钥为字节。
func PQMarshalPublicKey(pk kem.PublicKey) ([]byte, error) {
	return pk.MarshalBinary()
}

// PQUnmarshalPublicKey 从字节反序列化公钥。
func PQUnmarshalPublicKey(data []byte) (kem.PublicKey, error) {
	scheme := kyber1024.Scheme()
	pk, err := scheme.UnmarshalBinaryPublicKey(data)
	if err != nil {
		return nil, fmt.Errorf("pq: unmarshal pk: %w", err)
	}
	return pk, nil
}

// PQMarshalPrivateKey 序列化私钥为字节。
func PQMarshalPrivateKey(sk kem.PrivateKey) ([]byte, error) {
	return sk.MarshalBinary()
}

// PQUnmarshalPrivateKey 从字节反序列化私钥。
func PQUnmarshalPrivateKey(data []byte) (kem.PrivateKey, error) {
	scheme := kyber1024.Scheme()
	sk, err := scheme.UnmarshalBinaryPrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("pq: unmarshal sk: %w", err)
	}
	return sk, nil
}

// ============================================================
// 混合密钥交换 (Hybrid KEM)
// ============================================================

// HybridEncapsulate 执行混合密钥封装 (ECDH + Kyber-1024)。
//
// 混合策略: combine-then-KDF
//
//	hybrid_key = SHA256(ECDH_SS || Kyber_SS || "nekop2p-hybrid-kem-v1")
func HybridEncapsulate(ecdhPubKey [32]byte, pqPubKey kem.PublicKey) (*HybridKEMResult, error) {
	result := &HybridKEMResult{}

	// 1. 经典 ECDH
	ephPriv := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, ephPriv); err != nil {
		return nil, fmt.Errorf("hybrid: ecdh ephemeral: %w", err)
	}
	ephPub, err := curve25519.X25519(ephPriv, curve25519.Basepoint)
	if err != nil {
		Memzero(ephPriv)
		return nil, fmt.Errorf("hybrid: ecdh pubkey: %w", err)
	}
	ecdhSS, err := curve25519.X25519(ephPriv, ecdhPubKey[:])
	Memzero(ephPriv) // 清除临时私钥
	if err != nil {
		return nil, fmt.Errorf("hybrid: ecdh: %w", err)
	}
	defer Memzero(ecdhSS) // 确保 ECDH 共享密钥在函数返回后清零

	result.ClassicCiphertext = make([]byte, 32)
	copy(result.ClassicCiphertext, ephPub)

	// 2. 后量子 Kyber
	pqCT, pqSS, err := PQEncapsulate(pqPubKey)
	if err != nil {
		return nil, fmt.Errorf("hybrid: pq: %w", err)
	}
	result.PQCiphertext = pqCT
	defer Memzero(pqSS) // 确保 Kyber 共享密钥在函数返回后清零

	// 3. 混合
	result.HybridSharedKey = combineHybrid(ecdhSS, pqSS)
	return result, nil
}

// HybridDecapsulate 执行混合密钥解封装。
func HybridDecapsulate(ecdhPrivKey [32]byte, pqPrivKey kem.PrivateKey, classicCT, pqCT []byte) ([]byte, error) {
	if len(classicCT) != 32 {
		return nil, fmt.Errorf("hybrid: classic ct len=%d, want 32", len(classicCT))
	}

	var ephPub [32]byte
	copy(ephPub[:], classicCT)
	ecdhSS, err := curve25519.X25519(ecdhPrivKey[:], ephPub[:])
	if err != nil {
		return nil, fmt.Errorf("hybrid: ecdh: %w", err)
	}
	defer Memzero(ecdhSS[:])

	pqSS, err := PQDecapsulate(pqPrivKey, pqCT)
	if err != nil {
		return nil, fmt.Errorf("hybrid: pq: %w", err)
	}
	defer Memzero(pqSS)

	return combineHybrid(ecdhSS, pqSS), nil
}

func combineHybrid(ecdhSS, pqSS []byte) []byte {
	combined := make([]byte, 64+32)
	copy(combined[:32], ecdhSS)
	copy(combined[32:64], pqSS)
	copy(combined[64:], []byte("nekop2p-hybrid-kem-v1"))
	h := sha256.Sum256(combined)
	Memzero(combined)
	return h[:]
}

// HybridKEMResult 混合密钥交换的结果。
type HybridKEMResult struct {
	ClassicCiphertext []byte // 临时 ECDH 公钥 (32B)
	PQCiphertext      []byte // Kyber 密文
	HybridSharedKey   []byte // 混合共享密钥 (32B)
}

// ============================================================
// DualKeys 扩展
// ============================================================

// DualKeysWithPQ 包含后量子密钥的扩展密钥组。
type DualKeysWithPQ struct {
	*DualKeys
	PQKey *PQKeyPair
}

// GenerateDualKeysWithPQ 生成含后量子密钥的完整密钥组。
func GenerateDualKeysWithPQ() (*DualKeysWithPQ, error) {
	classic, err := GenerateDualKeys()
	if err != nil {
		return nil, err
	}
	pq, err := GeneratePQKeys()
	if err != nil {
		return nil, err
	}
	return &DualKeysWithPQ{
		DualKeys: classic,
		PQKey:    pq,
	}, nil
}

// Zero 清零所有密钥材料。
func (dk *DualKeysWithPQ) Zero() {
	dk.RecvKey.Zero()
	Memzero(dk.SendKey.Private[:])
	if dk.PQKey != nil {
		dk.PQKey.Zero()
	}
}

// HasPQ 检查是否配置了后量子密钥。
func (dk *DualKeysWithPQ) HasPQ() bool {
	return dk.PQKey != nil && dk.PQKey.Public != nil
}
