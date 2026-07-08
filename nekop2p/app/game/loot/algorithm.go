package loot

import (
	"math"
)

type LootTable struct {
	RegionID      string
	BaseValue     uint64
	ScalingFactor float64
	MaxValue      uint64
	Tables        []RarityTable
}

type RarityTable struct {
	Rarity int
	Weight float64
	Items  []LootItem
}

type LootItem struct {
	AssetType string
	Name      string
	MinAmount uint32
	MaxAmount uint32
}

type PlayerContribution struct {
	PlayerID         string
	KillContribution uint32
}

type LootAllocation struct {
	PlayerID string
	Value    uint64
	Items    []LootDrop
}

type LootDrop struct {
	AssetType string
	Name      string
	Quantity  uint32
}

func NewLootTable(regionID string, baseValue uint64, scalingFactor float64, maxValue uint64) *LootTable {
	return &LootTable{
		RegionID:      regionID,
		BaseValue:     baseValue,
		ScalingFactor: scalingFactor,
		MaxValue:      maxValue,
		Tables:        make([]RarityTable, 0),
	}
}

func (lt *LootTable) AddRarityTable(rarity int, weight float64, items []LootItem) {
	lt.Tables = append(lt.Tables, RarityTable{
		Rarity: rarity,
		Weight: weight,
		Items:  items,
	})
}

func CalculateLootDistribution(table *LootTable, participants []PlayerContribution, randomSeed uint64) []LootAllocation {
	if table == nil || len(participants) == 0 {
		return nil
	}

	vTotal := calculateTotalValue(table, participants)

	allocations := make([]LootAllocation, 0, len(participants))
	for _, pc := range participants {
		ki := float64(pc.KillContribution)
		playerSeed := randomSeed + uint64(hashString(pc.PlayerID))

		vi := uint64(ki * float64(vTotal))

		items := RollLoot(table, vi, playerSeed)

		allocations = append(allocations, LootAllocation{
			PlayerID: pc.PlayerID,
			Value:    vi,
			Items:    items,
		})
	}

	return allocations
}

func calculateTotalValue(table *LootTable, participants []PlayerContribution) uint64 {
	p := len(participants)
	if p == 0 {
		return 0
	}

	alpha := table.ScalingFactor
	if alpha <= 0 {
		alpha = 0.3
	}

	vTotal := float64(table.BaseValue) * (1 + alpha*math.Log(float64(p)+1))

	result := uint64(vTotal)
	if table.MaxValue > 0 && result > table.MaxValue {
		result = table.MaxValue
	}
	return result
}

func RollLoot(table *LootTable, value uint64, randomSeed uint64) []LootDrop {
	if table == nil || value == 0 || len(table.Tables) == 0 {
		return nil
	}

	rarity := rollRarity(table.Tables, randomSeed)

	if rarity < 0 || rarity >= len(table.Tables) {
		return nil
	}

	rt := table.Tables[rarity]
	if len(rt.Items) == 0 {
		return nil
	}

	itemIdx := rollInt(uint64(len(rt.Items)), randomSeed+1)
	item := rt.Items[itemIdx]

	quantity := item.MinAmount
	if item.MaxAmount > item.MinAmount {
		rangeSize := uint64(item.MaxAmount - item.MinAmount + 1)
		roll := rollInt(rangeSize, randomSeed+2)
		quantity = item.MinAmount + uint32(roll)
	}

	return []LootDrop{
		{
			AssetType: item.AssetType,
			Name:      item.Name,
			Quantity:  quantity,
		},
	}
}

func rollRarity(tables []RarityTable, seed uint64) int {
	totalWeight := 0.0
	for _, t := range tables {
		totalWeight += t.Weight
	}

	if totalWeight <= 0 {
		return 0
	}

	roll := rollFloat(seed) * totalWeight
	cumulative := 0.0
	for i, t := range tables {
		cumulative += t.Weight
		if roll < cumulative {
			return i
		}
	}

	return len(tables) - 1
}

func rollFloat(seed uint64) float64 {
	const (
		fnvOffset uint64 = 14695981039346656037
		fnvPrime  uint64 = 1099511628211
	)
	h := fnvOffset ^ seed
	h *= fnvPrime
	return float64(h%1000000) / 1000000.0
}

func rollInt(max uint64, seed uint64) uint64 {
	if max == 0 {
		return 0
	}
	f := rollFloat(seed)
	return uint64(f * float64(max))
}

func hashString(s string) uint64 {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

func DefaultLootTable() *LootTable {
	lt := NewLootTable("default", 100, 0.4, 5000)

	lt.AddRarityTable(0, 0.55, []LootItem{
		{AssetType: "material", Name: "Iron Ore", MinAmount: 1, MaxAmount: 5},
		{AssetType: "material", Name: "Wood Log", MinAmount: 2, MaxAmount: 8},
		{AssetType: "consumable", Name: "Health Potion", MinAmount: 1, MaxAmount: 3},
	})

	lt.AddRarityTable(1, 0.25, []LootItem{
		{AssetType: "material", Name: "Silver Ore", MinAmount: 1, MaxAmount: 3},
		{AssetType: "equipment", Name: "Iron Sword", MinAmount: 1, MaxAmount: 1},
		{AssetType: "consumable", Name: "Mana Potion", MinAmount: 1, MaxAmount: 2},
	})

	lt.AddRarityTable(2, 0.12, []LootItem{
		{AssetType: "material", Name: "Gold Ore", MinAmount: 1, MaxAmount: 2},
		{AssetType: "equipment", Name: "Steel Armor", MinAmount: 1, MaxAmount: 1},
		{AssetType: "cosmetic", Name: "Enchanted Cape", MinAmount: 1, MaxAmount: 1},
	})

	lt.AddRarityTable(3, 0.06, []LootItem{
		{AssetType: "material", Name: "Mithril Ore", MinAmount: 1, MaxAmount: 1},
		{AssetType: "equipment", Name: "Dragon Blade", MinAmount: 1, MaxAmount: 1},
		{AssetType: "cosmetic", Name: "Phoenix Feather", MinAmount: 1, MaxAmount: 1},
	})

	lt.AddRarityTable(4, 0.02, []LootItem{
		{AssetType: "equipment", Name: "Excalibur", MinAmount: 1, MaxAmount: 1},
		{AssetType: "cosmetic", Name: "Crown of Stars", MinAmount: 1, MaxAmount: 1},
		{AssetType: "certificate", Name: "Title Deed", MinAmount: 1, MaxAmount: 1},
	})

	return lt
}
