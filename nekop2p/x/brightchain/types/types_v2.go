//go:build cosmos

// Package types 定义明链模块的类型常量。
//
// Cosmos SDK 要求每个模块定义:
//   - ModuleName (模块名称)
//   - StoreKey (IAVL 存储键)
//   - 事件类型和属性键
//   - GenesisState
//   - 接口注册
//
// Package types 提供明链类型定义。
package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	// ModuleName 是模块名称。
	ModuleName = "brightchain"

	// StoreKey 是默认存储键。
	StoreKey = ModuleName

	// MinGenesisUsers 创世阶段最少用户数。
	MinGenesisUsers = 3
)

// ============================================================
// 事件类型
// ============================================================

const (
	EventTypeRegister   = "brightchain.register"
	EventTypeRepay      = "brightchain.repay"
	EventTypeCreateBond = "brightchain.create_bond"

	AttributeKeyChainID = "chain_id"
	AttributeKeyAmount  = "amount"
	AttributeKeyBondID  = "bond_id"
)

// ============================================================
// 创世状态
// ============================================================

// GenesisState 定义模块的创世状态。
type GenesisState struct {
	Accounts    []*BrightAccount `json:"accounts"`
	Bonds       []*GuaranteeBond `json:"bonds"`
	PoolBalance uint64            `json:"pool_balance"`
}

// DefaultGenesis 返回默认创世状态。
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Accounts:    []*BrightAccount{},
		Bonds:       []*GuaranteeBond{},
		PoolBalance: 0,
	}
}

// Validate 验证创世状态。
func (gs GenesisState) Validate() error {
	for i, account := range gs.Accounts {
		if len(account.RecvPk) == 0 {
			return sdkerrors.Wrapf(sdkerrors.ErrInvalidRequest, "account %d: missing recv_pk", i)
		}
		if len(account.SendPk) == 0 {
			return sdkerrors.Wrapf(sdkerrors.ErrInvalidRequest, "account %d: missing send_pk", i)
		}
	}
	return nil
}

// ============================================================
// 接口注册
// ============================================================

// RegisterInterfaces 向接口注册表注册消息类型。
func RegisterInterfaces(registry codectypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil),
		&MsgRegister{},
		&MsgRepay{},
		&MsgUpdateFriends{},
		&MsgGuarantee{},
	)
}

// RegisterLegacyAminoCodec 注册旧版 Amino 编解码器。
func RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	cdc.RegisterConcrete(&MsgRegister{}, "nekop2p/brightchain/MsgRegister", nil)
	cdc.RegisterConcrete(&MsgRepay{}, "nekop2p/brightchain/MsgRepay", nil)
}
