package beacon

import "errors"

// 哨兵错误：调用方可以用 errors.Is 判断信标操作失败原因。
var (
	// ErrInvalidSignature 当信标签名验证失败时返回（可能被伪造）。
	ErrInvalidSignature = errors.New("beacon: invalid signature")

	// ErrNonceMismatch 当信标 nonce 不匹配时返回（可能被重放）。
	ErrNonceMismatch = errors.New("beacon: nonce mismatch")

	// ErrBeaconExpired 当信标时间戳超出有效窗口时返回。
	ErrBeaconExpired = errors.New("beacon: expired")

	// ErrNoTargets 当构建信标时没有指定目标时返回。
	ErrNoTargets = errors.New("beacon: no targets specified")

	// ErrSlotDecryptFailed 当无法解密任何信标 slot 时返回（不是目标接收者）。
	ErrSlotDecryptFailed = errors.New("beacon: slot decryption failed")
)
