//go:build !cosmos

// Package darkchain 实现了 Dark Chain（暗链）Cosmos SDK 模块。
//
// Dark Chain 是"地下银行"——匿名借贷、信用票据、
// 混沌结算。所有身份均为零知识匿名。
//
// 模块职责：
//   - 贷款申请与审批
//   - 债务状态追踪
//   - 匿名化名管理
//   - 信用票据防双花（nullifier 集合）
//   - 身份标记防自我交易
//   - 交易结算指令生成
package darkchain

import (
	"encoding/json"

	"github.com/nekop2p/nekop2p/x/darkchain/keeper"
	"github.com/nekop2p/nekop2p/x/darkchain/types"
)

// AppModule 实现了 Cosmos SDK AppModule 接口。
type AppModule struct {
	keeper *keeper.Keeper
}

// NewAppModule 创建一个新的暗链模块。
func NewAppModule(k *keeper.Keeper) AppModule {
	return AppModule{keeper: k}
}

// Name 返回模块名称。
func (am AppModule) Name() string {
	return types.ModuleName
}

// RegisterServices 注册模块服务。
// 生产环境：注册 MsgServer 和 QueryServer 到 Cosmos SDK 路由
func (am AppModule) RegisterServices() {
	_ = am.keeper
}

// DefaultGenesis 返回默认创世状态。
func (am AppModule) DefaultGenesis() json.RawMessage {
	return json.RawMessage(`{"loans":[],"cycle_count":0}`)
}

// ValidateGenesis 验证创世状态。
func (am AppModule) ValidateGenesis(data json.RawMessage) error {
	// 验证 JSON 格式合法
	var gs struct {
		Loans      []json.RawMessage `json:"loans"`
		CycleCount uint64            `json:"cycle_count"`
	}
	return json.Unmarshal(data, &gs)
}

// InitGenesis 从创世数据初始化模块状态。
func (am AppModule) InitGenesis(data json.RawMessage) error {
	var gs struct {
		Loans      []struct {
			LoanID       string `json:"loan_id"`
			BorrowerAnon []byte `json:"borrower_anon"`
			Amount       uint64 `json:"amount"`
			TermDays     int64  `json:"term_days"`
			Status       int32  `json:"status"`
		} `json:"loans"`
	}
	if err := json.Unmarshal(data, &gs); err != nil {
		return err
	}
	for _, l := range gs.Loans {
		msg := &types.MsgRequestLoan{
			BorrowerAnon: l.BorrowerAnon,
			Amount:       l.Amount,
			TermDays:     l.TermDays,
		}
		loan, err := am.keeper.RequestLoan(msg)
		if err != nil {
			return err
		}
		if types.LoanStatus(l.Status) == types.LoanActive {
			loan.Status = types.LoanActive
		}
	}
	return nil
}

// ExportGenesis 导出模块当前状态。
func (am AppModule) ExportGenesis() json.RawMessage {
	type loanExport struct {
		LoanID       string `json:"loan_id"`
		BorrowerAnon []byte `json:"borrower_anon"`
		Amount       uint64 `json:"amount"`
		TermDays     int64  `json:"term_days"`
		Status       int32  `json:"status"`
	}
	loans := am.keeper.GetAllLoans()
	export := make([]loanExport, len(loans))
	for i, l := range loans {
		export[i] = loanExport{
			LoanID:       l.LoanID,
			BorrowerAnon: l.BorrowerAnon,
			Amount:       l.Amount,
			TermDays:     l.TermDays,
			Status:       int32(l.Status),
		}
	}
	data, err := json.Marshal(map[string]interface{}{
		"loans":        export,
		"cycle_count":  am.keeper.CycleCount(),
	})
	if err != nil {
		return json.RawMessage(`{"error":"marshal failed"}`)
	}
	return data
}

// BeginBlock 在每个区块开始时调用。
func (am AppModule) BeginBlock() {
	// 检查逾期贷款并标记违约
	overdue := am.keeper.GetOverdueLoans()
	for _, loan := range overdue {
		am.keeper.DefaultLoan(loan.LoanID)
	}
	// 推进周期（清空身份标记集合）
	am.keeper.AdvanceCycle()
}

// EndBlock 在每个区块结束时调用。
func (am AppModule) EndBlock() {
	// 推进暗链周期（清空身份标记集合）
	am.keeper.AdvanceCycle()
}

// ConsensusVersion 返回模块的共识版本。
func (am AppModule) ConsensusVersion() uint64 {
	return 1
}

var _ = types.StoreKey
