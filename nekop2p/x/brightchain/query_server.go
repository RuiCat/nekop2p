package brightchain

import (
	"fmt"

	"github.com/nekop2p/nekop2p/x/brightchain/keeper"
	"github.com/nekop2p/nekop2p/x/brightchain/types"
)

// ===== 查询类型 =====

// AccountQuery 按 chain_id 查询用户账户。
type AccountQuery struct {
	ChainID string
}

// AccountResponse 返回用户账户信息。
type AccountResponse struct {
	Address       string
	CreditScore   uint64
	TrustWeight   uint64
	CreditLimit   uint64
	SeedPhase     bool
	NodeRole      types.NodeRole
	FriendCount   int
	GuaranteedOf  int
	GuaranteedBy  int
}

// KeysQuery 按 chain_id 查询用户公钥。
type KeysQuery struct {
	ChainID string
}

// KeysResponse 返回用户公钥。
type KeysResponse struct {
	RecvPk []byte
	SendPk []byte
}

// TrustQuery 查询信任图信息。
type TrustQuery struct {
	ChainID string
}

// TrustResponse 返回信任图信息。
type TrustResponse struct {
	TrustWeight    uint64
	CreditScore    uint64
	CreditLimit    uint64
	GuarantorCount int
	SeedPhase      bool
}

// PoolQuery 查询资金池状态。
type PoolQuery struct{}

// PoolResponse 返回资金池状态。
type PoolResponse struct {
	TotalBalance    uint64
	SalaryRelay     uint64
	SalaryRecord    uint64
	SeedLoanReserve uint64
	BadDebtReserve  uint64
	Community       uint64
}

// BondQuery 按 bond_id 查询担保债券。
type BondQuery struct {
	BondID string
}

// BondResponse 返回担保债券信息。
type BondResponse struct {
	BondID     string
	Inviter    string
	Invitee    string
	TotalBond  uint64
	SeedLimit  uint64
	Status     types.BondStatus
}

// NodeQuery 查询节点列表。
type NodeQuery struct {
	Role types.NodeRole // 0 = 全部
}

// NodeResponse 返回节点列表。
type NodeResponse struct {
	Nodes []NodeInfo
}

// NodeInfo 节点基本信息。
type NodeInfo struct {
	ChainID    string
	Role       types.NodeRole
	TrustWeight uint64
}

// ===== QueryServerImpl =====

// QueryServerImpl 实现查询服务。
type QueryServerImpl struct {
	keeper *keeper.Keeper
}

// NewQueryServerImpl 创建新的 QueryServer。
func NewQueryServerImpl(k *keeper.Keeper) *QueryServerImpl {
	return &QueryServerImpl{keeper: k}
}

// QueryAccount 查询用户账户信息。
func (q *QueryServerImpl) QueryAccount(query *AccountQuery) (*AccountResponse, error) {
	if query.ChainID == "" {
		return nil, fmt.Errorf("query: chain_id is required")
	}
	ctx := types.UnwrapSDKContext(nil)
	block := q.keeper.GetUserBlock(ctx, parseChainIDForQuery(query.ChainID))
	if block == nil {
		return nil, fmt.Errorf("query: account not found: %s", query.ChainID)
	}
	return &AccountResponse{
		Address:       block.Address,
		CreditScore:   block.CreditScore,
		TrustWeight:   block.TrustWeight,
		CreditLimit:   block.CreditLimit,
		SeedPhase:     block.SeedPhase,
		NodeRole:      block.NodeRole,
		FriendCount:   len(block.Friends),
		GuaranteedOf:  len(block.GuaranteedOf),
		GuaranteedBy:  len(block.GuaranteedBy),
	}, nil
}

// QueryKeys 查询用户公钥。
func (q *QueryServerImpl) QueryKeys(query *KeysQuery) (*KeysResponse, error) {
	if query.ChainID == "" {
		return nil, fmt.Errorf("query: chain_id is required")
	}
	ctx := types.UnwrapSDKContext(nil)
	block := q.keeper.GetUserBlock(ctx, parseChainIDForQuery(query.ChainID))
	if block == nil {
		return nil, fmt.Errorf("query: account not found: %s", query.ChainID)
	}
	return &KeysResponse{
		RecvPk: block.RecvPk,
		SendPk: block.SendPk,
	}, nil
}

// QueryTrust 查询信任图信息。
func (q *QueryServerImpl) QueryTrust(query *TrustQuery) (*TrustResponse, error) {
	if query.ChainID == "" {
		return nil, fmt.Errorf("query: chain_id is required")
	}
	ctx := types.UnwrapSDKContext(nil)
	block := q.keeper.GetUserBlock(ctx, parseChainIDForQuery(query.ChainID))
	if block == nil {
		return nil, fmt.Errorf("query: account not found: %s", query.ChainID)
	}
	return &TrustResponse{
		TrustWeight:    block.TrustWeight,
		CreditScore:    block.CreditScore,
		CreditLimit:    block.CreditLimit,
		GuarantorCount: len(block.GuaranteedBy),
		SeedPhase:      block.SeedPhase,
	}, nil
}

// QueryPool 查询资金池状态。
func (q *QueryServerImpl) QueryPool(query *PoolQuery) (*PoolResponse, error) {
	ctx := types.UnwrapSDKContext(nil)
	pool := q.keeper.GetPool(ctx)
	return &PoolResponse{
		TotalBalance:    pool.TotalBalance,
		SalaryRelay:     pool.SalaryRelay,
		SalaryRecord:    pool.SalaryRecord,
		SeedLoanReserve: pool.SeedLoanReserve,
		BadDebtReserve:  pool.BadDebtReserve,
		Community:       pool.Community,
	}, nil
}

// QueryBond 查询担保债券信息。
func (q *QueryServerImpl) QueryBond(query *BondQuery) (*BondResponse, error) {
	if query.BondID == "" {
		return nil, fmt.Errorf("query: bond_id is required")
	}
	ctx := types.UnwrapSDKContext(nil)
	bond := q.keeper.GetBond(ctx, query.BondID)
	if bond == nil {
		return nil, fmt.Errorf("query: bond not found: %s", query.BondID)
	}
	return &BondResponse{
		BondID:     bond.BondId,
		Inviter:    string(bond.Inviter),
		Invitee:    string(bond.Invitee),
		TotalBond:  bond.TotalBond,
		SeedLimit:  bond.SeedLimit,
		Status:     bond.Status,
	}, nil
}

// QueryNodes 查询已注册的节点列表。
func (q *QueryServerImpl) QueryNodes(query *NodeQuery) (*NodeResponse, error) {
	ctx := types.UnwrapSDKContext(nil)
	users := q.keeper.GetAllUsers(ctx)
	var nodes []NodeInfo
	for _, block := range users {
		if query.Role == 0 || block.NodeRole == query.Role {
			nodes = append(nodes, NodeInfo{
				ChainID:     block.Address,
				Role:        block.NodeRole,
				TrustWeight: block.TrustWeight,
			})
		}
	}
	return &NodeResponse{Nodes: nodes}, nil
}

func parseChainIDForQuery(addr string) types.ChainID {
	var id types.ChainID
	copy(id[:], []byte(addr))
	return id
}

var _ types.QueryServer = (*QueryServerImpl)(nil)
