package inkwell

import (
	"fmt"
	"sync"
	"time"
)

// Scheduler 管理所有贷款的还款分片调度。
//
// 它跟踪每个贷款的分片支付状态，并在分片到期时
// 返回下一个应支付的分片。
type Scheduler struct {
	mu       sync.RWMutex
	payments map[string][]int // loanID → 已支付的分片索引列表
}

// NewScheduler 创建新的还款调度器。
func NewScheduler() *Scheduler {
	return &Scheduler{
		payments: make(map[string][]int),
	}
}

// RecordPayment 记录一个分片支付。
func (s *Scheduler) RecordPayment(loanID string, fragmentIndex int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.payments[loanID] = append(s.payments[loanID], fragmentIndex)
}

// GetPaidIndices 返回指定贷款已支付的分片索引。
func (s *Scheduler) GetPaidIndices(loanID string) []int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.payments[loanID]
}

// IsFullyPaid 检查贷款是否已全部还清。
func (s *Scheduler) IsFullyPaid(loanID string, totalFragments int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.payments[loanID]) >= totalFragments
}

// GetNextDue 返回下一个到期未支付的分片。
// 如果所有分片已到期但未付清，返回最早到期的那个。
func (s *Scheduler) GetNextDue(params *Params) (*FragmentPlan, error) {
	plan := params.GeneratePlan()
	paidSet := s.buildPaidSet(params.LoanID)

	now := time.Now()

	// 先找已到期的未支付分片
	var overdue []FragmentPlan
	for _, fp := range plan {
		if paidSet[fp.Index] {
			continue
		}
		if now.After(fp.DueAfter) {
			overdue = append(overdue, fp)
		}
	}

	if len(overdue) > 0 {
		// 返回最早到期的
		earliest := overdue[0]
		for _, fp := range overdue[1:] {
			if fp.DueAfter.Before(earliest.DueAfter) {
				earliest = fp
			}
		}
		return &earliest, nil
	}

	// 没有已到期的，找最近的未来分片
	var nextDue *FragmentPlan
	for _, fp := range plan {
		if paidSet[fp.Index] {
			continue
		}
		if nextDue == nil || fp.DueAfter.Before(nextDue.DueAfter) {
			nextDue = &fp
		}
	}

	if nextDue == nil {
		// 全部付清
		return nil, fmt.Errorf("all fragments paid")
	}

	return nextDue, fmt.Errorf("next fragment due at %s", nextDue.DueAfter.Format(time.RFC3339))
}

// RemainingAmount 返回贷款的剩余未付金额。
func (s *Scheduler) RemainingAmount(params *Params) uint64 {
	paidSet := s.buildPaidSet(params.LoanID)
	var remaining uint64
	for i, amount := range params.Fragments {
		if !paidSet[i] {
			remaining += amount
		}
	}
	return remaining
}

// PaymentProgress 返回支付进度 (paidCount, totalCount)。
func (s *Scheduler) PaymentProgress(loanID string, totalFragments int) (int, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.payments[loanID]), totalFragments
}

// buildPaidSet 构建已支付分片索引集合。
func (s *Scheduler) buildPaidSet(loanID [32]byte) map[int]bool {
	loanIDStr := string(loanID[:])
	s.mu.RLock()
	defer s.mu.RUnlock()
	paidSet := make(map[int]bool)
	for _, idx := range s.payments[loanIDStr] {
		paidSet[idx] = true
	}
	return paidSet
}
