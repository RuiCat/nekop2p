package state

import (
	"fmt"
	"sync"
	"time"
)

type GameStateMachine struct {
	GameID  string
	Version uint32
	State   *GameState
	Rules   *GameRules
	mu      sync.RWMutex
}

type GameState struct {
	Players    map[string]*PlayerState
	Assets     map[string]*AssetRecord
	Regions    map[string]*RegionState
	Activities map[string]*ActivityState
	Market     *MarketState
}

type PlayerState struct {
	PlayerID  string
	Level     uint32
	XP        uint64
	Gold      uint64
	Inventory []string
	Skills    map[string]uint32
}

type AssetRecord struct {
	AssetID   string
	OwnerID   string
	CreatedAt int64
}

type RegionState struct {
	RegionID  string
	MaxPlayers uint32
	Players   []string
}

type ActivityState struct {
	ActivityID string
	Type       ActionType
	StartedAt  int64
	Players    []string
}

type MarketState struct {
	Listings []MarketListing
}

type MarketListing struct {
	AssetID  string
	SellerID string
	Price    uint64
}

type GameRules struct {
	MaxLevel             uint32
	XPPerLevel           uint64
	MaxGold              uint64
	MaxInventorySlots    uint32
	RegionPlayerLimit    uint32
	ActivityTimeoutSecs  int64
}

type ActionType int

const (
	ActionKillMonster    ActionType = 0
	ActionCraftMaterial  ActionType = 1
	ActionTrade          ActionType = 2
	ActionPurchaseMall   ActionType = 3
	ActionEnterRegion    ActionType = 4
	ActionLeaveRegion    ActionType = 5
)

type Action struct {
	Type     ActionType
	PlayerID string
	TargetID string
	Amount   uint64
	Data     []byte
}

type StateTransition struct {
	Action Action
	Events []GameEvent
}

type GameEvent struct {
	Type     string
	PlayerID string
	Data     map[string]interface{}
}

func NewGameStateMachine(gameID string, rules *GameRules) *GameStateMachine {
	if rules == nil {
		rules = DefaultRules()
	}
	return &GameStateMachine{
		GameID:  gameID,
		Version: 1,
		State: &GameState{
			Players:    make(map[string]*PlayerState),
			Assets:     make(map[string]*AssetRecord),
			Regions:    make(map[string]*RegionState),
			Activities: make(map[string]*ActivityState),
			Market:     &MarketState{},
		},
		Rules: rules,
	}
}

func DefaultRules() *GameRules {
	return &GameRules{
		MaxLevel:            100,
		XPPerLevel:          1000,
		MaxGold:             999999999,
		MaxInventorySlots:   200,
		RegionPlayerLimit:   50,
		ActivityTimeoutSecs: 3600,
	}
}

func (gsm *GameStateMachine) GetState() *GameState {
	gsm.mu.RLock()
	defer gsm.mu.RUnlock()
	cp := *gsm.State
	cp.Players = make(map[string]*PlayerState, len(gsm.State.Players))
	for k, v := range gsm.State.Players {
		playerCopy := *v
		invCopy := make([]string, len(v.Inventory))
		copy(invCopy, v.Inventory)
		skillsCopy := make(map[string]uint32, len(v.Skills))
		for sk, sv := range v.Skills {
			skillsCopy[sk] = sv
		}
		playerCopy.Inventory = invCopy
		playerCopy.Skills = skillsCopy
		cp.Players[k] = &playerCopy
	}
	return &cp
}

func (gsm *GameStateMachine) Apply(action Action) (*StateTransition, error) {
	gsm.mu.Lock()
	defer gsm.mu.Unlock()

	if action.PlayerID == "" {
		return nil, fmt.Errorf("state: action requires PlayerID")
	}

	events := make([]GameEvent, 0)

	switch action.Type {
	case ActionKillMonster:
		evts, err := gsm.applyKillMonster(action)
		if err != nil {
			return nil, err
		}
		events = append(events, evts...)
	case ActionCraftMaterial:
		evts, err := gsm.applyCraftMaterial(action)
		if err != nil {
			return nil, err
		}
		events = append(events, evts...)
	case ActionTrade:
		evts, err := gsm.applyTrade(action)
		if err != nil {
			return nil, err
		}
		events = append(events, evts...)
	case ActionPurchaseMall:
		evts, err := gsm.applyPurchaseMall(action)
		if err != nil {
			return nil, err
		}
		events = append(events, evts...)
	case ActionEnterRegion:
		evts, err := gsm.applyEnterRegion(action)
		if err != nil {
			return nil, err
		}
		events = append(events, evts...)
	case ActionLeaveRegion:
		evts, err := gsm.applyLeaveRegion(action)
		if err != nil {
			return nil, err
		}
		events = append(events, evts...)
	default:
		return nil, fmt.Errorf("state: unknown action type: %d", action.Type)
	}

	return &StateTransition{
		Action: action,
		Events: events,
	}, nil
}

func (gsm *GameStateMachine) GetPlayer(playerID string) *PlayerState {
	gsm.mu.RLock()
	defer gsm.mu.RUnlock()
	return gsm.State.Players[playerID]
}

func (gsm *GameStateMachine) ensurePlayer(playerID string) *PlayerState {
	p, ok := gsm.State.Players[playerID]
	if !ok {
		p = &PlayerState{
			PlayerID:  playerID,
			Level:     1,
			XP:        0,
			Gold:      100,
			Inventory: make([]string, 0),
			Skills:    make(map[string]uint32),
		}
		gsm.State.Players[playerID] = p
	}
	return p
}

func (gsm *GameStateMachine) AddPlayer(playerID string) *PlayerState {
	gsm.mu.Lock()
	defer gsm.mu.Unlock()
	return gsm.ensurePlayer(playerID)
}

func (gsm *GameStateMachine) addXP(player *PlayerState, amount uint64) []GameEvent {
	player.XP += amount
	events := make([]GameEvent, 0)
	xpNeeded := uint64(player.Level) * gsm.Rules.XPPerLevel
	if player.XP >= xpNeeded && player.Level < gsm.Rules.MaxLevel {
		player.Level++
		player.XP -= xpNeeded
		events = append(events, GameEvent{
			Type:     "level_up",
			PlayerID: player.PlayerID,
			Data: map[string]interface{}{
				"new_level": player.Level,
			},
		})
	}
	return events
}

func (gsm *GameStateMachine) addGold(player *PlayerState, amount uint64) []GameEvent {
	if player.Gold+amount > gsm.Rules.MaxGold {
		player.Gold = gsm.Rules.MaxGold
	} else {
		player.Gold += amount
	}
	return []GameEvent{{
		Type:     "gold_change",
		PlayerID: player.PlayerID,
		Data: map[string]interface{}{
			"amount": amount,
			"new_balance": player.Gold,
		},
	}}
}

func (gsm *GameStateMachine) deductGold(player *PlayerState, amount uint64) error {
	if player.Gold < amount {
		return fmt.Errorf("state: insufficient gold: have %d, need %d", player.Gold, amount)
	}
	player.Gold -= amount
	return nil
}

func (gsm *GameStateMachine) applyKillMonster(action Action) ([]GameEvent, error) {
	player := gsm.ensurePlayer(action.PlayerID)
	events := make([]GameEvent, 0)

	xpGain := action.Amount
	if xpGain == 0 {
		xpGain = uint64(player.Level) * 10
	}
	evts := gsm.addXP(player, xpGain)
	events = append(events, evts...)

	goldGain := uint64(player.Level) * 5
	evts = gsm.addGold(player, goldGain)
	events = append(events, evts...)

	events = append(events, GameEvent{
		Type:     "monster_killed",
		PlayerID: action.PlayerID,
		Data: map[string]interface{}{
			"target_id": action.TargetID,
			"xp":        xpGain,
			"gold":      goldGain,
		},
	})

	return events, nil
}

func (gsm *GameStateMachine) applyCraftMaterial(action Action) ([]GameEvent, error) {
	player := gsm.ensurePlayer(action.PlayerID)

	cost := action.Amount
	if cost == 0 {
		cost = 50
	}
	if err := gsm.deductGold(player, cost); err != nil {
		return nil, err
	}

	assetID := fmt.Sprintf("%s-crafted-%d", action.PlayerID[:8], time.Now().UnixNano())
	gsm.State.Assets[assetID] = &AssetRecord{
		AssetID:   assetID,
		OwnerID:   action.PlayerID,
		CreatedAt: time.Now().Unix(),
	}

	if len(player.Inventory) < int(gsm.Rules.MaxInventorySlots) {
		player.Inventory = append(player.Inventory, assetID)
	}

	return []GameEvent{{
		Type:     "craft_complete",
		PlayerID: action.PlayerID,
		Data: map[string]interface{}{
			"asset_id": assetID,
			"cost":     cost,
		},
	}}, nil
}

func (gsm *GameStateMachine) applyTrade(action Action) ([]GameEvent, error) {
	if action.TargetID == "" {
		return nil, fmt.Errorf("state: trade requires TargetID (buyer)")
	}
	seller := gsm.State.Players[action.PlayerID]
	if seller == nil {
		return nil, fmt.Errorf("state: seller %s not found", action.PlayerID)
	}
	buyer := gsm.ensurePlayer(action.TargetID)

	assetID := string(action.Data)
	assetRecord, ok := gsm.State.Assets[assetID]
	if !ok {
		return nil, fmt.Errorf("state: asset %s not found", assetID)
	}
	if assetRecord.OwnerID != action.PlayerID {
		return nil, fmt.Errorf("state: seller %s does not own asset %s", action.PlayerID, assetID)
	}

	price := action.Amount
	if err := gsm.deductGold(buyer, price); err != nil {
		return nil, err
	}
	gsm.addGold(seller, price)

	assetRecord.OwnerID = action.TargetID

	sellerInv := make([]string, 0, len(seller.Inventory))
	for _, id := range seller.Inventory {
		if id != assetID {
			sellerInv = append(sellerInv, id)
		}
	}
	seller.Inventory = sellerInv

	if len(buyer.Inventory) < int(gsm.Rules.MaxInventorySlots) {
		buyer.Inventory = append(buyer.Inventory, assetID)
	}

	return []GameEvent{{
		Type:     "trade_complete",
		PlayerID: action.PlayerID,
		Data: map[string]interface{}{
			"buyer":    action.TargetID,
			"asset_id": assetID,
			"price":    price,
		},
	}}, nil
}

func (gsm *GameStateMachine) applyPurchaseMall(action Action) ([]GameEvent, error) {
	player := gsm.ensurePlayer(action.PlayerID)

	price := action.Amount
	if price == 0 {
		price = 100
	}
	if err := gsm.deductGold(player, price); err != nil {
		return nil, err
	}

	assetID := fmt.Sprintf("%s-mall-%d", action.PlayerID[:8], time.Now().UnixNano())
	gsm.State.Assets[assetID] = &AssetRecord{
		AssetID:   assetID,
		OwnerID:   action.PlayerID,
		CreatedAt: time.Now().Unix(),
	}

	if len(player.Inventory) < int(gsm.Rules.MaxInventorySlots) {
		player.Inventory = append(player.Inventory, assetID)
	}

	return []GameEvent{{
		Type:     "purchase_complete",
		PlayerID: action.PlayerID,
		Data: map[string]interface{}{
			"item_id":  action.TargetID,
			"asset_id": assetID,
			"price":    price,
		},
	}}, nil
}

func (gsm *GameStateMachine) applyEnterRegion(action Action) ([]GameEvent, error) {
	gsm.ensurePlayer(action.PlayerID)

	region, ok := gsm.State.Regions[action.TargetID]
	if !ok {
		region = &RegionState{
			RegionID:   action.TargetID,
			MaxPlayers: gsm.Rules.RegionPlayerLimit,
			Players:    make([]string, 0),
		}
		gsm.State.Regions[action.TargetID] = region
	}

	if uint32(len(region.Players)) >= region.MaxPlayers {
		return nil, fmt.Errorf("state: region %s is full (%d/%d)", action.TargetID, len(region.Players), region.MaxPlayers)
	}

	for _, pid := range region.Players {
		if pid == action.PlayerID {
			return nil, nil
		}
	}

	region.Players = append(region.Players, action.PlayerID)

	return []GameEvent{{
		Type:     "region_enter",
		PlayerID: action.PlayerID,
		Data: map[string]interface{}{
			"region_id":    action.TargetID,
			"player_count": len(region.Players),
		},
	}}, nil
}

func (gsm *GameStateMachine) applyLeaveRegion(action Action) ([]GameEvent, error) {
	gsm.ensurePlayer(action.PlayerID)

	region, ok := gsm.State.Regions[action.TargetID]
	if !ok {
		return nil, nil
	}

	for i, pid := range region.Players {
		if pid == action.PlayerID {
			region.Players = append(region.Players[:i], region.Players[i+1:]...)
			break
		}
	}

	return []GameEvent{{
		Type:     "region_leave",
		PlayerID: action.PlayerID,
		Data: map[string]interface{}{
			"region_id":    action.TargetID,
			"player_count": len(region.Players),
		},
	}}, nil
}

func (gsm *GameStateMachine) CreateActivity(activityID, activityType string, startedAt int64) error {
	gsm.mu.Lock()
	defer gsm.mu.Unlock()

	if activityID == "" {
		return fmt.Errorf("state: activity ID cannot be empty")
	}
	if _, exists := gsm.State.Activities[activityID]; exists {
		return fmt.Errorf("state: activity %s already exists", activityID)
	}

	var at ActionType
	switch activityType {
	case "KILL_MONSTER":
		at = ActionKillMonster
	case "CRAFT_MATERIAL":
		at = ActionCraftMaterial
	case "TRADE":
		at = ActionTrade
	default:
		return fmt.Errorf("state: unknown activity type: %s", activityType)
	}

	if startedAt == 0 {
		startedAt = time.Now().Unix()
	}

	gsm.State.Activities[activityID] = &ActivityState{
		ActivityID: activityID,
		Type:       at,
		StartedAt:  startedAt,
		Players:    make([]string, 0),
	}
	return nil
}

func (gsm *GameStateMachine) JoinActivity(activityID, playerID string) error {
	gsm.mu.Lock()
	defer gsm.mu.Unlock()

	act, ok := gsm.State.Activities[activityID]
	if !ok {
		return fmt.Errorf("state: activity %s not found", activityID)
	}
	if playerID == "" {
		return fmt.Errorf("state: player ID cannot be empty")
	}

	for _, pid := range act.Players {
		if pid == playerID {
			return fmt.Errorf("state: player %s already joined activity %s", playerID, activityID)
		}
	}

	act.Players = append(act.Players, playerID)
	return nil
}

func (gsm *GameStateMachine) CompleteActivity(activityID string) (*ActivityState, error) {
	gsm.mu.Lock()
	defer gsm.mu.Unlock()

	act, ok := gsm.State.Activities[activityID]
	if !ok {
		return nil, fmt.Errorf("state: activity %s not found", activityID)
	}

	delete(gsm.State.Activities, activityID)
	return act, nil
}

func (gsm *GameStateMachine) CleanupExpiredActivities() int {
	gsm.mu.Lock()
	defer gsm.mu.Unlock()

	now := time.Now().Unix()
	timeout := gsm.Rules.ActivityTimeoutSecs
	removed := 0
	for id, act := range gsm.State.Activities {
		if now-act.StartedAt > timeout {
			delete(gsm.State.Activities, id)
			removed++
		}
	}
	return removed
}

func (gsm *GameStateMachine) ListMarketItem(sellerID, assetID string, price uint64) error {
	gsm.mu.Lock()
	defer gsm.mu.Unlock()

	if price == 0 {
		return fmt.Errorf("state: price must be positive")
	}

	seller := gsm.State.Players[sellerID]
	if seller == nil {
		return fmt.Errorf("state: seller %s not found", sellerID)
	}

	assetRecord, ok := gsm.State.Assets[assetID]
	if !ok {
		return fmt.Errorf("state: asset %s not found", assetID)
	}
	if assetRecord.OwnerID != sellerID {
		return fmt.Errorf("state: seller %s does not own asset %s", sellerID, assetID)
	}

	sellerInv := make([]string, 0, len(seller.Inventory))
	found := false
	for _, id := range seller.Inventory {
		if id == assetID {
			found = true
			continue
		}
		sellerInv = append(sellerInv, id)
	}
	if !found {
		return fmt.Errorf("state: asset %s not in seller inventory", assetID)
	}
	seller.Inventory = sellerInv

	gsm.State.Market.Listings = append(gsm.State.Market.Listings, MarketListing{
		AssetID:  assetID,
		SellerID: sellerID,
		Price:    price,
	})
	return nil
}

func (gsm *GameStateMachine) BuyMarketItem(buyerID, listingID string) error {
	gsm.mu.Lock()
	defer gsm.mu.Unlock()

	buyer := gsm.State.Players[buyerID]
	if buyer == nil {
		return fmt.Errorf("state: buyer %s not found", buyerID)
	}

	listingIdx := findMarketListing(gsm.State.Market.Listings, listingID)
	if listingIdx == -1 {
		return fmt.Errorf("state: listing %s not found", listingID)
	}

	listing := gsm.State.Market.Listings[listingIdx]
	if buyerID == listing.SellerID {
		return fmt.Errorf("state: cannot buy your own listing")
	}

	if err := gsm.deductGold(buyer, listing.Price); err != nil {
		return err
	}

	seller := gsm.ensurePlayer(listing.SellerID)
	gsm.addGold(seller, listing.Price)

	assetRecord, ok := gsm.State.Assets[listing.AssetID]
	if ok {
		assetRecord.OwnerID = buyerID
	}

	if len(buyer.Inventory) < int(gsm.Rules.MaxInventorySlots) {
		buyer.Inventory = append(buyer.Inventory, listing.AssetID)
	}

	gsm.State.Market.Listings = append(
		gsm.State.Market.Listings[:listingIdx],
		gsm.State.Market.Listings[listingIdx+1:]...,
	)
	return nil
}

func (gsm *GameStateMachine) CancelMarketListing(sellerID, listingID string) error {
	gsm.mu.Lock()
	defer gsm.mu.Unlock()

	seller := gsm.State.Players[sellerID]
	if seller == nil {
		return fmt.Errorf("state: seller %s not found", sellerID)
	}

	listingIdx := findMarketListing(gsm.State.Market.Listings, listingID)
	if listingIdx == -1 {
		return fmt.Errorf("state: listing %s not found", listingID)
	}

	listing := gsm.State.Market.Listings[listingIdx]
	if listing.SellerID != sellerID {
		return fmt.Errorf("state: listing %s does not belong to seller %s", listingID, sellerID)
	}

	if len(seller.Inventory) < int(gsm.Rules.MaxInventorySlots) {
		seller.Inventory = append(seller.Inventory, listing.AssetID)
	}

	gsm.State.Market.Listings = append(
		gsm.State.Market.Listings[:listingIdx],
		gsm.State.Market.Listings[listingIdx+1:]...,
	)
	return nil
}

func findMarketListing(listings []MarketListing, listingID string) int {
	for i, l := range listings {
		if l.AssetID == listingID {
			return i
		}
	}
	return -1
}

func (gsm *GameStateMachine) PlayerCount() int {
	gsm.mu.RLock()
	defer gsm.mu.RUnlock()
	return len(gsm.State.Players)
}

func (gsm *GameStateMachine) AssetCount() int {
	gsm.mu.RLock()
	defer gsm.mu.RUnlock()
	return len(gsm.State.Assets)
}
