//go:build !cosmos

// Package app 提供 NekoApp — 主链应用。
//
// NekoApp 组合明链和暗链模块，使用 Cosmos SDK 注册它们，
// 并实现 ABCI 接口。
//
// 在生产环境中，此文件与以下模块集成：
//   github.com/cosmos/cosmos-sdk/types
//   github.com/cosmos/cosmos-sdk/baseapp
//   github.com/tendermint/tendermint/abci/types
package app

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/nekop2p/nekop2p/app/game/state"
	"github.com/nekop2p/nekop2p/store"
	"github.com/nekop2p/nekop2p/x/brightchain"
	brightkeeper "github.com/nekop2p/nekop2p/x/brightchain/keeper"
	brighttypes "github.com/nekop2p/nekop2p/x/brightchain/types"
	darkchain "github.com/nekop2p/nekop2p/x/darkchain"
	darkkeeper "github.com/nekop2p/nekop2p/x/darkchain/keeper"
	darktypes "github.com/nekop2p/nekop2p/x/darkchain/types"
	"github.com/nekop2p/nekop2p/x/node"
)

// NekoApp 是主链应用。
type NekoApp struct {
	store *store.ChainStore
	mempool *MemPool

	BrightChainKeeper *brightkeeper.Keeper
	DarkChainKeeper   *darkkeeper.Keeper

	BrightChainModule brightchain.AppModule
	DarkChainModule   darkchain.AppModule

	NodeGovernor *node.NodeGovernor // 节点治理（黑名单/罚没/任期）
	VRG          *VRGState          // 虚拟根网图运行时状态

	currentHeight int64
	lastSalaryPay int64
	nodePerformance map[string]*NodeStats
	npMu            sync.Mutex // 保护 nodePerformance 并发访问
	txHistory      []*Tx     // 最近交易记录 (最多100条)
	txMu           sync.Mutex
	gameRegistry map[string]*brighttypes.GameInfo // 游戏注册表
	gameEngines  map[string]*state.GameStateMachine // 游戏状态机引擎
	gamesMu      sync.Mutex
	gameServers  map[string]string // node_id → game_id 绑定
	isOnline     func(chainID string) bool // 在线检测回调（由节点层注入）
	onBlockCommitted func(height int64, hash [32]byte) // 区块提交回调
	// 区域节点批量提交回调
	onCommitBatches func(height int64) []interface{} // → []*region.RegionBatch
}

type NodeStats struct {
	RelayCount   uint64
	OnlineBlocks uint64
	TotalBlocks  uint64
}

// NewNekoApp 创建一个新的 NekoApp。
func NewNekoApp(dataDir string) (*NekoApp, error) {
	s, err := store.New(dataDir)
	if err != nil {
		return nil, err
	}
	bk := brightkeeper.NewKeeper(s, brighttypes.StoreKey)
	dk := darkkeeper.NewKeeper(s)

	app := &NekoApp{
		store:              s,
		mempool:            NewMemPool(),
		BrightChainKeeper:  bk,
		DarkChainKeeper:    dk,
		BrightChainModule:  brightchain.NewAppModule(bk),
		DarkChainModule:    darkchain.NewAppModule(dk),
		NodeGovernor:    node.NewNodeGovernor(),
		nodePerformance:  make(map[string]*NodeStats),
		gameRegistry:     make(map[string]*brighttypes.GameInfo),
		gameEngines:      make(map[string]*state.GameStateMachine),
		gameServers:      make(map[string]string),
	}

	// 从持久化恢复区块高度
	if h := bk.Height(); h > 0 {
		app.currentHeight = h
	}

	return app, nil
}

// BeginBlocker 在每个区块开始时被调用。
func (app *NekoApp) BeginBlocker(ctx brighttypes.Context) {
	app.currentHeight++

	// 0. 节点治理：检查过期任期和黑名单清理
	app.processNodeGovernance(ctx)

	// 1. 检查逾期担保金 → 处理违约
	app.checkOverdueBonds(ctx)

	// 2. 暗链：检查逾期贷款 + 推进周期
	app.DarkChainModule.BeginBlock()
}

// processNodeGovernance 处理节点治理任务。
func (app *NekoApp) processNodeGovernance(ctx brighttypes.Context) {
	// 清理过期黑名单条目（每小时一次，即 ~1800 个区块）
	if app.currentHeight%1800 == 0 {
		cleaned := app.NodeGovernor.Blacklist.CleanExpired()
		if cleaned > 0 {
			log.Printf("[governance] cleaned %d expired blacklist entries", cleaned)
		}
	}

	// 检查任期过期（每天一次，即 ~43200 个区块）
	if app.currentHeight%43200 == 0 {
		expired := app.NodeGovernor.TermManager.ExpiredTerms()
		for _, term := range expired {
			// 任期过期的正式节点降级
			app.BrightChainKeeper.SetNodeRole(ctx, term.NodeID, brighttypes.NodeRole_NONE)
			app.NodeGovernor.TermManager.EndTerm(term.NodeID)
			log.Printf("[governance] node %s term expired (term %d), demoted to NONE",
				node.FormatNodeID(term.NodeID), term.TermNumber)
		}
	}
}

// EndBlocker 在每个区块结束时被调用。
func (app *NekoApp) EndBlocker(ctx brighttypes.Context) {
	// 1. 记录节点在线表现
	app.recordNodePerformance()

	// 2. 每月支付节点工资
	blocksPerMonth := int64(43200)
	if app.currentHeight-app.lastSalaryPay >= blocksPerMonth {
		app.processMonthlySalary(ctx)
		app.lastSalaryPay = app.currentHeight
	}

	// 2.5 节点治理：罚没检查（每月）
	app.processSlashing(ctx)

	// 3. 每季度处理节点轮换/淘汰
	blocksPerQuarter := blocksPerMonth * 3
	if app.currentHeight%blocksPerQuarter == 0 {
		app.processQuarterlyRotation(ctx)
	}

	// 4. 重新计算信任权重
	app.recalculateTrustWeights(ctx)

	// 5. 持久化区块高度（每区块持久化，防止崩溃丢失）
	app.BrightChainKeeper.SetHeight(app.currentHeight)
	app.DarkChainKeeper.SetHeight(app.currentHeight)

	// 6. VRG 纪元推进（每个区块）
	app.EpochTick(app.currentHeight)

	// 7. 区域节点批量提交到全局链 (记录层)
	if app.onCommitBatches != nil && app.currentHeight%100 == 0 {
		batches := app.onCommitBatches(app.currentHeight)
		if len(batches) > 0 {
			log.Printf("[chain] 全局链记录: %d 个区域批量提交 (height=%d)", len(batches), app.currentHeight)
		}
	}
}

// ===== 区块钩子实现 =====

func (app *NekoApp) checkOverdueBonds(ctx brighttypes.Context) {
	now := time.Now().Unix()
	bonds := app.BrightChainKeeper.ListBonds(ctx)
	for _, bond := range bonds {
		if bond.Status == brighttypes.BondStatus_ACTIVE && bond.UnlockAt > 0 && bond.UnlockAt < now {
			app.BrightChainKeeper.ForfeitBond(ctx, bond.BondId)
		}
	}
}

func (app *NekoApp) processMonthlySalary(ctx brighttypes.Context) {
	pool := app.BrightChainKeeper.GetPool(ctx)
	salaryBudget := pool.SalaryRelay + pool.SalaryRecord
	if salaryBudget == 0 {
		return
	}

	// 获取所有正式节点
	users := app.BrightChainKeeper.GetAllUsers(ctx)
	type nodeEntry struct {
		chainID     string
		trustWeight uint64
		role        brighttypes.NodeRole
	}
	var officialNodes []nodeEntry
	var totalWeight uint64
	for _, block := range users {
		if block.NodeRole == brighttypes.NodeRole_OFFICIAL_RELAY ||
			block.NodeRole == brighttypes.NodeRole_OFFICIAL_RECORD {
			officialNodes = append(officialNodes, nodeEntry{
				chainID:     block.Address,
				trustWeight: block.TrustWeight,
				role:        block.NodeRole,
			})
			totalWeight += block.TrustWeight
		}
	}
	if len(officialNodes) == 0 || totalWeight == 0 {
		return
	}

	// 按信任权重比例分配工资
	relayBudget := salaryBudget / 2  // 50% 给中继节点
	recordBudget := salaryBudget / 2 // 50% 给记录节点

	var relayTotal, recordTotal uint64
	for _, node := range officialNodes {
		if node.role == brighttypes.NodeRole_OFFICIAL_RELAY {
			relayTotal += node.trustWeight
		} else {
			recordTotal += node.trustWeight
		}
	}

	for _, node := range officialNodes {
		var salary uint64
		if node.role == brighttypes.NodeRole_OFFICIAL_RELAY && relayTotal > 0 {
			// 先除后乘防 uint64 溢出
			salary = (relayBudget / relayTotal) * node.trustWeight
		} else if recordTotal > 0 {
			salary = (recordBudget / recordTotal) * node.trustWeight
		}
		if salary > 0 {
			app.BrightChainKeeper.CreditSalary(ctx, node.chainID, salary)
		}
	}

	// 消耗预算
	pool.SalaryRelay -= relayBudget
	pool.SalaryRecord -= recordBudget
}

func (app *NekoApp) processQuarterlyRotation(ctx brighttypes.Context) {
	users := app.BrightChainKeeper.GetAllUsers(ctx)

	type ranked struct {
		chainID string
		role    brighttypes.NodeRole
		score   uint64 // 综合评分
	}
	var officials []ranked
	var nonOfficials []ranked

	for _, block := range users {
		app.npMu.Lock()
		stats := app.nodePerformance[block.Address]
		app.npMu.Unlock()
		r := ranked{chainID: block.Address, role: block.NodeRole}

		// 综合评分 = 信任权重 × 0.4 + 在线率 × 0.3 + 转发量 × 0.3
		onlineRate := float64(0.5)
		if stats != nil && stats.TotalBlocks > 0 {
			onlineRate = float64(stats.OnlineBlocks) / float64(stats.TotalBlocks)
		}
		relayScore := uint64(0)
		if stats != nil {
			relayScore = stats.RelayCount
		}
		r.score = uint64(float64(block.TrustWeight)*0.4 +
			onlineRate*float64(block.TrustWeight)*0.3 +
			float64(relayScore)*0.3)

		if block.NodeRole == brighttypes.NodeRole_OFFICIAL_RELAY ||
			block.NodeRole == brighttypes.NodeRole_OFFICIAL_RECORD {
			officials = append(officials, r)
		} else {
			nonOfficials = append(nonOfficials, r)
		}
	}

	// 排名：按评分降序
	sort.Slice(officials, func(i, j int) bool { return officials[i].score > officials[j].score })
	sort.Slice(nonOfficials, func(i, j int) bool { return nonOfficials[i].score > nonOfficials[j].score })

	// 末位 10% 降级
	var demoteCount int
	if len(officials) >= 10 {
		demoteCount = len(officials) / 10
		if demoteCount < 1 {
			demoteCount = 1
		}
		for i := len(officials) - demoteCount; i < len(officials); i++ {
			app.BrightChainKeeper.SetNodeRole(ctx, officials[i].chainID, brighttypes.NodeRole_NONE)
			// 结束任期
			app.NodeGovernor.TermManager.EndTerm(officials[i].chainID)
		}
	}

	// 非正式节点前 10% 升级
	if len(nonOfficials) >= 10 {
		promoteCount := len(nonOfficials) / 10
		if promoteCount < 1 {
			promoteCount = 1
		}
		for i := 0; i < promoteCount && i < len(nonOfficials); i++ {
			nodeID := nonOfficials[i].chainID
			// 检查黑名单：被封禁节点不能升级
			if app.IsNodeBanned(nodeID) {
				continue
			}
			// 检查是否可以连任
			if !app.NodeGovernor.TermManager.CanRenew(nodeID) {
				log.Printf("[governance] node %s cannot renew (max consecutive terms reached)", node.FormatNodeID(nodeID))
				continue
			}
			app.BrightChainKeeper.SetNodeRole(ctx, nodeID, brighttypes.NodeRole_OFFICIAL_RELAY)
			app.NodeGovernor.TermManager.StartTerm(nodeID, "relay")
			log.Printf("[governance] promoted node %s to OFFICIAL_RELAY (term %d)",
				node.FormatNodeID(nodeID),
				app.NodeGovernor.TermManager.GetCurrentTerm(nodeID).TermNumber)
		}
	}

	// 重置表现统计
	app.nodePerformance = make(map[string]*NodeStats)
}

func (app *NekoApp) recalculateTrustWeights(ctx brighttypes.Context) {
	app.BrightChainKeeper.RecalculateTrustWeights(ctx)
}

// ===== 节点表现追踪 =====

func (app *NekoApp) recordNodePerformance() {
	users := app.BrightChainKeeper.GetAllUsers(struct{}{})
	app.npMu.Lock()
	defer app.npMu.Unlock()
	for _, block := range users {
		if block.NodeRole == brighttypes.NodeRole_NONE {
			continue
		}
		stats, ok := app.nodePerformance[block.Address]
		if !ok {
			stats = &NodeStats{}
			app.nodePerformance[block.Address] = stats
		}
		stats.TotalBlocks++
		if app.isOnline != nil && !app.isOnline(block.Address) {
			continue
		}
		stats.OnlineBlocks++
	}
}

// RecordRelay 记录一个节点的转发次数。
func (app *NekoApp) RecordRelay(chainID string, count uint64) {
	app.npMu.Lock()
	defer app.npMu.Unlock()
	stats, ok := app.nodePerformance[chainID]
	if !ok {
		stats = &NodeStats{}
		app.nodePerformance[chainID] = stats
	}
	stats.RelayCount += count
}

// processSlashing 处理节点罚没检查（每月执行一次）。
func (app *NekoApp) processSlashing(ctx brighttypes.Context) {
	users := app.BrightChainKeeper.GetAllUsers(ctx)
	for _, block := range users {
		if block.NodeRole != brighttypes.NodeRole_OFFICIAL_RELAY &&
			block.NodeRole != brighttypes.NodeRole_OFFICIAL_RECORD {
			continue
		}

		// 计算节点评分
		app.npMu.Lock()
		stats := app.nodePerformance[block.Address]
		app.npMu.Unlock()

		onlineRate := float64(0.5)
		relayScore := uint64(0)
		if stats != nil && stats.TotalBlocks > 0 {
			onlineRate = float64(stats.OnlineBlocks) / float64(stats.TotalBlocks)
			relayScore = stats.RelayCount
		}
		score := uint64(float64(block.TrustWeight)*0.4 +
			onlineRate*float64(block.TrustWeight)*0.3 +
			float64(relayScore)*0.3)

		// 检查是否应罚没
		shouldSlash, slashAmount := app.NodeGovernor.Slashing.RecordPerformance(
			block.Address, score, block.CreditLimit,
		)

		if shouldSlash && slashAmount > 0 {
			// 执行罚没：减少 CreditLimit
			if block.CreditLimit >= slashAmount {
				block.CreditLimit -= slashAmount
			} else {
				block.CreditLimit = 0
			}
			block.TrustWeight = block.TrustWeight * 9 / 10 // 罚没伴随信任降低
			app.BrightChainKeeper.UpdateUserBlock(block)

			// 将罚没金额转入坏账准备金
			pool := app.BrightChainKeeper.GetPool(ctx)
			pool.BadDebtReserve += slashAmount
			pool.TotalBalance += slashAmount
			app.BrightChainKeeper.SetPool(pool)

			log.Printf("[governance] slashed node %s: -%d credit (score=%d, consecutive=%d)",
				node.FormatNodeID(block.Address), slashAmount, score,
				app.NodeGovernor.Slashing.GetState(block.Address).ConsecutiveLowPerfCount)
		}
	}
}

// IsNodeBanned 检查节点是否在黑名单中。
func (app *NekoApp) IsNodeBanned(chainID string) bool {
	return app.NodeGovernor.Blacklist.IsBanned(chainID)
}

// BanNode 将节点加入黑名单。
func (app *NekoApp) BanNode(chainID, reason, bannedBy string, duration time.Duration) {
	app.NodeGovernor.Blacklist.Ban(chainID, reason, bannedBy, duration)
	// 同时撤销正式节点角色
	app.BrightChainKeeper.SetNodeRole(nil, chainID, brighttypes.NodeRole_NONE)
	log.Printf("[governance] banned node %s: %s (duration=%v)", node.FormatNodeID(chainID), reason, duration)
}

// RecordGameTx 三路分账: 作者所有权 + 服务器运营 + 基础设施
// txType 为 "game_tx" 时，customData 会转发给 GameStateMachine.Apply()
func (app *NekoApp) RecordGameTx(gameID, serverAddr, txType string, feeAmount uint64, customData []byte) {
	app.gamesMu.Lock()
	game, ok := app.gameRegistry[gameID]
	engine := app.gameEngines[gameID]
	app.gamesMu.Unlock()
	if !ok || feeAmount == 0 { return }

	// 向游戏状态机转发自定义数据
	if txType == "game_tx" && engine != nil && len(customData) > 0 {
		var action state.Action
		if err := json.Unmarshal(customData, &action); err == nil {
			engine.Apply(action)
		}
	}

	// 安全计算分账 (防止溢出)
	authorFee := feeAmount * game.AuthorShare / 100
	serverFee := feeAmount * game.ServerShare / 100
	poolFee := feeAmount - authorFee - serverFee

	pool := app.BrightChainKeeper.GetPool(nil)
	pool.GameFees += poolFee
	pool.TotalBalance += feeAmount
	app.BrightChainKeeper.SetPool(pool) // 持久化资金池

	// 服务器收益 (持久化)
	id := parseChainIDStr(serverAddr)
	block := app.BrightChainKeeper.GetUserBlock(nil, id)
	if block != nil {
		block.GameEarnings += serverFee
		app.BrightChainKeeper.UpdateUserBlock(block)
	}

	// 作者收益 (持久化)
	authorID := parseChainIDStr(game.AuthorID)
	authorBlock := app.BrightChainKeeper.GetUserBlock(nil, authorID)
	if authorBlock != nil && authorID != id { // 避免重复更新(作者=服务器时)
		authorBlock.GameEarnings += authorFee
		app.BrightChainKeeper.UpdateUserBlock(authorBlock)
	}

	app.gamesMu.Lock()
	game.TotalTxs++
	game.TotalFees += feeAmount
	app.gamesMu.Unlock()
}

// RegisterGame 注册新游戏 (作者设定分账比例)
func (app *NekoApp) RegisterGame(gameID, author, name string, feeRate, authorShare, serverShare uint64) {
	app.gamesMu.Lock()
	defer app.gamesMu.Unlock()
	if _, exists := app.gameRegistry[gameID]; exists { return }
	if authorShare+serverShare > 100 {
		authorShare = 30; serverShare = 50 // 默认: 作者30% 服务器50% 池20%
	}
	app.gameRegistry[gameID] = &brighttypes.GameInfo{
		GameID: gameID, AuthorID: author, Name: name,
		FeeRate: feeRate, AuthorShare: authorShare, ServerShare: serverShare,
		PoolShare: 100 - authorShare - serverShare,
		Status: 0, CreatedAt: time.Now().Unix(),
	}

	if _, exists := app.gameEngines[gameID]; !exists {
		app.gameEngines[gameID] = state.NewGameStateMachine(gameID, nil)
	}

	log.Printf("[game] 新游戏: %s 作者=%x 作者:%d%% 服务器:%d%% 池:%d%%",
		name, author[:8], authorShare, serverShare, 100-authorShare-serverShare)
}

// BindGameServer 节点注册为游戏服务器
func (app *NekoApp) BindGameServer(nodeID, gameID string) {
	app.gamesMu.Lock()
	defer app.gamesMu.Unlock()
	if _, exists := app.gameRegistry[gameID]; !exists { return } // 游戏不存在
	app.gameServers[nodeID] = gameID
	// 持久化节点角色升级
	id := parseChainIDStr(nodeID)
	block := app.BrightChainKeeper.GetUserBlock(nil, id)
	if block != nil && block.NodeRole == brighttypes.NodeRole_NONE {
		block.NodeRole = brighttypes.NodeRole_GAME_SERVER
		app.BrightChainKeeper.UpdateUserBlock(block)
	}
	log.Printf("[game] 服务器绑定: %x → 游戏 %s", nodeID[:8], gameID)
}

// GetGameEngine 返回指定游戏的 GameStateMachine 实例。
func (app *NekoApp) GetGameEngine(gameID string) *state.GameStateMachine {
	app.gamesMu.Lock()
	defer app.gamesMu.Unlock()
	return app.gameEngines[gameID]
}

// GetGames 返回所有已注册游戏
func (app *NekoApp) GetGames() []*brighttypes.GameInfo {
	app.gamesMu.Lock()
	defer app.gamesMu.Unlock()
	result := make([]*brighttypes.GameInfo, 0, len(app.gameRegistry))
	for _, g := range app.gameRegistry {
		result = append(result, g)
	}
	return result
}

func parseChainIDStr(addr string) brighttypes.ChainID {
	var id brighttypes.ChainID
	if len(addr) == 64 {
		if decoded, err := hex.DecodeString(addr); err == nil && len(decoded) == 32 {
			copy(id[:], decoded)
			return id
		}
	}
	// 回退：原始字节拷贝
	copy(id[:], []byte(addr))
	return id
}

// ===== 公开方法 =====

func (app *NekoApp) Name() string { return "nekop2p" }

// Height 返回当前区块高度。
func (app *NekoApp) Height() int64 { return app.currentHeight }

// SetOnlineChecker 注入在线检测回调（由节点层在 p2p 启动后调用）。
func (app *NekoApp) SetOnlineChecker(fn func(chainID string) bool) {
	app.isOnline = fn
}

// Shutdown 优雅关闭应用，持久化最终状态。
func (app *NekoApp) Shutdown() {
	app.BrightChainKeeper.SetHeight(app.currentHeight)
	app.DarkChainKeeper.SetHeight(app.currentHeight)
	app.store.Close()
}

// ===== 交易历史 =====

// RecordTx 记录一笔交易到历史。
func (app *NekoApp) RecordTx(tx *Tx) {
	app.txMu.Lock()
	defer app.txMu.Unlock()
	app.txHistory = append(app.txHistory, tx)
	if len(app.txHistory) > 100 {
		app.txHistory = app.txHistory[len(app.txHistory)-100:]
	}
}

// ConfirmTxs 将指定 ID 的交易标记为已确认。
func (app *NekoApp) ConfirmTxs(txIDs []string, blockNum int64) {
	app.txMu.Lock()
	defer app.txMu.Unlock()
	for _, tx := range app.txHistory {
		if tx.Status == "pending" {
			for _, id := range txIDs {
				if tx.ID == id {
					tx.Status = "confirmed"
					tx.BlockNum = blockNum
					break
				}
			}
		}
	}
}

// GetRecentTxs 返回最近交易状态列表。
func (app *NekoApp) GetRecentTxs() []TxStatus {
	app.txMu.Lock()
	defer app.txMu.Unlock()
	result := make([]TxStatus, 0, len(app.txHistory))
	for i := len(app.txHistory) - 1; i >= 0; i-- {
		tx := app.txHistory[i]
		dataHex := ""
		if len(tx.Data) > 0 {
			dataHex = fmt.Sprintf("%x", tx.Data)
			if len(dataHex) > 64 {
				dataHex = dataHex[:64] + "..."
			}
		}
		result = append(result, TxStatus{
			ID:       tx.ID[:12],
			Type:     tx.Type,
			Status:   tx.Status,
			BlockNum: tx.BlockNum,
			Time:     tx.CreatedAt,
			Data:     dataHex,
		})
	}
	return result
}

// PendingCount 返回待处理交易数。
func (app *NekoApp) PendingCount() int {
	return app.mempool.Size()
}

var _ = brighttypes.ModuleName
var _ = darktypes.ModuleName
