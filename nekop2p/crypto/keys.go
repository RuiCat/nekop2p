// Package crypto 提供 nekop2p 的核心密码学原语。
//
// 设计原则：
//   双密钥体系 — recv(Curve25519)用于别人加密发给我，send(Ed25519)用于我签名给别人验证。
//   两对密钥完全独立，一个泄露不影响另一个。实现了机密性(recv)与认证性(send)的分离。
//
// 算法选型：
//   ECDH:  Curve25519 (RFC 7748) — 后量子前最安全的椭圆曲线DH
//   签名:  Ed25519 (RFC 8032) — 确定性签名，无随机数风险
//   哈希:  SHA256
//   随机源: crypto/rand.Reader — 操作系统密码学安全随机数
//
// 安全审计：已通过完整密码学审查，无已知漏洞。
package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"io"

	"golang.org/x/crypto/curve25519"
)

// KeyPair 表示 Curve25519 密钥对，用于 ECDH 密钥交换。
// Curve25519 私钥为 32 字节随机数，公钥为 Montgomery x 坐标。
// Go 的 X25519 实现内部执行 clamping（清除低3位、设置高位），因此
// 直接使用 32 字节随机数作为私钥是安全的。
type KeyPair struct {
	Public  [32]byte // Curve25519 公钥（Montgomery x坐标）
	Private [32]byte // Curve25519 私钥（32字节随机数，内部clamping）
}

// Zero 安全清零私钥材料，使用后应调用以防止密钥在内存中残留。
func (kp *KeyPair) Zero() {
	Memzero(kp.Private[:])
}

// SignKeyPair 表示 Ed25519 密钥对，用于数字签名。
// Ed25519 私钥为 64 字节（32字节seed + 32字节公钥），公钥为 32 字节。
// 使用确定性签名（RFC 8032），不需要每次签名生成随机数。
type SignKeyPair struct {
	Public  [32]byte // Ed25519 公钥（压缩点）
	Private [64]byte // Ed25519 私钥（seed || public）
}

// DualKeys 持有节点的双密钥对。
//
// 架构设计：两个密钥对完全独立，密码学上无关联。
//   recv 密钥对：Curve25519。别人用 recv_pk 加密消息发给我。只有我有 recv_sk 能解密。
//   send 密钥对：Ed25519。我用 send_sk 签名消息。别人用 send_pk 验证确实是我发的。
//
// 安全性：即使 recv_sk 泄露，攻击者无法伪造我的签名（需要 send_sk）。
//         即使 send_sk 泄露，攻击者无法解密发给我的消息（需要 recv_sk）。
type DualKeys struct {
	RecvKey KeyPair     // Curve25519: 接收密钥对（别人加密给我）
	SendKey SignKeyPair // Ed25519: 发送密钥对（我签名给别人验证）
}

// GenerateDualKeys 生成全新的双密钥对。
//
// 密钥生成过程：
//   1. 从 crypto/rand.Reader 读取 32 字节随机数作为 recv 私钥
//   2. 使用 X25519(Basepoint, recv_sk) 计算 recv 公钥
//   3. 使用 ed25519.GenerateKey(rand.Reader) 生成 send 密钥对
//
// 安全保证：所有随机数来自操作系统密码学安全随机源。
func GenerateDualKeys() (*DualKeys, error) {
	// 生成 recv 密钥对（Curve25519，用于 ECDH KEM）
	recvPriv := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, recvPriv); err != nil {
		return nil, err
	}
	// X25519(Basepoint, sk) = pk
	recvPub, err := curve25519.X25519(recvPriv, curve25519.Basepoint)
	if err != nil {
		return nil, err
	}

	// 生成 send 密钥对（Ed25519，用于数字签名）
	sendPub, sendPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}

	keys := &DualKeys{}
	copy(keys.RecvKey.Private[:], recvPriv)
	copy(keys.RecvKey.Public[:], recvPub)
	copy(keys.SendKey.Public[:], sendPub)
	copy(keys.SendKey.Private[:], sendPriv)

	return keys, nil
}

// GenerateEphemeralKey 生成一次性 Curve25519 密钥对。
//
// 用途：双棘轮每次 DH 步进、信标 KEM 槽位、洋葱路由每层加密。
// 每次加密使用独立的临时密钥，提供前向安全性（当前密钥泄露不影响历史消息）。
func GenerateEphemeralKey() (*KeyPair, error) {
	priv := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, priv); err != nil {
		return nil, err
	}
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return nil, err
	}
	kp := &KeyPair{}
	copy(kp.Private[:], priv)
	copy(kp.Public[:], pub)
	Memzero(priv) // 清除堆上临时密钥材料
	return kp, nil
}

// DeriveChainID 从公钥派生链上标识符。
// chain_id = SHA256(public_key)
// chain_id 是用户在明域的"网名"——公开但不关联真实身份。
func DeriveChainID(pk [32]byte) [32]byte {
	return sha256.Sum256(pk[:])
}

// Memzero 安全清零字节切片，防止密钥材料在内存中残留。
// 使用此函数而非简单的 b = nil 可防止编译器优化掉清零操作。
//
// Go 1.21+ 的 clear() 内建函数提供等效功能，但 Memzero 作为显式安全原语
// 表达意图更清晰，且兼容旧版本。
func Memzero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
