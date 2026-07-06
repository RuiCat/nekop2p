// Package crypto — 签名、PRF、密钥派生。
//
// 签名:  Ed25519 (RFC 8032) — 确定性签名，无随机数重用风险
// PRF:   HMAC-SHA256 — 密码学安全的伪随机函数
// 派生:  HMAC-SHA256 单步派生 — 仅用于高熵密钥输入
//
// 安全审计：✅ 已通过。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
)

// Sign 使用 Ed25519 私钥对数据进行签名。
// 返回 64 字节的 Ed25519 签名。
// Ed25519 是确定性签名算法（RFC 8032），每次对相同数据产生相同签名。
func Sign(priv *[64]byte, data []byte) []byte {
	return ed25519.Sign(priv[:], data)
}

// Verify 验证 Ed25519 签名。
// 返回 true 表示签名有效，数据未被篡改且确实来自持有 send_sk 的人。
func Verify(pub *[32]byte, data []byte, sig []byte) bool {
	return ed25519.Verify(pub[:], data, sig)
}

// PRF 伪随机函数：PRF(key, data) = HMAC-SHA256(key, data)。
//
// 用途：
//   - 暗域 identity_marker = PRF(master_secret, cycle_marker)
//   - 暗域 nullifier = PRF(master_secret, serial)
//   - 好友介绍凭证中的派生密钥
//
// HMAC-SHA256 是经过充分分析的密码学 PRF，输出 32 字节。
func PRF(key []byte, data []byte) [32]byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}

// DeriveKey 从高熵密钥派生子密钥。
// result = HMAC-SHA256(secret, info)
//
// 注意：这不是完整的 HKDF！仅适用于从高熵输入派生单个子密钥的场景。
// 如需从低熵输入（如密码）派生，应使用完整的 HKDF-Extract + HKDF-Expand。
//
// 当前用途（都是高熵输入）：
//   - 信标体加密密钥派生
//   - 信用票据 owner_key 派生
func DeriveKey(secret []byte, info []byte) [32]byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(info)
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}

// NewAESGCM 创建 AES-256-GCM AEAD cipher。
// key 必须是 32 字节（AES-256）。
//
// GCM 模式同时提供机密性和完整性保护。
// 任何对密文的修改都会导致解密失败（认证标签不匹配）。
func NewAESGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("AES-256 requires 32-byte key, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// RandomBytes 生成 n 字节密码学安全随机数。
// 使用 crypto/rand.Reader（操作系统熵源）。
func RandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, err
	}
	return b, nil
}
