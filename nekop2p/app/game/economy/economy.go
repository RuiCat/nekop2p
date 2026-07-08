package economy

import (
	"fmt"
	"sync"
	"time"
)

type EconomyParams struct {
	GoldPerRecharge  float64
	GoldPerWithdraw  float64
	WithdrawCooldown int64
	MaxDailyWithdraw uint64
	GlobalDropRate   float64
	LootScalingAlpha float64
	CraftCostMult    float64
	MallPrices       map[string]uint64
}

type GameEconomy struct {
	GameID                string
	Params                *EconomyParams
	TotalGoldInCirculation uint64
	TotalGoldReserve      uint64
	DailyWithdraws        map[string]uint64
	LastWithdrawAt        map[string]int64
	mu                    sync.RWMutex
}

func DefaultEconomyParams() *EconomyParams {
	return &EconomyParams{
		GoldPerRecharge:  100.0,
		GoldPerWithdraw:  100.0,
		WithdrawCooldown: 86400,
		MaxDailyWithdraw: 100000,
		GlobalDropRate:   1.0,
		LootScalingAlpha: 0.4,
		CraftCostMult:    1.0,
		MallPrices: map[string]uint64{
			"health_potion":    10,
			"mana_potion":      15,
			"iron_sword":       100,
			"steel_armor":      500,
			"dragon_blade":     5000,
			"excalibur":        50000,
			"enchanted_cape":   250,
			"phoenix_feather":  1000,
			"crown_of_stars":   10000,
			"title_deed":       20000,
		},
	}
}

func NewGameEconomy(gameID string, params *EconomyParams) *GameEconomy {
	if params == nil {
		params = DefaultEconomyParams()
	}
	return &GameEconomy{
		GameID:          gameID,
		Params:          params,
		DailyWithdraws:  make(map[string]uint64),
		LastWithdrawAt:  make(map[string]int64),
	}
}

func (ge *GameEconomy) Recharge(playerID string, externalAmount uint64) (uint64, error) {
	ge.mu.Lock()
	defer ge.mu.Unlock()

	if externalAmount == 0 {
		return 0, fmt.Errorf("economy: recharge amount must be positive")
	}

	goldAmount := uint64(float64(externalAmount) * ge.Params.GoldPerRecharge)
	if goldAmount == 0 {
		goldAmount = 1
	}

	if ge.TotalGoldReserve+goldAmount < ge.TotalGoldReserve {
		return 0, fmt.Errorf("economy: gold reserve overflow")
	}
	ge.TotalGoldReserve += goldAmount
	ge.TotalGoldInCirculation += goldAmount

	return goldAmount, nil
}

func (ge *GameEconomy) Withdraw(playerID string, goldAmount uint64) (uint64, error) {
	ge.mu.Lock()
	defer ge.mu.Unlock()

	if goldAmount == 0 {
		return 0, fmt.Errorf("economy: withdraw amount must be positive")
	}

	if ge.Params.MaxDailyWithdraw > 0 {
		dailyTotal := ge.DailyWithdraws[playerID] + goldAmount
		if dailyTotal > ge.Params.MaxDailyWithdraw {
			return 0, fmt.Errorf("economy: daily withdraw limit exceeded: %d/%d",
				dailyTotal, ge.Params.MaxDailyWithdraw)
		}
	}

	if ge.Params.WithdrawCooldown > 0 {
		lastAt, ok := ge.LastWithdrawAt[playerID]
		if ok {
			elapsed := time.Now().Unix() - lastAt
			if elapsed < ge.Params.WithdrawCooldown {
				remaining := ge.Params.WithdrawCooldown - elapsed
				return 0, fmt.Errorf("economy: withdraw cooldown active, %d seconds remaining", remaining)
			}
		}
	}

	if goldAmount > ge.TotalGoldInCirculation {
		return 0, fmt.Errorf("economy: insufficient gold in circulation: %d/%d",
			goldAmount, ge.TotalGoldInCirculation)
	}

	externalAmount := uint64(float64(goldAmount) / ge.Params.GoldPerWithdraw)
	if externalAmount == 0 {
		externalAmount = 1
	}

	ge.TotalGoldInCirculation -= goldAmount
	ge.DailyWithdraws[playerID] += goldAmount
	ge.LastWithdrawAt[playerID] = time.Now().Unix()

	return externalAmount, nil
}

func (ge *GameEconomy) GetMallPrice(itemID string) uint64 {
	ge.mu.RLock()
	defer ge.mu.RUnlock()

	price, ok := ge.Params.MallPrices[itemID]
	if !ok {
		return 0
	}
	return price
}

func (ge *GameEconomy) SetMallPrice(itemID string, price uint64) {
	ge.mu.Lock()
	defer ge.mu.Unlock()
	ge.Params.MallPrices[itemID] = price
}

func (ge *GameEconomy) UpdateParams(newParams *EconomyParams) error {
	if newParams == nil {
		return fmt.Errorf("economy: nil params")
	}

	ge.mu.Lock()
	defer ge.mu.Unlock()

	ge.Params.GoldPerRecharge = newParams.GoldPerRecharge
	ge.Params.GoldPerWithdraw = newParams.GoldPerWithdraw
	ge.Params.WithdrawCooldown = newParams.WithdrawCooldown
	ge.Params.MaxDailyWithdraw = newParams.MaxDailyWithdraw
	ge.Params.GlobalDropRate = newParams.GlobalDropRate
	ge.Params.LootScalingAlpha = newParams.LootScalingAlpha
	ge.Params.CraftCostMult = newParams.CraftCostMult

	if newParams.MallPrices != nil {
		ge.Params.MallPrices = newParams.MallPrices
	}

	return nil
}

func (ge *GameEconomy) GetParams() *EconomyParams {
	ge.mu.RLock()
	defer ge.mu.RUnlock()
	cp := *ge.Params
	return &cp
}

func (ge *GameEconomy) GetCirculation() uint64 {
	ge.mu.RLock()
	defer ge.mu.RUnlock()
	return ge.TotalGoldInCirculation
}

func (ge *GameEconomy) GetReserve() uint64 {
	ge.mu.RLock()
	defer ge.mu.RUnlock()
	return ge.TotalGoldReserve
}

func (ge *GameEconomy) GetDailyWithdraw(playerID string) uint64 {
	ge.mu.RLock()
	defer ge.mu.RUnlock()
	return ge.DailyWithdraws[playerID]
}

func (ge *GameEconomy) ResetDailyWithdraws() {
	ge.mu.Lock()
	defer ge.mu.Unlock()
	ge.DailyWithdraws = make(map[string]uint64)
}

func (ge *GameEconomy) AddGoldReserve(amount uint64) error {
	ge.mu.Lock()
	defer ge.mu.Unlock()

	if ge.TotalGoldReserve+amount < ge.TotalGoldReserve {
		return fmt.Errorf("economy: gold reserve overflow")
	}
	ge.TotalGoldReserve += amount
	return nil
}

func (ge *GameEconomy) IssueGold(amount uint64) error {
	ge.mu.Lock()
	defer ge.mu.Unlock()

	if amount > ge.TotalGoldReserve {
		return fmt.Errorf("economy: insufficient reserve: need %d, have %d", amount, ge.TotalGoldReserve)
	}

	ge.TotalGoldReserve -= amount
	ge.TotalGoldInCirculation += amount
	return nil
}

func (ge *GameEconomy) BurnGold(amount uint64) error {
	ge.mu.Lock()
	defer ge.mu.Unlock()

	if amount > ge.TotalGoldInCirculation {
		return fmt.Errorf("economy: insufficient circulation: need %d, have %d", amount, ge.TotalGoldInCirculation)
	}

	ge.TotalGoldInCirculation -= amount
	return nil
}

func (ge *GameEconomy) GetEffectiveDropRate() float64 {
	ge.mu.RLock()
	defer ge.mu.RUnlock()
	rate := ge.Params.GlobalDropRate
	if rate < 0.5 {
		rate = 0.5
	}
	if rate > 2.0 {
		rate = 2.0
	}
	return rate
}
