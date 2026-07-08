// Package keeper 实现暗链模块的状态管理。
//
// 使用 BoltDB 持久化：贷款、nullifier、身份标记。
package keeper

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nekop2p/nekop2p/inkwell"
	"github.com/nekop2p/nekop2p/store"
	"github.com/nekop2p/nekop2p/x/darkchain/types"
	zk "github.com/nekop2p/nekop2p/x/zk"
)

// Keeper 管理暗链模块的状态（BoltDB 持久化）。
type Keeper struct {
	store      *store.ChainStore
	cycleCount uint64
	// 身份标记: 用于防自我交易，带锁保护并发访问
	markerMu         sync.RWMutex
	identityMarkers  map[string]bool
	zkVerifier          *zk.Verifier             // ZK 证明验证器
	creditTree          *store.CreditMerkleTree   // 信用票据 Merkle 树
	shadowClaimCallback func(loan *types.LoanRecord) // ShadowClaim 发行回调
}

// SetShadowClaimCallback 设置 ShadowClaim 发行回调（由 brightchain keeper 注册）。
func (k *Keeper) SetShadowClaimCallback(cb func(loan *types.LoanRecord)) {
	k.shadowClaimCallback = cb
}

// NewKeeper 创建新的暗链 Keeper。
func NewKeeper(s *store.ChainStore) *Keeper {
	k := &Keeper{
		store:           s,
		identityMarkers: make(map[string]bool),
		creditTree:      store.NewCreditMerkleTree(),
	}
	// 从持久化存储恢复信用树
	k.restoreCreditTree()
	return k
}

// restoreCreditTree 从 BoltDB 恢复 Merkle 信用树。
func (k *Keeper) restoreCreditTree() {
	var commitments [][]byte
	k.store.Read(func(tx *store.Tx) error {
		return tx.ForEachCreditCommitment(func(_ string, data []byte) error {
			commitments = append(commitments, data)
			return nil
		})
	})
	if len(commitments) > 0 {
		k.creditTree.Restore(commitments)
	}
}

// CreditTreeRoot 返回当前信用 Merkle 树根。
func (k *Keeper) CreditTreeRoot() [32]byte {
	return k.creditTree.Root()
}

// CreditTreeCount 返回信用树中承诺数量。
func (k *Keeper) CreditTreeCount() int {
	return k.creditTree.Count()
}

// AddCreditCommitment 向信用树添加信用票据承诺并持久化。
func (k *Keeper) AddCreditCommitment(commitment []byte) error {
	key := fmt.Sprintf("credit-%x", commitment[:16]) // 使用前16字节(128bit)防碰撞
	k.creditTree.AddCommitment(commitment)
	return k.store.Write(func(tx *store.Tx) error {
		return tx.PutCreditCommitment(key, commitment)
	})
}

// SpendCreditCommitment 花费信用票据承诺（从树中移除并持久化删除）。
func (k *Keeper) SpendCreditCommitment(commitment []byte) error {
	if !k.creditTree.RemoveCommitment(commitment) {
		return fmt.Errorf("darkchain: credit commitment not found")
	}
	key := fmt.Sprintf("credit-%x", commitment[:16])
	return k.store.Write(func(tx *store.Tx) error {
		return tx.DeleteCreditCommitment(key)
	})
}

// GenerateCreditProof 为指定承诺生成 Merkle 包含性证明。
func (k *Keeper) GenerateCreditProof(commitment []byte) (*store.MerkleProof, error) {
	return k.creditTree.GenerateProof(commitment)
}

// SetZkVerifier 设置 ZK 证明验证器。
func (k *Keeper) SetZkVerifier(v *zk.Verifier) {
	k.zkVerifier = v
}

// ZkVerifier 返回当前的 ZK 验证器（可能为 nil）。
func (k *Keeper) ZkVerifier() *zk.Verifier {
	return k.zkVerifier
}

// ===== 贷款管理 =====

func (k *Keeper) RequestLoan(msg *types.MsgRequestLoan) (*types.LoanRecord, error) {
	if msg.Amount == 0 {
		return nil, fmt.Errorf("darkchain: loan amount must be > 0")
	}
	if msg.TermDays <= 0 || msg.TermDays > 365 {
		return nil, fmt.Errorf("darkchain: term must be 1-365 days, got %d", msg.TermDays)
	}

	// === UTXO 信用票据花费 ===
	// 如果提供了信用票据，使用 UTXO 系统验证和花费。
	var spentNullifiers [][32]byte
	if len(msg.CreditNotes) > 0 {
		// 反序列化信用票据
		notes := make([]*CreditNote, 0, len(msg.CreditNotes))
		for i, rawNote := range msg.CreditNotes {
			note, err := deserializeCreditNote(rawNote)
			if err != nil {
				return nil, fmt.Errorf("darkchain: deserialize credit note %d: %w", i, err)
			}
			notes = append(notes, note)
		}

		// 花费票据（验证 Merkle 证明 + nullifier 防双花）
		result, err := k.SpendCreditNotes(notes, msg.MasterSecret, msg.Amount)
		if err != nil {
			return nil, fmt.Errorf("darkchain: spend credit notes: %w", err)
		}

		spentNullifiers = result.Nullifiers
		log.Printf("[darkchain] spent %d credit notes (total=%d) for loan amount=%d, change=%d, nullifiers=%d",
			len(result.SpentNotes), result.TotalSpent, msg.Amount, result.ChangeAmount, len(result.Nullifiers))

		// 如果提供了 ZK 证明，额外验证（信用>=阈值的零知识证明）
		if len(msg.ZkCreditProof) > 0 && k.zkVerifier != nil {
			creditAssignment := zk.NewCreditAssignment(msg.Amount)
			if err := k.zkVerifier.VerifyCreditProof(msg.ZkCreditProof, creditAssignment); err != nil {
				return nil, fmt.Errorf("darkchain: zk credit proof invalid: %w", err)
			}
		}
	} else if len(msg.ZkCreditProof) > 0 && k.zkVerifier != nil {
		// 仅 ZK 证明路径（无 UTXO 票据时使用 ZK 证明信用达标）
		creditAssignment := zk.NewCreditAssignment(msg.Amount)
		if err := k.zkVerifier.VerifyCreditProof(msg.ZkCreditProof, creditAssignment); err != nil {
			return nil, fmt.Errorf("darkchain: zk credit proof invalid: %w", err)
		}
		log.Printf("[darkchain] zk credit proof verified for loan amount=%d", msg.Amount)
	} else {
		// 创世阶段或无验证器：允许基础借贷（信用限制由明链 keeper 管理）
		log.Printf("[darkchain] no credit proof required (genesis/simple mode) for amount=%d", msg.Amount)
	}

	loanID := k.generateLoanID(msg.BorrowerAnon, msg.Amount)
	now := time.Now().Unix()
	loan := &types.LoanRecord{
		LoanID:             loanID,
		BorrowerAnon:       msg.BorrowerAnon,
		Amount:             msg.Amount,
		TermDays:           msg.TermDays,
		Status:             types.LoanPending,
		ZkCreditProof:      msg.ZkCreditProof,
		InkwellParams:      msg.InkwellSeed,
		BorrowerSeedCommit: msg.BorrowerSeedCommit,
		SpentNullifiers:    spentNullifiers,
		CreatedAt:          now,
		DueDate:            now + msg.TermDays*86400,
	}

	data, err := store.HybridMarshal(loan)
	if err != nil {
		return nil, fmt.Errorf("darkchain: marshal loan: %w", err)
	}
	err = k.store.Write(func(tx *store.Tx) error { return tx.PutLoan(loanID, data) })
	return loan, err
}

func (k *Keeper) ApproveLoan(msg *types.MsgApproveLoan) (*types.LoanRecord, error) {
	loan := k.GetLoan(msg.LoanID)
	if loan == nil {
		return nil, fmt.Errorf("darkchain: loan not found: %s", msg.LoanID)
	}
	if loan.Status != types.LoanPending {
		return nil, fmt.Errorf("darkchain: loan %s is not pending", msg.LoanID)
	}

	// === Commit-Reveal 种子验证 ===
	// 如果借款人提供了种子承诺，验证贷款人揭示的种子是否匹配
	var zeroSeed [32]byte
	if loan.BorrowerSeedCommit != zeroSeed {
		// 验证借款人种子承诺：SHA256(seed || party_id || nonce) == commit
		borrowerPartyID := fmt.Sprintf("borrower-%s", msg.LoanID)
		borrowerValid := inkwell.SeedReveal(loan.BorrowerSeedCommit, msg.BorrowerSeed, msg.BorrowerSeedNonce, borrowerPartyID)
		if !borrowerValid {
			return nil, fmt.Errorf("darkchain: borrower seed reveal mismatch for loan %s", msg.LoanID)
		}

		// 验证贷款人种子承诺（如果提供了双向 commit-reveal）
		var lenderCommitZero [32]byte
		if msg.LenderSeedCommit != lenderCommitZero {
			lenderPartyID := fmt.Sprintf("lender-%s", msg.LoanID)
			lenderValid := inkwell.SeedReveal(msg.LenderSeedCommit, msg.LenderSeed, msg.LenderSeedNonce, lenderPartyID)
			if !lenderValid {
				return nil, fmt.Errorf("darkchain: lender seed reveal mismatch for loan %s", msg.LoanID)
			}
		}

		// 合并双方种子生成最终混沌参数种子
		combinedSeed := inkwell.CombineSeeds(msg.BorrowerSeed, msg.LenderSeed)

		// 生成 Inkwell 混沌结算参数
		var loanIDBytes [32]byte
		copy(loanIDBytes[:], []byte(msg.LoanID))
		var borrowerAnonID [32]byte
		copy(borrowerAnonID[:], loan.BorrowerAnon)

		params, err := inkwell.GenerateParams(
			loanIDBytes,
			loan.Amount,
			msg.BorrowerSeed,
			msg.LenderSeed,
			time.Now(),
			borrowerAnonID,
		)
		if err != nil {
			return nil, fmt.Errorf("darkchain: generate inkwell params: %w", err)
		}

		// 序列化参数用于存储
		paramsData, err := store.HybridMarshal(params)
		if err != nil {
			return nil, fmt.Errorf("darkchain: marshal inkwell params: %w", err)
		}

		loan.CombinedSeed = combinedSeed
		loan.InkwellParams = paramsData
	} else if len(msg.InkwellParams) > 0 {
		// 向后兼容：无 commit-reveal 时直接使用提供的参数
		loan.InkwellParams = msg.InkwellParams
	}

	loan.LenderAnon = msg.LenderAnon
	loan.Status = types.LoanActive
	data, err := store.HybridMarshal(loan)
	if err != nil {
		return nil, fmt.Errorf("darkchain: marshal loan: %w", err)
	}
	err = k.store.Write(func(tx *store.Tx) error { return tx.PutLoan(msg.LoanID, data) })
	if err != nil {
		return nil, err
	}

	// 🔗 触发 ShadowClaim 自动发行（桥接到明域二级市场）
	if k.shadowClaimCallback != nil {
		k.shadowClaimCallback(loan)
	}

	return loan, nil
}

func (k *Keeper) SettleLoan(msg *types.MsgSettleLoan) (*types.LoanRecord, error) {
	loan := k.GetLoan(msg.LoanID)
	if loan == nil {
		return nil, fmt.Errorf("darkchain: loan not found: %s", msg.LoanID)
	}
	if loan.Status != types.LoanActive {
		return nil, fmt.Errorf("darkchain: loan %s is not active", msg.LoanID)
	}
	loan.Status = types.LoanSettled
	loan.SettledAt = time.Now().Unix()
	loan.DeliveryProof = msg.DeliveryProof
	loan.SettlementCmd = msg.SettlementCmd
	data, err := store.HybridMarshal(loan)
	if err != nil {
		return nil, fmt.Errorf("darkchain: marshal loan: %w", err)
	}
	return loan, k.store.Write(func(tx *store.Tx) error { return tx.PutLoan(msg.LoanID, data) })
}

func (k *Keeper) DefaultLoan(loanID string) error {
	loan := k.GetLoan(loanID)
	if loan == nil {
		return fmt.Errorf("darkchain: loan not found: %s", loanID)
	}
	loan.Status = types.LoanDefaulted
	data, err := store.HybridMarshal(loan)
	if err != nil {
		return fmt.Errorf("darkchain: marshal loan: %w", err)
	}
	return k.store.Write(func(tx *store.Tx) error { return tx.PutLoan(loanID, data) })
}

func (k *Keeper) GetLoan(loanID string) *types.LoanRecord {
	var result *types.LoanRecord
	k.store.Read(func(tx *store.Tx) error {
		data := tx.GetLoan(loanID)
		if data != nil {
			if err := store.HybridUnmarshal(data, &result); err != nil {
				// 数据损坏：静默处理，调用方检查 nil
				result = nil
			}
		}
		return nil
	})
	return result
}

func (k *Keeper) GetActiveLoans() []*types.LoanRecord {
	return k.filterLoans(func(l *types.LoanRecord) bool { return l.Status == types.LoanActive })
}

func (k *Keeper) GetOverdueLoans() []*types.LoanRecord {
	now := time.Now().Unix()
	return k.filterLoans(func(l *types.LoanRecord) bool {
		return l.Status == types.LoanActive && l.DueDate > 0 && l.DueDate < now
	})
}

func (k *Keeper) GetAllLoans() []*types.LoanRecord { return k.filterLoans(nil) }

func (k *Keeper) filterLoans(filter func(*types.LoanRecord) bool) []*types.LoanRecord {
	var result []*types.LoanRecord
	k.store.Read(func(tx *store.Tx) error {
		return tx.ForEachLoan(func(_ string, data []byte) error {
			var l types.LoanRecord
			if store.HybridUnmarshal(data, &l) == nil {
				if filter == nil || filter(&l) {
					result = append(result, &l)
				}
			}
			return nil
		})
	})
	return result
}

// ===== Nullifier =====

// MarkNullifier 原子化记录 nullifier（防 TOCTOU 竞态）。
// 在单个 BoltDB 写事务中完成"检查-写入"，
// 杜绝并发双花窗口。
func (k *Keeper) MarkNullifier(nullifier string) bool {
	var firstWrite bool
	err := k.store.Write(func(tx *store.Tx) error {
		if tx.HasNullifier(nullifier) {
			return nil // 已存在，非首写
		}
		firstWrite = true
		return tx.PutNullifier(nullifier)
	})
	return err == nil && firstWrite
}

func (k *Keeper) IsNullifierSpent(nullifier string) bool {
	var spent bool
	k.store.Read(func(tx *store.Tx) error { spent = tx.HasNullifier(nullifier); return nil })
	return spent
}

// ===== 身份标记 =====

// identityMarkers 存储已看到的身份标记（用于防自我交易）。
// 现在存储在 Keeper 结构体中，带 RWMutex 保护并发访问。
// 每周期（AdvanceCycle）清空。

func (k *Keeper) RecordIdentityMarker(marker string) bool {
	k.markerMu.Lock()
	defer k.markerMu.Unlock()
	if k.identityMarkers[marker] {
		return false // 重复标记：同一人在同一周期内尝试双重交易
	}
	k.identityMarkers[marker] = true
	return true
}

func (k *Keeper) HasIdentityMarker(marker string) bool {
	k.markerMu.RLock()
	defer k.markerMu.RUnlock()
	return k.identityMarkers[marker]
}

func (k *Keeper) AdvanceCycle() {
	k.markerMu.Lock()
	defer k.markerMu.Unlock()
	k.cycleCount++
	k.identityMarkers = make(map[string]bool) // 新周期：清除所有旧标记
}

func (k *Keeper) CycleCount() uint64 { return k.cycleCount }

// ===== 持久化 Height =====

func (k *Keeper) SetHeight(h int64) {
	k.store.Write(func(tx *store.Tx) error { return tx.SetHeight(h) })
}

func (k *Keeper) Height() int64 {
	var h int64
	k.store.Read(func(tx *store.Tx) error { h = tx.Height(); return nil })
	return h
}

// generateLoanID 从借款人匿名化名和金额生成贷款 ID。
func (k *Keeper) generateLoanID(borrowerAnon []byte, amount uint64) string {
	prefix := borrowerAnon
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	return fmt.Sprintf("loan-%x-%d-%d", prefix, amount, time.Now().UnixNano())
}

// GetRecentAnonIDs 实现 inkwell.RelayPathProvider 接口。
// 从暗链近期活跃贷款中提取匿名化名列表，用于中继路径选择。
func (k *Keeper) GetRecentAnonIDs(limit int) [][32]byte {
	loans := k.GetAllLoans()
	seen := make(map[string]bool)
	var result [][32]byte

	// 按时间倒序排列（较新的贷款在前）
	for i := len(loans) - 1; i >= 0 && len(result) < limit; i-- {
		anonKey := string(loans[i].BorrowerAnon)
		if !seen[anonKey] && len(loans[i].BorrowerAnon) >= 32 {
			seen[anonKey] = true
			var anonID [32]byte
			copy(anonID[:], loans[i].BorrowerAnon[:32])
			result = append(result, anonID)
		}
		if loans[i].LenderAnon != nil && len(loans[i].LenderAnon) >= 32 {
			lenderKey := string(loans[i].LenderAnon)
			if !seen[lenderKey] && len(result) < limit {
				seen[lenderKey] = true
				var anonID [32]byte
				copy(anonID[:], loans[i].LenderAnon[:32])
				result = append(result, anonID)
			}
		}
	}
	return result
}
