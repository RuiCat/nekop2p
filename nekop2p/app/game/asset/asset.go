package asset

import (
	"fmt"
	"time"

	"github.com/nekop2p/nekop2p/app/game/state"
)

const MaxInventorySlots = 100

type AssetType int

const (
	AssetMaterial    AssetType = 0
	AssetEquipment   AssetType = 1
	AssetConsumable  AssetType = 2
	AssetCosmetic    AssetType = 3
	AssetCertificate AssetType = 4
	AssetGold        AssetType = 5
)

type Asset struct {
	AssetID  string
	Type     AssetType
	Name     string
	Rarity   int
	Owner    string
	Metadata map[string]interface{}
	History  []OwnershipRecord
}

type OwnershipRecord struct {
	From   string
	To     string
	At     int64
	TxHash string
}

type Recipe struct {
	RecipeID string
	Name     string
	Inputs   []RecipeIngredient
	Outputs  []RecipeOutput
	MinLevel uint32
}

type RecipeIngredient struct {
	AssetID  string
	Quantity uint32
}

type RecipeOutput struct {
	AssetType AssetType
	Rarity    int
	Name      string
	MinStat   uint32
	MaxStat   uint32
}

func NewAsset(assetID string, assetType AssetType, name string, rarity int, owner string) *Asset {
	return &Asset{
		AssetID:  assetID,
		Type:     assetType,
		Name:     name,
		Rarity:   rarity,
		Owner:    owner,
		Metadata: make(map[string]interface{}),
		History:  make([]OwnershipRecord, 0),
	}
}

func NewRecipe(recipeID, name string, minLevel uint32, inputs []RecipeIngredient, outputs []RecipeOutput) *Recipe {
	return &Recipe{
		RecipeID: recipeID,
		Name:     name,
		Inputs:   inputs,
		Outputs:  outputs,
		MinLevel: minLevel,
	}
}

func TransferOwnership(asset *Asset, from, to string) error {
	if asset == nil {
		return fmt.Errorf("asset: nil asset")
	}
	if asset.Owner != from {
		return fmt.Errorf("asset: owner mismatch: expected %s, got %s", from, asset.Owner)
	}
	if from == to {
		return fmt.Errorf("asset: cannot transfer to same owner")
	}

	record := OwnershipRecord{
		From:   from,
		To:     to,
		At:     time.Now().Unix(),
		TxHash: "",
	}

	asset.Owner = to
	asset.History = append(asset.History, record)
	return nil
}

func TransferOwnershipWithTx(asset *Asset, from, to, txHash string) error {
	if asset == nil {
		return fmt.Errorf("asset: nil asset")
	}
	if asset.Owner != from {
		return fmt.Errorf("asset: owner mismatch: expected %s, got %s", from, asset.Owner)
	}
	if from == to {
		return fmt.Errorf("asset: cannot transfer to same owner")
	}

	record := OwnershipRecord{
		From:   from,
		To:     to,
		At:     time.Now().Unix(),
		TxHash: txHash,
	}

	asset.Owner = to
	asset.History = append(asset.History, record)
	return nil
}

func CraftMaterial(player *state.PlayerState, recipe *Recipe, ingredients map[string]uint32) (*Asset, error) {
	if player == nil {
		return nil, fmt.Errorf("asset: nil player")
	}
	if recipe == nil {
		return nil, fmt.Errorf("asset: nil recipe")
	}
	if player.Level < recipe.MinLevel {
		return nil, fmt.Errorf("asset: player level %d < required level %d", player.Level, recipe.MinLevel)
	}

	for _, input := range recipe.Inputs {
		held, ok := ingredients[input.AssetID]
		if !ok || held < input.Quantity {
			return nil, fmt.Errorf("asset: insufficient ingredient %s: need %d, have %d",
				input.AssetID, input.Quantity, held)
		}
	}

	craftCost := uint64(len(recipe.Inputs)) * 10
	if player.Gold < craftCost {
		return nil, fmt.Errorf("asset: insufficient gold for crafting: need %d, have %d", craftCost, player.Gold)
	}
	player.Gold -= craftCost

	newAssetID := fmt.Sprintf("%s-craft-%s-%d", player.PlayerID[:8], recipe.RecipeID, time.Now().UnixNano())
	outputType := AssetEquipment
	outputRarity := 0
	outputName := recipe.Name + " Result"
	if len(recipe.Outputs) > 0 {
		outputType = recipe.Outputs[0].AssetType
		outputRarity = recipe.Outputs[0].Rarity
		outputName = recipe.Outputs[0].Name
	}

	asset := NewAsset(newAssetID, outputType, outputName, outputRarity, player.PlayerID)
	asset.Metadata["recipe_id"] = recipe.RecipeID
	asset.Metadata["crafted_at"] = time.Now().Unix()

	if len(player.Inventory) >= MaxInventorySlots {
		return nil, fmt.Errorf("asset: inventory full (%d items)", MaxInventorySlots)
	}
	player.Inventory = append(player.Inventory, newAssetID)

	return asset, nil
}

func GetRarityLabel(rarity int) string {
	switch rarity {
	case 0:
		return "common"
	case 1:
		return "fine"
	case 2:
		return "rare"
	case 3:
		return "epic"
	case 4:
		return "legendary"
	default:
		return "unknown"
	}
}

func GetAssetTypeLabel(t AssetType) string {
	switch t {
	case AssetMaterial:
		return "material"
	case AssetEquipment:
		return "equipment"
	case AssetConsumable:
		return "consumable"
	case AssetCosmetic:
		return "cosmetic"
	case AssetCertificate:
		return "certificate"
	case AssetGold:
		return "gold"
	default:
		return "unknown"
	}
}

func ValidateAsset(a *Asset) error {
	if a == nil {
		return fmt.Errorf("asset: nil asset")
	}
	if a.AssetID == "" {
		return fmt.Errorf("asset: empty AssetID")
	}
	if a.Owner == "" {
		return fmt.Errorf("asset: empty Owner")
	}
	if a.Rarity < 0 || a.Rarity > 4 {
		return fmt.Errorf("asset: invalid rarity %d", a.Rarity)
	}
	if a.Name == "" {
		return fmt.Errorf("asset: empty Name")
	}
	if a.Type < 0 || a.Type > 5 {
		return fmt.Errorf("asset: invalid type %d", a.Type)
	}
	if a.Metadata == nil {
		return fmt.Errorf("asset: nil Metadata")
	}
	return nil
}
