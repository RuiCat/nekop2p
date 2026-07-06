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

func (ms msgServer) RequestLoan(ctx context.Context, msg *types.MsgRequestLoan) (*types.MsgRequestLoanResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	loan, err := ms.k.RequestLoan(sdkCtx, msg)
	if err != nil {
		return nil, err
	}

	return &types.MsgRequestLoanResponse{
		LoanId: loan.LoanId,
		Status: loan.Status.String(),
	}, nil
}

func (ms msgServer) ApproveLoan(ctx context.Context, msg *types.MsgApproveLoan) (*types.MsgApproveLoanResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	loan, err := ms.k.ApproveLoan(sdkCtx, msg)
	if err != nil {
		return nil, err
	}

	return &types.MsgApproveLoanResponse{
		LoanId: loan.LoanId,
		Status: loan.Status.String(),
	}, nil
}

func (ms msgServer) SettleLoan(ctx context.Context, msg *types.MsgSettleLoan) (*types.MsgSettleLoanResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	// TODO: 验证 ZK 还款证明

	if err := ms.k.SettleLoan(sdkCtx, msg.LoanId); err != nil {
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

func (qs queryServer) Loan(ctx context.Context, req *types.QueryLoanRequest) (*types.QueryLoanResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	loan, err := qs.k.GetLoan(sdkCtx, req.LoanId)
	if err != nil {
		return nil, err
	}

	return &types.QueryLoanResponse{Loan: loan}, nil
}

func (qs queryServer) LoansByAnon(ctx context.Context, req *types.QueryLoansByAnonRequest) (*types.QueryLoansByAnonResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	allLoans := qs.k.GetAllLoans(sdkCtx)
	var matching []*types.LoanRecord
	for _, loan := range allLoans {
		if string(loan.BorrowerAnon) == string(req.AnonId) ||
			string(loan.LenderAnon) == string(req.AnonId) {
			matching = append(matching, loan)
		}
	}

	return &types.QueryLoansByAnonResponse{Loans: matching}, nil
}

func (qs queryServer) Nullifier(ctx context.Context, req *types.QueryNullifierRequest) (*types.QueryNullifierResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	return &types.QueryNullifierResponse{
		IsSpent: qs.k.IsNullifierSpent(sdkCtx, req.Nullifier),
	}, nil
}
