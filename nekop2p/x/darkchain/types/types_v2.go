//go:build cosmos

// Package types 定义暗链模块的类型常量（Cosmos SDK 版本）。
//
// 本文件在 //go:build cosmos 标签下编译，提供完整的类型定义。
// 生产环境中，消息类型应由 protoc 从 .proto 文件生成。
package types

import (
	"context"
	"fmt"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	ModuleName = "darkchain"
	StoreKey   = ModuleName
)

// ============================================================
// 事件类型
// ============================================================

const (
	EventTypeRequestLoan = "darkchain.request_loan"
	EventTypeApproveLoan = "darkchain.approve_loan"
	EventTypeSettleLoan  = "darkchain.settle_loan"

	AttributeKeyLoanID   = "loan_id"
	AttributeKeyAmount   = "amount"
	AttributeKeyBorrower = "borrower_anon"
)

// ============================================================
// 枚举类型
// ============================================================

type LoanStatus int32

const (
	LoanStatus_REQUESTED LoanStatus = 0
	LoanStatus_APPROVED  LoanStatus = 1
	LoanStatus_SETTLED   LoanStatus = 2
	LoanStatus_DEFAULTED LoanStatus = 3
)

func (s LoanStatus) String() string {
	switch s {
	case LoanStatus_REQUESTED:
		return "REQUESTED"
	case LoanStatus_APPROVED:
		return "APPROVED"
	case LoanStatus_SETTLED:
		return "SETTLED"
	case LoanStatus_DEFAULTED:
		return "DEFAULTED"
	default:
		return "UNKNOWN"
	}
}

// ============================================================
// 核心数据结构
// ============================================================

// LoanRecord 借贷记录。
type LoanRecord struct {
	LoanId        []byte     `json:"loan_id"`
	BorrowerAnon  []byte     `json:"borrower_anon"`
	LenderAnon    []byte     `json:"lender_anon"`
	Amount        uint64     `json:"amount"`
	TermDays      int64      `json:"term_days"`
	Status        LoanStatus `json:"status"`
	InkwellParams []byte     `json:"inkwell_params"`
	CreatedAt     int64      `json:"created_at"`
	DueAt         int64      `json:"due_at"`
	SettledAt     int64      `json:"settled_at"`
}

// Nullifier 双花保护标记。
type Nullifier struct {
	Value   []byte `json:"value"`
	SpentBy []byte `json:"spent_by"`
	SpentAt int64  `json:"spent_at"`
}

// CreditNote 信用票据。
type CreditNote struct {
	Value      uint64 `json:"value"`
	OwnerKey   []byte `json:"owner_key"`
	Serial     []byte `json:"serial"`
	Commitment []byte `json:"commitment"`
}

// InkwellParams 混沌结算参数。
type InkwellParams struct {
	Seed        []byte   `json:"seed"`
	TotalAmount uint64   `json:"total_amount"`
	WindowStart int64    `json:"window_start"`
	WindowEnd   int64    `json:"window_end"`
	Fragments   []uint64 `json:"fragments"`
	RelayPath   []string `json:"relay_path"`
}

// ============================================================
// 消息类型（手写桩 — Phase 6 迁移至 proto 生成）
// ============================================================

// MsgRequestLoan 借贷请求消息。
type MsgRequestLoan struct {
	BorrowerAnon   []byte `json:"borrower_anon"`
	Amount         uint64 `json:"amount"`
	TermDays       int64  `json:"term_days"`
	ZkCreditProof  []byte `json:"zk_credit_proof"`
	InkwellSeed    []byte `json:"inkwell_seed"`
	Sender         []byte `json:"sender"`
}

func (msg *MsgRequestLoan) Reset()         {}
func (msg *MsgRequestLoan) String() string { return fmt.Sprintf("MsgRequestLoan{amount:%d}", msg.Amount) }
func (msg *MsgRequestLoan) ProtoMessage()  {}
func (msg *MsgRequestLoan) ValidateBasic() error { return nil }
func (msg *MsgRequestLoan) GetSigners() []sdk.AccAddress {
	return []sdk.AccAddress{sdk.AccAddress(msg.Sender)}
}

// MsgRequestLoanResponse 借贷请求响应。
type MsgRequestLoanResponse struct {
	LoanId []byte `json:"loan_id"`
	Status string `json:"status"`
}

func (m *MsgRequestLoanResponse) Reset()         {}
func (m *MsgRequestLoanResponse) String() string { return "MsgRequestLoanResponse" }
func (m *MsgRequestLoanResponse) ProtoMessage()  {}

// MsgApproveLoan 批准放款消息。
type MsgApproveLoan struct {
	LoanId        []byte `json:"loan_id"`
	LenderAnon    []byte `json:"lender_anon"`
	InkwellParams []byte `json:"inkwell_params"`
	Sender        []byte `json:"sender"`
}

func (msg *MsgApproveLoan) Reset()         {}
func (msg *MsgApproveLoan) String() string { return "MsgApproveLoan" }
func (msg *MsgApproveLoan) ProtoMessage()  {}
func (msg *MsgApproveLoan) ValidateBasic() error { return nil }
func (msg *MsgApproveLoan) GetSigners() []sdk.AccAddress {
	return []sdk.AccAddress{sdk.AccAddress(msg.Sender)}
}

// MsgApproveLoanResponse 批准放款响应。
type MsgApproveLoanResponse struct {
	LoanId []byte `json:"loan_id"`
	Status string `json:"status"`
}

func (m *MsgApproveLoanResponse) Reset()         {}
func (m *MsgApproveLoanResponse) String() string { return "MsgApproveLoanResponse" }
func (m *MsgApproveLoanResponse) ProtoMessage()  {}

// MsgSettleLoan 结清贷款消息。
type MsgSettleLoan struct {
	LoanId   []byte `json:"loan_id"`
	Sender   []byte `json:"sender"`
}

func (msg *MsgSettleLoan) Reset()         {}
func (msg *MsgSettleLoan) String() string { return "MsgSettleLoan" }
func (msg *MsgSettleLoan) ProtoMessage()  {}
func (msg *MsgSettleLoan) ValidateBasic() error { return nil }
func (msg *MsgSettleLoan) GetSigners() []sdk.AccAddress {
	return []sdk.AccAddress{sdk.AccAddress(msg.Sender)}
}

// MsgSettleLoanResponse 结清贷款响应。
type MsgSettleLoanResponse struct{}

func (m *MsgSettleLoanResponse) Reset()         {}
func (m *MsgSettleLoanResponse) String() string { return "MsgSettleLoanResponse" }
func (m *MsgSettleLoanResponse) ProtoMessage()  {}

// ============================================================
// MsgServer / QueryServer 接口
// ============================================================

// MsgServer 是暗链消息服务器接口。
type MsgServer interface {
	RequestLoan(ctx context.Context, msg *MsgRequestLoan) (*MsgRequestLoanResponse, error)
	ApproveLoan(ctx context.Context, msg *MsgApproveLoan) (*MsgApproveLoanResponse, error)
	SettleLoan(ctx context.Context, msg *MsgSettleLoan) (*MsgSettleLoanResponse, error)
}

// QueryServer 是暗链查询服务器接口。
type QueryServer interface {
	Loan(ctx context.Context, req *QueryLoanRequest) (*QueryLoanResponse, error)
	LoansByAnon(ctx context.Context, req *QueryLoansByAnonRequest) (*QueryLoansByAnonResponse, error)
	Nullifier(ctx context.Context, req *QueryNullifierRequest) (*QueryNullifierResponse, error)
}

// QueryLoanRequest 贷款查询请求。
type QueryLoanRequest struct {
	LoanId []byte `json:"loan_id"`
}

// QueryLoanResponse 贷款查询响应。
type QueryLoanResponse struct {
	Loan *LoanRecord `json:"loan"`
}

// QueryLoansByAnonRequest 按化名查询贷款请求。
type QueryLoansByAnonRequest struct {
	AnonId []byte `json:"anon_id"`
}

// QueryLoansByAnonResponse 按化名查询贷款响应。
type QueryLoansByAnonResponse struct {
	Loans []*LoanRecord `json:"loans"`
}

// QueryNullifierRequest Nullifier 查询请求。
type QueryNullifierRequest struct {
	Nullifier []byte `json:"nullifier"`
}

// QueryNullifierResponse Nullifier 查询响应。
type QueryNullifierResponse struct {
	IsSpent bool `json:"is_spent"`
}

// RegisterMsgServer 注册消息服务器（桩实现，等待 proto 生成）。
func RegisterMsgServer(srv interface{}, impl MsgServer) {
	// 生产环境：由 protobuf 生成的 RegisterMsgServer 替代
}

// RegisterQueryServer 注册查询服务器（桩实现，等待 proto 生成）。
func RegisterQueryServer(srv interface{}, impl QueryServer) {
	// 生产环境：由 protobuf 生成的 RegisterQueryServer 替代
}

// ============================================================
// 创世状态
// ============================================================

// GenesisState 定义暗链模块创世状态。
type GenesisState struct {
	Loans       []*LoanRecord  `json:"loans"`
	Nullifiers  []*Nullifier   `json:"nullifiers"`
	CreditNotes []*CreditNote  `json:"credit_notes"`
}

// DefaultGenesis 返回默认创世状态。
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Loans:       []*LoanRecord{},
		Nullifiers:  []*Nullifier{},
		CreditNotes: []*CreditNote{},
	}
}

// Validate 验证创世状态。
func (gs GenesisState) Validate() error {
	return nil
}

// ============================================================
// 接口注册（手写桩 — Phase 6 迁移至 proto 生成）
// ============================================================

// RegisterInterfaces 向接口注册表注册消息类型。
func RegisterInterfaces(registry codectypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil),
		&MsgRequestLoan{},
		&MsgApproveLoan{},
		&MsgSettleLoan{},
	)
}

// RegisterLegacyAminoCodec 注册旧版 Amino 编解码器。
func RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	cdc.RegisterConcrete(&MsgRequestLoan{}, "nekop2p/darkchain/MsgRequestLoan", nil)
	cdc.RegisterConcrete(&MsgApproveLoan{}, "nekop2p/darkchain/MsgApproveLoan", nil)
	cdc.RegisterConcrete(&MsgSettleLoan{}, "nekop2p/darkchain/MsgSettleLoan", nil)
}
