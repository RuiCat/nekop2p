// Package types 提供明链模块的类型定义。
//
// 生产环境中，这些类型由 protoc 从 .proto 文件生成。
// 本文件为模块骨架提供接口桩。
package types

// ChainID 是一个 32 字节的链上标识符。
type ChainID [32]byte

func (c ChainID) String() string {
	return string(c[:])
}

// Context 包装 Cosmos SDK 上下文。
// 生产环境：sdk.Context
type Context interface{}

// Configurator 包装 Cosmos SDK 配置器。
type Configurator interface {
	MsgServer() interface{}
	QueryServer() interface{}
}

// MsgServer 是消息服务器接口。
type MsgServer interface {
	// 在 tx.proto 中定义
}

// QueryServer 是查询服务器接口。
type QueryServer interface {
	// 在 query.proto 中定义
}

// RegisterMsgServer 注册消息服务器。模块骨架桩。
func RegisterMsgServer(srv interface{}, impl MsgServer) {}

// RegisterQueryServer 注册查询服务器。模块骨架桩。
func RegisterQueryServer(srv interface{}, impl QueryServer) {}

// UnwrapSDKContext 提取 SDK 上下文。桩。
func UnwrapSDKContext(ctx Context) Context { return ctx }


// ===== Proto 消息类型（通常由 protoc 生成）=====

type NodeRole int32

const (
	NodeRole_NONE            NodeRole = 0
	NodeRole_PUBLIC          NodeRole = 1
	NodeRole_OFFICIAL_RELAY  NodeRole = 2
	NodeRole_OFFICIAL_RECORD NodeRole = 3
	NodeRole_GAME_SERVER     NodeRole = 4 // 游戏服务器节点
)

type BondStatus int32

const (
	BondStatus_ACTIVE    BondStatus = 0
	BondStatus_RELEASED  BondStatus = 1
	BondStatus_FORFEITED BondStatus = 2
)

type UserBlock struct {
	Address        string
	AccountNumber  uint64
	Sequence       uint64
	RecvPk         []byte
	SendPk         []byte
	CreditScore    uint64
	TrustWeight    uint64
	TotalRepayAmount uint64
	CreditLimit    uint64
	SeedPhase      bool
	Guarantors     [][]byte
	NodeRole       NodeRole
	NodeTermStart  int64
	NodeTermEnd    int64
	Friends        []*FriendRecord
	GuaranteedOf   []*BondRef
	GuaranteedBy   []*BondRef
	GameEarnings   uint64 // 游戏分成累计收益
}

// ===== 游戏注册表 =====

type GameInfo struct {
	GameID       string   // 游戏唯一标识
	AuthorID     string   // 游戏作者 chain_id (所有权)
	Name         string   // 游戏名称
	FeeRate      uint64   // 总手续费率 (千分比)
	AuthorShare  uint64   // 作者分成比例 (%)
	ServerShare  uint64   // 服务器分成比例 (%)
	PoolShare    uint64   // 基础设施分成比例 (%)
	Status       int32    // 0=活跃 1=暂停 2=关闭
	CreatedAt    int64
	TotalTxs     uint64
	TotalFees    uint64
}

type MsgRegisterGame struct {
	GameID      string
	AuthorID    string
	Name        string
	FeeRate     uint64
	AuthorShare uint64
	ServerShare uint64
}

// 游戏服务器绑定: 节点注册为某个游戏的服务器
type GameServerBinding struct {
	NodeID  string // 服务器节点 chain_id
	GameID  string // 服务的游戏
	Since   int64  // 开始服务时间
}

type FriendRecord struct {
	ChainId      []byte // Proto 字段名: chain_id
	RecvPk       []byte
	SendPk       []byte
	TrustDist    uint32
	AddedAt      int64
	IntroducedBy []byte
}

type BondRef struct {
	BondId     string
	OtherParty []byte
}

type GuaranteeBond struct {
	BondId                  string
	Inviter                 []byte
	Invitee                 []byte
	LockedNoteCommitments   [][]byte
	TotalBond               uint64
	Coefficient             uint64
	SeedLimit               uint64
	LockedAt                int64
	UnlockAt                int64
	Status                  BondStatus
}

type Pool struct {
	TotalBalance     uint64
	SalaryRelay      uint64
	SalaryRecord     uint64
	SeedLoanReserve  uint64
	BadDebtReserve   uint64
	Community        uint64
	GameFees         uint64 // 游戏交易手续费池
	GameCommission   uint64 // 游戏服务器分成池
}

// ===== Message types =====

type MsgRegister struct {
	RecvPk          []byte
	SendPk          []byte
	ZkIdentityProof []byte
	GuarantorSigs   [][]byte
}

type MsgRegisterResponse struct {
	Address  string
	Sequence uint64
}

type MsgUpdateFriends struct {
	Sender string
	Add    []*FriendRecord
	Remove [][]byte
}

type MsgUpdateFriendsResponse struct{}

type MsgGuarantee struct {
	Inviter               string
	Invitee               string
	BondNoteCommitments   [][]byte
	Coefficient           uint64
	LockPeriodDays        int64
	ZkLegitimate          []byte
}

type MsgGuaranteeResponse struct {
	BondId string
}

type MsgReleaseBond struct {
	BondId   string
	Inviter  string
}

type MsgReleaseBondResponse struct{}

type MsgRepay struct {
	FromAddress   string
	Amount        uint64
	ZkRepayProof  []byte
	InkwellRef    []byte
}

type MsgRepayResponse struct{}

type MsgRegisterNode struct {
	NodeAddress     string
	Role            NodeRole
	ZkWeightProof   []byte
	ExamResultHash  []byte
}

type MsgRegisterNodeResponse struct{}
