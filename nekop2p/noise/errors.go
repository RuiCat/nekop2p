package noise

import "errors"

// 哨兵错误：调用方可以用 errors.Is 判断握手失败原因。
var (
	// ErrHandshakeFailed 当 Noise 握手在密码学层面失败时返回。
	ErrHandshakeFailed = errors.New("noise: handshake failed")

	// ErrDecryptFailed 当解密握手消息失败时返回（密钥不匹配或消息被篡改）。
	ErrDecryptFailed = errors.New("noise: decrypt failed")

	// ErrMessageTooShort 当收到的握手消息长度不足时返回。
	ErrMessageTooShort = errors.New("noise: message too short")

	// ErrUnknownPattern 当使用不支持的握手模式时返回。
	ErrUnknownPattern = errors.New("noise: unknown pattern")

	// ErrDHFailed 当 ECDH 密钥交换失败时返回。
	ErrDHFailed = errors.New("noise: DH failed")
)
