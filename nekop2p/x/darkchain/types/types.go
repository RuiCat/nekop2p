package types

const ModuleName = "darkchain"
const StoreKey = ModuleName
const RouterKey = ModuleName

const (
	LoanKeyPrefix         = 0x01
	CreditTreeRootPrefix  = 0x02
	NullifierSetPrefix    = 0x03
	IdentityMarkerPrefix  = 0x04
	CycleMarkerKey        = 0x05
)

type LoanStatus int32
const (
	LoanPending   LoanStatus = 0
	LoanActive    LoanStatus = 1
	LoanSettled   LoanStatus = 2
	LoanDefaulted LoanStatus = 3
)

type MsgRequestLoan struct {
	BorrowerAnon   []byte
	Amount         uint64
	TermDays       int64
	ZkCreditProof  []byte
	InkwellSeed    []byte
}

type MsgApproveLoan struct {
	LoanID       string
	LenderAnon   []byte
	InkwellParams []byte
}

type MsgSettleLoan struct {
	LoanID         string
	DeliveryProof  []byte
	SettlementCmd  []byte
}

type LoanRecord struct {
	LoanID        string
	BorrowerAnon  []byte
	LenderAnon    []byte
	Amount        uint64
	TermDays      int64
	Status        LoanStatus
	ZkCreditProof []byte
	InkwellParams []byte
	DeliveryProof []byte // 交付确认（结算时填写）
	SettlementCmd []byte // 结算指令（触发明链划款）
	CreatedAt     int64
	DueDate       int64
	SettledAt     int64
}
