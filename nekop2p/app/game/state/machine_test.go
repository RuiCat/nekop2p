package state

import (
	"testing"
)

func TestCreateActivity(t *testing.T) {
	gsm := NewGameStateMachine("test-game", nil)

	err := gsm.CreateActivity("raid-1", "KILL_MONSTER", 0)
	if err != nil {
		t.Fatalf("CreateActivity failed: %v", err)
	}

	err = gsm.CreateActivity("", "KILL_MONSTER", 0)
	if err == nil {
		t.Fatal("expected error for empty activity ID")
	}

	err = gsm.CreateActivity("raid-1", "KILL_MONSTER", 0)
	if err == nil {
		t.Fatal("expected error for duplicate activity")
	}

	err = gsm.CreateActivity("bad-type", "UNKNOWN", 0)
	if err == nil {
		t.Fatal("expected error for unknown activity type")
	}
}

func TestJoinActivity(t *testing.T) {
	gsm := NewGameStateMachine("test-game", nil)
	gsm.CreateActivity("raid-1", "KILL_MONSTER", 0)

	err := gsm.JoinActivity("raid-1", "player-1")
	if err != nil {
		t.Fatalf("JoinActivity failed: %v", err)
	}

	err = gsm.JoinActivity("raid-1", "player-1")
	if err == nil {
		t.Fatal("expected error for duplicate join")
	}

	err = gsm.JoinActivity("nonexistent", "player-1")
	if err == nil {
		t.Fatal("expected error for nonexistent activity")
	}

	err = gsm.JoinActivity("raid-1", "")
	if err == nil {
		t.Fatal("expected error for empty player ID")
	}
}

func TestCompleteActivity(t *testing.T) {
	gsm := NewGameStateMachine("test-game", nil)
	gsm.CreateActivity("raid-1", "KILL_MONSTER", 0)
	gsm.JoinActivity("raid-1", "player-1")

	act, err := gsm.CompleteActivity("raid-1")
	if err != nil {
		t.Fatalf("CompleteActivity failed: %v", err)
	}
	if act.ActivityID != "raid-1" {
		t.Fatalf("expected raid-1, got %s", act.ActivityID)
	}
	if len(act.Players) != 1 {
		t.Fatalf("expected 1 player, got %d", len(act.Players))
	}

	_, err = gsm.CompleteActivity("raid-1")
	if err == nil {
		t.Fatal("expected error after activity already completed")
	}
}

func TestMarketOperations(t *testing.T) {
	gsm := NewGameStateMachine("test-game", nil)
	player := gsm.AddPlayer("player-1")
	player.Gold = 500

	gsm.Apply(Action{
		Type:     ActionCraftMaterial,
		PlayerID: "player-1",
		Amount:   50,
	})

	if gsm.AssetCount() == 0 {
		t.Fatal("expected at least 1 asset after crafting")
	}

	var assetID string
	for id := range gsm.State.Assets {
		assetID = id
		break
	}

	err := gsm.ListMarketItem("player-1", assetID, 100)
	if err != nil {
		t.Fatalf("ListMarketItem failed: %v", err)
	}

	err = gsm.ListMarketItem("nonexistent", assetID, 100)
	if err == nil {
		t.Fatal("expected error for nonexistent seller")
	}

	player2 := gsm.AddPlayer("player-2")
	player2.Gold = 500

	err = gsm.BuyMarketItem("player-2", assetID)
	if err != nil {
		t.Fatalf("BuyMarketItem failed: %v", err)
	}
	if player2.Gold != 400 {
		t.Fatalf("expected 400 gold after purchase, got %d", player2.Gold)
	}

	err = gsm.BuyMarketItem("player-2", assetID)
	if err == nil {
		t.Fatal("expected error for already-purchased listing")
	}

	err = gsm.BuyMarketItem("player-1", assetID)
	if err == nil {
		t.Fatal("expected error for self-purchase")
	}
}

func TestCancelMarketListing(t *testing.T) {
	gsm := NewGameStateMachine("test-game", nil)
	player := gsm.AddPlayer("player-1")
	player.Gold = 500

	gsm.Apply(Action{
		Type:     ActionCraftMaterial,
		PlayerID: "player-1",
		Amount:   50,
	})

	var assetID string
	for id := range gsm.State.Assets {
		assetID = id
		break
	}

	err := gsm.ListMarketItem("player-1", assetID, 100)
	if err != nil {
		t.Fatalf("ListMarketItem failed: %v", err)
	}

	err = gsm.CancelMarketListing("player-1", assetID)
	if err != nil {
		t.Fatalf("CancelMarketListing failed: %v", err)
	}
}

func TestApplyKillMonsterGivesXP(t *testing.T) {
	gsm := NewGameStateMachine("test-game", nil)
	player := gsm.AddPlayer("player-1")

	transition, err := gsm.Apply(Action{
		Type:     ActionKillMonster,
		PlayerID: "player-1",
		TargetID: "dragon",
		Amount:   100,
	})
	if err != nil {
		t.Fatalf("Apply KillMonster failed: %v", err)
	}
	if player.XP < 100 {
		t.Fatalf("expected XP >= 100, got %d", player.XP)
	}
	if player.Gold == 100 {
		t.Fatal("expected gold gain from kill")
	}
	hasMonsterEvent := false
	for _, evt := range transition.Events {
		if evt.Type == "monster_killed" {
			hasMonsterEvent = true
			break
		}
	}
	if !hasMonsterEvent {
		t.Fatal("expected monster_killed event")
	}
}

func TestApplyCraftMaterialConsumesGold(t *testing.T) {
	gsm := NewGameStateMachine("test-game", nil)
	player := gsm.AddPlayer("player-1")
	initialGold := player.Gold

	_, err := gsm.Apply(Action{
		Type:     ActionCraftMaterial,
		PlayerID: "player-1",
		Amount:   50,
	})
	if err != nil {
		t.Fatalf("Apply CraftMaterial failed: %v", err)
	}
	if player.Gold >= initialGold {
		t.Fatal("expected gold to be consumed")
	}
}

func TestApplyTradeTransfersItems(t *testing.T) {
	gsm := NewGameStateMachine("test-game", nil)
	seller := gsm.AddPlayer("seller-1")
	seller.Gold = 500
	buyer := gsm.AddPlayer("buyer-11")
	buyer.Gold = 500

	gsm.Apply(Action{
		Type:     ActionCraftMaterial,
		PlayerID: "seller-1",
		Amount:   50,
	})

	var assetID string
	for id := range gsm.State.Assets {
		assetID = id
		break
	}

	_, err := gsm.Apply(Action{
		Type:     ActionTrade,
		PlayerID: "seller-1",
		TargetID: "buyer-11",
		Amount:   80,
		Data:     []byte(assetID),
	})
	if err != nil {
		t.Fatalf("Apply Trade failed: %v", err)
	}

	asset := gsm.State.Assets[assetID]
	if asset.OwnerID != "buyer-11" {
		t.Fatalf("expected owner to be buyer, got %s", asset.OwnerID)
	}
}
