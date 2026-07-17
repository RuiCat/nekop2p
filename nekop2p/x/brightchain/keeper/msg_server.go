//go:build cosmos

// Package keeper 实现明链 MsgServer (交易消息处理器)。
//
// MsgServer 是 Cosmos SDK 的标准交易路由机制。
// 每个 Msg 类型对应一个 handler 方法。
//
// Package keeper 提供明链交易处理。
package keeper

import (
	"context"
	"fmt"
	"log"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/nekop2p/nekop2p/x/brightchain/types"
)

// msgServer 实现 types.MsgServer 接口。
type msgServer struct {
	k Keeper
}

// NewMsgServerImpl 创建 MsgServer 实现。
func NewMsgServerImpl(keeper Keeper) types.MsgServer {
	return &msgServer{k: keeper}
}

// Register 处理用户注册。
func (ms msgServer) Register(goCtx context.Context, msg *types.MsgRegister) (*types.MsgRegisterResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	account, err := ms.k.RegisterUser(ctx, msg)
	if err != nil {
		return nil, err
	}

	return &types.MsgRegisterResponse{
		ChainId: fmt.Sprintf("%x", account.SendPk[:8]),
	}, nil
}

// Repay 处理贷款还款 (语义翻转: 向资金池注资)。
func (ms msgServer) Repay(goCtx context.Context, msg *types.MsgRepay) (*types.MsgRepayResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	// ZK 还款证明验证 (Phase 2.5: gnark 电路就绪后激活)
	if len(msg.ZkRepayProof) > 0 {
		log.Printf("[brightchain] WARNING: zk repay proof accepted without verification (Phase 2.5 pending)")
	}
	// 跨链结算 (Phase 4: 根据 inkwell_ref 触发暗链 SettleLoan)
	if len(msg.InkwellRef) > 0 {
		log.Printf("[brightchain] inkwell_ref present: %x (Phase 4: cross-chain settlement pending)", msg.InkwellRef[:8])
	}

	amount := msg.Amount.Amount.Uint64()

	if err := ms.k.AddToPool(ctx, amount); err != nil {
		return nil, err
	}

	ctx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.EventTypeRepay,
			sdk.NewAttribute(types.AttributeKeyAmount, fmt.Sprintf("%d", amount)),
		),
	)

	return &types.MsgRepayResponse{
		Repaid: amount,
	}, nil
}

// UpdateFriends 更新好友列表。
func (ms msgServer) UpdateFriends(goCtx context.Context, msg *types.MsgUpdateFriends) (*types.MsgUpdateFriendsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	account, err := ms.k.GetUser(ctx, []byte(msg.Sender))
	if err != nil {
		return nil, err
	}

	// 添加新好友
	for _, friend := range msg.Add {
		account.Friends = append(account.Friends, friend)
	}

	// 移除好友
	removeSet := make(map[string]bool)
	for _, id := range msg.Remove {
		removeSet[string(id)] = true
	}
	filtered := account.Friends[:0]
	for _, f := range account.Friends {
		if !removeSet[string(f.ChainId)] {
			filtered = append(filtered, f)
		}
	}
	account.Friends = filtered

	if err := ms.k.SetUser(ctx, account); err != nil {
		return nil, err
	}

	return &types.MsgUpdateFriendsResponse{}, nil
}

// Guarantee 创建担保债券。
func (ms msgServer) Guarantee(goCtx context.Context, msg *types.MsgGuarantee) (*types.MsgGuaranteeResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	bond, err := ms.k.CreateBond(ctx, msg)
	if err != nil {
		return nil, err
	}

	return &types.MsgGuaranteeResponse{
		BondId: bond.BondId,
	}, nil
}

// ReleaseBond 释放担保债券。
func (ms msgServer) ReleaseBond(goCtx context.Context, msg *types.MsgReleaseBond) (*types.MsgReleaseBondResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	bond, err := ms.k.GetBond(ctx, msg.BondId)
	if err != nil {
		return nil, err
	}

	bond.Status = types.BondStatus_RELEASED
	if err := ms.k.SetBond(ctx, bond); err != nil {
		return nil, err
	}

	return &types.MsgReleaseBondResponse{}, nil
}

// ForfeitBond 没收违约担保债券。
func (ms msgServer) ForfeitBond(goCtx context.Context, msg *types.MsgForfeitBond) (*types.MsgForfeitBondResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	bond, err := ms.k.GetBond(ctx, msg.BondId)
	if err != nil {
		return nil, err
	}

	bond.Status = types.BondStatus_FORFEITED
	if err := ms.k.SetBond(ctx, bond); err != nil {
		return nil, err
	}

	return &types.MsgForfeitBondResponse{}, nil
}

// ============================================================
// QueryServer 实现
// ============================================================

// queryServer 实现 types.QueryServer 接口。
type queryServer struct {
	k Keeper
}

// NewQueryServerImpl 创建 QueryServer 实现。
func NewQueryServerImpl(keeper Keeper) types.QueryServer {
	return &queryServer{k: keeper}
}

// User 查询用户信息。
func (qs queryServer) User(goCtx context.Context, req *types.QueryUserRequest) (*types.QueryUserResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	account, err := qs.k.GetUser(ctx, req.ChainId)
	if err != nil {
		return nil, err
	}

	return &types.QueryUserResponse{
		Account: account,
	}, nil
}

// Pool 查询资金池余额。
func (qs queryServer) Pool(goCtx context.Context, req *types.QueryPoolRequest) (*types.QueryPoolResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	return &types.QueryPoolResponse{
		Balance: qs.k.GetPoolBalance(ctx),
	}, nil
}
