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
	BorrowerAnon       []byte
	Amount             uint64
	TermDays           int64
	ZkCreditProof      []byte
	InkwellSeed        []byte
	// === Commit-Reveal 种子协商 ===
	BorrowerSeedCommit [32]byte // 借款人的种子承诺
	BorrowerSeedNonce  [32]byte // 借款人的 nonce
	// === UTXO 信用票据花费 ===
	CreditNotes    [][]byte // 花费的信用票据（序列化，含 commitment+serial+ownerKey）
	CreditProofs   [][]byte // 每张票据的 Merkle 证明
	MasterSecret   [32]byte // 票据所有者的 master_secret（用于生成 nullifier）
}

type MsgApproveLoan struct {
	LoanID       string
	LenderAnon   []byte
	InkwellParams []byte
	// === Commit-Reveal 种子协商 ===
	LenderSeed        [32]byte // 贷款人的种子（揭示）
	LenderSeedNonce   [32]byte // 贷款人的 nonce
	BorrowerSeed      [32]byte // 借款人的种子（贷款人已通过链下交换获知）
	BorrowerSeedNonce [32]byte // 借款人的 nonce
	LenderSeedCommit  [32]byte // 贷款人的种子承诺（可选：用于双向 commit-reveal）
}

// MsgRevealSeed 独立的种子揭示消息（可选，用于分离 commit 和 reveal 阶段）
type MsgRevealSeed struct {
	LoanID      string
	PartyAnon   []byte   // 揭示方匿名化名
	Seed        [32]byte // 揭示的种子
	Nonce       [32]byte // 揭示的 nonce
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
	// === Commit-Reveal 种子协商状态 ===
	BorrowerSeedCommit [32]byte // 借款人的种子承诺
	CombinedSeed       [32]byte // 合并后的最终种子
	// === UTXO 信用票据花费追踪 ===
	SpentNullifiers [][32]byte // 该贷款花费的 nullifier 列表
}
