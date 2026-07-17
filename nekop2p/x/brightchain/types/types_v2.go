//go:build cosmos

// Package types 定义明链模块的类型常量（Cosmos SDK 版本）。
//
// 本文件在 //go:build cosmos 标签下编译，提供完整的类型定义，
// 替代非 Cosmos 模式下的 types.go + keys.go。
//
// 生产环境中，消息类型应由 protoc 从 .proto 文件生成。
// 当前阶段使用手写桩类型，后续 Phase 6 迁移至 proto 生成。
package types

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
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
// 枚举类型
// ============================================================

type NodeRole int32

const (
	NodeRole_NONE            NodeRole = 0
	NodeRole_PUBLIC          NodeRole = 1
	NodeRole_OFFICIAL_RELAY  NodeRole = 2
	NodeRole_OFFICIAL_RECORD NodeRole = 3
	NodeRole_GAME_SERVER     NodeRole = 4
)

type BondStatus int32

const (
	BondStatus_ACTIVE    BondStatus = 0
	BondStatus_RELEASED  BondStatus = 1
	BondStatus_FORFEITED BondStatus = 2
)

// ============================================================
// 核心数据结构
// ============================================================

// BrightAccount 是明链用户账户（Cosmos 版本）。
// 每个用户拥有一个链上数据块，只能被自己更新。
type BrightAccount struct {
	Address          string          `json:"address"`
	AccountNumber    uint64          `json:"account_number"`
	Sequence         uint64          `json:"sequence"`
	RecvPk           []byte          `json:"recv_pk"`
	SendPk           []byte          `json:"send_pk"`
	CreditScore      uint64          `json:"credit_score"`
	TrustWeight      uint64          `json:"trust_weight"`
	TotalRepayAmount uint64          `json:"total_repay_amount"`
	SalaryEarnings   uint64          `json:"salary_earnings"`
	CreditLimit      uint64          `json:"credit_limit"`
	SeedPhase        bool            `json:"seed_phase"`
	Guarantors       [][]byte        `json:"guarantors"`
	NodeRole         NodeRole        `json:"node_role"`
	NodeTermStart    int64           `json:"node_term_start"`
	NodeTermEnd      int64           `json:"node_term_end"`
	Friends          []*FriendRecord `json:"friends"`
	GuaranteedOf     []*BondRef      `json:"guaranteed_of"`
	GuaranteedBy     []*BondRef      `json:"guaranteed_by"`
	GameEarnings     uint64          `json:"game_earnings"`
}

func (a BrightAccount) GetAddress() sdk.AccAddress {
	return sdk.AccAddress([]byte(a.Address))
}

// FriendRecord 好友记录。
type FriendRecord struct {
	ChainId      []byte `json:"chain_id"`
	RecvPk       []byte `json:"recv_pk"`
	SendPk       []byte `json:"send_pk"`
	TrustDist    uint32 `json:"trust_dist"`
	AddedAt      int64  `json:"added_at"`
	IntroducedBy []byte `json:"introduced_by"`
}

// BondRef 担保引用。
type BondRef struct {
	BondId     string `json:"bond_id"`
	OtherParty []byte `json:"other_party"`
}

// GuaranteeBond 担保债券。
type GuaranteeBond struct {
	BondId                string     `json:"bond_id"`
	Inviter               []byte     `json:"inviter"`
	Invitee               []byte     `json:"invitee"`
	LockedNoteCommitments [][]byte   `json:"locked_note_commitments"`
	BondNotes             [][]byte   `json:"bond_notes"`
	TotalBond             uint64     `json:"total_bond"`
	Coefficient           uint64     `json:"coefficient"`
	SeedLimit             uint64     `json:"seed_limit"`
	LockPeriodDays        int64      `json:"lock_period_days"`
	LockedAt              int64      `json:"locked_at"`
	UnlockAt              int64      `json:"unlock_at"`
	CreatedAt             int64      `json:"created_at"`
	Status                BondStatus `json:"status"`
}

// Pool 资金池 (含利息系统)。
type Pool struct {
	TotalBalance   uint64 `json:"total_balance"`
	SalaryRelay    uint64 `json:"salary_relay"`
	SalaryRecord   uint64 `json:"salary_record"`
	SeedLoanReserve uint64 `json:"seed_loan_reserve"`
	BadDebtReserve uint64 `json:"bad_debt_reserve"`
	Community      uint64 `json:"community"`
	GameFees       uint64 `json:"game_fees"`
	GameCommission uint64 `json:"game_commission"`
	InterestEarned  uint64 `json:"interest_earned"`
	InterestReserve uint64 `json:"interest_reserve"`
}

// GameInfo 游戏信息。
type GameInfo struct {
	GameID      string `json:"game_id"`
	AuthorID    string `json:"author_id"`
	Name        string `json:"name"`
	FeeRate     uint64 `json:"fee_rate"`
	AuthorShare uint64 `json:"author_share"`
	ServerShare uint64 `json:"server_share"`
	PoolShare   uint64 `json:"pool_share"`
	Status      int32  `json:"status"`
	CreatedAt   int64  `json:"created_at"`
	TotalTxs    uint64 `json:"total_txs"`
	TotalFees   uint64 `json:"total_fees"`
}

// ============================================================
// 消息类型（手写桩 — Phase 6 迁移至 proto 生成）
// ============================================================

// MsgRegister 用户注册消息。
type MsgRegister struct {
	RecvPk          []byte   `json:"recv_pk"`
	SendPk          []byte   `json:"send_pk"`
	ZkIdentityProof []byte   `json:"zk_identity_proof"`
	GuarantorSigs   [][]byte `json:"guarantor_sigs"`
	Sender          string   `json:"sender"`
	Sequence        uint64   `json:"sequence"` // 交易序号 (防重放)
}

func (msg *MsgRegister) Reset()         {}
func (msg *MsgRegister) String() string { return fmt.Sprintf("MsgRegister{sender:%s}", msg.Sender[:16]) }
func (msg *MsgRegister) ProtoMessage()  {}
func (msg *MsgRegister) ValidateBasic() error {
	if len(msg.RecvPk) == 0 || len(msg.SendPk) == 0 {
		return sdkerrors.ErrInvalidRequest.Wrap("recv_pk and send_pk required")
	}
	return nil
}
func (msg *MsgRegister) GetSigners() []sdk.AccAddress {
	return []sdk.AccAddress{sdk.AccAddress([]byte(msg.Sender))}
}

// MsgRegisterResponse 用户注册响应。
type MsgRegisterResponse struct {
	ChainId  string `json:"chain_id"`
	Sequence uint64 `json:"sequence"`
}

func (m *MsgRegisterResponse) Reset()         {}
func (m *MsgRegisterResponse) String() string { return fmt.Sprintf("MsgRegisterResponse{chain:%s}", m.ChainId) }
func (m *MsgRegisterResponse) ProtoMessage()  {}

// MsgRepay 还款消息。
type MsgRepay struct {
	FromAddress  string   `json:"from_address"`
	Amount       sdk.Coin `json:"amount"`
	ZkRepayProof []byte   `json:"zk_repay_proof"`
	InkwellRef   []byte   `json:"inkwell_ref"`
	Sender       string   `json:"sender"`
	Sequence     uint64   `json:"sequence"` // 交易序号 (防重放)
}

func (msg *MsgRepay) Reset()         {}
func (msg *MsgRepay) String() string { return fmt.Sprintf("MsgRepay{from:%s}", msg.FromAddress[:16]) }
func (msg *MsgRepay) ProtoMessage()  {}
func (msg *MsgRepay) ValidateBasic() error {
	if msg.Amount.IsZero() {
		return sdkerrors.ErrInvalidRequest.Wrap("amount required")
	}
	return nil
}
func (msg *MsgRepay) GetSigners() []sdk.AccAddress {
	return []sdk.AccAddress{sdk.AccAddress([]byte(msg.Sender))}
}

// MsgRepayResponse 还款响应。
type MsgRepayResponse struct {
	Repaid uint64 `json:"repaid"`
}

func (m *MsgRepayResponse) Reset()         {}
func (m *MsgRepayResponse) String() string { return "MsgRepayResponse" }
func (m *MsgRepayResponse) ProtoMessage()  {}

// MsgUpdateFriends 更新好友消息。
type MsgUpdateFriends struct {
	Sender string          `json:"sender"`
	Add    []*FriendRecord `json:"add"`
	Remove [][]byte        `json:"remove"`
}

func (msg *MsgUpdateFriends) Reset()         {}
func (msg *MsgUpdateFriends) String() string { return "MsgUpdateFriends" }
func (msg *MsgUpdateFriends) ProtoMessage()  {}
func (msg *MsgUpdateFriends) ValidateBasic() error { return nil }
func (msg *MsgUpdateFriends) GetSigners() []sdk.AccAddress {
	return []sdk.AccAddress{sdk.AccAddress([]byte(msg.Sender))}
}

// MsgUpdateFriendsResponse 更新好友响应。
type MsgUpdateFriendsResponse struct{}

func (m *MsgUpdateFriendsResponse) Reset()         {}
func (m *MsgUpdateFriendsResponse) String() string { return "MsgUpdateFriendsResponse" }
func (m *MsgUpdateFriendsResponse) ProtoMessage()  {}

// MsgGuarantee 担保消息。
type MsgGuarantee struct {
	Sender              string   `json:"sender"`
	Inviter             string   `json:"inviter"`
	Invitee             string   `json:"invitee"`
	BondNotes           [][]byte `json:"bond_notes"`
	BondNoteCommitments [][]byte `json:"bond_note_commitments"`
	Coefficient         uint64   `json:"coefficient"`
	LockPeriodDays      int64    `json:"lock_period_days"`
	ZkLegitimate        []byte   `json:"zk_legitimate"`
	Sequence            uint64   `json:"sequence"` // 交易序号 (防重放)
}

func (msg *MsgGuarantee) Reset()         {}
func (msg *MsgGuarantee) String() string { return "MsgGuarantee" }
func (msg *MsgGuarantee) ProtoMessage()  {}
func (msg *MsgGuarantee) ValidateBasic() error { return nil }
func (msg *MsgGuarantee) GetSigners() []sdk.AccAddress {
	return []sdk.AccAddress{sdk.AccAddress([]byte(msg.Sender))}
}

// MsgGuaranteeResponse 担保响应。
type MsgGuaranteeResponse struct {
	BondId string `json:"bond_id"`
}

func (m *MsgGuaranteeResponse) Reset()         {}
func (m *MsgGuaranteeResponse) String() string { return "MsgGuaranteeResponse" }
func (m *MsgGuaranteeResponse) ProtoMessage()  {}

// MsgReleaseBond 释放担保消息。
type MsgReleaseBond struct {
	BondId  string `json:"bond_id"`
	Inviter string `json:"inviter"`
}

func (msg *MsgReleaseBond) Reset()         {}
func (msg *MsgReleaseBond) String() string { return "MsgReleaseBond" }
func (msg *MsgReleaseBond) ProtoMessage()  {}
func (msg *MsgReleaseBond) ValidateBasic() error { return nil }
func (msg *MsgReleaseBond) GetSigners() []sdk.AccAddress {
	return []sdk.AccAddress{sdk.AccAddress([]byte(msg.Inviter))}
}

// MsgReleaseBondResponse 释放担保响应。
type MsgReleaseBondResponse struct{}

func (m *MsgReleaseBondResponse) Reset()         {}
func (m *MsgReleaseBondResponse) String() string { return "MsgReleaseBondResponse" }
func (m *MsgReleaseBondResponse) ProtoMessage()  {}

// MsgForfeitBond 没收担保消息。
type MsgForfeitBond struct {
	BondId string `json:"bond_id"`
	Sender string `json:"sender"`
}

func (msg *MsgForfeitBond) Reset()         {}
func (msg *MsgForfeitBond) String() string { return "MsgForfeitBond" }
func (msg *MsgForfeitBond) ProtoMessage()  {}
func (msg *MsgForfeitBond) ValidateBasic() error { return nil }
func (msg *MsgForfeitBond) GetSigners() []sdk.AccAddress {
	return []sdk.AccAddress{sdk.AccAddress([]byte(msg.Sender))}
}

// MsgForfeitBondResponse 没收担保响应。
type MsgForfeitBondResponse struct{}

func (m *MsgForfeitBondResponse) Reset()         {}
func (m *MsgForfeitBondResponse) String() string { return "MsgForfeitBondResponse" }
func (m *MsgForfeitBondResponse) ProtoMessage()  {}

// ============================================================
// MsgServer / QueryServer 接口
// ============================================================

// MsgServer 是明链消息服务器接口。
type MsgServer interface {
	Register(ctx context.Context, msg *MsgRegister) (*MsgRegisterResponse, error)
	Repay(ctx context.Context, msg *MsgRepay) (*MsgRepayResponse, error)
	UpdateFriends(ctx context.Context, msg *MsgUpdateFriends) (*MsgUpdateFriendsResponse, error)
	Guarantee(ctx context.Context, msg *MsgGuarantee) (*MsgGuaranteeResponse, error)
	ReleaseBond(ctx context.Context, msg *MsgReleaseBond) (*MsgReleaseBondResponse, error)
	ForfeitBond(ctx context.Context, msg *MsgForfeitBond) (*MsgForfeitBondResponse, error)
}

// QueryServer 是明链查询服务器接口。
type QueryServer interface {
	User(ctx context.Context, req *QueryUserRequest) (*QueryUserResponse, error)
	Pool(ctx context.Context, req *QueryPoolRequest) (*QueryPoolResponse, error)
}

// QueryUserRequest 用户查询请求。
type QueryUserRequest struct {
	ChainId []byte `json:"chain_id"`
}

// QueryUserResponse 用户查询响应。
type QueryUserResponse struct {
	Account *BrightAccount `json:"account"`
}

// QueryPoolRequest 资金池查询请求。
type QueryPoolRequest struct{}

// QueryPoolResponse 资金池查询响应。
type QueryPoolResponse struct {
	Balance uint64 `json:"balance"`
}

// RegisterMsgServer 注册消息服务器（桩实现，等待 proto 生成）。
func RegisterMsgServer(srv interface{}, impl MsgServer) {
	// 生产环境：由 protobuf 生成的 RegisterMsgServer 替代
}

// RegisterQueryServer 注册查询服务器（桩实现，等待 proto 生成）。
func RegisterQueryServer(srv interface{}, impl QueryServer) {
	// 生产环境：由 protobuf 生成的 RegisterQueryServer 替代
}

// UnwrapSDKContext 恒等映射。
func UnwrapSDKContext(ctx sdk.Context) sdk.Context { return ctx }

// ============================================================
// 创世状态
// ============================================================

// GenesisState 定义模块的创世状态。
type GenesisState struct {
	Accounts    []*BrightAccount  `json:"accounts"`
	Bonds       []*GuaranteeBond  `json:"bonds"`
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
			return fmt.Errorf("account %d: missing recv_pk", i)
		}
		if len(account.SendPk) == 0 {
			return fmt.Errorf("account %d: missing send_pk", i)
		}
	}
	return nil
}

// ============================================================
// 接口注册（手写桩 — Phase 6 迁移至 proto 生成）
// ============================================================

// RegisterInterfaces 向接口注册表注册消息类型。
func RegisterInterfaces(registry codectypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil),
		&MsgRegister{},
		&MsgRepay{},
		&MsgUpdateFriends{},
		&MsgGuarantee{},
		&MsgReleaseBond{},
		&MsgForfeitBond{},
	)
}

// RegisterLegacyAminoCodec 注册旧版 Amino 编解码器。
func RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	cdc.RegisterConcrete(&MsgRegister{}, "nekop2p/brightchain/MsgRegister", nil)
	cdc.RegisterConcrete(&MsgRepay{}, "nekop2p/brightchain/MsgRepay", nil)
	cdc.RegisterConcrete(&MsgUpdateFriends{}, "nekop2p/brightchain/MsgUpdateFriends", nil)
	cdc.RegisterConcrete(&MsgGuarantee{}, "nekop2p/brightchain/MsgGuarantee", nil)
	cdc.RegisterConcrete(&MsgReleaseBond{}, "nekop2p/brightchain/MsgReleaseBond", nil)
	cdc.RegisterConcrete(&MsgForfeitBond{}, "nekop2p/brightchain/MsgForfeitBond", nil)
}

// ============================================================
// 帮助函数
// ============================================================

// MarshalJSON 将 BrightAccount 序列化为 JSON（用于创世导出）。
func (a *BrightAccount) MarshalJSON() ([]byte, error) {
	return json.Marshal(a)
}

// UnmarshalJSON 从 JSON 反序列化 BrightAccount。
func (a *BrightAccount) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, a)
}

// ============================================================
// 动态手续费参数 (治理可调)
// ============================================================

// FeeParams 手续费分配参数。
type FeeParams struct {
	SalaryRelayRate  uint64 `json:"salary_relay_rate"`  // 中继节点比例 (% 默认25)
	SalaryRecordRate uint64 `json:"salary_record_rate"` // 记录节点比例 (% 默认25)
	SeedLoanRate     uint64 `json:"seed_loan_rate"`     // 种子借贷比例 (% 默认20)
	BadDebtRate      uint64 `json:"bad_debt_rate"`      // 坏账准备金比例 (% 默认15)
	CommunityRate    uint64 `json:"community_rate"`     // 社区基金比例 (% 默认15)
	DynamicEnabled   bool   `json:"dynamic_enabled"`    // 是否启用动态费率
}

// DefaultFeeParams 返回默认手续费参数。
func DefaultFeeParams() FeeParams {
	return FeeParams{
		SalaryRelayRate:  25,
		SalaryRecordRate: 25,
		SeedLoanRate:     20,
		BadDebtRate:      15,
		CommunityRate:    15,
		DynamicEnabled:   false,
	}
}

// Validate 验证参数合法性（比例合计=100）。
func (fp FeeParams) Validate() error {
	sum := fp.SalaryRelayRate + fp.SalaryRecordRate + fp.SeedLoanRate + fp.BadDebtRate + fp.CommunityRate
	if sum != 100 {
		return fmt.Errorf("fee params sum must be 100, got %d", sum)
	}
	return nil
}
