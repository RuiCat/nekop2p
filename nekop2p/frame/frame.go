package frame

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"golang.org/x/crypto/chacha20poly1305"
	"lukechampine.com/blake3"
)

// SessionKeys 保存连接的发送和接收加密密钥。
// 所有字段的并发访问由 mu 保护。
type SessionKeys struct {
	SendKey   [32]byte // 加密出站帧的密钥
	RecvKey   [32]byte // 解密入站帧的密钥
	SendNonce uint64   // 单调递增的发送计数器
	RecvNonce uint64   // 最近看到的接收计数器（防重放）
	hasRecv   bool     // 是否已接收过至少一帧（防 nonce=0 重放绕过）
	mu        sync.Mutex
}

// NewSessionKeys 从原始密钥材料创建会话密钥。
// 密钥必须恰好为32字节（ChaCha20-Poly1305），否则 panic。
// 调用方保证：所有密钥来自 Noise 握手结果，始终为 32 字节。
func NewSessionKeys(sendKey, recvKey []byte) *SessionKeys {
	if len(sendKey) != 32 || len(recvKey) != 32 {
		panic(fmt.Sprintf("frame: NewSessionKeys requires 32-byte keys, got send=%d recv=%d", len(sendKey), len(recvKey)))
	}
	sk := &SessionKeys{}
	copy(sk.SendKey[:], sendKey)
	copy(sk.RecvKey[:], recvKey)
	return sk
}

// makeNonce 从会话密钥派生随机化 nonce 前缀，防止前 16 字节始终为零。
// 双方基于共享密钥一致地推导相同前缀，因此无需在线上传输完整 nonce。
func (sk *SessionKeys) makeNonce(nonceVal uint64, isSend bool) [chacha20poly1305.NonceSizeX]byte {
	var nonce [chacha20poly1305.NonceSizeX]byte
	var keyForPrefix [32]byte
	if isSend {
		keyForPrefix = sk.SendKey
	} else {
		keyForPrefix = sk.RecvKey
	}
	prefix := blake3.Sum256(append(keyForPrefix[:], []byte("-nonce-prefix")...))
	copy(nonce[:16], prefix[:16])
	binary.LittleEndian.PutUint64(nonce[16:], nonceVal)
	return nonce
}

// WriteEncryptedFrame 加密一帧完整数据并写入 writer。
// 线程安全：多个 goroutine 可并发调用。
func (sk *SessionKeys) WriteEncryptedFrame(w io.Writer, f *Frame) error {
	// 1. 序列化内层帧
	inner := make([]byte, InnerHeaderLen+len(f.Payload))
	inner[0] = f.Version
	inner[1] = f.Type
	inner[2] = f.Flags
	binary.BigEndian.PutUint16(inner[3:5], f.StreamID)
	copy(inner[5:], f.Payload)

	// 2. ChaCha20-Poly1305 使用长度前缀作为附加数据进行加密
	aead, err := chacha20poly1305.NewX(sk.SendKey[:])
	if err != nil {
		return fmt.Errorf("chacha20poly1305: %w", err)
	}

	// 原子地获取并递增发送 nonce（防 nonce 重用）
	sk.mu.Lock()
	nonceVal := sk.SendNonce
	sk.SendNonce++
	sk.mu.Unlock()

	nonce := sk.makeNonce(nonceVal, true)

	// 将帧长度绑定到 AEAD 认证中
	lenBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(lenBytes, uint16(LenLen+NonceLen+len(inner)+AuthTagLen))
	encrypted := aead.Seal(nil, nonce[:], inner, lenBytes)

	// 3. 写入外层帧: [Len(2B) | Nonce(8B) | Ciphertext | Tag(16B)]
	frameLen := LenLen + NonceLen + len(encrypted)
	if frameLen > MaxFrameSize {
		return fmt.Errorf("frame too large: %d", frameLen)
	}

	lenBuf := make([]byte, LenLen)
	binary.BigEndian.PutUint16(lenBuf, uint16(frameLen))

	if _, err := w.Write(lenBuf); err != nil {
		return err
	}
	if _, err := w.Write(nonce[16:]); err != nil {
		return err
	}
	if _, err := w.Write(encrypted); err != nil {
		return err
	}

	return nil
}

// ReadEncryptedFrame 从 reader 中读取并解密一帧完整数据。
func (sk *SessionKeys) ReadEncryptedFrame(r io.Reader) (*Frame, error) {
	// 1. 读取长度前缀（2 字节）
	lenBuf := make([]byte, LenLen)
	if _, err := io.ReadFull(r, lenBuf); err != nil {
		return nil, fmt.Errorf("read length: %w", err)
	}
	frameLen := binary.BigEndian.Uint16(lenBuf)
	if frameLen < LenLen+NonceLen+AuthTagLen || frameLen > MaxFrameSize {
		return nil, fmt.Errorf("frame too short: %d", frameLen)
	}

	// 2. 读取 nonce + 密文（已限制大小，防认证前大分配 DoS）
	remaining := make([]byte, frameLen-LenLen)
	if _, err := io.ReadFull(r, remaining); err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	// 3. ChaCha20-Poly1305 解密
	aead, err := chacha20poly1305.NewX(sk.RecvKey[:])
	if err != nil {
		return nil, fmt.Errorf("chacha20poly1305: %w", err)
	}

	recvNonce := binary.LittleEndian.Uint64(remaining[:NonceLen])
	nonce := sk.makeNonce(recvNonce, false)
	encrypted := remaining[NonceLen:]

	// 验证帧长度已被认证
	lenBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(lenBytes, frameLen)

	inner, err := aead.Open(nil, nonce[:], encrypted, lenBytes)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	if len(inner) < InnerHeaderLen {
		return nil, fmt.Errorf("inner frame too short: %d", len(inner))
	}

	// 4. 防重放：检查 nonce 是否严格大于上次见到的值
	// 已修复：原实现中 nonce=0 的帧在首帧后仍可被重放（&& sk.RecvNonce > 0 条件绕过）
	sk.mu.Lock()
	if sk.hasRecv && recvNonce <= sk.RecvNonce {
		sk.mu.Unlock()
		return nil, fmt.Errorf("replay detected: nonce %d <= last seen %d", recvNonce, sk.RecvNonce)
	}
	sk.RecvNonce = recvNonce
	sk.hasRecv = true
	sk.mu.Unlock()

	// 5. 解析内层帧
	f := &Frame{
		Version:  inner[0],
		Type:     inner[1],
		Flags:    inner[2],
		StreamID: binary.BigEndian.Uint16(inner[3:5]),
		Payload:  inner[5:],
	}

	if f.Version != ProtocolVersion {
		return nil, fmt.Errorf("unsupported protocol version: %d", f.Version)
	}

	return f, nil
}

// GenerateSessionKey 生成一个随机的 32 字节密钥，用于 ChaCha20-Poly1305。
func GenerateSessionKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	return key, nil
}

// NewAEAD 使用一个密钥创建 ChaCha20-Poly1305 AEAD 密码器。
func NewAEAD(key []byte) (cipher.AEAD, error) {
	return chacha20poly1305.NewX(key)
}
