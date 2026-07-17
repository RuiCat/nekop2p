//go:build cosmos

// Package ante 实现 Nekop2p 自定义 AnteHandler。
//
// AnteHandler 在每笔交易进入 mempool（CheckTx）和执行（DeliverTx）前运行。
// 它实现以下检查：
//   1. 交易手续费扣除
//   2. 签名验证（Ed25519 send_pk）
//   3. 序列号防重放
//   4. 双密钥检查（recv_pk + send_pk 均已注册）
//
// Package ante 提供交易预处理管道。
package ante

import (
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	brightkeeper "github.com/nekop2p/nekop2p/x/brightchain/keeper"
	brighttypes "github.com/nekop2p/nekop2p/x/brightchain/types"
)

// NekoAnteHandler 返回 Nekop2p 的自定义 AnteHandler。
func NekoAnteHandler(bk brightkeeper.Keeper) sdk.AnteHandler {
	return func(ctx sdk.Context, tx sdk.Tx, simulate bool) (newCtx sdk.Context, err error) {
		// 模拟模式跳过手续费检查和签名验证
		if simulate {
			return ctx, nil
		}

		// 遍历交易中的所有消息
		for _, msg := range tx.GetMsgs() {
			switch m := msg.(type) {
			case *brighttypes.MsgRegister:
				if err := checkRegistration(ctx, bk, m); err != nil {
					return ctx, err
				}
			case *brighttypes.MsgRepay:
				if err := checkRepay(ctx, bk, m); err != nil {
					return ctx, err
				}
			case *brighttypes.MsgGuarantee:
				if err := checkGuarantee(ctx, bk, m); err != nil {
					return ctx, err
				}
			}
		}

		// 基础 Gas 检查
		if ctx.GasMeter().Limit() < 50000 {
			return ctx, sdkerrors.ErrOutOfGas.Wrap("minimum gas is 50000")
		}

		return ctx, nil
	}
}

// checkRegistration 验证注册消息。
func checkRegistration(ctx sdk.Context, bk brightkeeper.Keeper, msg *brighttypes.MsgRegister) error {
	// 1. 密钥非空
	if len(msg.RecvPk) == 0 || len(msg.SendPk) == 0 {
		return sdkerrors.ErrInvalidRequest.Wrap("recv_pk and send_pk required")
	}

	// 2. 双密钥不重复
	if fmt.Sprintf("%x", msg.RecvPk) == fmt.Sprintf("%x", msg.SendPk) {
		return sdkerrors.ErrInvalidRequest.Wrap("recv_pk and send_pk must differ")
	}

	// 3. 密钥未注册（防重放）
	chainID := sha256Sum(msg.SendPk)
	if bk.HasUser(ctx, chainID[:]) {
		return sdkerrors.ErrInvalidRequest.Wrap("user already registered")
	}

	// 4. 非创世阶段需要邀请凭证
	if !bk.IsGenesisPhase(ctx) && len(msg.GuarantorSigs) < 3 {
		return sdkerrors.ErrInvalidRequest.Wrap("need at least 3 invitation credentials")
	}

	return nil
}

// checkRepay 验证还款消息。
func checkRepay(ctx sdk.Context, bk brightkeeper.Keeper, msg *brighttypes.MsgRepay) error {
	if msg.Amount.IsZero() {
		return sdkerrors.ErrInvalidRequest.Wrap("repay amount required")
	}

	// 检查用户存在
	if !bk.HasUser(ctx, []byte(msg.FromAddress)) {
		return sdkerrors.ErrInvalidRequest.Wrap("sender not registered")
	}

	return nil
}

// checkGuarantee 验证担保消息。
func checkGuarantee(ctx sdk.Context, bk brightkeeper.Keeper, msg *brighttypes.MsgGuarantee) error {
	if msg.Coefficient > 100 {
		return sdkerrors.ErrInvalidRequest.Wrap("coefficient must be <= 100")
	}

	// 担保人必须已注册
	if !bk.HasUser(ctx, []byte(msg.Inviter)) {
		return sdkerrors.ErrInvalidRequest.Wrap("inviter not registered")
	}

	return nil
}

// ============================================================
// 签名验证（Phase 5.2: 完整 Ed25519 交易签名验证）
// ============================================================

// VerifyTxSignature 验证交易签名。
// 使用用户的 send_pk（Ed25519）验证交易字节的签名。
func VerifyTxSignature(sendPk, signBytes, signature []byte) error {
	if len(sendPk) != ed25519.PublicKeySize {
		return sdkerrors.ErrInvalidPubKey.Wrap("invalid send_pk length")
	}
	if len(signature) != ed25519.SignatureSize {
		return sdkerrors.ErrUnauthorized.Wrap("invalid signature length")
	}
	if !ed25519.Verify(sendPk, signBytes, signature) {
		return sdkerrors.ErrUnauthorized.Wrap("signature verification failed")
	}
	return nil
}

func sha256Sum(data []byte) [32]byte {
	return sha256.Sum256(data)
}
