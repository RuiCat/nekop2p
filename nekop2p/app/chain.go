//go:build !cosmos

package app

import (
	"crypto/sha256"
	"crypto/ed25519"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nekop2p/nekop2p/consensus"
	brighttypes "github.com/nekop2p/nekop2p/x/brightchain/types"
	darktypes "github.com/nekop2p/nekop2p/x/darkchain/types"
)

// ChainConfig 链运行配置。
type ChainConfig struct {
	BlockInterval time.Duration // 出块间隔（默认 2s）
	IsValidator   bool          // 是否为验证者
}

// DefaultChainConfig 返回默认链配置。
func DefaultChainConfig() ChainConfig {
	return ChainConfig{
		BlockInterval: 2 * time.Second,
		IsValidator:   true,
	}
}

// RunChain 启动链运行循环。
// 阻塞直到 ctx 被取消。
func (app *NekoApp) RunChain(cfg ChainConfig) {
	engine := consensus.NewSimpleEngine(cfg.BlockInterval, cfg.IsValidator)

	// 从持久化恢复区块高度
	savedHeight := app.BrightChainKeeper.Height()
	if savedHeight > 0 {
		log.Printf("[chain] 从持久化恢复高度: %d", savedHeight)
		app.currentHeight = savedHeight
	}

	if err := engine.Start(); err != nil {
		log.Printf("[chain] 共识引擎启动失败: %v", err)
		return
	}
	defer engine.Stop()

	blockCh := engine.SubscribeBlocks()
	log.Printf("[chain] 链运行中，出块间隔: %s", cfg.BlockInterval)

	for event := range blockCh {
		app.processBlock(event)
	}
}

// processBlock 处理一个区块。
func (app *NekoApp) processBlock(event consensus.BlockEvent) {
	startTime := time.Now()
	ctx := struct{}{}

	// 1. BeginBlock
	app.BeginBlocker(ctx)

	// 2. 从 MemPool 取出交易并执行
	txs := app.mempool.Drain(100)
	txIDs := make([]string, len(txs))
	for i, tx := range txs {
		app.executeTx(tx)
		txIDs[i] = tx.ID
	}
	// 标记已执行的交易为已确认
	if len(txIDs) > 0 {
		app.ConfirmTxs(txIDs, app.currentHeight)
	}

	// 3. EndBlock
	app.EndBlocker(ctx)

	elapsed := time.Since(startTime)
	if app.currentHeight <= 5 || app.currentHeight%100 == 0 || elapsed > 100*time.Millisecond {
		log.Printf("[chain] 区块 #%d | 交易: %d | 耗时: %v | 用户: %d | 贷款: %d",
			app.currentHeight, len(txs), elapsed,
			len(app.BrightChainKeeper.GetAllUsers(ctx)),
			len(app.DarkChainKeeper.GetAllLoans()))
	}
}

// executeTx 执行一笔交易（含重放防护）。
func (app *NekoApp) executeTx(tx *Tx) {
	// === 重放防护层 ===

	// 0. 交易去重检查（基于交易 ID）
	if app.isTxReplay(tx.ID) {
		log.Printf("[chain] replay rejected: tx %s already executed", tx.ID[:12])
		return
	}

	// 1. 黑名单检查
	if tx.SenderID != "" && app.IsNodeBanned(tx.SenderID) {
		log.Printf("[chain] rejected tx from banned node: %s", tx.SenderID[:16])
		return
	}

	// 2. 签名验证（如果交易携带签名）
	if tx.SenderID != "" && len(tx.Signature) > 0 && len(tx.SignBytes) > 0 {
		chainID := parseChainIDStr(tx.SenderID)
		block := app.BrightChainKeeper.GetUserBlock(struct{}{}, chainID)
		if block != nil && len(block.SendPk) == 32 {
			if !ed25519Verify(block.SendPk, tx.SignBytes, tx.Signature) {
				log.Printf("[chain] signature rejected: invalid signature for %s", tx.SenderID[:16])
				return
			}
		}
	}

	// 3. 序列号防重放检查
	if tx.SenderID != "" && tx.Sequence > 0 {
		currentSeq := app.BrightChainKeeper.GetSequence(struct{}{}, tx.SenderID)
		if tx.Sequence <= currentSeq {
			log.Printf("[chain] sequence rejected: tx seq=%d <= chain seq=%d for %s",
				tx.Sequence, currentSeq, tx.SenderID[:16])
			return
		}
	}

	// 4. 记录交易 ID（防重放）
	app.markTxExecuted(tx.ID)

	// 5. 执行后递增发送者 Sequence（防重放）
	defer func() {
		if tx.SenderID != "" && tx.Sequence > 0 {
			app.BrightChainKeeper.IncrementSequence(struct{}{}, tx.SenderID)
		}
	}()

	switch tx.Type {
	case "register":
		if len(tx.Data) < 64 {
			log.Printf("[chain] register tx too short: %d bytes", len(tx.Data))
			return
		}
		msg := &brighttypes.MsgRegister{RecvPk: tx.Data[:32], SendPk: tx.Data[32:64]}
		// 解析邀请凭证（如果有）
		if len(tx.Data) > 64 {
			// 格式: recv_pk(32) + send_pk(32) + invite_count(1) + N×cred(216)
			count := int(tx.Data[64])
			offset := 65
			for i := 0; i < count && offset+216 <= len(tx.Data); i++ {
				cred := make([]byte, 216)
				copy(cred, tx.Data[offset:offset+216])
				msg.GuarantorSigs = append(msg.GuarantorSigs, cred)
				offset += 216
			}
		}
		_, err := app.BrightChainKeeper.RegisterUser(struct{}{}, msg)
		if err != nil {
			log.Printf("[chain] register failed: %v", err)
		}
	case "guarantee":
		// 简化：Data = inviter(32) + invitee(32)
		if len(tx.Data) >= 64 {
			if _, err := app.BrightChainKeeper.CreateBond(struct{}{}, &brighttypes.MsgGuarantee{
				Inviter: string(tx.Data[:32]), Invitee: string(tx.Data[32:64]),
			}); err != nil {
				log.Printf("[chain] guarantee failed: %v", err)
			}
		}
	case "repay":
		// Data = payer(32) + amount(8) + [loan_id(32)] (可选暗链贷款ID)
		if len(tx.Data) >= 40 {
			amt := uint64(tx.Data[32])<<56 | uint64(tx.Data[33])<<48 | uint64(tx.Data[34])<<40 | uint64(tx.Data[35])<<32 |
				uint64(tx.Data[36])<<24 | uint64(tx.Data[37])<<16 | uint64(tx.Data[38])<<8 | uint64(tx.Data[39])
			if err := app.BrightChainKeeper.CollectFees(struct{}{}, amt); err != nil {
				log.Printf("[chain] collect fees failed: %v", err)
			}
			// 如果附带暗链贷款ID，触发暗链结算
			if len(tx.Data) >= 72 {
				loanID := string(tx.Data[40:72])
				if _, err := app.DarkChainKeeper.SettleLoan(&darktypes.MsgSettleLoan{
					LoanID: loanID,
				}); err != nil {
					log.Printf("[chain] settle loan failed for %s: %v", loanID[:16], err)
				} else {
					log.Printf("[chain] loan %s settled via bright chain repay", loanID[:16])
				}
			}
		}
	case "loan":
		msg := &darktypes.MsgRequestLoan{BorrowerAnon: tx.Data[:32], Amount: 100, TermDays: 30}
		loan, err := app.DarkChainKeeper.RequestLoan(msg)
		if err != nil {
			log.Printf("[chain] loan request failed: %v", err)
			return
		}
		if loan != nil {
			if _, err := app.DarkChainKeeper.ApproveLoan(&darktypes.MsgApproveLoan{LoanID: loan.LoanID, LenderAnon: []byte("pool")}); err != nil {
				log.Printf("[chain] loan approve failed: %v", err)
			}
		}
		case "game_tx":
		// 游戏交易: Data = game_id_len(1) + game_id(N) + server_addr(32) + fee(4) + custom_data
		if len(tx.Data) >= 38 {
			idLen := int(tx.Data[0])
			if idLen < 1 || idLen > 32 || len(tx.Data) < 1+idLen+32+4 {
				return
			}
			gameID := string(tx.Data[1 : 1+idLen])
			offset := 1 + idLen
			serverAddr := string(tx.Data[offset : offset+32])
			offset += 32
			fee := uint64(tx.Data[offset])<<24 | uint64(tx.Data[offset+1])<<16 |
				uint64(tx.Data[offset+2])<<8 | uint64(tx.Data[offset+3])
			offset += 4
			customData := tx.Data[offset:]
			app.RecordGameTx(gameID, serverAddr, "game_tx", fee, customData)
		}
	case "register_game":
		// 注册新游戏: game_id_len(1) + game_id(N) + author(32) + fee_rate(2) + author_share(1) + server_share(1) + name
		if len(tx.Data) >= 38 {
			idLen := int(tx.Data[0])
			if idLen < 1 || idLen > 32 || len(tx.Data) < 1+idLen+32+4 { return }
			gameID := string(tx.Data[1 : 1+idLen])
			offset := 1 + idLen
			author := string(tx.Data[offset : offset+32])
			offset += 32
			feeRate := uint64(tx.Data[offset])<<8 | uint64(tx.Data[offset+1])
			authorShare := uint64(tx.Data[offset+2])
			serverShare := uint64(tx.Data[offset+3])
			offset += 4
			name := ""
			if len(tx.Data) > offset { name = string(tx.Data[offset:]) }
			app.RegisterGame(gameID, author, name, feeRate, authorShare, serverShare)
		}
	case "bind_game":
		// 节点绑定为游戏服务器: node_id(32) + game_id_len(1) + game_id(N)
		if len(tx.Data) >= 34 {
			nodeID := string(tx.Data[:32])
			idLen := int(tx.Data[32])
			if idLen < 1 || idLen > 32 || len(tx.Data) < 33+idLen { return }
			gameID := string(tx.Data[33 : 33+idLen])
			app.BindGameServer(nodeID, gameID)
		}
	default:
		// 未知交易类型，跳过
	}
}

// SubmitTx 提交一笔交易到链上。
func (app *NekoApp) SubmitTx(txType string, data []byte) string {
	tx := &Tx{
		ID:        generateTxID(data),
		Type:      txType,
		Data:      data,
		SenderID:  "", // 由上层 API 设置（从认证令牌关联）
		Status:    "pending",
		CreatedAt: time.Now().Unix(),
	}
	app.mempool.Submit(tx)
	app.RecordTx(tx)
	return tx.ID
}

func generateTxID(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:16])
}

// ===== 重放防护辅助 =====

var (
	executedTxIDs   = make(map[string]bool) // 已执行交易 ID 集合
	executedTxIDsMu sync.Mutex
)

// isTxReplay 检查交易 ID 是否已被执行（防重放）。
func (app *NekoApp) isTxReplay(txID string) bool {
	executedTxIDsMu.Lock()
	defer executedTxIDsMu.Unlock()
	return executedTxIDs[txID]
}

// markTxExecuted 标记交易 ID 为已执行。
func (app *NekoApp) markTxExecuted(txID string) {
	executedTxIDsMu.Lock()
	defer executedTxIDsMu.Unlock()
	executedTxIDs[txID] = true
	// 定期清理旧条目（保留最近 10000 条）
	if len(executedTxIDs) > 10000 {
		// 简单策略：清空一半
		count := 0
		for k := range executedTxIDs {
			delete(executedTxIDs, k)
			count++
			if count >= 5000 {
				break
			}
		}
	}
}


// ResetReplayCache 重置重放防护缓存（仅用于测试）。
func ResetReplayCache() {
	executedTxIDsMu.Lock()
	defer executedTxIDsMu.Unlock()
	executedTxIDs = make(map[string]bool)
}

// isTxReplay 检查交易 ID 是否已被执行（线程安全）。
func isTxReplaySafe(txID string) bool {
	executedTxIDsMu.Lock()
	defer executedTxIDsMu.Unlock()
	return executedTxIDs[txID]
}

// markTxExecutedSafe 标记交易 ID 为已执行（线程安全）。
func markTxExecutedSafe(txID string) {
	executedTxIDsMu.Lock()
	defer executedTxIDsMu.Unlock()
	executedTxIDs[txID] = true
}

// ed25519Verify 验证 Ed25519 签名（链上验证辅助）。
func ed25519Verify(pubKey, message, sig []byte) bool {
	if len(sig) != 64 || len(pubKey) != 32 {
		return false
	}
	return ed25519.Verify(pubKey, message, sig)
}

// RunOnce 手动执行一个区块（用于测试或单步调试），包含交易处理。
func (app *NekoApp) RunOnce() {
	ctx := struct{}{}
	app.BeginBlocker(ctx)
	txs := app.mempool.Drain(100)
	for _, tx := range txs {
		app.executeTx(tx)
	}
	app.EndBlocker(ctx)
}
