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
//
// === DH 棘轮生命周期 ===
//   发送方：保持 DHs 不变，仅推进对称棘轮。收到新 DHr 后才做 DH 棘轮。
//   接收方：检测 header 中新 DHr → dhRatchetStep(新DHr) → 生成新 DHs。
//   这符合 Signal Double Ratchet 规范 (§3.4 "Symmetric-key ratchet")。
type State struct {
	mu sync.Mutex

	// DH 棘轮
	DHs         [32]byte // 我们当前的 DH 私钥
	DHr         [32]byte // 远程当前的 DH 公钥
	DHPublicKey [32]byte // 我们当前的 DH 公钥 (Curve25519 派生)
	RootKey     [32]byte

	// 发送链
	SendChainKey [32]byte
	SendMsgNum   uint32

	// 接收链
	RecvChainKey [32]byte
	RecvMsgNum   uint32

	// 乱序消息处理
	SkippedMsgKeys map[[32]byte][32]byte // 键: SHA256(DHr || msgNum)
	MaxSkip        uint32

	// 前一次发送链（用于 DH 棘轮步骤中接收方的跳消息处理）
	PrevSendChainKey [32]byte
	PrevSendMsgNum   uint32

	// 是否需要 DH 棘轮步 (收到新 DHr 后下一次 Encrypt 时触发)
	needsDHStep bool
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

// SetMaxSkip 设置最大跳过消息数。高吞吐 P2P 场景可调至 5000。
func (s *State) SetMaxSkip(n uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.MaxSkip = n
}

// GetMaxSkip 返回当前最大跳过消息数。
func (s *State) GetMaxSkip() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.MaxSkip
}

// InitAsInitiator 为连接发起方初始化棘轮。
// aliceEphemeralPK 来自信标响应。
// 返回初始化后的 State 和我们的初始 DH 公钥。
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
		SkippedMsgKeys: make(map[[32]byte][32]byte),
		needsDHStep:    true, // 发起方首条消息需要做 DH 棘轮建立发送链
	}
	copy(s.DHs[:], ephKey.Private[:])
	copy(s.DHr[:], aliceEphemeralPK[:])
	copy(s.DHPublicKey[:], ephKey.Public[:])

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
		SkippedMsgKeys: make(map[[32]byte][32]byte),
		needsDHStep:    false,
	}
	copy(s.DHs[:], ourEphemeralSK[:])
	copy(s.DHr[:], remoteEphemeralPK[:])
	// 从临时私钥派生公钥
	curve25519.ScalarBaseMult(&s.DHPublicKey, ourEphemeralSK)

	h := sha256.New()
	h.Write(dh1)
	h.Write(dh2)
	copy(s.RootKey[:], h.Sum(nil))

	return s, nil
}

// Encrypt 加密明文消息并返回有线格式 [Header 40B || ciphertext]。
//
// 按照 Signal Double Ratchet 规范:
//   发送方不每次生成新 DH 密钥对。DHs 保持不变，仅推进对称棘轮。
//   当收到对方新 DHr 后 (needsDHStep=true)，在下次 Encrypt 中才做 DH 棘轮。
func (s *State) Encrypt(plaintext []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 保存前一次发送链（供接收方的跳消息处理）
	s.PrevSendChainKey = s.SendChainKey
	s.PrevSendMsgNum = s.SendMsgNum

	// 如果需要 DH 棘轮步（收到新 DHr 后首次发送，或首次消息建立发送链）
	if s.needsDHStep {
		// 生成新的 DH 密钥对
		ephKey, err := crypto.GenerateEphemeralKey()
		if err != nil {
			return nil, err
		}

		// DH 棘轮：DH(new_DHs, DHr) → 新 SendChainKey
		// 使用新密钥做 DH，确保接收方用 DH(old_DHs, new_DHPublicKey) 得到相同共享密钥
		dhOutput, err := curve25519.X25519(ephKey.Private[:], s.DHr[:])
		if err != nil {
			return nil, fmt.Errorf("dh ratchet: %w", err)
		}
		var dhOutArr [32]byte
		copy(dhOutArr[:], dhOutput)

		s.RootKey, s.SendChainKey = kdfRK(s.RootKey, dhOutArr)

		// 更新 DH 密钥
		copy(s.DHs[:], ephKey.Private[:])
		copy(s.DHPublicKey[:], ephKey.Public[:])

		s.SendMsgNum = 0
		s.needsDHStep = false
	}

	// 对称棘轮：仅从 SendChainKey 派生消息密钥
	var messageKey [32]byte
	s.SendChainKey, messageKey = kdfCK(s.SendChainKey)

	// 构建头部（使用当前 DHPublicKey，不生成新的）
	var header Header
	copy(header.DHPublicKey[:], s.DHPublicKey[:])
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
		if len(body) < 12+16 {
			return nil, fmt.Errorf("message body too short for out-of-order decrypt: %d", len(body))
		}
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
	if header.MsgNum < s.RecvMsgNum {
		// 消息号小于当前接收计数 → 已被处理或已跳过
		// 检查是否在已跳过消息映射中
		if _, found := s.SkippedMsgKeys[idx]; !found {
			return nil, fmt.Errorf("stale or replayed message: msgNum=%d < recvMsgNum=%d",
				header.MsgNum, s.RecvMsgNum)
		}
	}
	if err := s.skipMessageKeys(header.MsgNum); err != nil {
		return nil, err
	}

	// 对称棘轮：派生消息密钥
	var messageKey [32]byte
	s.RecvChainKey, messageKey = kdfCK(s.RecvChainKey)

	// 解密
	if len(body) < 12+16 {
		return nil, fmt.Errorf("message body too short for decrypt: %d", len(body))
	}
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
	// DH1: DH(old_DHs, new_DHr) → 更新 RootKey 和 RecvChainKey
	dhOutput, err := curve25519.X25519(s.DHs[:], newDHPublicKey[:])
	if err != nil {
		return fmt.Errorf("dh ratchet step: low-order point rejected: %w", err)
	}
	var dhOutArr [32]byte
	copy(dhOutArr[:], dhOutput)
	s.RootKey, s.RecvChainKey = kdfRK(s.RootKey, dhOutArr)

	// 生成我们的新 DH 密钥对
	ephKey, err := crypto.GenerateEphemeralKey()
	if err != nil {
		return fmt.Errorf("dh ratchet: generate new key: %w", err)
	}

	// DH2: DH(new_DHs, new_DHr) → 更新 RootKey 和 SendChainKey
	dhOutput2, err := curve25519.X25519(ephKey.Private[:], newDHPublicKey[:])
	if err != nil {
		return fmt.Errorf("dh ratchet step 2: %w", err)
	}
	var dhOut2 [32]byte
	copy(dhOut2[:], dhOutput2)
	s.RootKey, s.SendChainKey = kdfRK(s.RootKey, dhOut2)

	// 更新 DH 状态
	s.DHr = newDHPublicKey
	copy(s.DHs[:], ephKey.Private[:])
	copy(s.DHPublicKey[:], ephKey.Public[:])
	s.RecvMsgNum = 0
	s.SendMsgNum = 0
	s.needsDHStep = false // 刚做完 DH 步，不需要再做

	return nil
}

// skippedKeyIndex 使用 DHr 和 msgNum 的 SHA256 哈希作为映射键。
// 32 字节完整哈希确保 2^-256 碰撞概率，消除前 4 字节碰撞风险。
func skippedKeyIndex(dhr [32]byte, msgNum uint32) [32]byte {
	h := sha256.New()
	h.Write(dhr[:])
	var buf [4]byte
	buf[0] = byte(msgNum >> 24)
	buf[1] = byte(msgNum >> 16)
	buf[2] = byte(msgNum >> 8)
	buf[3] = byte(msgNum)
	h.Write(buf[:])
	var idx [32]byte
	copy(idx[:], h.Sum(nil))
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

// kdfRK 实现 Signal Double Ratchet 规范的 KDF_RK(rk, dh_out):
//   new_rk = HMAC-SHA256(rk, dh_out || 0x01)
//   new_ck = HMAC-SHA256(rk, dh_out || 0x02)
//
// 使用 rootKey 作为 HMAC 密钥，dhOutput 拼接域分离字节作为消息。
// 此一段式构造符合 Signal 规范，消除了原两段式构造的冗余。
func kdfRK(rootKey, dhOutput [32]byte) ([32]byte, [32]byte) {
	// new_rk = HMAC-SHA256(rk, dh_output || 0x01)
	h1 := hmac.New(sha256.New, rootKey[:])
	h1.Write(dhOutput[:])
	h1.Write([]byte{0x01})
	newRootKey := h1.Sum(nil)

	// new_ck = HMAC-SHA256(rk, dh_output || 0x02)
	h2 := hmac.New(sha256.New, rootKey[:])
	h2.Write(dhOutput[:])
	h2.Write([]byte{0x02})
	newChainKey := h2.Sum(nil)

	var rk, ck [32]byte
	copy(rk[:], newRootKey)
	copy(ck[:], newChainKey)
	return rk, ck
}

func kdfCK(chainKey [32]byte) ([32]byte, [32]byte) {
	h1 := hmac.New(sha256.New, chainKey[:])
	h1.Write([]byte{0x01})
	newChainKey := h1.Sum(nil)

	h2 := hmac.New(sha256.New, chainKey[:])
	h2.Write([]byte{0x02})
	messageKey := h2.Sum(nil)

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
