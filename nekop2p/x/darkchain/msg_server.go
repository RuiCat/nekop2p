//go:build !cosmos

package darkchain

import (
	"fmt"

	"github.com/nekop2p/nekop2p/x/darkchain/keeper"
	"github.com/nekop2p/nekop2p/x/darkchain/types"
)

// MsgServerImpl 实现暗链的消息服务器。
type MsgServerImpl struct {
	keeper *keeper.Keeper
}

// NewMsgServerImpl 创建新的暗链 MsgServer。
func NewMsgServerImpl(k *keeper.Keeper) *MsgServerImpl {
	return &MsgServerImpl{keeper: k}
}

// RequestLoan 提交匿名借贷申请。
func (m *MsgServerImpl) RequestLoan(msg *types.MsgRequestLoan) (*types.LoanRecord, error) {
	if msg.Amount == 0 {
		return nil, fmt.Errorf("request_loan: amount must be > 0")
	}
	if msg.TermDays <= 0 || msg.TermDays > 365 {
		return nil, fmt.Errorf("request_loan: term must be 1-365 days")
	}
	// 生产环境：验证 ZK 信用证明
	// zkKeeper.VerifyCreditProof(msg.ZkCreditProof)

	return m.keeper.RequestLoan(msg)
}

// ApproveLoan 批准匿名贷款。
func (m *MsgServerImpl) ApproveLoan(msg *types.MsgApproveLoan) (*types.LoanRecord, error) {
	if msg.LoanID == "" {
		return nil, fmt.Errorf("approve_loan: loan_id is required")
	}
	// 生产环境：验证贷款人有权放贷（信用票据充足）
	return m.keeper.ApproveLoan(msg)
}

// SettleLoan 结算贷款（交付确认）。
func (m *MsgServerImpl) SettleLoan(msg *types.MsgSettleLoan) (*types.LoanRecord, error) {
	if msg.LoanID == "" {
		return nil, fmt.Errorf("settle_loan: loan_id is required")
	}
	// 生产环境：验证交付证明
	// zkKeeper.VerifyDeliveryProof(msg.DeliveryProof)
	return m.keeper.SettleLoan(msg)
}

// CheckOverdueLoans 检查逾期贷款并标记违约。
func (m *MsgServerImpl) CheckOverdueLoans() []*types.LoanRecord {
	overdue := m.keeper.GetOverdueLoans()
	for _, loan := range overdue {
		m.keeper.DefaultLoan(loan.LoanID)
	}
	return overdue
}
