package author

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/nekop2p/nekop2p/app/game/economy"
)

const MinAnnouncementPeriod int64 = 72 * 3600

var (
	ErrNilParams           = errors.New("author: new params must not be nil")
	ErrGameNotRegistered   = errors.New("author: game not registered")
	ErrUnauthorized        = errors.New("author: author not authorized for this game")
	ErrChangeNotFound      = errors.New("author: pending change not found")
	ErrNotYetEffective     = errors.New("author: announcement period has not elapsed")
	ErrEmergencyReason     = errors.New("author: emergency change requires a reason")
	ErrGameAlreadyRegistered = errors.New("author: game already registered with a different author")
)

type AuditLog struct {
	LogID       [32]byte
	GameID      string
	AuthorID    string
	Action      string
	OldValues   map[string]interface{}
	NewValues   map[string]interface{}
	AnnouncedAt int64
	EffectiveAt int64
	ExecutedAt  int64
	Emergency   bool
	Reason      string
}

type PendingChange struct {
	ChangeID    [32]byte
	GameID      string
	AuthorID    string
	NewParams   *economy.EconomyParams
	AnnouncedAt int64
	EffectiveAt int64
	Emergency   bool
	Reason      string
}

type AuthorGovernor struct {
	mu             sync.RWMutex
	pendingChanges map[string]*PendingChange
	auditLogs      []*AuditLog
	gameAuthors    map[string]string
}

func NewAuthorGovernor() *AuthorGovernor {
	return &AuthorGovernor{
		pendingChanges: make(map[string]*PendingChange),
		auditLogs:      make([]*AuditLog, 0),
		gameAuthors:    make(map[string]string),
	}
}

func (ag *AuthorGovernor) RegisterGameAuthor(gameID, authorID string) error {
	if gameID == "" {
		return errors.New("author: gameID must not be empty")
	}
	if authorID == "" {
		return errors.New("author: authorID must not be empty")
	}

	ag.mu.Lock()
	defer ag.mu.Unlock()

	existing, ok := ag.gameAuthors[gameID]
	if ok && existing != authorID {
		return fmt.Errorf("%w: game %s already registered to %s", ErrGameAlreadyRegistered, gameID, existing)
	}

	ag.gameAuthors[gameID] = authorID
	return nil
}

func (ag *AuthorGovernor) VerifyAuthor(gameID, authorID string) bool {
	ag.mu.RLock()
	defer ag.mu.RUnlock()

	registered, ok := ag.gameAuthors[gameID]
	if !ok {
		return false
	}
	return registered == authorID
}

func (ag *AuthorGovernor) ProposeChange(gameID, authorID string, newParams *economy.EconomyParams, reason string) (*PendingChange, error) {
	if newParams == nil {
		return nil, ErrNilParams
	}

	if err := ag.checkAuthor(gameID, authorID); err != nil {
		return nil, err
	}

	announcedAt := time.Now().Unix()

	return ag.createPendingChange(gameID, authorID, newParams, reason, announcedAt, false)
}

func (ag *AuthorGovernor) ProposeEmergencyChange(gameID, authorID string, newParams *economy.EconomyParams, reason string) (*PendingChange, error) {
	if newParams == nil {
		return nil, ErrNilParams
	}
	if reason == "" {
		return nil, ErrEmergencyReason
	}

	if err := ag.checkAuthor(gameID, authorID); err != nil {
		return nil, err
	}

	announcedAt := time.Now().Unix()

	return ag.createPendingChange(gameID, authorID, newParams, reason, announcedAt, true)
}

func (ag *AuthorGovernor) ExecuteChange(changeID string, currentTime int64, currentParams *economy.EconomyParams) (*AuditLog, error) {
	ag.mu.Lock()
	defer ag.mu.Unlock()

	change, ok := ag.pendingChanges[changeID]
	if !ok {
		return nil, ErrChangeNotFound
	}

	if !change.Emergency && currentTime < change.EffectiveAt {
		return nil, fmt.Errorf("%w: effective at %d, current time %d", ErrNotYetEffective, change.EffectiveAt, currentTime)
	}

	action := "UPDATE_PARAMS"
	if change.Emergency {
		action = "EMERGENCY"
	}

	oldValues := paramsToMap(currentParams)
	newValues := paramsToMap(change.NewParams)

	logID := generateLogID(change.ChangeID, currentTime)

	auditLog := &AuditLog{
		LogID:       logID,
		GameID:      change.GameID,
		AuthorID:    change.AuthorID,
		Action:      action,
		OldValues:   oldValues,
		NewValues:   newValues,
		AnnouncedAt: change.AnnouncedAt,
		EffectiveAt: change.EffectiveAt,
		ExecutedAt:  currentTime,
		Emergency:   change.Emergency,
		Reason:      change.Reason,
	}

	delete(ag.pendingChanges, changeID)
	ag.auditLogs = append(ag.auditLogs, auditLog)

	return auditLog, nil
}

func (ag *AuthorGovernor) CancelChange(changeID string, authorID string) error {
	ag.mu.Lock()
	defer ag.mu.Unlock()

	change, ok := ag.pendingChanges[changeID]
	if !ok {
		return ErrChangeNotFound
	}

	if change.AuthorID != authorID {
		return ErrUnauthorized
	}

	delete(ag.pendingChanges, changeID)
	return nil
}

func (ag *AuthorGovernor) GetPendingChanges(gameID string) []*PendingChange {
	ag.mu.RLock()
	defer ag.mu.RUnlock()

	var result []*PendingChange
	for _, change := range ag.pendingChanges {
		if change.GameID == gameID {
			result = append(result, change)
		}
	}
	return result
}

func (ag *AuthorGovernor) GetAllPendingChanges() []*PendingChange {
	ag.mu.RLock()
	defer ag.mu.RUnlock()

	result := make([]*PendingChange, 0, len(ag.pendingChanges))
	for _, change := range ag.pendingChanges {
		result = append(result, change)
	}
	return result
}

func (ag *AuthorGovernor) GetAuditLogs(gameID string) []*AuditLog {
	ag.mu.RLock()
	defer ag.mu.RUnlock()

	var result []*AuditLog
	for _, log := range ag.auditLogs {
		if log.GameID == gameID {
			result = append(result, log)
		}
	}
	return result
}

func (ag *AuthorGovernor) ProcessExpiredChanges(currentTime int64, econ *economy.GameEconomy) []*AuditLog {
	ag.mu.Lock()
	defer ag.mu.Unlock()

	var executed []*AuditLog

	for key, change := range ag.pendingChanges {
		if change.Emergency {
			continue
		}
		if currentTime < change.EffectiveAt {
			continue
		}

		oldValues := paramsToMap(econ.GetParams())

		logID := generateLogID(change.ChangeID, currentTime)
		newValues := paramsToMap(change.NewParams)

		econ.UpdateParams(change.NewParams)

		auditLog := &AuditLog{
			LogID:       logID,
			GameID:      change.GameID,
			AuthorID:    change.AuthorID,
			Action:      "UPDATE_PARAMS",
			OldValues:   oldValues,
			NewValues:   newValues,
			AnnouncedAt: change.AnnouncedAt,
			EffectiveAt: change.EffectiveAt,
			ExecutedAt:  currentTime,
			Emergency:   false,
			Reason:      change.Reason,
		}

		delete(ag.pendingChanges, key)
		ag.auditLogs = append(ag.auditLogs, auditLog)
		executed = append(executed, auditLog)
	}

	return executed
}

func (ag *AuthorGovernor) checkAuthor(gameID, authorID string) error {
	ag.mu.RLock()
	defer ag.mu.RUnlock()

	registered, ok := ag.gameAuthors[gameID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrGameNotRegistered, gameID)
	}
	if registered != authorID {
		return fmt.Errorf("%w: %s", ErrUnauthorized, authorID)
	}
	return nil
}

func (ag *AuthorGovernor) createPendingChange(gameID, authorID string, newParams *economy.EconomyParams, reason string, announcedAt int64, emergency bool) (*PendingChange, error) {
	changeID := generateChangeID(gameID, authorID, announcedAt, reason)

	effectiveAt := announcedAt + MinAnnouncementPeriod
	if emergency {
		effectiveAt = announcedAt
	}

	change := &PendingChange{
		ChangeID:    changeID,
		GameID:      gameID,
		AuthorID:    authorID,
		NewParams:   newParams,
		AnnouncedAt: announcedAt,
		EffectiveAt: effectiveAt,
		Emergency:   emergency,
		Reason:      reason,
	}

	key := hex.EncodeToString(changeID[:])

	ag.mu.Lock()
	ag.pendingChanges[key] = change
	ag.mu.Unlock()

	return change, nil
}

func generateChangeID(gameID, authorID string, announcedAt int64, reason string) [32]byte {
	input := gameID + "||" + authorID + "||" + strconv.FormatInt(announcedAt, 10) + "||" + reason
	return sha256.Sum256([]byte(input))
}

func generateLogID(changeID [32]byte, executedAt int64) [32]byte {
	input := append(changeID[:], []byte("||exec||")...)
	input = append(input, []byte(strconv.FormatInt(executedAt, 10))...)
	return sha256.Sum256(input)
}

func paramsToMap(params *economy.EconomyParams) map[string]interface{} {
	if params == nil {
		return nil
	}

	mallCopy := make(map[string]uint64, len(params.MallPrices))
	for k, v := range params.MallPrices {
		mallCopy[k] = v
	}

	return map[string]interface{}{
		"gold_per_recharge":  params.GoldPerRecharge,
		"gold_per_withdraw":  params.GoldPerWithdraw,
		"withdraw_cooldown":  params.WithdrawCooldown,
		"max_daily_withdraw": params.MaxDailyWithdraw,
		"global_drop_rate":   params.GlobalDropRate,
		"loot_scaling_alpha": params.LootScalingAlpha,
		"craft_cost_mult":    params.CraftCostMult,
		"mall_prices":        mallCopy,
	}
}
