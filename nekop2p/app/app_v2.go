//go:build cosmos

// Package app 提供 NekoApp — 基于 Cosmos SDK BaseApp 的双链应用。
//
// v0.50+ 架构:
//   - BaseApp → ABCI 接口
//   - rootmulti.Store → IAVL 多存储
//   - AppModule → 模块注册
//   - CometBFT → BFT 共识 (Phase C)
//
// Package app 提供 Cosmos SDK 主链应用。
package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"cosmossdk.io/log"
	"cosmossdk.io/store/rootmulti"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"

	"github.com/nekop2p/nekop2p/store"
	brightmodule "github.com/nekop2p/nekop2p/x/brightchain/module"
	brightkeeper "github.com/nekop2p/nekop2p/x/brightchain/keeper"
	brighttypes "github.com/nekop2p/nekop2p/x/brightchain/types"
	darkmodule "github.com/nekop2p/nekop2p/x/darkchain/module"
	darkkeeper "github.com/nekop2p/nekop2p/x/darkchain/keeper"
	darktypes "github.com/nekop2p/nekop2p/x/darkchain/types"
)

const (
	// AppName 是应用程序名称。
	AppName = "nekop2p"

	// DefaultBondDenom 是默认质押代币面额。
	DefaultBondDenom = "uneko"
)

var (
	// DefaultNodeHome 是默认节点数据目录。
	DefaultNodeHome = os.ExpandEnv("$HOME/.nekop2p")

	// ModuleBasics 是所有模块的基础管理器。
	ModuleBasics = module.NewBasicManager(
		brightmodule.AppModuleBasic{},
		darkmodule.AppModuleBasic{},
	)
)

// NekoApp 是基于 Cosmos SDK BaseApp 的双链应用。
type NekoApp struct {
	*baseapp.BaseApp

	// 编解码器
	appCodec          codec.Codec
	legacyAmino       *codec.LegacyAmino
	interfaceRegistry codectypes.InterfaceRegistry

	// 存储键
	keys map[string]*storetypes.KVStoreKey

	// 模块 Keeper
	BrightChainKeeper brightkeeper.Keeper
	DarkChainKeeper   darkkeeper.Keeper

	// 模块管理器
	mm *module.Manager

	// 配置
	configurator module.Configurator
}

// NewNekoApp 创建并初始化 NekoApp。
func NewNekoApp(
	logger log.Logger,
	db store.DB,
	invCheckPeriod uint,
	encodingConfig EncodingConfig,
	appOpts AppOptions,
	baseAppOptions ...func(*baseapp.BaseApp),
) *NekoApp {
	// === 编码配置 ===
	appCodec := encodingConfig.Marshaler
	legacyAmino := encodingConfig.Amino
	interfaceRegistry := encodingConfig.InterfaceRegistry

	// === BaseApp 初始化 ===
	bApp := baseapp.NewBaseApp(AppName, logger, db, encodingConfig.TxConfig.TxDecoder(), baseAppOptions...)
	bApp.SetCommitMultiStoreTracer(nil)
	bApp.SetVersion(Version)

	// === 存储键 ===
	keys := storetypes.NewKVStoreKeys(
		storetypes.StoreKey(brighttypes.StoreKey),
		storetypes.StoreKey(darktypes.StoreKey),
	)

	// === 创建 Keeper ===
	brightKeeper := brightkeeper.NewKeeper(
		appCodec,
		keys[brighttypes.StoreKey],
	)

	darkKeeper := darkkeeper.NewKeeper(
		appCodec,
		keys[darktypes.StoreKey],
	)

	// === 创建应用实例 ===
	app := &NekoApp{
		BaseApp:           bApp,
		appCodec:          appCodec,
		legacyAmino:       legacyAmino,
		interfaceRegistry: interfaceRegistry,
		keys:              keys,
		BrightChainKeeper: brightKeeper,
		DarkChainKeeper:   darkKeeper,
	}

	// === 注册模块 ===
	app.mm = module.NewManager(
		brightmodule.NewAppModule(appCodec, app.BrightChainKeeper),
		darkmodule.NewAppModule(appCodec, app.DarkChainKeeper),
	)

	// 设置模块注册顺序
	app.mm.SetOrderBeginBlockers(
		brighttypes.ModuleName,
		darktypes.ModuleName,
	)
	app.mm.SetOrderEndBlockers(
		darktypes.ModuleName,
		brighttypes.ModuleName,
	)
	app.mm.SetOrderInitGenesis(
		brighttypes.ModuleName,
		darktypes.ModuleName,
	)

	// 注册服务
	app.mm.RegisterInvariants(nil)  // 暂无不变检查
	app.configurator = module.NewConfigurator(app.appCodec, app.MsgServiceRouter(), app.GRPCQueryRouter())
	app.mm.RegisterServices(app.configurator)

	// === 初始化存储 ===
	app.MountKVStores(keys)

	// === 设置 AnteHandler ===
	// TODO: 添加自定义 AnteHandler (签名验证、双密钥检查)
	// app.SetAnteHandler(NewNekoAnteHandler(app.BrightChainKeeper))

	// === 加载最新状态 ===
	if err := app.LoadLatestVersion(); err != nil {
		logger.Error("failed to load latest version", "error", err)
		os.Exit(1)
	}

	return app
}

// Name 返回应用名称。
func (app *NekoApp) Name() string {
	return AppName
}

// LegacyAmino 返回旧版 Amino 编解码器。
func (app *NekoApp) LegacyAmino() *codec.LegacyAmino {
	return app.legacyAmino
}

// AppCodec 返回应用编解码器。
func (app *NekoApp) AppCodec() codec.Codec {
	return app.appCodec
}

// InterfaceRegistry 返回接口注册表。
func (app *NekoApp) InterfaceRegistry() codectypes.InterfaceRegistry {
	return app.interfaceRegistry
}

// GetKey 按名称返回存储键。
func (app *NekoApp) GetKey(keyName string) *storetypes.KVStoreKey {
	return app.keys[keyName]
}

// GetStoreKeys 返回所有存储键。
func (app *NekoApp) GetStoreKeys() []storetypes.StoreKey {
	keys := make([]storetypes.StoreKey, 0, len(app.keys))
	for _, key := range app.keys {
		keys = append(keys, key)
	}
	return keys
}

// ModuleManager 返回模块管理器。
func (app *NekoApp) ModuleManager() *module.Manager {
	return app.mm
}

// GetSubspace 返回参数子空间 (暂未实现 params 模块)。
func (app *NekoApp) GetSubspace(moduleName string) {
	// TODO: 实现 params 子空间
}

// ============================================================
// 编码配置
// ============================================================

// EncodingConfig 包含所有编码相关配置。
type EncodingConfig struct {
	InterfaceRegistry codectypes.InterfaceRegistry
	Marshaler         codec.Codec
	TxConfig          client.TxConfig
	Amino             *codec.LegacyAmino
}

// MakeEncodingConfig 创建编码配置。
func MakeEncodingConfig() EncodingConfig {
	interfaceRegistry := codectypes.NewInterfaceRegistry()
	marshaler := codec.NewProtoCodec(interfaceRegistry)
	txConfig := MakeTxConfig(marshaler)

	return EncodingConfig{
		InterfaceRegistry: interfaceRegistry,
		Marshaler:         marshaler,
		TxConfig:          txConfig,
		Amino:             codec.NewLegacyAmino(),
	}
}

// MakeTxConfig 创建交易配置。
func MakeTxConfig(marshaler codec.Codec) client.TxConfig {
	// 使用 Cosmos SDK 标准 TxConfig
	// 实际实现需引入 x/auth/tx 包
	return nil // TODO: 实现 TxConfig
}

// AppOptions 是应用选项接口。
type AppOptions interface {
	Get(string) interface{}
}

// Version 返回应用版本。
const Version = "4.0.0-cosmos"

// ============================================================
// 导出状态 (用于创世文件)
// ============================================================

// ExportAppStateAndValidators 导出应用状态。
func (app *NekoApp) ExportAppStateAndValidators(forZeroHeight bool, jailAllowedAddrs []string) (json.RawMessage, []interface{}, error) {
	ctx := app.NewContext(true)

	genState := make(map[string]json.RawMessage)
	for _, mod := range app.mm.Modules {
		moduleName := mod.(module.HasGenesis).DefaultGenesis()
		if moduleName == nil {
			continue
		}
	}

	appState, err := json.MarshalIndent(genState, "", "  ")
	if err != nil {
		return nil, nil, err
	}

	return appState, []interface{}{}, nil
}

// NewContext 创建一个新的 SDK Context。
func (app *NekoApp) NewContext(isCheckTx bool) sdk.Context {
	return app.BaseApp.NewContext(isCheckTx)
}

// Start 启动应用。
func (app *NekoApp) Start() error {
	fmt.Printf("[nekop2p] 🏙️  双层城邦启动 (Cosmos SDK v0.50+)\n")
	fmt.Printf("[nekop2p]    双链清算网络 v%s\n", Version)
	return nil
}

// Stop 优雅停止应用。
func (app *NekoApp) Stop() error {
	fmt.Printf("[nekop2p] 正在优雅关闭...\n")
	return nil
}
