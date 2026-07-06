package dark

import "errors"

// 哨兵错误：调用方可以用 errors.Is 判断具体错误类型。
var (
	// ErrInsufficientCredit 当输入票据总额不足以覆盖交易金额时返回。
	ErrInsufficientCredit = errors.New("dark: insufficient credit")

	// ErrSelfDealing 当交易双方具有相同的身份标记（同一个人）时返回。
	ErrSelfDealing = errors.New("dark: self-dealing detected")

	// ErrInvalidStatus 当在错误的交易状态下执行操作时返回。
	ErrInvalidStatus = errors.New("dark: invalid transaction status")

	// ErrNoInputNotes 当交易没有输入票据时返回。
	ErrNoInputNotes = errors.New("dark: no input notes")

	// ErrTxFailed 当交易验证失败时返回。
	ErrTxFailed = errors.New("dark: transaction failed")
)
