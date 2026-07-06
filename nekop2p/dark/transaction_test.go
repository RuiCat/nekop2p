package dark_test

import (
	"testing"

	"github.com/nekop2p/nekop2p/dark"
)

func TestTransactionLifecycle(t *testing.T) {
	aliceKeys, _ := dark.GenerateKeys()
	bobKeys, _ := dark.GenerateKeys()

	// 为 Alice 创建信用票据
	notes := make([]*dark.CreditNote, 3)
	for i := uint64(0); i < 3; i++ {
		n, err := aliceKeys.CreateNote(500, i)
		if err != nil {
			t.Fatalf("create note %d: %v", i, err)
		}
		notes[i] = n
	}

	// 计算周期标记
	var prevHash [32]byte
	prevHash[0] = 0xFF
	cycleMarker := dark.CycleMarker(prevHash, 100)

	// 创建交易
	tx, err := dark.NewTransaction(&dark.TxConfig{
		CycleMarker: cycleMarker,
		PartyAKeys:  aliceKeys,
		PartyBKeys:  bobKeys,
		InputNotes:  notes,
		Amount:      300,
	})
	if err != nil {
		t.Fatalf("new transaction: %v", err)
	}

	if tx.Status != dark.TxPending {
		t.Errorf("initial status: got %v, want pending", tx.Status)
	}

	// 验证（防自我交易检查）
	if err := tx.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if tx.Status != dark.TxVerified {
		t.Errorf("status after verify: got %v, want verified", tx.Status)
	}

	// 提交 — 仅选中的票据（1张500面额覆盖300）
	nullifiers, err := tx.Commit()
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if len(nullifiers) != 1 {
		t.Errorf("nullifiers: got %d, want 1 (only 1 note needed for 300)", len(nullifiers))
	}

	// 结算
	changeNotes, err := tx.Settle()
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if len(changeNotes) == 0 {
		t.Error("should have change notes")
	}
}

func TestTransactionInsufficientCredit(t *testing.T) {
	aliceKeys, _ := dark.GenerateKeys()
	bobKeys, _ := dark.GenerateKeys()

	notes := make([]*dark.CreditNote, 1)
	notes[0], _ = aliceKeys.CreateNote(100, 0)

	var prevHash [32]byte
	cycleMarker := dark.CycleMarker(prevHash, 1)

	_, err := dark.NewTransaction(&dark.TxConfig{
		CycleMarker: cycleMarker,
		PartyAKeys:  aliceKeys,
		PartyBKeys:  bobKeys,
		InputNotes:  notes,
		Amount:      500, // 超过可用额度
	})
	if err == nil {
		t.Error("should fail with insufficient credit")
	}
}

func TestAntiSelfDeal(t *testing.T) {
	aliceKeys, _ := dark.GenerateKeys()

	notes := make([]*dark.CreditNote, 3)
	for i := uint64(0); i < 3; i++ {
		notes[i], _ = aliceKeys.CreateNote(500, i)
	}

	var prevHash [32]byte
	cycleMarker := dark.CycleMarker(prevHash, 1)

	// Alice 和自己交易...!
	tx, err := dark.NewTransaction(&dark.TxConfig{
		CycleMarker: cycleMarker,
		PartyAKeys:  aliceKeys,
		PartyBKeys:  aliceKeys, // 相同密钥!
		InputNotes:  notes,
		Amount:      300,
	})
	if err != nil {
		t.Fatalf("new transaction: %v", err)
	}

	// 验证应检测到自我交易
	if err := tx.Verify(); err == nil {
		t.Error("self-dealing should be detected")
	}
}

func TestCounterIncrements(t *testing.T) {
	keys, _ := dark.GenerateKeys()

	id1 := keys.AnonID(0)
	id2 := keys.AnonID(1)

	if id1 == id2 {
		t.Error("different counters should produce different anon_ids")
	}
}
