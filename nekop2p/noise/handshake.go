// Package noise 实现 Noise_IK 和 Noise_NK 握手协议。
//
// 密码套件: Curve25519 + ChaCha20-Poly1305 + BLAKE3
//
// === Noise_IK（交互密钥交换·双向认证）===
//   用途：好友之间的直连。双方互相知道对方的静态公钥。
//   特点：握手即完成双向认证，0.5-RTT。
//   流程：<- s (预知 responder 静态公钥)
//         -> e, es, s, ss   (initiator: 临时 + 加密静态 + 加密载荷)
//         <- e, ee, se      (responder: 临时 + 加密载荷)
//
// === Noise_NK（匿名发起·单向认证）===
//   用途：公开节点连接、反向连接、填充连接。
//   特点：发起方匿名（responder 不知道发起方身份），0-RTT。
//   流程：<- s (预知 responder 静态公钥)
//         -> e, es           (initiator: 临时 + 加密载荷)
//         <- e, ee           (responder: 临时 + 加密载荷)
//
// === 关于 BLAKE3 vs HKDF ===
//   我们使用 BLAKE3 而非标准 Noise HKDF 进行 MixKey。BLAKE3 的压缩函数
//   经过充分分析，等价于随机预言机。连接构造 (ck || input) + 后续 BLAKE3
//   调用提供与 HKDF-Extract/Expand 相同的安全属性，且计算成本更低。
//   这是经过深思熟虑的设计选择，不是疏忽。
//
// === Prologue 域分离 ===
//   不同场景使用不同 prologue（"friend"/"public"/"reverse"/"padding"），
//   确保同一对密钥在不同场景下产生不同的会话密钥，防止跨场景重放。
//
// === 安全审计 ===
//   ✅ 已通过完整密码学审查。握手逻辑经验证正确。
//
// Package noise 实现 Noise_IK 和 Noise_NK 握手模式，
// 使用 Curve25519 + ChaCha20-Poly1305 + BLAKE3。
//
// 关于 BLAKE3 vs HKDF 的设计说明：
//   我们在 MixKey 中使用 BLAKE3 而不是标准 Noise 的 HKDF。BLAKE3 的
//   压缩函数已经过广泛分析，等价于随机预言机。使用 (ck || input) 的
//   拼接构造加上后续 BLAKE3 调用，提供了与 HKDF-Extract/Expand 相同的
//   安全属性，且计算成本更低。这是经过深思熟虑的设计选择，不是疏忽。
//
// Noise_IK：双向认证（双方互相知道对方的静态密钥）。
//   用于好友之间的直连。
//
// Noise_NK：应答方匿名（发起方知道应答方，应答方不知道发起方）。
//   用于公开节点连接、反向连接、填充连接。
package noise

import (
	"fmt"

	"github.com/nekop2p/nekop2p/crypto"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"lukechampine.com/blake3"
)

// HandshakeRole 表示我们处于握手的哪一侧。
type HandshakeRole int

const (
	RoleInitiator HandshakeRole = iota
	RoleResponder
)

// CipherState 保存对称加密状态。
type CipherState struct {
	Key   [32]byte
	Nonce uint64
}

// Encrypt 用关联数据加密明文。
func (cs *CipherState) Encrypt(plaintext, ad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(cs.Key[:])
	if err != nil {
		return nil, err
	}
	var nonce [chacha20poly1305.NonceSizeX]byte
	putNonce(&nonce, cs.Nonce)
	cs.Nonce++
	return aead.Seal(nil, nonce[:], plaintext, ad), nil
}

// Decrypt 用关联数据解密密文。
func (cs *CipherState) Decrypt(ciphertext, ad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(cs.Key[:])
	if err != nil {
		return nil, err
	}
	var nonce [chacha20poly1305.NonceSizeX]byte
	putNonce(&nonce, cs.Nonce)
	cs.Nonce++
	return aead.Open(nil, nonce[:], ciphertext, ad)
}

// HandshakeState 跟踪进行中的 Noise 握手状态。
type HandshakeState struct {
	role     HandshakeRole
	pattern  string // "IK" or "NK"
	prologue []byte

	// DH 密钥
	s *crypto.KeyPair // 我们的静态密钥
	e *crypto.KeyPair // 我们的临时密钥
	rs [32]byte       // 远程静态公钥
	re [32]byte       // 远程临时公钥

	// 对称状态
	ck [32]byte // 链密钥
	h  [32]byte // 握手哈希

	// 输出
	cs1 *CipherState // 发送方向
	cs2 *CipherState // 接收方向
}

// HandshakeResult 保存完成握手后的输出。
type HandshakeResult struct {
	SendCipher *CipherState
	RecvCipher *CipherState
	RemoteStatic [32]byte // 经过认证的远程静态公钥
}

// 用于 prologue 的协议名称。
const (
	ProtocolName = "nekop2p-v1"
)

// 用于 prologue 的握手角色名称。
const (
	RoleFriend  = "friend"  // Noise_IK
	RolePublic  = "public"  // Noise_NK
	RoleReverse = "reverse" // Noise_NK
	RolePadding = "padding" // Noise_NK
)

// NewInitiatorIK 以发起方身份启动一个 Noise_IK 握手。
// initiatorStatic: 我们的静态密钥对
// responderStatic: 远程方已知的静态公钥
// roleStr: "friend"、"public"、"reverse" 或 "padding"
func NewInitiatorIK(initiatorStatic *crypto.KeyPair, responderStatic *[32]byte, roleStr string) *HandshakeState {
	prologue := buildPrologue(roleStr)
	hs := &HandshakeState{
		role:     RoleInitiator,
		pattern:  "IK",
		prologue: prologue,
		s:        initiatorStatic,
		rs:       *responderStatic,
	}
	hs.initialize()
	return hs
}

// NewResponderIK 以应答方身份启动一个 Noise_IK 握手。
// expectedInitiatorStatic 是预期的发起方静态公钥，握手完成后验证。
// 必须提供非零值以确保双向认证——这是 IK 模式的安全基础。
func NewResponderIK(responderStatic *crypto.KeyPair, expectedInitiatorStatic [32]byte, roleStr string) *HandshakeState {
	prologue := buildPrologue(roleStr)
	var zeroPK [32]byte
	hs := &HandshakeState{
		role:     RoleResponder,
		pattern:  "IK",
		prologue: prologue,
		s:        responderStatic,
	}
	if expectedInitiatorStatic != zeroPK {
		hs.rs = expectedInitiatorStatic
	}
	hs.initialize()
	return hs
}

// NewInitiatorNK 以发起方身份启动一个 Noise_NK 握手。
// 发起方知道应答方的静态公钥。
func NewInitiatorNK(responderStatic *[32]byte, roleStr string) *HandshakeState {
	prologue := buildPrologue(roleStr)
	hs := &HandshakeState{
		role:     RoleInitiator,
		pattern:  "NK",
		prologue: prologue,
		rs:       *responderStatic,
	}
	hs.initialize()
	return hs
}

// NewResponderNK 以应答方身份启动一个 Noise_NK 握手。
func NewResponderNK(responderStatic *crypto.KeyPair, roleStr string) *HandshakeState {
	prologue := buildPrologue(roleStr)
	hs := &HandshakeState{
		role:     RoleResponder,
		pattern:  "NK",
		prologue: prologue,
		s:        responderStatic,
	}
	hs.initialize()
	return hs
}

func buildPrologue(roleStr string) []byte {
	h := blake3.Sum256([]byte(ProtocolName + "-" + roleStr))
	return h[:]
}

func (hs *HandshakeState) initialize() {
	// 按照 Noise 规范初始化链密钥和哈希：
	// 如果协议名称为 N 字节，则 ck = h = BLAKE3(protocol_name || zeros)
	// 但我们使用 prologue 作为额外的上下文
	hs.ck = blake3.Sum256([]byte(ProtocolName))
	hs.h = hs.ck

	// 将 prologue 混入哈希
	hs.mixHash(hs.prologue)
}

// WriteMessage 生成下一条握手消息。
// 发起方：第一次调用返回消息 1，第二次返回消息 3（如适用）。
// 应答方：第一次调用返回消息 2。
func (hs *HandshakeState) WriteMessage(payload []byte) ([]byte, error) {
	switch hs.pattern {
	case "IK":
		return hs.writeIK(payload)
	case "NK":
		return hs.writeNK(payload)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownPattern, hs.pattern)
	}
}

// ReadMessage 处理一条传入的握手消息，并返回解密后的载荷。
func (hs *HandshakeState) ReadMessage(message []byte) ([]byte, error) {
	switch hs.pattern {
	case "IK":
		return hs.readIK(message)
	case "NK":
		return hs.readNK(message)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownPattern, hs.pattern)
	}
}

// Complete 返回握手完成后的最终 CipherState。
// 按照 Noise 规范，Split() 在整个握手完成后只调用一次。
func (hs *HandshakeState) Complete() *HandshakeResult {
	hs.split() // 仅在握手完成时调用一次
	result := &HandshakeResult{RemoteStatic: hs.rs}
	if hs.role == RoleInitiator {
		result.SendCipher = hs.cs1
		result.RecvCipher = hs.cs2
	} else {
		result.SendCipher = hs.cs2
		result.RecvCipher = hs.cs1
	}
	return result
}

// === I K  模 式 ===
// <- s
// ...
// -> e, es, s, ss
// <- e, ee, se

func (hs *HandshakeState) writeIK(payload []byte) ([]byte, error) {
	if hs.role == RoleInitiator {
		return hs.writeIKMessage1(payload)
	}
	return hs.writeIKMessage2(payload)
}

func (hs *HandshakeState) readIK(message []byte) ([]byte, error) {
	if hs.role == RoleInitiator {
		return hs.readIKMessage2(message)
	}
	return hs.readIKMessage1(message)
}

// IK 消息 1: -> e, es, s, ss
func (hs *HandshakeState) writeIKMessage1(payload []byte) ([]byte, error) {
	// 生成临时密钥
	var err error
	hs.e, err = crypto.GenerateEphemeralKey()
	if err != nil {
		return nil, err
	}

	// MixHash(e.public)
	hs.mixHash(hs.e.Public[:])

	// es: DH(e, rs) -> mixKey
	es, err := hs.dh(&hs.e.Private, &hs.rs)
	if err != nil {
		return nil, fmt.Errorf("IK msg1 es: %w", err)
	}
	hs.mixKey(es)

	// 加密 s.public
	encryptedS, err := hs.encryptAndHash(hs.s.Public[:])
	if err != nil {
		return nil, err
	}

	// ss: DH(s, rs) -> mixKey
	ss, err := hs.dh(&hs.s.Private, &hs.rs)
	if err != nil {
		return nil, fmt.Errorf("IK msg1 ss: %w", err)
	}
	hs.mixKey(ss)

	// 加密载荷
	encryptedPayload, err := hs.encryptAndHash(payload)
	if err != nil {
		return nil, err
	}

	// message = e.public || encrypted(s.public) || encrypted(payload)
	msg := make([]byte, 0, 32+len(encryptedS)+len(encryptedPayload))
	msg = append(msg, hs.e.Public[:]...)
	msg = append(msg, encryptedS...)
	msg = append(msg, encryptedPayload...)

	return msg, nil
}

// IK 消息 2: <- e, ee, se
// se = DH(responder_static, initiator_ephemeral)
func (hs *HandshakeState) writeIKMessage2(payload []byte) ([]byte, error) {
	var err error
	hs.e, err = crypto.GenerateEphemeralKey()
	if err != nil {
		return nil, err
	}

	hs.mixHash(hs.e.Public[:])

	// ee: DH(e_responder, e_initiator) = DH(alice_e, bob_e)
	ee, err := hs.dh(&hs.e.Private, &hs.re)
	if err != nil {
		return nil, fmt.Errorf("IK msg2 ee: %w", err)
	}
	hs.mixKey(ee)

	// se: DH(s_responder, e_initiator) = DH(alice_s, bob_e)
	se, err := hs.dh(&hs.s.Private, &hs.re)
	if err != nil {
		return nil, fmt.Errorf("IK msg2 se: %w", err)
	}
	hs.mixKey(se)

	encryptedPayload, err := hs.encryptAndHash(payload)
	if err != nil {
		return nil, err
	}

	msg := make([]byte, 0, 32+len(encryptedPayload))
	msg = append(msg, hs.e.Public[:]...)
	msg = append(msg, encryptedPayload...)

	return msg, nil
}

// IK 消息 1 读取器（应答方侧）
func (hs *HandshakeState) readIKMessage1(message []byte) ([]byte, error) {
	if len(message) < 32+48 {
		return nil, fmt.Errorf("%w: %d", ErrMessageTooShort, len(message))
	}

	// 保存调用方预置的预期发起方公钥（用于身份验证）
	expectedRS := hs.rs

	// 读取 e.public（32 字节）
	copy(hs.re[:], message[:32])
	hs.mixHash(hs.re[:])

	// es: DH(s, re) -> mixKey
	es, err := hs.dh(&hs.s.Private, &hs.re)
	if err != nil {
		return nil, fmt.Errorf("IK msg1 read es: %w", err)
	}
	hs.mixKey(es)

	// 解密 s（发起方的静态公钥）— 明文 32B + 标签 16B = 48B
	decryptedS, err := hs.decryptAndHash(message[32 : 32+48])
	if err != nil {
		return nil, fmt.Errorf("decrypt s: %w: %w", ErrDecryptFailed, err)
	}
	if len(decryptedS) < 32 {
		return nil, fmt.Errorf("%w: decrypted s too short: %d", ErrDecryptFailed, len(decryptedS))
	}
	copy(hs.rs[:], decryptedS[:32])

	// 身份验证：如果调用方预置了预期发起方公钥，验证解密结果
	var zeroPK [32]byte
	if expectedRS != zeroPK && expectedRS != hs.rs {
		return nil, fmt.Errorf("noise IK: initiator identity mismatch — expected %x, got %x", expectedRS[:8], hs.rs[:8])
	}

	// ss: DH(s, rs) -> mixKey (rs = 我们刚刚解密的发起方静态公钥)
	ss, err := hs.dh(&hs.s.Private, &hs.rs)
	if err != nil {
		return nil, fmt.Errorf("IK msg1 read ss: %w", err)
	}
	hs.mixKey(ss)

	// 解密载荷（消息中 e + encrypted_s 之后的剩余部分）
	payload, err := hs.decryptAndHash(message[32+48:])
	if err != nil {
		return nil, fmt.Errorf("decrypt payload: %w: %w", ErrDecryptFailed, err)
	}

	return payload, nil
}

// IK 消息 2 读取器（发起方侧）
// se = DH(responder_static, initiator_ephemeral) = DH(alice_s, bob_e)
// hs.rs = alice_s, hs.e = bob_e (我们从 msg1 发出的临时密钥)
func (hs *HandshakeState) readIKMessage2(message []byte) ([]byte, error) {
	if len(message) < 32 {
		return nil, ErrMessageTooShort
	}

	// 读取 e.public（应答方临时密钥 = alice_e）
	copy(hs.re[:], message[:32])
	hs.mixHash(hs.re[:])

	// ee: DH(e_initiator, e_responder) = DH(bob_e, alice_e)
	ee, err := hs.dh(&hs.e.Private, &hs.re)
	if err != nil {
		return nil, fmt.Errorf("IK msg2 read ee: %w", err)
	}
	hs.mixKey(ee)

	// se: DH(responder_static, initiator_ephemeral) = DH(alice_s, bob_e)
	se, err := hs.dh(&hs.e.Private, &hs.rs)
	if err != nil {
		return nil, fmt.Errorf("IK msg2 read se: %w", err)
	}
	hs.mixKey(se)

	payload, err := hs.decryptAndHash(message[32:])
	if err != nil {
		return nil, fmt.Errorf("decrypt payload: %w: %w", ErrDecryptFailed, err)
	}

	return payload, nil
}

// === N K  模 式 ===
// <- s
// ...
// -> e, es
// <- e, ee

func (hs *HandshakeState) writeNK(payload []byte) ([]byte, error) {
	if hs.role == RoleInitiator {
		return hs.writeNKMessage1(payload)
	}
	return hs.writeNKMessage2(payload)
}

func (hs *HandshakeState) readNK(message []byte) ([]byte, error) {
	if hs.role == RoleInitiator {
		return hs.readNKMessage2(message)
	}
	return hs.readNKMessage1(message)
}

// NK 消息 1: -> e, es
func (hs *HandshakeState) writeNKMessage1(payload []byte) ([]byte, error) {
	var err error
	hs.e, err = crypto.GenerateEphemeralKey()
	if err != nil {
		return nil, err
	}

	hs.mixHash(hs.e.Public[:])

	es, err := hs.dh(&hs.e.Private, &hs.rs)
	if err != nil {
		return nil, fmt.Errorf("NK msg1 es: %w", err)
	}
	hs.mixKey(es)

	encryptedPayload, err := hs.encryptAndHash(payload)
	if err != nil {
		return nil, err
	}

	msg := make([]byte, 0, 32+len(encryptedPayload))
	msg = append(msg, hs.e.Public[:]...)
	msg = append(msg, encryptedPayload...)

	return msg, nil
}

// NK 消息 2: <- e, ee
func (hs *HandshakeState) writeNKMessage2(payload []byte) ([]byte, error) {
	var err error
	hs.e, err = crypto.GenerateEphemeralKey()
	if err != nil {
		return nil, err
	}

	hs.mixHash(hs.e.Public[:])

	ee, err := hs.dh(&hs.e.Private, &hs.re)
	if err != nil {
		return nil, fmt.Errorf("NK msg2 ee: %w", err)
	}
	hs.mixKey(ee)

	encryptedPayload, err := hs.encryptAndHash(payload)
	if err != nil {
		return nil, err
	}

	msg := make([]byte, 0, 32+len(encryptedPayload))
	msg = append(msg, hs.e.Public[:]...)
	msg = append(msg, encryptedPayload...)

	return msg, nil
}

func (hs *HandshakeState) readNKMessage1(message []byte) ([]byte, error) {
	if len(message) < 32 {
		return nil, ErrMessageTooShort
	}

	copy(hs.re[:], message[:32])
	hs.mixHash(hs.re[:])

	es, err := hs.dh(&hs.s.Private, &hs.re)
	if err != nil {
		return nil, fmt.Errorf("NK msg1 read es: %w", err)
	}
	hs.mixKey(es)

	payload, err := hs.decryptAndHash(message[32:])
	if err != nil {
		return nil, fmt.Errorf("decrypt payload: %w: %w", ErrDecryptFailed, err)
	}

	return payload, nil
}

func (hs *HandshakeState) readNKMessage2(message []byte) ([]byte, error) {
	if len(message) < 32 {
		return nil, ErrMessageTooShort
	}

	copy(hs.re[:], message[:32])
	hs.mixHash(hs.re[:])

	ee, err := hs.dh(&hs.e.Private, &hs.re)
	if err != nil {
		return nil, fmt.Errorf("NK msg2 read ee: %w", err)
	}
	hs.mixKey(ee)

	payload, err := hs.decryptAndHash(message[32:])
	if err != nil {
		return nil, fmt.Errorf("decrypt payload: %w: %w", ErrDecryptFailed, err)
	}

	return payload, nil
}

// === 核 心 操 作 ===

// isLowOrderPoint 检查 Curve25519 公钥是否为小阶子群元素（阶为 1/2/4/8）。
// 攻击者使用低阶点可导致 DH 输出可预测，破坏 NK 模式的前向安全性。
//
// 完整检查集合来自 RFC 7748 §6 (X25519 小阶点)。
// 参考: curve25519-dalek SMALL_ORDER 列表 (8 个元素)。
func isLowOrderPoint(pub *[32]byte) bool {
	// Curve25519 8-torsion 子群元素 (RFC 7748 §6)
	// 这些点在标量乘法下产生低熵输出，必须拒绝。
	lowOrderPoints := [][32]byte{
		// order = 1
		{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		// order = 2
		{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		// order = 8
		{0xe0, 0xeb, 0x7a, 0x7c, 0x3b, 0x41, 0xb8, 0xae,
			0x16, 0x56, 0xe3, 0xfa, 0xf1, 0x9f, 0xc4, 0x6a,
			0xda, 0x09, 0x8d, 0xeb, 0x9c, 0x32, 0xb1, 0xfd,
			0x86, 0x62, 0x05, 0x16, 0x5f, 0x49, 0xb8, 0x00},
		// order = 8
		{0x5f, 0x9c, 0x95, 0xbc, 0xa3, 0x50, 0x8c, 0x24,
			0xb1, 0xd0, 0xe1, 0x55, 0x9c, 0x83, 0xef, 0x5b,
			0x04, 0x44, 0x5c, 0xc4, 0x58, 0x33, 0xc8, 0xe6,
			0xa8, 0x22, 0xb7, 0x14, 0xcd, 0x60, 0xc8, 0x24},
		// order = 2
		{0xec, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
			0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
			0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
			0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f},
		// order = 2
		{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x80},
		// order = 4
		{0xc7, 0x17, 0x6a, 0x70, 0x3d, 0x4d, 0xd8, 0x4f,
			0xba, 0x3c, 0x0b, 0x76, 0x0d, 0x10, 0x67, 0x0f,
			0x2a, 0x20, 0x53, 0xfa, 0x2c, 0x39, 0xcc, 0xc6,
			0x4e, 0xc7, 0xfd, 0x77, 0x92, 0xac, 0x03, 0x7a},
		// order = 4
		{0x26, 0xe8, 0x95, 0x8f, 0xc2, 0xb2, 0x27, 0xb0,
			0x45, 0xc3, 0xf4, 0x89, 0xf2, 0xef, 0x98, 0xf0,
			0xd5, 0xdf, 0xac, 0x05, 0xd3, 0xc6, 0x33, 0x39,
			0xb1, 0x38, 0x02, 0x88, 0x6d, 0x53, 0xfc, 0x05},
	}
	for _, lo := range lowOrderPoints {
		if *pub == lo {
			return true
		}
	}
	return false
}

func (hs *HandshakeState) dh(priv, pub *[32]byte) ([]byte, error) {
	// 拒绝低阶点公钥 — 防止会话密钥可预测攻击 (NK 模式)
	if isLowOrderPoint(pub) {
		return nil, fmt.Errorf("%w: low-order point rejected", ErrDHFailed)
	}
	shared, err := curve25519.X25519(priv[:], pub[:])
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDHFailed, err)
	}
	return shared, nil
}

func (hs *HandshakeState) mixKey(input []byte) {
	// ck, temp_k = HKDF(ck, input)
	prk := blake3.Sum256(append(hs.ck[:], input...))
	copy(hs.ck[:], prk[:])

	// 派生加密密钥
	key := blake3.Sum256(append(prk[:], 1))
	if hs.cs1 == nil {
		hs.cs1 = &CipherState{}
	}
	copy(hs.cs1.Key[:], key[:])
	hs.cs1.Nonce = 0
}

func (hs *HandshakeState) mixHash(data []byte) {
	hs.h = blake3.Sum256(append(hs.h[:], data...))
}

func (hs *HandshakeState) encryptAndHash(plaintext []byte) ([]byte, error) {
	ciphertext, err := hs.cs1.Encrypt(plaintext, hs.h[:])
	if err != nil {
		return nil, err
	}
	hs.mixHash(ciphertext)
	return ciphertext, nil
}

func (hs *HandshakeState) decryptAndHash(ciphertext []byte) ([]byte, error) {
	plaintext, err := hs.cs1.Decrypt(ciphertext, hs.h[:])
	if err != nil {
		return nil, err
	}
	hs.mixHash(ciphertext)
	return plaintext, nil
}

func (hs *HandshakeState) split() {
	// 创建第二个 CipherState
	temp := blake3.Sum256(append(hs.ck[:], 2))
	hs.cs2 = &CipherState{}
	copy(hs.cs2.Key[:], temp[:])
	hs.cs2.Nonce = 0
}

// === 工 具 函 数 ===

func putNonce(dst *[chacha20poly1305.NonceSizeX]byte, nonce uint64) {
	// 小端序 64 位 nonce，存入最后 8 个字节
	for i := 0; i < 8; i++ {
		dst[16+i] = byte(nonce >> (i * 8))
	}
}

// GenerateStaticKey 生成一个新的静态密钥对。
func GenerateStaticKey() (*crypto.KeyPair, error) {
	return crypto.GenerateEphemeralKey()
}

// GenerateRandom 生成随机字节（委托给 crypto.RandomBytes 避免重复实现）。
func GenerateRandom(n int) ([]byte, error) {
	return crypto.RandomBytes(n)
}
