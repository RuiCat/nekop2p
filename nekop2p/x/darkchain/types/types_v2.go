//go:build cosmos

// Package types 定义暗链模块的类型常量。
package types

const (
	ModuleName = "darkchain"
	StoreKey   = ModuleName
)

const (
	EventTypeRequestLoan = "darkchain.request_loan"
	EventTypeApproveLoan = "darkchain.approve_loan"
	EventTypeSettleLoan  = "darkchain.settle_loan"

	AttributeKeyLoanID    = "loan_id"
	AttributeKeyAmount    = "amount"
	AttributeKeyBorrower  = "borrower_anon"
)

// GenesisState 定义暗链模块创世状态。
type GenesisState struct {
	Loans       []*LoanRecord `json:"loans"`
	Nullifiers  []*Nullifier  `json:"nullifiers"`
	CreditNotes []*CreditNote `json:"credit_notes"`
}

func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Loans:       []*LoanRecord{},
		Nullifiers:  []*Nullifier{},
		CreditNotes: []*CreditNote{},
	}
}

func (gs GenesisState) Validate() error {
	return nil
}
