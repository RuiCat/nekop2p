package beacon

import (
	"crypto/ed25519"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/nekop2p/nekop2p/crypto"
)

// BuildParams 保存构建信标所需的参数。
type BuildParams struct {
	SenderChainID   [ChainIDSize]byte
	SenderIPv6      [IPv6Size]byte
	SenderPort      uint16
	SendPrivKey     [64]byte // 用于签名的 Ed25519 私钥

	Targets []Target
}

// Target 是要包含在信标中的好友。
type Target struct {
	ChainID [ChainIDSize]byte
	RecvPK  [32]byte // 用于 KEM 的 Curve25519 公钥
}

// Build 从参数构造一个完整的 BeaconPacket。
func Build(params *BuildParams) (*BeaconPacket, []byte, error) {
	// 1. 生成随机 payload_sym_key
	payloadKey, err := crypto.RandomBytes(32)
	if err != nil {
		return nil, nil, err
	}

	// 2. 生成信标 nonce
	beaconNonce, err := crypto.RandomBytes(NonceSize)
	if err != nil {
		return nil, nil, err
	}

	// 3. 构造内层载荷
	inner := make([]byte, ChainIDSize+IPv6Size+2+NonceSize+8)
	offset := 0
	copy(inner[offset:offset+ChainIDSize], params.SenderChainID[:])
	offset += ChainIDSize
	copy(inner[offset:offset+IPv6Size], params.SenderIPv6[:])
	offset += IPv6Size
	binary.BigEndian.PutUint16(inner[offset:offset+2], params.SenderPort)
	offset += 2
	copy(inner[offset:offset+NonceSize], beaconNonce)
	offset += NonceSize
	binary.BigEndian.PutUint64(inner[offset:offset+8], uint64(time.Now().Unix()))

	// 签名
	sig := crypto.Sign(&params.SendPrivKey, inner)
	inner = append(inner, sig...)

	// 4. 用 AES-256-GCM 加密主体
	aesKey := crypto.DeriveKey(payloadKey, []byte("beacon-body"))
	aead, err := crypto.NewAESGCM(aesKey[:])
	if err != nil {
		return nil, nil, err
	}
	nonce, err := crypto.RandomBytes(12)
	if err != nil {
		return nil, nil, err
	}
	bodyCiphertext := aead.Seal(nil, nonce, inner, nil)
	bodyCiphertext = append(nonce, bodyCiphertext...) // 前置 nonce

	// 5. 为每个目标构建槽位
	realCount := len(params.Targets)
	paddedCount := ((realCount + SlotBatch - 1) / SlotBatch) * SlotBatch
	slots := make([]BeaconSlot, paddedCount)

	for i, target := range params.Targets {
		wrapped, err := crypto.KEMEncrypt(&target.RecvPK, payloadKey)
		if err != nil {
			return nil, nil, err
		}
		// wrapped = ephemeral_pk(32) || nonce(12) || ciphertext
		copy(slots[i].EphemeralPK[:], wrapped[:32])
		// EncryptedKey 存储: nonce(12) || ciphertext (最长为 64B)
		copy(slots[i].EncryptedKey[:], wrapped[32:])
	}

	// 填充槽位以随机数据填充
	for i := realCount; i < paddedCount; i++ {
		rb, err := crypto.RandomBytes(32)
		if err != nil {
			return nil, nil, fmt.Errorf("beacon: random fill: %w", err)
		}
		copy(slots[i].EphemeralPK[:], rb)
		rb2, err := crypto.RandomBytes(32)
		if err != nil {
			return nil, nil, fmt.Errorf("beacon: random fill: %w", err)
		}
		copy(slots[i].EncryptedKey[:], rb2)
	}

	// 6. 构建数据包
	randomTag, err := crypto.RandomBytes(RandomTagSize)
	if err != nil {
		return nil, nil, err
	}

	bp := &BeaconPacket{
		HopCount:       0,
		HopMax:         MaxHopDefault,
		SlotCount:      uint8(paddedCount),
		RealCount:      uint8(realCount),
		BodyCiphertext: bodyCiphertext,
		Slots:          slots,
	}
	copy(bp.RandomTag[:], randomTag)

	// 提取原始 beacon_nonce，供调用者跟踪待处理响应
	var pendingNonce [NonceSize]byte
	copy(pendingNonce[:], beaconNonce)

	return bp, pendingNonce[:], nil
}

// BuildResponse 构造一个 ResponsePacket，用以发回给信标发送者。
func BuildResponse(inner *InnerPayload, responderChainID [ChainIDSize]byte,
	responderIPv6 [IPv6Size]byte, responderPort uint16, ephemeralRatchetPK [32]byte,
	sendPrivKey [64]byte) (*ResponsePacket, error) {

	// 1. 构造内层响应
	respInner := make([]byte, IPv6Size+2+NonceSize+32+8)
	offset := 0
	copy(respInner[offset:offset+IPv6Size], responderIPv6[:])
	offset += IPv6Size
	binary.BigEndian.PutUint16(respInner[offset:offset+2], responderPort)
	offset += 2
	copy(respInner[offset:offset+NonceSize], inner.BeaconNonce[:])
	offset += NonceSize
	copy(respInner[offset:offset+32], ephemeralRatchetPK[:])
	offset += 32
	binary.BigEndian.PutUint64(respInner[offset:offset+8], uint64(time.Now().Unix()))

	// 签名
	sig := crypto.Sign(&sendPrivKey, respInner)
	signed := append(respInner, sig...)

	// 2. 用发送者的 recv_pk 加密（需要从链中查询）
	// 当前由调用者提供加密后的载荷
	// （加密是外部完成的，因为我们需要发送者的 recv_pk）

	return &ResponsePacket{
		ResponderChainID: responderChainID,
		EncryptedPayload: signed, // 将由调用者加密
	}, nil
}

// VerifyResponse 验证一个 BeaconResponse。
func VerifyResponse(encryptedPayload []byte, recvPriv *[32]byte, sendPK *[32]byte,
	expectedNonce [NonceSize]byte) (*InnerResponse, error) {

	// 1. 用我们的 recv_sk 解密
	plain, err := crypto.KEMDecrypt(recvPriv, encryptedPayload)
	if err != nil {
		return nil, fmt.Errorf("decrypt response: %w", err)
	}

	// 2. 解析内层响应
	if len(plain) < IPv6Size+2+NonceSize+32+8+SigSize {
		return nil, fmt.Errorf("response too short")
	}

	resp := &InnerResponse{}
	offset := 0
	copy(resp.ResponderIPv6[:], plain[offset:offset+IPv6Size])
	offset += IPv6Size
	resp.ResponderPort = binary.BigEndian.Uint16(plain[offset : offset+2])
	offset += 2
	copy(resp.BeaconNonce[:], plain[offset:offset+NonceSize])
	offset += NonceSize
	copy(resp.EphemeralRatchetPK[:], plain[offset:offset+32])
	offset += 32
	resp.Timestamp = int64(binary.BigEndian.Uint64(plain[offset : offset+8]))
	offset += 8
	sig := plain[offset:]

	// 3. 验证签名
	signedData := plain[:offset]
	if !ed25519.Verify(sendPK[:], signedData, sig) {
		return nil, ErrInvalidSignature
	}

	// 4. 验证 nonce 匹配
	if resp.BeaconNonce != expectedNonce {
		return nil, ErrNonceMismatch
	}

	// 5. 验证时间戳（5 分钟窗口）
	ts := time.Unix(resp.Timestamp, 0)
	if time.Since(ts) > 5*time.Minute || time.Since(ts) < -5*time.Minute {
		return nil, fmt.Errorf("%w: %s", ErrBeaconExpired, ts)
	}

	return resp, nil
}
