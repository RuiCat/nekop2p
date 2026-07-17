//go:build cosmos

// Package module 实现明链的 Cosmos SDK AppModule (v0.54+)。
//
// AppModule 负责:
//   - 注册 MsgServer (交易处理)
//   - 注册 QueryServer (链上查询)
//   - BeginBlock/EndBlock 钩子 (周期维护)
//   - InitGenesis/ExportGenesis (创世状态)
//
// Package module 提供明链 Cosmos SDK 模块注册。
package module

import (
	"encoding/json"
	"fmt"

	"cosmossdk.io/core/appmodule"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"google.golang.org/grpc"

	"github.com/nekop2p/nekop2p/x/brightchain/keeper"
	"github.com/nekop2p/nekop2p/x/brightchain/types"
)

var (
	_ module.AppModule      = AppModule{}
	_ module.AppModuleBasic = AppModuleBasic{}
	_ appmodule.HasServices = AppModule{}
)

// AppModuleBasic 定义模块的基本信息。
type AppModuleBasic struct {
	cdc codec.Codec
}

func (AppModuleBasic) Name() string {
	return types.ModuleName
}

func (AppModuleBasic) RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	types.RegisterLegacyAminoCodec(cdc)
}

func (AppModuleBasic) RegisterInterfaces(registry codectypes.InterfaceRegistry) {
	types.RegisterInterfaces(registry)
}

func (AppModuleBasic) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	gs := types.DefaultGenesis()
	bz, _ := json.Marshal(gs)
	return bz
}

func (AppModuleBasic) ValidateGenesis(cdc codec.JSONCodec, _ client.TxEncodingConfig, bz json.RawMessage) error {
	var genesis types.GenesisState
	if err := json.Unmarshal(bz, &genesis); err != nil {
		return fmt.Errorf("failed to unmarshal brightchain genesis: %w", err)
	}
	return genesis.Validate()
}

func (AppModuleBasic) RegisterGRPCGatewayRoutes(clientCtx client.Context, mux *runtime.ServeMux) {
	// Phase 6: 注册 gRPC-Gateway 路由
}

// AppModule 实现 module.AppModule 接口。
type AppModule struct {
	AppModuleBasic
	keeper keeper.Keeper
}

// NewAppModule 创建新的明链 AppModule。
func NewAppModule(cdc codec.Codec, k keeper.Keeper) AppModule {
	return AppModule{
		AppModuleBasic: AppModuleBasic{cdc: cdc},
		keeper:         k,
	}
}

// RegisterServices 注册 gRPC 服务 (v0.54+ API)。
func (am AppModule) RegisterServices(registrar grpc.ServiceRegistrar) error {
	types.RegisterMsgServer(registrar, keeper.NewMsgServerImpl(am.keeper))
	types.RegisterQueryServer(registrar, keeper.NewQueryServerImpl(am.keeper))
	return nil
}

func (am AppModule) IsOnePerModuleType() {}
func (am AppModule) IsAppModule()        {}

func (am AppModule) RegisterInvariants(ir sdk.InvariantRegistry) {
	// Phase 5: 注册不变检查
}

func (am AppModule) InitGenesis(ctx sdk.Context, cdc codec.JSONCodec, data json.RawMessage) {
	var genesis types.GenesisState
	if err := json.Unmarshal(data, &genesis); err != nil {
		panic(fmt.Sprintf("unmarshal genesis: %v", err))
	}
	am.keeper.InitGenesis(ctx, &genesis)
}

func (am AppModule) ExportGenesis(ctx sdk.Context, cdc codec.JSONCodec) json.RawMessage {
	genesis := am.keeper.ExportGenesis(ctx)
	bz, _ := json.Marshal(genesis)
	return bz
}

func (am AppModule) ConsensusVersion() uint64 {
	return 1
}

func (am AppModule) BeginBlock(ctx sdk.Context) error {
	am.keeper.BeginBlocker(ctx)
	return nil
}

func (am AppModule) EndBlock(ctx sdk.Context) error {
	am.keeper.EndBlocker(ctx)
	return nil
}
