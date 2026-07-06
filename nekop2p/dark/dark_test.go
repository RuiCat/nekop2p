package dark_test

import (
	"bytes"
	"testing"

	"github.com/nekop2p/nekop2p/dark"
)

func TestAnonIDDeterministic(t *testing.T) {
	dk, _ := dark.GenerateKeys()

	id1 := dk.AnonID(1)
	id2 := dk.AnonID(1)
	id3 := dk.AnonID(2)

	if id1 != id2 {
		t.Error("same counter should produce same anon_id")
	}
	if id1 == id3 {
		t.Error("different counters should produce different anon_ids")
	}
}

func TestAnonIDDifferentKeys(t *testing.T) {
	dk1, _ := dark.GenerateKeys()
	dk2, _ := dark.GenerateKeys()

	id1 := dk1.AnonID(1)
	id2 := dk2.AnonID(1)

	if id1 == id2 {
		t.Error("different master_secrets should produce different anon_ids")
	}
}

func TestIdentityMarker(t *testing.T) {
	dk, _ := dark.GenerateKeys()
	var cycle [32]byte
	cycle[0] = 0xAA

	marker1 := dk.IdentityMarker(cycle)
	marker2 := dk.IdentityMarker(cycle)

	if marker1 != marker2 {
		t.Error("same cycle should produce same identity marker")
	}

	// 不同周期
	cycle[0] = 0xBB
	marker3 := dk.IdentityMarker(cycle)
	if marker1 == marker3 {
		t.Error("different cycles should produce different markers")
	}
}

func TestIdentityMarkerAntiSelfDeal(t *testing.T) {
	dk, _ := dark.GenerateKeys()
	var cycle [32]byte

	// 同一个人，不同 anon_id，相同周期 → 相同身份标记
	marker1 := dk.IdentityMarker(cycle)
	marker2 := dk.IdentityMarker(cycle)

	if marker1 != marker2 {
		t.Error("same person should produce same marker (this is the anti-self-deal check)")
	}

	// 不同的人
	dk2, _ := dark.GenerateKeys()
	marker3 := dk2.IdentityMarker(cycle)

	if marker1 == marker3 {
		t.Error("different people should produce different markers")
	}
}

func TestCycleMarker(t *testing.T) {
	var prevHash [32]byte
	prevHash[0] = 0xFF

	cm1 := dark.CycleMarker(prevHash, 100)
	cm2 := dark.CycleMarker(prevHash, 100)
	cm3 := dark.CycleMarker(prevHash, 101)

	if cm1 != cm2 {
		t.Error("same inputs should produce same cycle marker")
	}
	if cm1 == cm3 {
		t.Error("different height should produce different cycle marker")
	}
}

func TestCreditNoteCreateAndSpend(t *testing.T) {
	dk, _ := dark.GenerateKeys()

	note, err := dk.CreateNote(500, 0)
	if err != nil {
		t.Fatal(err)
	}

	if note.Value != 500 {
		t.Errorf("value: got %d, want 500", note.Value)
	}
	if !note.VerifyCommitment() {
		t.Error("commitment verification failed")
	}

	// 生成 nullifier
	nullifier := dk.Nullifier(note)
	if nullifier == [32]byte{} {
		t.Error("nullifier should not be zero")
	}
}

func TestCreditNoteDoubleSpend(t *testing.T) {
	dk, _ := dark.GenerateKeys()

	note, _ := dk.CreateNote(1000, 0)

	nullifier1 := dk.Nullifier(note)
	nullifier2 := dk.Nullifier(note)

	if nullifier1 != nullifier2 {
		t.Error("same note should produce same nullifier (double-spend detection)")
	}
}

func TestCreditNoteSerialUniqueness(t *testing.T) {
	dk, _ := dark.GenerateKeys()

	note1, _ := dk.CreateNote(100, 0)
	note2, _ := dk.CreateNote(200, 1)

	if bytes.Equal(note1.Serial[:], note2.Serial[:]) {
		t.Error("different notes should have different serials")
	}
	if note1.Commitment == note2.Commitment {
		t.Error("different notes should have different commitments")
	}
}

func TestSelectNotes(t *testing.T) {
	dk, _ := dark.GenerateKeys()

	notes := []*dark.CreditNote{}
	for i := uint64(0); i < 5; i++ {
		n, _ := dk.CreateNote(100, i)
		notes = append(notes, n)
	}

	selected, change, err := dark.SelectNotes(notes, 250)
	if err != nil {
		t.Fatal(err)
	}

	total := dark.TotalValue(selected)
	if total < 250 {
		t.Errorf("selected total %d < required 250", total)
	}
	if change != total-250 {
		t.Errorf("change %d != %d-250", change, total)
	}
}

func TestSelectNotesInsufficient(t *testing.T) {
	dk, _ := dark.GenerateKeys()

	var notes []*dark.CreditNote
	for i := uint64(0); i < 3; i++ {
		n, _ := dk.CreateNote(100, i)
		notes = append(notes, n)
	}

	_, _, err := dark.SelectNotes(notes, 500)
	if err == nil {
		t.Error("should fail with insufficient credit")
	}
}
