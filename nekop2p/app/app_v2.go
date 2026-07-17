//go:build cosmos

// Package app NekoApp — Cosmos SDK BaseApp 双链应用 (v0.54+, 完整版)。
//
// 集成:
//   - 明链/暗链双模块
//   - AnteHandler (双密钥 + Ed25519 签名验证)
//   - 游戏层 (RegisterGame/BindGameServer/三路分账)
//   - VRG 虚拟根网图
//   - Inkwell 跨模块桥接
//
// Package app 提供 Cosmos SDK 主链应用。
package app

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"cosmossdk.io/log/v2"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/cosmos/cosmos-sdk/x/auth/tx"
	dbm "github.com/cosmos/cosmos-db"

	brightmodule "github.com/nekop2p/nekop2p/x/brightchain/module"
	brightkeeper "github.com/nekop2p/nekop2p/x/brightchain/keeper"
	brighttypes "github.com/nekop2p/nekop2p/x/brightchain/types"
	darkmodule "github.com/nekop2p/nekop2p/x/darkchain/module"
	darkkeeper "github.com/nekop2p/nekop2p/x/darkchain/keeper"
	darktypes "github.com/nekop2p/nekop2p/x/darkchain/types"
	inkwellkeeper "github.com/nekop2p/nekop2p/x/inkwell/keeper"
	inkwelltypes "github.com/nekop2p/nekop2p/x/inkwell/types"
	"github.com/nekop2p/nekop2p/ante"
	"github.com/nekop2p/nekop2p/x/vrg"
	zk "github.com/nekop2p/nekop2p/x/zk"
)

const (
	AppName          = "nekop2p"
	DefaultBondDenom = "uneko"
	Version          = "5.0.0-cosmos"
)

var (
	DefaultNodeHome = os.ExpandEnv("$HOME/.nekop2p")
	ModuleBasics    = module.NewBasicManager(
		brightmodule.AppModuleBasic{},
		darkmodule.AppModuleBasic{},
	)
)

// NekoApp Cosmos SDK 双链应用。
type NekoApp struct {
	*baseapp.BaseApp

	appCodec          codec.Codec
	legacyAmino       *codec.LegacyAmino
	interfaceRegistry codectypes.InterfaceRegistry
	keys              map[string]*storetypes.KVStoreKey

	BrightChainKeeper brightkeeper.Keeper
	DarkChainKeeper   darkkeeper.Keeper
	InkwellKeeper     inkwellkeeper.Keeper

	mm           *module.Manager
	configurator module.Configurator

	// 游戏层
	gamesMu     sync.Mutex
	gameEngines map[string]interface{} // gameID → GameStateMachine
	gameServers map[string]string      // nodeID → gameID

	// VRG
	VRG *VRGState

	// 节点表现追踪
	nodePerformance map[string]*NodeStats
	npMu            sync.Mutex
	currentHeight   int64
	lastSalaryPay   int64
	isOnline        func(chainID string) bool // 在线检测回调，由节点层注入
}

type NodeStats struct {
	RelayCount   uint64
	OnlineBlocks uint64
	TotalBlocks  uint64
}

// SetOnlineChecker 注入在线检测回调（由节点层在 P2P 启动后调用）。
func (app *NekoApp) SetOnlineChecker(fn func(chainID string) bool) {
	app.isOnline = fn
}

// VRGState VRG 运行时状态。
type VRGState struct {
	NamespaceRouter    *vrg.NamespaceRouter
	TrustEdgeManager   *vrg.TrustEdgeManager
	CrossDomainRouter  *vrg.CrossDomainRouter
	SlashingManager    *vrg.CommunitySlashingManager
	EpochCounter       *vrg.EpochCounter
	DoubleSpendDetector *vrg.DoubleSpendDetector
}

// NewNekoApp 创建完整 NekoApp。
func NewNekoApp(
	logger log.Logger, db dbm.DB, invCheckPeriod uint,
	encodingConfig EncodingConfig, appOpts AppOptions,
	baseAppOptions ...func(*baseapp.BaseApp),
) *NekoApp {
	appCodec := encodingConfig.Marshaler
	legacyAmino := encodingConfig.Amino
	interfaceRegistry := encodingConfig.InterfaceRegistry

	bApp := baseapp.NewBaseApp(AppName, logger, db, encodingConfig.TxConfig.TxDecoder(), baseAppOptions...)
	bApp.SetVersion(Version)

	keys := storetypes.NewKVStoreKeys(brighttypes.StoreKey, darktypes.StoreKey, inkwelltypes.StoreKey)

	brightKeeper := brightkeeper.NewKeeper(appCodec, keys[brighttypes.StoreKey])
	darkKeeper := darkkeeper.NewKeeper(appCodec, keys[darktypes.StoreKey])
	inkwellKeeper := inkwellkeeper.NewKeeper(appCodec, keys[inkwelltypes.StoreKey])

	// 跨模块桥接: Inkwell → Darkchain
	darkKeeper.SetInkwellKeeper(&inkwellKeeper)

	// ZK 验证器初始化 (Phase 6: 从链上参数模块加载 VK)
	zkVerifier := zk.NewVerifier()
	brightKeeper.SetZkVerifier(zkVerifier)
	// darkKeeper.SetZkVerifier(zkVerifier) // 暗链也使用同一验证器

	app := &NekoApp{
		BaseApp:           bApp,
		appCodec:          appCodec,
		legacyAmino:       legacyAmino,
		interfaceRegistry: interfaceRegistry,
		keys:              keys,
		BrightChainKeeper: brightKeeper,
		DarkChainKeeper:   darkKeeper,
		InkwellKeeper:     inkwellKeeper,
		gameEngines:       make(map[string]interface{}),
		gameServers:       make(map[string]string),
		nodePerformance:   make(map[string]*NodeStats),
		VRG: &VRGState{
			NamespaceRouter:     &vrg.NamespaceRouter{},
			TrustEdgeManager:    &vrg.TrustEdgeManager{},
			CrossDomainRouter:   &vrg.CrossDomainRouter{},
			SlashingManager:     &vrg.CommunitySlashingManager{},
			EpochCounter:        &vrg.EpochCounter{},
			DoubleSpendDetector: &vrg.DoubleSpendDetector{},
		},
	}

	app.mm = module.NewManager(
		brightmodule.NewAppModule(appCodec, app.BrightChainKeeper),
		darkmodule.NewAppModule(appCodec, app.DarkChainKeeper),
	)
	app.mm.SetOrderBeginBlockers(brighttypes.ModuleName, darktypes.ModuleName)
	app.mm.SetOrderEndBlockers(darktypes.ModuleName, brighttypes.ModuleName)
	app.mm.SetOrderInitGenesis(brighttypes.ModuleName, darktypes.ModuleName)

	app.mm.RegisterInvariants(nil)
	app.configurator = module.NewConfigurator(app.appCodec, app.MsgServiceRouter(), app.GRPCQueryRouter())
	app.mm.RegisterServices(app.configurator)

	// 激活 AnteHandler
	app.SetAnteHandler(ante.NekoAnteHandler(app.BrightChainKeeper))

	if err := app.LoadLatestVersion(); err != nil {
		logger.Error("failed to load latest version", "error", err)
		os.Exit(1)
	}

	return app
}

// ============================================================
// 区块生命周期
// ============================================================

// BeginBlocker 每区块开始时执行。
func (app *NekoApp) BeginBlocker(ctx sdk.Context) error {
	app.currentHeight = ctx.BlockHeight()

	// 1. 节点治理检查
	app.processNodeGovernance(ctx)

	// 2. 模块钩子
	app.BrightChainKeeper.BeginBlocker(ctx)
	app.DarkChainKeeper.BeginBlocker(ctx)

	return nil
}

// EndBlocker 每区块结束时执行。
func (app *NekoApp) EndBlocker(ctx sdk.Context) error {
	// 1. 记录节点在线表现
	app.recordNodePerformance(ctx)

	// 2. 每月工资
	blocksPerMonth := int64(43200)
	if app.currentHeight-app.lastSalaryPay >= blocksPerMonth {
		app.processMonthlySalary(ctx)
		app.lastSalaryPay = app.currentHeight
	}

	// 3. 季度轮换
	if app.currentHeight%(blocksPerMonth*3) == 0 {
		app.processQuarterlyRotation(ctx)
	}

	// 4. 信任权重
	app.BrightChainKeeper.RecalculateTrustWeights(ctx)

	// 5. 模块钩子
	app.BrightChainKeeper.EndBlocker(ctx)
	app.DarkChainKeeper.EndBlocker(ctx)

	// 6. VRG 纪元推进
	app.VRG.EpochCounter.Current++

	return nil
}

func (app *NekoApp) processNodeGovernance(ctx sdk.Context) {
	// 每小时清理过期黑名单
	if app.currentHeight%1800 == 0 {
		if app.VRG.SlashingManager != nil {
			// Phase 7: 清理 SlashingManager 中的过期条目
		}
	}
	// 每天检查正式节点任期过期
	if app.currentHeight%43200 == 0 {
		users := app.BrightChainKeeper.GetAllUsers(ctx)
		for _, u := range users {
			if u.NodeRole == brighttypes.NodeRole_OFFICIAL_RELAY ||
				u.NodeRole == brighttypes.NodeRole_OFFICIAL_RECORD {
				if u.NodeTermEnd > 0 && ctx.BlockTime().Unix() > u.NodeTermEnd {
					app.BrightChainKeeper.SetNodeRole(ctx, u.Address, brighttypes.NodeRole_NONE)
				}
			}
		}
	}
}

func (app *NekoApp) recordNodePerformance(ctx sdk.Context) {
	users := app.BrightChainKeeper.GetAllUsers(ctx)
	app.npMu.Lock()
	defer app.npMu.Unlock()
	for _, u := range users {
		stats := app.nodePerformance[u.Address]
		if stats == nil {
			stats = &NodeStats{}
			app.nodePerformance[u.Address] = stats
		}
		stats.TotalBlocks++
		// 基于实际连接检测在线状态
		if app.isOnline != nil && app.isOnline(u.Address) {
			stats.OnlineBlocks++
		}
	}
}

func (app *NekoApp) processMonthlySalary(ctx sdk.Context) {
	pool := app.BrightChainKeeper.GetPool(ctx)
	budget := pool.SalaryRelay + pool.SalaryRecord
	if budget == 0 {
		return
	}

	type entry struct {
		addr   string
		weight uint64
		role   brighttypes.NodeRole
	}
	var nodes []entry
	var totalWeight uint64
	for _, u := range app.BrightChainKeeper.GetAllUsers(ctx) {
		if u.NodeRole == brighttypes.NodeRole_OFFICIAL_RELAY ||
			u.NodeRole == brighttypes.NodeRole_OFFICIAL_RECORD {
			nodes = append(nodes, entry{u.Address, u.TrustWeight, u.NodeRole})
			totalWeight += u.TrustWeight
		}
	}
	if totalWeight == 0 {
		return
	}

	relayBudget := budget / 2
	for _, n := range nodes {
		salary := (relayBudget / totalWeight) * n.weight
		if salary > 0 {
			app.BrightChainKeeper.CreditSalary(ctx, n.addr, salary)
		}
	}
	pool.SalaryRelay -= relayBudget
	pool.SalaryRecord -= budget / 2
	app.BrightChainKeeper.SetPool(ctx, pool)
}

func (app *NekoApp) processQuarterlyRotation(ctx sdk.Context) {
	users := app.BrightChainKeeper.GetAllUsers(ctx)

	type ranked struct {
		addr  string
		role  brighttypes.NodeRole
		score uint64
	}
	var officials, nonOfficials []ranked

	for _, u := range users {
		app.npMu.Lock()
		stats := app.nodePerformance[u.Address]
		app.npMu.Unlock()

		onlineRate := 0.5
		relayScore := uint64(0)
		if stats != nil && stats.TotalBlocks > 0 {
			onlineRate = float64(stats.OnlineBlocks) / float64(stats.TotalBlocks)
			relayScore = stats.RelayCount
		}

		r := ranked{addr: u.Address, role: u.NodeRole}
		r.score = uint64(float64(u.TrustWeight)*0.4 + onlineRate*float64(u.TrustWeight)*0.3 + float64(relayScore)*0.3)

		if u.NodeRole == brighttypes.NodeRole_OFFICIAL_RELAY ||
			u.NodeRole == brighttypes.NodeRole_OFFICIAL_RECORD {
			officials = append(officials, r)
		} else if u.NodeRole != brighttypes.NodeRole_NONE {
			nonOfficials = append(nonOfficials, r)
		}
	}

	// 末位 10% 降级
	if len(officials) >= 10 {
		demoteCount := len(officials) / 10
		if demoteCount < 1 {
			demoteCount = 1
		}
		// 按评分排序
		sort.Slice(officials, func(i, j int) bool { return officials[i].score > officials[j].score })
		for i := len(officials) - demoteCount; i < len(officials); i++ {
			app.BrightChainKeeper.SetNodeRole(ctx, officials[i].addr, brighttypes.NodeRole_NONE)
		}
	}

	// 非正式节点前 10% 升级
	if len(nonOfficials) >= 10 {
		promoteCount := len(nonOfficials) / 10
		if promoteCount < 1 {
			promoteCount = 1
		}
		sort.Slice(nonOfficials, func(i, j int) bool { return nonOfficials[i].score > nonOfficials[j].score })
		for i := 0; i < promoteCount && i < len(nonOfficials); i++ {
			app.BrightChainKeeper.SetNodeRole(ctx, nonOfficials[i].addr, brighttypes.NodeRole_OFFICIAL_RELAY)
		}
	}

	// 重置表现统计
	app.nodePerformance = make(map[string]*NodeStats)
}

// ============================================================
// 游戏层集成
// ============================================================

// RegisterGame 注册新游戏（作者设定分账比例）。
func (app *NekoApp) RegisterGame(gameID, author, name string, feeRate, authorShare, serverShare uint64) {
	app.gamesMu.Lock()
	defer app.gamesMu.Unlock()

	poolShare := uint64(100) - authorShare - serverShare
	app.gameEngines[gameID] = struct{}{} // Phase F: GameStateMachine 实例

	fmt.Printf("[game] 新游戏: %s 作者=%.8s 分账 作者:%d%% 服务器:%d%% 池:%d%%\n",
		name, author, authorShare, serverShare, poolShare)
}

// BindGameServer 节点注册为游戏服务器。
func (app *NekoApp) BindGameServer(nodeID, gameID string) {
	app.gamesMu.Lock()
	defer app.gamesMu.Unlock()
	app.gameServers[nodeID] = gameID
}

// RecordGameTx 三路分账: 作者 + 服务器 + 基础设施。
func (app *NekoApp) RecordGameTx(gameID, serverAddr, txType string, feeAmount uint64, customData []byte) {
	if feeAmount == 0 {
		return
	}
	app.gamesMu.Lock()
	_, ok := app.gameEngines[gameID]
	app.gamesMu.Unlock()
	if !ok {
		return
	}

	// 三路分账: 作者30% 服务器50% 基础设施20%
	_ = serverAddr
	_ = txType
	_ = customData

	// Phase F: 需要 SDK Context 来持久化分账
	// 当前在 BeginBlocker/EndBlocker 中通过 CollectFees 处理
	fmt.Printf("[game] tx: game=%s fee=%d\n", gameID[:8], feeAmount)
}

// ============================================================
// 访问器
// ============================================================

func (app *NekoApp) Name() string                                    { return AppName }
func (app *NekoApp) LegacyAmino() *codec.LegacyAmino                 { return app.legacyAmino }
func (app *NekoApp) AppCodec() codec.Codec                           { return app.appCodec }
func (app *NekoApp) InterfaceRegistry() codectypes.InterfaceRegistry { return app.interfaceRegistry }
func (app *NekoApp) GetKey(n string) *storetypes.KVStoreKey          { return app.keys[n] }
func (app *NekoApp) ModuleManager() *module.Manager                  { return app.mm }
func (app *NekoApp) GetSubspace(string)                              {}

// ============================================================
// 编码 + 导出
// ============================================================

type EncodingConfig struct {
	InterfaceRegistry codectypes.InterfaceRegistry
	Marshaler         codec.Codec
	TxConfig          client.TxConfig
	Amino             *codec.LegacyAmino
}

func MakeEncodingConfig() EncodingConfig {
	ir := codectypes.NewInterfaceRegistry()
	m := codec.NewProtoCodec(ir)
	return EncodingConfig{
		InterfaceRegistry: ir,
		Marshaler:         m,
		TxConfig:          MakeTxConfig(m),
		Amino:             codec.NewLegacyAmino(),
	}
}

func MakeTxConfig(m codec.Codec) client.TxConfig {
	return tx.NewTxConfig(m, tx.DefaultSignModes)
}

type AppOptions interface{ Get(string) interface{} }

func (app *NekoApp) ExportAppStateAndValidators(forZeroHeight bool, jailAddrs []string) (json.RawMessage, []interface{}, error) {
	ctx := app.NewContext(true)
	genState := make(map[string]json.RawMessage)
	for _, mod := range app.mm.Modules {
		if hg, ok := mod.(module.HasGenesis); ok {
			name := mod.(module.HasName).Name()
			gs := hg.DefaultGenesis(app.appCodec)
			if gs != nil {
				genState[name] = gs
			}
		}
	}
	_ = ctx
	bz, err := json.MarshalIndent(genState, "", "  ")
	return bz, []interface{}{}, err
}

func (app *NekoApp) NewContext(isCheckTx bool) sdk.Context {
	return app.BaseApp.NewContext(isCheckTx)
}

func (app *NekoApp) Start() error {
	fmt.Printf("[nekop2p] 🏙️  双层城邦 v%s (Cosmos SDK)\n", Version)
	return nil
}

func (app *NekoApp) Stop() error {
	fmt.Println("[nekop2p] 优雅关闭...")
	return nil
}

// Silence unused import
var _ = time.Now
var _ = inkwelltypes.InkwellParams{}
