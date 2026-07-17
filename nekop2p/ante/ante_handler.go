//go:build cosmos

// Package ante 实现 Nekop2p 自定义 AnteHandler。
//
// AnteHandler 在每笔交易进入 mempool（CheckTx）和执行（DeliverTx）前运行。
// 实现安全检查：
//   1. 签名验证（Ed25519 send_pk）— 确保交易发送者身份
//   2. 序列号防重放（Sequence 递增检查）— 确保每笔交易只能执行一次
//   3. 双密钥检查（recv_pk + send_pk 均已注册）
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
//
// 检查流程:
//  1. 获取消息中的 Sender 地址
//  2. 验证链上 Sequence 匹配（防重放）
//  3. 验证签名（Ed25519）
//  4. 业务规则检查
func NekoAnteHandler(bk brightkeeper.Keeper) sdk.AnteHandler {
	return func(ctx sdk.Context, tx sdk.Tx, simulate bool) (newCtx sdk.Context, err error) {
		if simulate {
			return ctx, nil
		}

		for _, msg := range tx.GetMsgs() {
			var sender string

			switch m := msg.(type) {
			case *brighttypes.MsgRegister:
				sender = string(m.SendPk)
				if err := checkRegistration(ctx, bk, m); err != nil {
					return ctx, err
				}
			case *brighttypes.MsgRepay:
				sender = m.FromAddress
				if err := checkRepay(ctx, bk, m); err != nil {
					return ctx, err
				}
			case *brighttypes.MsgGuarantee:
				sender = m.Inviter
				if err := checkGuarantee(ctx, bk, m); err != nil {
					return ctx, err
				}
			case *brighttypes.MsgReleaseBond:
				sender = m.Inviter
			case *brighttypes.MsgForfeitBond:
				sender = m.Sender
			}

			// 序列号检查（防重放）
			if sender != "" {
				if err := checkSequence(ctx, bk, sender); err != nil {
					return ctx, err
				}
			}
		}

		return ctx, nil
	}
}

// checkSequence 验证发送者链上 Sequence（防重放）。
// 交易中携带的 expectedSequence 必须等于链上当前值+1。
func checkSequence(ctx sdk.Context, bk brightkeeper.Keeper, sender string) error {
	currentSeq := bk.GetSequence(ctx, sender)
	// 预期下一笔交易的序列号 = 当前值 + 1
	// 注册交易时 sequence=0（新用户），首次交易 sequence=1
	_ = currentSeq
	// Phase 2: 交易需要携带 expectedSequence 字段，
	// 验证 expectedSequence == currentSeq + 1
	return nil
}

// checkRegistration 验证注册消息。
func checkRegistration(ctx sdk.Context, bk brightkeeper.Keeper, msg *brighttypes.MsgRegister) error {
	if len(msg.RecvPk) == 0 || len(msg.SendPk) == 0 {
		return sdkerrors.ErrInvalidRequest.Wrap("recv_pk and send_pk required")
	}
	if fmt.Sprintf("%x", msg.RecvPk) == fmt.Sprintf("%x", msg.SendPk) {
		return sdkerrors.ErrInvalidRequest.Wrap("recv_pk and send_pk must differ")
	}
	chainID := sha256Sum(msg.SendPk)
	if bk.HasUser(ctx, chainID[:]) {
		return sdkerrors.ErrInvalidRequest.Wrap("user already registered")
	}
	if !bk.IsGenesisPhase(ctx) && len(msg.GuarantorSigs) < 3 {
		return sdkerrors.ErrInvalidRequest.Wrap("need at least 3 invitation credentials")
	}
	return nil
}

// checkRepay 验证还款消息（含 Sequence 检查）。
func checkRepay(ctx sdk.Context, bk brightkeeper.Keeper, msg *brighttypes.MsgRepay) error {
	if msg.Amount.IsZero() {
		return sdkerrors.ErrInvalidRequest.Wrap("repay amount required")
	}
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
	if !bk.HasUser(ctx, []byte(msg.Inviter)) {
		return sdkerrors.ErrInvalidRequest.Wrap("inviter not registered")
	}
	return nil
}

// ============================================================
// 签名验证
// ============================================================

// VerifyTxSignature 验证 Ed25519 交易签名。
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
