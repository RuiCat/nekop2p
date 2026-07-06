package keeper_test

import (
	"os"
	"testing"

	"github.com/nekop2p/nekop2p/store"
	"github.com/nekop2p/nekop2p/x/darkchain/keeper"
	"github.com/nekop2p/nekop2p/x/darkchain/types"
)

func newTestKeeper(t *testing.T) *keeper.Keeper {
	dir, err := os.MkdirTemp("", "darkkeeper-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s, err := store.New(dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	return keeper.NewKeeper(s)
}

// ===== 贷款管理测试 =====

func TestRequestLoan(t *testing.T) {
	k := newTestKeeper(t)

	msg := &types.MsgRequestLoan{
		BorrowerAnon: []byte("anon-borrower-32-bytes-xxxxxxxx"),
		Amount:       500,
		TermDays:     30,
	}

	loan, err := k.RequestLoan(msg)
	if err != nil {
		t.Fatalf("request loan: %v", err)
	}
	if loan.Status != types.LoanPending {
		t.Errorf("expected Pending, got %d", loan.Status)
	}
	if loan.Amount != 500 {
		t.Errorf("amount: got %d, want 500", loan.Amount)
	}
}

func TestRequestLoanZeroAmount(t *testing.T) {
	k := newTestKeeper(t)
	_, err := k.RequestLoan(&types.MsgRequestLoan{
		BorrowerAnon: []byte("anon"),
		Amount:       0,
	})
	if err == nil {
		t.Error("zero amount should fail")
	}
}

func TestRequestLoanInvalidTerm(t *testing.T) {
	k := newTestKeeper(t)
	_, err := k.RequestLoan(&types.MsgRequestLoan{
		BorrowerAnon: []byte("anon"),
		Amount:       100,
		TermDays:     400, // > 365
	})
	if err == nil {
		t.Error("term > 365 should fail")
	}
}

func TestApproveLoan(t *testing.T) {
	k := newTestKeeper(t)

	loan, _ := k.RequestLoan(&types.MsgRequestLoan{
		BorrowerAnon: []byte("anon-borrower"),
		Amount:       300,
		TermDays:     30,
	})

	approved, err := k.ApproveLoan(&types.MsgApproveLoan{
		LoanID:     loan.LoanID,
		LenderAnon: []byte("anon-lender"),
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.Status != types.LoanActive {
		t.Error("loan should be active after approval")
	}
}

func TestApproveLoanTwice(t *testing.T) {
	k := newTestKeeper(t)

	loan, _ := k.RequestLoan(&types.MsgRequestLoan{
		BorrowerAnon: []byte("anon-b"),
		Amount:       100,
		TermDays:     30,
	})

	k.ApproveLoan(&types.MsgApproveLoan{
		LoanID: loan.LoanID, LenderAnon: []byte("lender"),
	})

	_, err := k.ApproveLoan(&types.MsgApproveLoan{
		LoanID: loan.LoanID, LenderAnon: []byte("lender2"),
	})
	if err == nil {
		t.Error("second approval should fail")
	}
}

func TestSettleLoan(t *testing.T) {
	k := newTestKeeper(t)

	loan, _ := k.RequestLoan(&types.MsgRequestLoan{
		BorrowerAnon: []byte("anon-borrower-s"),
		Amount:       200,
		TermDays:     30,
	})
	k.ApproveLoan(&types.MsgApproveLoan{
		LoanID: loan.LoanID, LenderAnon: []byte("lender"),
	})

	settled, err := k.SettleLoan(&types.MsgSettleLoan{
		LoanID:        loan.LoanID,
		DeliveryProof: []byte("proof"),
	})
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if settled.Status != types.LoanSettled {
		t.Error("loan should be settled")
	}
}

func TestDefaultLoan(t *testing.T) {
	k := newTestKeeper(t)

	loan, _ := k.RequestLoan(&types.MsgRequestLoan{
		BorrowerAnon: []byte("anon-defaulter"),
		Amount:       500,
		TermDays:     30,
	})
	k.ApproveLoan(&types.MsgApproveLoan{
		LoanID: loan.LoanID, LenderAnon: []byte("lender"),
	})

	err := k.DefaultLoan(loan.LoanID)
	if err != nil {
		t.Fatalf("default: %v", err)
	}

	retrieved := k.GetLoan(loan.LoanID)
	if retrieved.Status != types.LoanDefaulted {
		t.Error("loan should be defaulted")
	}
}

func TestGetLoan(t *testing.T) {
	k := newTestKeeper(t)

	loan, _ := k.RequestLoan(&types.MsgRequestLoan{
		BorrowerAnon: []byte("anon-get"),
		Amount:       100,
		TermDays:     30,
	})

	retrieved := k.GetLoan(loan.LoanID)
	if retrieved == nil {
		t.Fatal("GetLoan returned nil")
	}
	if retrieved.Amount != 100 {
		t.Error("amount mismatch")
	}
}

func TestGetLoanNotFound(t *testing.T) {
	k := newTestKeeper(t)
	result := k.GetLoan("nonexistent")
	if result != nil {
		t.Error("GetLoan should return nil for unknown loan")
	}
}

// ===== Nullifier 测试 =====

func TestMarkNullifier(t *testing.T) {
	k := newTestKeeper(t)

	ok := k.MarkNullifier("nullifier-001")
	if !ok {
		t.Error("first mark should succeed")
	}
	if !k.IsNullifierSpent("nullifier-001") {
		t.Error("nullifier should be marked as spent")
	}
}

func TestMarkNullifierDoubleSpend(t *testing.T) {
	k := newTestKeeper(t)

	k.MarkNullifier("nullifier-002")
	ok := k.MarkNullifier("nullifier-002")
	if ok {
		t.Error("second mark should fail (double spend)")
	}
}

// ===== 身份标记测试 =====

func TestRecordIdentityMarker(t *testing.T) {
	k := newTestKeeper(t)

	ok := k.RecordIdentityMarker("marker-001")
	if !ok {
		t.Error("first record should succeed")
	}
}

func TestAdvanceCycle(t *testing.T) {
	k := newTestKeeper(t)

	k.RecordIdentityMarker("marker-cycle1")
	k.AdvanceCycle()

	// 新周期可以重新使用标记
	ok := k.RecordIdentityMarker("marker-cycle1")
	if !ok {
		t.Error("new cycle should allow same marker")
	}

	if k.CycleCount() != 1 {
		t.Errorf("cycle count: got %d, want 1", k.CycleCount())
	}
}

// ===== 逾期贷款测试 =====

func TestGetActiveLoans(t *testing.T) {
	k := newTestKeeper(t)

	for i := 0; i < 3; i++ {
		loan, _ := k.RequestLoan(&types.MsgRequestLoan{
			BorrowerAnon: []byte{byte('a' + i), 0, 0, 0, 0, 0, 0, 0, 0, 0},
			Amount:       100,
			TermDays:     30,
		})
		k.ApproveLoan(&types.MsgApproveLoan{
			LoanID: loan.LoanID, LenderAnon: []byte("lender-32-b"),
		})
	}

	active := k.GetActiveLoans()
	if len(active) != 3 {
		t.Errorf("expected 3 active loans, got %d", len(active))
	}
}

func TestGetOverdueLoans(t *testing.T) {
	k := newTestKeeper(t)

	// 创建一笔贷款并批准
	loan, err := k.RequestLoan(&types.MsgRequestLoan{
		BorrowerAnon: []byte("anon-overdue-32-bytes-xxxxxxx"),
		Amount:       100,
		TermDays:     30,
	})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_, err = k.ApproveLoan(&types.MsgApproveLoan{
		LoanID:     loan.LoanID,
		LenderAnon: []byte("lender-32-bytes-xxxxxxxxxxxx"),
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}

	// 获取逾期贷款列表（不应 panic）
	overdue := k.GetOverdueLoans()
	// 30天贷款通常不会立即逾期
	_ = overdue
}

func TestGetAllLoans(t *testing.T) {
	k := newTestKeeper(t)

	for i := 0; i < 5; i++ {
		k.RequestLoan(&types.MsgRequestLoan{
			BorrowerAnon: []byte{byte('a' + i), 0, 0, 0, 0, 0, 0, 0, 0, 0},
			Amount:       uint64((i + 1) * 100),
			TermDays:     30,
		})
	}

	all := k.GetAllLoans()
	if len(all) != 5 {
		t.Errorf("expected 5 loans, got %d", len(all))
	}
}
