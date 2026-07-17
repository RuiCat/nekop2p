//go:build cosmos

// Package types 定义 Inkwell 混沌结算池模块类型 (Cosmos SDK 版本)。
package types

const (
	ModuleName = "inkwell"
	StoreKey   = ModuleName
)

// InkwellParams 混沌结算参数。
type InkwellParams struct {
	LoanID        string   `json:"loan_id"`
	Seed          []byte   `json:"seed"`
	TotalAmount   uint64   `json:"total_amount"`
	WindowStart   int64    `json:"window_start"`
	WindowEnd     int64    `json:"window_end"`
	FragmentCount int      `json:"fragment_count"`
	Fragments     []uint64 `json:"fragments"`
	RelayEnabled  bool     `json:"relay_enabled"`
	RelayPath     []string `json:"relay_path"`
}

// GenesisState 创世状态。
type GenesisState struct {
	ActiveParams []*InkwellParams `json:"active_params"`
}

func DefaultGenesis() *GenesisState { return &GenesisState{} }

// MsgServer / QueryServer 接口桩。
type MsgServer interface{}
type QueryServer interface{}

func RegisterMsgServer(srv interface{}, impl MsgServer)   {}
func RegisterQueryServer(srv interface{}, impl QueryServer) {}
