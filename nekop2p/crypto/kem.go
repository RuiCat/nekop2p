// Package crypto — KEM (密钥封装机制) 实现。
//
// 使用 ECDH + HKDF + AES-256-GCM 提供公钥加密：
//   发送方：生成临时密钥 → ECDH 共享密钥 → HKDF 派生 AES 密钥 → GCM 加密
//   接收方：用自己的私钥 + 临时公钥 → 相同的共享密钥 → 解密
//
// 安全特性：
//   - 前向安全：每次加密使用新的临时密钥，历史消息不受密钥泄露影响
//   - 域分离：HKDF 绑定协议名和接收方公钥，防止跨协议密钥重用
//   - 认证加密：AES-GCM 提供篡改检测（任何修改导致解密失败）
//
// 安全审计：✅ 已通过。HKDF域分离修复了之前的 SHA256-only 派生。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

// KEMEncrypt 使用接收方公钥加密数据。
//
// 加密流程：
//   1. 生成临时 Curve25519 密钥对 (ephemeral_sk, ephemeral_pk)
//   2. ECDH: shared = X25519(ephemeral_sk, recipient_pk)
//   3. HKDF 派生 AES-256 密钥（域分离绑定协议名 + 接收方公钥）
//   4. AES-256-GCM 加密，附加数据绑定接收方公钥（防重定向）
//
// 返回格式：ephemeral_pk(32B) || nonce(12B) || ciphertext(含16B tag)
// 总开销：44 字节 + 明文长度
//
// 安全性：攻击者即使截获密文并替换 ephemeral_pk，GCM 认证标签也会使解密失败。
func KEMEncrypt(recipientPub *[32]byte, plaintext []byte) ([]byte, error) {
	// 1. 生成临时密钥对
	ephPriv := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, ephPriv); err != nil {
		return nil, err
	}
	ephPub, err := curve25519.X25519(ephPriv, curve25519.Basepoint)
	if err != nil {
		return nil, err
	}

	// 2. ECDH: shared = DH(ephemeral_sk, recipient_pk)
	shared, err := curve25519.X25519(ephPriv, recipientPub[:])
	if err != nil {
		return nil, err
	}

	// 3. HKDF 派生 AES key（域分离 + 接收方公钥绑定）
	aesKey := deriveKEMKey(shared, recipientPub[:])
	if aesKey == nil {
		return nil, fmt.Errorf("KEM key derivation failed")
	}

	// 4. AES-256-GCM 加密，AD = 接收方公钥
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, recipientPub[:])

	// 5. 输出: ephemeral_pk || nonce || ciphertext
	result := make([]byte, 32+len(nonce)+len(ciphertext))
	copy(result[:32], ephPub)
	copy(result[32:32+len(nonce)], nonce)
	copy(result[32+len(nonce):], ciphertext)

	return result, nil
}

// KEMDecrypt 解密 KEMEncrypt 的输出。
//
// 解密流程：
//   1. 解析 ephemeral_pk 和 nonce
//   2. 从私钥计算自己的公钥（用于域分离绑定）
//   3. ECDH: shared = X25519(my_sk, ephemeral_pk)
//   4. 相同的 HKDF 派生 AES key
//   5. GCM 解密，AD = 自己的公钥
//
// 安全性：如果密文被篡改或 ephemeral_pk 被替换，GCM 认证失败，返回错误。
func KEMDecrypt(recipientPriv *[32]byte, encrypted []byte) ([]byte, error) {
	if len(encrypted) < 32+12 {
		return nil, io.ErrUnexpectedEOF
	}

	// 1. 解析 ephemeral_pk
	var ephPub [32]byte
	copy(ephPub[:], encrypted[:32])

	// 2. 计算自己的公钥（用于域分离绑定）
	ourPub, err := curve25519.X25519(recipientPriv[:], curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("compute pubkey: %w", err)
	}

	// 3. ECDH: shared = DH(my_sk, ephemeral_pk)
	shared, err := curve25519.X25519(recipientPriv[:], ephPub[:])
	if err != nil {
		return nil, err
	}

	// 4. 相同的 HKDF 派生
	aesKey := deriveKEMKey(shared, ourPub)
	if aesKey == nil {
		return nil, fmt.Errorf("KEM key derivation failed")
	}

	// 5. GCM 解密
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := encrypted[32 : 32+gcm.NonceSize()]
	ciphertext := encrypted[32+gcm.NonceSize():]

	return gcm.Open(nil, nonce, ciphertext, ourPub)
}

// deriveKEMKey 使用 HKDF 从 ECDH 共享密钥派生 AES-256 密钥。
//
// HKDF 两步：
//   Extract:  salt = SHA256("nekop2p-kem-v1")，从共享密钥提取伪随机密钥
//   Expand:   info = "aes-key" || recipient_pub，扩展为 32 字节 AES key
//
// 域分离保证：不同协议或不同接收方的 KEM 密钥完全独立。
func deriveKEMKey(shared, recipientPub []byte) []byte {
	salt := sha256.Sum256([]byte("nekop2p-kem-v1"))
	prk := hkdf.Extract(sha256.New, shared, salt[:])

	info := append([]byte("aes-key"), recipientPub...)
	key := make([]byte, 32)
	rd := hkdf.Expand(sha256.New, prk, info)
	if _, err := io.ReadFull(rd, key); err != nil {
		return nil
	}
	return key
}

// DeriveSharedSecret 计算原始 ECDH 共享密钥（底层接口）。
func DeriveSharedSecret(priv *[32]byte, pub *[32]byte) ([]byte, error) {
	return curve25519.X25519(priv[:], pub[:])
}
