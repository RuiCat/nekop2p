package loot

import (
	"testing"
)

func TestCalculateLootDistributionDeterministic(t *testing.T) {
	table := DefaultLootTable()

	participants := []PlayerContribution{
		{PlayerID: "player-a", KillContribution: 3},
		{PlayerID: "player-b", KillContribution: 5},
		{PlayerID: "player-c", KillContribution: 2},
	}

	seed := uint64(42)
	result1 := CalculateLootDistribution(table, participants, seed)
	result2 := CalculateLootDistribution(table, participants, seed)

	if len(result1) != len(result2) {
		t.Fatal("same seed should produce same number of allocations")
	}

	for i := range result1 {
		if result1[i].PlayerID != result2[i].PlayerID {
			t.Fatalf("player mismatch at idx %d: %s vs %s", i, result1[i].PlayerID, result2[i].PlayerID)
		}
		if result1[i].Value != result2[i].Value {
			t.Fatalf("value mismatch at idx %d: %d vs %d", i, result1[i].Value, result2[i].Value)
		}
		if len(result1[i].Items) != len(result2[i].Items) {
			t.Fatalf("item count mismatch at idx %d", i)
		}
		if len(result1[i].Items) > 0 && len(result2[i].Items) > 0 {
			if result1[i].Items[0].Name != result2[i].Items[0].Name {
				t.Fatalf("item name mismatch at idx %d: %s vs %s",
					i, result1[i].Items[0].Name, result2[i].Items[0].Name)
			}
			if result1[i].Items[0].Quantity != result2[i].Items[0].Quantity {
				t.Fatalf("item quantity mismatch at idx %d: %d vs %d",
					i, result1[i].Items[0].Quantity, result2[i].Items[0].Quantity)
			}
		}
	}
}

func TestCalculateLootDistributionDifferentSeed(t *testing.T) {
	table := DefaultLootTable()

	participants := []PlayerContribution{
		{PlayerID: "player-a", KillContribution: 5},
	}

	result1 := CalculateLootDistribution(table, participants, 1)
	result2 := CalculateLootDistribution(table, participants, 999)

	if len(result1) == 0 || len(result2) == 0 {
		t.Fatal("expected non-empty results")
	}
	if result1[0].Items[0].Name != result2[0].Items[0].Name &&
		result1[0].Items[0].Quantity == result2[0].Items[0].Quantity {
		t.Log("different seeds happened to produce same result (unlikely but possible)")
	}
}

func TestCalculateLootDistributionEmpty(t *testing.T) {
	result := CalculateLootDistribution(nil, nil, 0)
	if result != nil {
		t.Fatal("expected nil for nil table and nil participants")
	}

	table := DefaultLootTable()
	result = CalculateLootDistribution(table, nil, 0)
	if result != nil {
		t.Fatal("expected nil for empty participants")
	}
}

func TestRollLootDeterministic(t *testing.T) {
	table := DefaultLootTable()

	drops1 := RollLoot(table, 100, 42)
	drops2 := RollLoot(table, 100, 42)

	if len(drops1) != len(drops2) {
		t.Fatal("same seed should produce same drops")
	}
	if drops1[0].Name != drops2[0].Name {
		t.Fatalf("name mismatch: %s vs %s", drops1[0].Name, drops2[0].Name)
	}
	if drops1[0].Quantity != drops2[0].Quantity {
		t.Fatalf("quantity mismatch: %d vs %d", drops1[0].Quantity, drops2[0].Quantity)
	}
}
