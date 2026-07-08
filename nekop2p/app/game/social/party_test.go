package social

import (
	"testing"
)

func TestCreateParty(t *testing.T) {
	pm := NewPartyManager()

	p, err := pm.CreateParty("leader-1")
	if err != nil {
		t.Fatalf("CreateParty failed: %v", err)
	}
	if p.LeaderID != "leader-1" {
		t.Fatalf("expected leader-1, got %s", p.LeaderID)
	}
	if len(p.Members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(p.Members))
	}

	_, err = pm.CreateParty("leader-1")
	if err == nil {
		t.Fatal("expected error: leader already in a party")
	}
}

func TestInviteToPartyDoesNotAutoAdd(t *testing.T) {
	pm := NewPartyManager()
	pm.CreateParty("leader-1")
	partyID := pm.GetPlayerParty("leader-1").PartyID

	err := pm.InviteToParty(partyID, "leader-1", "player-2")
	if err != nil {
		t.Fatalf("InviteToParty failed: %v", err)
	}

	if pm.GetPlayerParty("player-2") != nil {
		t.Fatal("expected player-2 NOT to be in a party after invite only")
	}
}

func TestRecordKillContribution(t *testing.T) {
	pm := NewPartyManager()
	pm.CreateParty("leader-1")
	partyID := pm.GetPlayerParty("leader-1").PartyID

	pm.RecordKillContribution(partyID, "leader-1", 10)
	pm.RecordKillContribution(partyID, "leader-1", 5)

	contribs := pm.GetPartyKillContributions(partyID)
	if contribs["leader-1"] != 15 {
		t.Fatalf("expected 15 kills, got %d", contribs["leader-1"])
	}
}

func TestGetPartyKillContributionsNonDestructive(t *testing.T) {
	pm := NewPartyManager()
	pm.CreateParty("leader-1")
	partyID := pm.GetPlayerParty("leader-1").PartyID

	pm.RecordKillContribution(partyID, "leader-1", 10)

	first := pm.GetPartyKillContributions(partyID)
	if first["leader-1"] != 10 {
		t.Fatalf("first read: expected 10, got %d", first["leader-1"])
	}

	second := pm.GetPartyKillContributions(partyID)
	if second["leader-1"] != 10 {
		t.Fatalf("second read: expected 10 (non-destructive), got %d", second["leader-1"])
	}

	err := pm.ResetKillContributions(partyID)
	if err != nil {
		t.Fatalf("ResetKillContributions failed: %v", err)
	}

	third := pm.GetPartyKillContributions(partyID)
	if len(third) != 0 {
		t.Fatalf("expected empty after reset, got %d entries", len(third))
	}
}
