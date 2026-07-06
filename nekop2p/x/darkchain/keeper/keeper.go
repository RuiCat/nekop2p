// Package keeper 实现暗链模块的状态管理。
//
// 使用 BoltDB 持久化：贷款、nullifier、身份标记。
package keeper

import (
	"fmt"
	"sync"
	"time"

	"github.com/nekop2p/nekop2p/store"
	"github.com/nekop2p/nekop2p/x/darkchain/types"
)

// Keeper 管理暗链模块的状态（BoltDB 持久化）。
type Keeper struct {
	store      *store.ChainStore
	cycleCount uint64
	// 身份标记: 用于防自我交易，带锁保护并发访问
	markerMu         sync.RWMutex
	identityMarkers  map[string]bool
}

// NewKeeper 创建新的暗链 Keeper。
func NewKeeper(s *store.ChainStore) *Keeper {
	return &Keeper{
		store:           s,
		identityMarkers: make(map[string]bool),
	}
}

// ===== 贷款管理 =====

func (k *Keeper) RequestLoan(msg *types.MsgRequestLoan) (*types.LoanRecord, error) {
	if msg.Amount == 0 {
		return nil, fmt.Errorf("darkchain: loan amount must be > 0")
	}
	if msg.TermDays <= 0 || msg.TermDays > 365 {
		return nil, fmt.Errorf("darkchain: term must be 1-365 days, got %d", msg.TermDays)
	}

	loanID := generateLoanID(msg.BorrowerAnon, msg.Amount)
	now := time.Now().Unix()
	loan := &types.LoanRecord{
		LoanID:        loanID,
		BorrowerAnon:  msg.BorrowerAnon,
		Amount:        msg.Amount,
		TermDays:      msg.TermDays,
		Status:        types.LoanPending,
		ZkCreditProof: msg.ZkCreditProof,
		InkwellParams: msg.InkwellSeed,
		CreatedAt:     now,
		DueDate:       now + msg.TermDays*86400,
	}

	data, err := store.Marshal(loan)
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
	loan.LenderAnon = msg.LenderAnon
	loan.InkwellParams = msg.InkwellParams
	loan.Status = types.LoanActive
	data, err := store.Marshal(loan)
	if err != nil {
		return nil, fmt.Errorf("darkchain: marshal loan: %w", err)
	}
	return loan, k.store.Write(func(tx *store.Tx) error { return tx.PutLoan(msg.LoanID, data) })
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
	data, err := store.Marshal(loan)
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
	data, err := store.Marshal(loan)
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
			if err := store.Unmarshal(data, &result); err != nil {
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
			if store.Unmarshal(data, &l) == nil {
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

func (k *Keeper) MarkNullifier(nullifier string) bool {
	exists := false
	k.store.Read(func(tx *store.Tx) error { exists = tx.HasNullifier(nullifier); return nil })
	if exists {
		return false
	}
	k.store.Write(func(tx *store.Tx) error { return tx.PutNullifier(nullifier) })
	return true
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

func generateLoanID(borrowerAnon []byte, amount uint64) string {
	prefix := borrowerAnon
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	return fmt.Sprintf("loan-%x-%d-%d", prefix, amount, time.Now().UnixNano())
}
