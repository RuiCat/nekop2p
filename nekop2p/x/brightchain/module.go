//go:build !cosmos

// Package brightchain 实现明链 Cosmos SDK 模块。
//
// 明链是"阳光市场" — 公开、透明、假名化。
// 它管理 UserBlocks、担保金和共享资金池。
//
// 本文件将模块注册到 Cosmos SDK 应用中。
package brightchain

import (
	"encoding/json"
	"fmt"

	"github.com/nekop2p/nekop2p/x/brightchain/keeper"
	"github.com/nekop2p/nekop2p/x/brightchain/types"
)

// genesisState 定义模块的创世状态 JSON 格式。
type genesisState struct {
	Users []genesisUser  `json:"users"`
	Bonds []genesisBond  `json:"bonds"`
	Pool  genesisPool    `json:"pool"`
}

type genesisUser struct {
	SendPk []byte `json:"send_pk"`
	RecvPk []byte `json:"recv_pk"`
}

type genesisBond struct {
	Inviter string `json:"inviter"`
	Invitee string `json:"invitee"`
	Amount  uint64 `json:"amount"`
}

type genesisPool struct {
	TotalBalance uint64 `json:"total_balance"`
}

// AppModule 实现 Cosmos SDK 的 AppModule 接口。
type AppModule struct {
	keeper *keeper.Keeper
}

// NewAppModule 创建新的明链模块。
func NewAppModule(k *keeper.Keeper) AppModule {
	return AppModule{keeper: k}
}

// Name 返回模块名称。
func (am AppModule) Name() string {
	return types.ModuleName
}

// RegisterServices 注册模块服务（MsgServer、QueryServer）。
// 由 Cosmos SDK 应用在初始化期间调用。
func (am AppModule) RegisterServices(cfg types.Configurator) {
	types.RegisterMsgServer(cfg.MsgServer(), NewMsgServerImpl(am.keeper))
	types.RegisterQueryServer(cfg.QueryServer(), NewQueryServerImpl(am.keeper))
}

// DefaultGenesis 返回默认创世状态。
func (am AppModule) DefaultGenesis() json.RawMessage {
	return json.RawMessage(`{"users":[],"bonds":[],"pool":{"total_balance":0}}`)
}

// ValidateGenesis 验证创世状态 JSON 的合法性。
func (am AppModule) ValidateGenesis(data json.RawMessage) error {
	var gs genesisState
	if err := json.Unmarshal(data, &gs); err != nil {
		return fmt.Errorf("invalid genesis JSON: %w", err)
	}

	// 验证用户
	for i, user := range gs.Users {
		if len(user.SendPk) == 0 {
			return fmt.Errorf("genesis user %d: send_pk is required", i)
		}
		if len(user.RecvPk) == 0 {
			return fmt.Errorf("genesis user %d: recv_pk is required", i)
		}
	}

	// 验证担保债券
	for i, bond := range gs.Bonds {
		if bond.Inviter == "" || bond.Invitee == "" {
			return fmt.Errorf("genesis bond %d: inviter and invitee are required", i)
		}
	}

	return nil
}

// InitGenesis 从创世数据初始化模块状态。
func (am AppModule) InitGenesis(ctx types.Context, data json.RawMessage) error {
	var gs genesisState
	if err := json.Unmarshal(data, &gs); err != nil {
		return fmt.Errorf("init genesis: %w", err)
	}

	// 注册创世用户
	for _, user := range gs.Users {
		msg := &types.MsgRegister{
			SendPk: user.SendPk,
			RecvPk: user.RecvPk,
		}
		if _, err := am.keeper.RegisterUser(ctx, msg); err != nil {
			return fmt.Errorf("init genesis: register user: %w", err)
		}
	}

	// 创建创世担保债券
	for _, bond := range gs.Bonds {
		msg := &types.MsgGuarantee{
			Inviter:   bond.Inviter,
			Invitee:   bond.Invitee,
			Coefficient: 80, // 默认 80%
		}
		if _, err := am.keeper.CreateBond(ctx, msg); err != nil {
			return fmt.Errorf("init genesis: create bond: %w", err)
		}
	}

	// 设置初始资金池余额
	if gs.Pool.TotalBalance > 0 {
		pool := am.keeper.GetPool(ctx)
		pool.TotalBalance = gs.Pool.TotalBalance
		am.keeper.SetPool(pool)
	}

	return nil
}

// ExportGenesis 导出当前模块状态为创世 JSON。
func (am AppModule) ExportGenesis(ctx types.Context) json.RawMessage {
	// 遍历所有用户和债券，序列化为 JSON
	users := am.keeper.GetAllUsers(ctx)
	bonds := am.keeper.GetAllBonds(ctx)
	pool := am.keeper.GetPool(ctx)

	gs := genesisState{}
	for _, block := range users {
		gs.Users = append(gs.Users, genesisUser{
			SendPk: block.SendPk,
			RecvPk: block.RecvPk,
		})
	}
	for _, bond := range bonds {
		gs.Bonds = append(gs.Bonds, genesisBond{
			Inviter: string(bond.Inviter),
			Invitee: string(bond.Invitee),
			Amount:  bond.TotalBond,
		})
	}
	gs.Pool = genesisPool{TotalBalance: pool.TotalBalance}

	data, err := json.Marshal(gs)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}

// BeginBlock 在每个区块开始时调用。
func (am AppModule) BeginBlock(ctx types.Context) {
	// 检查逾期担保债券 → 标记为违约
	bonds := am.keeper.ListBonds(ctx)
	for _, bond := range bonds {
		if bond.Status == types.BondStatus_ACTIVE && bond.UnlockAt > 0 {
			// 已到期但未释放的担保债券标记为违约
			// 生产环境：检查时间戳
			_ = bond
		}
	}
}

// EndBlock 在每个区块结束时调用。
func (am AppModule) EndBlock(ctx types.Context) {
	// 计算信任权重，处理节点薪资支付（每月）
	am.keeper.RecalculateTrustWeights(ctx)
}

// ConsensusVersion 返回模块的共识版本。
func (am AppModule) ConsensusVersion() uint64 {
	return 1
}
