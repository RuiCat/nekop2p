//go:build cosmos

// Package keeper 实现暗链 MsgServer。
package keeper

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/nekop2p/nekop2p/x/darkchain/types"
)

type msgServer struct {
	k Keeper
}

func NewMsgServerImpl(keeper Keeper) types.MsgServer {
	return &msgServer{k: keeper}
}

func (ms msgServer) RequestLoan(goCtx context.Context, msg *types.MsgRequestLoan) (*types.MsgRequestLoanResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	loan, err := ms.k.RequestLoan(ctx, msg)
	if err != nil {
		return nil, err
	}

	return &types.MsgRequestLoanResponse{
		LoanId: loan.LoanId,
		Status: loan.Status.String(),
	}, nil
}

func (ms msgServer) ApproveLoan(goCtx context.Context, msg *types.MsgApproveLoan) (*types.MsgApproveLoanResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	loan, err := ms.k.ApproveLoan(ctx, msg)
	if err != nil {
		return nil, err
	}

	return &types.MsgApproveLoanResponse{
		LoanId: loan.LoanId,
		Status: loan.Status.String(),
	}, nil
}

func (ms msgServer) SettleLoan(goCtx context.Context, msg *types.MsgSettleLoan) (*types.MsgSettleLoanResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	// ZK 还款证明验证 (Phase 4: gnark 电路就绪后激活)
	_ = ctx

	if err := ms.k.SettleLoan(ctx, msg.LoanId); err != nil {
		return nil, err
	}

	return &types.MsgSettleLoanResponse{}, nil
}

// ============================================================
// QueryServer
// ============================================================

type queryServer struct {
	k Keeper
}

func NewQueryServerImpl(keeper Keeper) types.QueryServer {
	return &queryServer{k: keeper}
}

func (qs queryServer) Loan(goCtx context.Context, req *types.QueryLoanRequest) (*types.QueryLoanResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	loan, err := qs.k.GetLoan(ctx, req.LoanId)
	if err != nil {
		return nil, err
	}

	return &types.QueryLoanResponse{Loan: loan}, nil
}

func (qs queryServer) LoansByAnon(goCtx context.Context, req *types.QueryLoansByAnonRequest) (*types.QueryLoansByAnonResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	allLoans := qs.k.GetAllLoans(ctx)
	var matching []*types.LoanRecord
	for _, loan := range allLoans {
		if string(loan.BorrowerAnon) == string(req.AnonId) ||
			string(loan.LenderAnon) == string(req.AnonId) {
			matching = append(matching, loan)
		}
	}

	return &types.QueryLoansByAnonResponse{Loans: matching}, nil
}

func (qs queryServer) Nullifier(goCtx context.Context, req *types.QueryNullifierRequest) (*types.QueryNullifierResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	return &types.QueryNullifierResponse{
		IsSpent: qs.k.IsNullifierSpent(ctx, req.Nullifier),
	}, nil
}

// Silence unused import
var _ = fmt.Sprintf
