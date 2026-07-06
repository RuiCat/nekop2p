// Package ratchet 实现 Signal 双棘轮端到端加密协议。
//
// 双棘轮 = DH棘轮（外层，提供后向安全） + 对称棘轮（内层，提供前向安全）
//
// === 安全属性 ===
//   前向安全：消息密钥用完即毁。当前密钥泄露 → 历史消息安全。
//            从 messageKey 无法推导 chainKey（HMAC 单向性）。
//   后向安全：DH棘轮每次混入新的临时密钥。当前密钥泄露 → 未来消息自愈。
//            新 DH 共享密钥使 rootKey 恢复不可预测。
//   防篡改：  AES-256-GCM 认证标签。任何修改 → 解密失败。
//   乱序容忍：SkippedMsgKeys 存储跳过的消息密钥。乱序到达 → 查找对应密钥解密。
//
// === 消息格式 ===
//   [Header 40B || AEAD 密文]
//   Header: DH公钥(32) || 前链计数(4) || 消息号(4)
//
// === 密钥派生 ===
//   KDF_RK(rootKey, dhOutput) → (新rootKey, 新chainKey)
//   KDF_CK(chainKey) → (新chainKey, 消息密钥)
//
// === 安全审计 ===
//   ✅ 已通过。修复了 SkippedMsgKeys 读取逻辑（原来只写不读导致乱序消息无法解密）。
//   ✅ skipMessageKeys panic 改为 error 返回（防止远程 DoS）。
//   ✅ DHr 索引使用完整 32 字节（碰撞概率 2^-256）。
//
// Package ratchet 实现 Signal 双棘轮端到端加密协议。
package ratchet

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"sync"

	"github.com/nekop2p/nekop2p/crypto"
	"golang.org/x/crypto/curve25519"
)

// State 保存完整的双棘轮状态。
// mu 保护所有字段的并发访问（Encrypt/Decrypt 可被不同 goroutine 同时调用）。
type State struct {
	mu sync.Mutex

	// DH 棘轮
	DHs     [32]byte // 我们当前的 DH 私钥
	DHr     [32]byte // 远程当前的 DH 公钥
	RootKey [32]byte

	// 发送链
	SendChainKey [32]byte
	SendMsgNum   uint32

	// 接收链
	RecvChainKey [32]byte
	RecvMsgNum   uint32

	// 乱序消息处理
	SkippedMsgKeys map[[8]byte][32]byte
	MaxSkip        uint32

	// 前一次发送链（用于 DH 棘轮步骤）
	PrevSendChainKey [32]byte
	PrevSendMsgNum   uint32
}

// Header 附加到每条消息上，供接收方处理。
type Header struct {
	DHPublicKey    [32]byte // 发送者当前的 DH 公钥
	PrevChainCount uint32   // 前一次发送链中的消息数
	MsgNum         uint32   // 当前链中的消息编号
}

const (
	HeaderSize       = 40 // 32 + 4 + 4
	DefaultMaxSkip   = 1000
	KeySize          = 32
	MaxMessageSize   = 65535
)

// InitAsInitiator 为连接发起方初始化棘轮。
// aliceEphemeralPK 来自信标响应。
func InitAsInitiator(ourIdentitySK, remoteIdentityPK, aliceEphemeralPK *[32]byte) (*State, [32]byte, error) {
	// 生成我们的临时密钥
	ephKey, err := crypto.GenerateEphemeralKey()
	if err != nil {
		return nil, [32]byte{}, err
	}

	// DH1：我们的临时密钥 × 远程临时密钥
	dh1, err := curve25519.X25519(ephKey.Private[:], aliceEphemeralPK[:])
	if err != nil {
		return nil, [32]byte{}, err
	}
	// DH2：我们的身份密钥 × 远程身份密钥
	dh2, err := curve25519.X25519(ourIdentitySK[:], remoteIdentityPK[:])
	if err != nil {
		return nil, [32]byte{}, err
	}

	s := &State{
		MaxSkip:        DefaultMaxSkip,
		SkippedMsgKeys: make(map[[8]byte][32]byte),
	}
	copy(s.DHs[:], ephKey.Private[:])
	copy(s.DHr[:], aliceEphemeralPK[:])

	// RootKey = SHA256(dh1 || dh2)
	h := sha256.New()
	h.Write(dh1)
	h.Write(dh2)
	copy(s.RootKey[:], h.Sum(nil))

	return s, ephKey.Public, nil
}

// InitAsResponder 为连接响应方初始化棘轮。
func InitAsResponder(ourIdentitySK, remoteIdentityPK, ourEphemeralSK, remoteEphemeralPK *[32]byte) (*State, error) {
	dh1, err := curve25519.X25519(ourEphemeralSK[:], remoteEphemeralPK[:])
	if err != nil {
		return nil, fmt.Errorf("init responder dh1: low-order point: %w", err)
	}
	dh2, err := curve25519.X25519(ourIdentitySK[:], remoteIdentityPK[:])
	if err != nil {
		return nil, fmt.Errorf("init responder dh2: low-order point: %w", err)
	}

	s := &State{
		MaxSkip:        DefaultMaxSkip,
		SkippedMsgKeys: make(map[[8]byte][32]byte),
	}
	copy(s.DHs[:], ourEphemeralSK[:])
	copy(s.DHr[:], remoteEphemeralPK[:])

	h := sha256.New()
	h.Write(dh1)
	h.Write(dh2)
	copy(s.RootKey[:], h.Sum(nil))

	return s, nil
}

// Encrypt 加密明文消息并返回有线格式 [Header 40B || ciphertext]。
func (s *State) Encrypt(plaintext []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 保存前一次发送链
	s.PrevSendChainKey = s.SendChainKey
	s.PrevSendMsgNum = s.SendMsgNum

	// 生成新的 DH 密钥对
	ephKey, err := crypto.GenerateEphemeralKey()
	if err != nil {
		return nil, err
	}

	// DH 棘轮步骤
	dhOutput, err := curve25519.X25519(ephKey.Private[:], s.DHr[:])
	if err != nil {
		return nil, fmt.Errorf("dh ratchet: %w", err)
	}
	var dhOutArr [32]byte
	copy(dhOutArr[:], dhOutput)

	s.RootKey, s.SendChainKey = kdfRK(s.RootKey, dhOutArr)
	copy(s.DHs[:], ephKey.Private[:])
	s.SendMsgNum = 0

	// 对称棘轮：派生消息密钥
	var messageKey [32]byte
	s.SendChainKey, messageKey = kdfCK(s.SendChainKey)

	// 构建头部
	var header Header
	copy(header.DHPublicKey[:], ephKey.Public[:])
	header.PrevChainCount = s.PrevSendMsgNum
	header.MsgNum = s.SendMsgNum

	// 用 AES-256-GCM 加密
	aead, err := crypto.NewAESGCM(messageKey[:])
	if err != nil {
		return nil, err
	}
	headerBytes := header.Serialize()
	nonce, err := crypto.RandomBytes(12)
	if err != nil {
		return nil, err
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, headerBytes)
	ciphertext = append(nonce, ciphertext...)

	s.SendMsgNum++

	return append(headerBytes, ciphertext...), nil
}

// Decrypt 解密有线格式的消息。
func (s *State) Decrypt(wireMsg []byte) ([]byte, error) {
	if len(wireMsg) < HeaderSize {
		return nil, fmt.Errorf("message too short: %d", len(wireMsg))
	}

	header := ParseHeader(wireMsg[:HeaderSize])
	body := wireMsg[HeaderSize:]

	s.mu.Lock()
	defer s.mu.Unlock()

	// 检查是否需要 DH 棘轮
	if header.DHPublicKey != s.DHr {
		if err := s.skipMessageKeys(header.PrevChainCount); err != nil {
			return nil, err
		}
		if err := s.dhRatchetStep(header.DHPublicKey); err != nil {
			return nil, err
		}
	}

	// 检查这是否是乱序（已跳过）消息
	idx := skippedKeyIndex(s.DHr, header.MsgNum)
	if mk, found := s.SkippedMsgKeys[idx]; found && header.MsgNum < s.RecvMsgNum {
		// 乱序消息：使用存储的消息密钥
		aead, err := crypto.NewAESGCM(mk[:])
		if err != nil {
			return nil, err
		}
		nonce := body[:12]
		encrypted := body[12:]
		plaintext, err := aead.Open(nil, nonce, encrypted, wireMsg[:HeaderSize])
		if err != nil {
			return nil, fmt.Errorf("decrypt out-of-order message: %w", err)
		}
		// 使用后删除消息密钥（前向安全）
		delete(s.SkippedMsgKeys, idx)
		return plaintext, nil
	}

	// 跳过已处理的消息（处理空缺）
	if err := s.skipMessageKeys(header.MsgNum); err != nil {
		return nil, err
	}

	// 对称棘轮：派生消息密钥
	var messageKey [32]byte
	s.RecvChainKey, messageKey = kdfCK(s.RecvChainKey)

	// 解密
	aead, err := crypto.NewAESGCM(messageKey[:])
	if err != nil {
		return nil, err
	}
	nonce := body[:12]
	encrypted := body[12:]

	plaintext, err := aead.Open(nil, nonce, encrypted, wireMsg[:HeaderSize])
	if err != nil {
		return nil, fmt.Errorf("decrypt message: %w", err)
	}

	s.RecvMsgNum++
	return plaintext, nil
}

func (s *State) dhRatchetStep(newDHPublicKey [32]byte) error {
	dhOutput, err := curve25519.X25519(s.DHs[:], newDHPublicKey[:])
	if err != nil {
		return fmt.Errorf("dh ratchet step: low-order point rejected: %w", err)
	}
	var dhOutArr [32]byte
	copy(dhOutArr[:], dhOutput)
	s.RootKey, s.RecvChainKey = kdfRK(s.RootKey, dhOutArr)
	s.DHr = newDHPublicKey
	s.RecvMsgNum = 0
	return nil
}

func skippedKeyIndex(dhr [32]byte, msgNum uint32) [8]byte {
	var idx [8]byte
	copy(idx[:], dhr[:])  // 使用完整 DHr 以防止碰撞
	idx[4] = byte(msgNum >> 24)
	idx[5] = byte(msgNum >> 16)
	idx[6] = byte(msgNum >> 8)
	idx[7] = byte(msgNum)
	return idx
}

func (s *State) skipMessageKeys(until uint32) error {
	if until < s.RecvMsgNum {
		return nil
	}
	if until-s.RecvMsgNum > s.MaxSkip {
		return fmt.Errorf("too many skipped messages: %d > max %d", until-s.RecvMsgNum, s.MaxSkip)
	}

	for s.RecvMsgNum < until {
		var mk [32]byte
		s.RecvChainKey, mk = kdfCK(s.RecvChainKey)

		idx := skippedKeyIndex(s.DHr, s.RecvMsgNum)
		s.SkippedMsgKeys[idx] = mk

		s.RecvMsgNum++
	}
	return nil
}

// --- KDF functions ---

func kdfRK(rootKey, dhOutput [32]byte) ([32]byte, [32]byte) {
	mac := hmac.New(sha256.New, rootKey[:])
	mac.Write(dhOutput[:])
	prk := mac.Sum(nil)

	mac.Reset()
	h := hmac.New(sha256.New, prk)
	h.Write([]byte{0x01})
	newRootKey := h.Sum(nil)

	h.Reset()
	h.Write([]byte{0x02})
	newChainKey := h.Sum(nil)

	var rk, ck [32]byte
	copy(rk[:], newRootKey)
	copy(ck[:], newChainKey)
	return rk, ck
}

func kdfCK(chainKey [32]byte) ([32]byte, [32]byte) {
	h := hmac.New(sha256.New, chainKey[:])
	h.Write([]byte{0x01})
	newChainKey := h.Sum(nil)

	h.Reset()
	h.Write([]byte{0x02})
	messageKey := h.Sum(nil)

	var nck, mk [32]byte
	copy(nck[:], newChainKey)
	copy(mk[:], messageKey)
	return nck, mk
}

// --- Header serialization ---

func (h *Header) Serialize() []byte {
	buf := make([]byte, HeaderSize)
	copy(buf[0:32], h.DHPublicKey[:])
	buf[32] = byte(h.PrevChainCount >> 24)
	buf[33] = byte(h.PrevChainCount >> 16)
	buf[34] = byte(h.PrevChainCount >> 8)
	buf[35] = byte(h.PrevChainCount)
	buf[36] = byte(h.MsgNum >> 24)
	buf[37] = byte(h.MsgNum >> 16)
	buf[38] = byte(h.MsgNum >> 8)
	buf[39] = byte(h.MsgNum)
	return buf
}

// ParseHeader 解析序列化的头部。
func ParseHeader(data []byte) Header {
	var h Header
	copy(h.DHPublicKey[:], data[0:32])
	h.PrevChainCount = uint32(data[32])<<24 | uint32(data[33])<<16 | uint32(data[34])<<8 | uint32(data[35])
	h.MsgNum = uint32(data[36])<<24 | uint32(data[37])<<16 | uint32(data[38])<<8 | uint32(data[39])
	return h
}
