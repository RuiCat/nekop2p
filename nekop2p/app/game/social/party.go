package social

import (
	"fmt"
	"sync"
	"time"
)

type Party struct {
	PartyID          string
	LeaderID         string
	Members          []string
	MaxSize          int
	CreatedAt        int64
	KillContributions map[string]uint32
}

type PartyManager struct {
	mu       sync.RWMutex
	parties  map[string]*Party
	byPlayer map[string]string
}

func NewPartyManager() *PartyManager {
	return &PartyManager{
		parties:  make(map[string]*Party),
		byPlayer: make(map[string]string),
	}
}

func (pm *PartyManager) CreateParty(leaderID string) (*Party, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if leaderID == "" {
		return nil, fmt.Errorf("social: leader ID cannot be empty")
	}
	if _, exists := pm.byPlayer[leaderID]; exists {
		return nil, fmt.Errorf("social: player %s is already in a party", leaderID)
	}

	now := time.Now().Unix()
	partyID := generateID("party-" + leaderID)

	p := &Party{
		PartyID:           partyID,
		LeaderID:          leaderID,
		Members:           []string{leaderID},
		MaxSize:           5,
		CreatedAt:         now,
		KillContributions: make(map[string]uint32),
	}

	pm.parties[partyID] = p
	pm.byPlayer[leaderID] = partyID

	return p, nil
}

func (pm *PartyManager) InviteToParty(partyID, inviterID, targetID string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	p, ok := pm.parties[partyID]
	if !ok {
		return fmt.Errorf("social: party %s not found", partyID)
	}
	if p.LeaderID != inviterID {
		return fmt.Errorf("social: only the party leader can invite players")
	}
	if inviterID == targetID {
		return fmt.Errorf("social: cannot invite yourself")
	}
	if _, exists := pm.byPlayer[targetID]; exists {
		return fmt.Errorf("social: player %s is already in a party", targetID)
	}
	if len(p.Members) >= p.MaxSize {
		return fmt.Errorf("social: party %s is full (%d/%d)", partyID, len(p.Members), p.MaxSize)
	}

	return nil
}

func (pm *PartyManager) LeaveParty(partyID, playerID string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	p, ok := pm.parties[partyID]
	if !ok {
		return fmt.Errorf("social: party %s not found", partyID)
	}

	idx := -1
	for i, m := range p.Members {
		if m == playerID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("social: player %s is not a member of party %s", playerID, partyID)
	}

	p.Members = append(p.Members[:idx], p.Members[idx+1:]...)
	delete(pm.byPlayer, playerID)
	delete(p.KillContributions, playerID)

	if len(p.Members) == 0 {
		delete(pm.parties, partyID)
		return nil
	}

	if p.LeaderID == playerID {
		p.LeaderID = p.Members[0]
	}

	return nil
}

func (pm *PartyManager) DisbandParty(partyID string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	p, ok := pm.parties[partyID]
	if !ok {
		return fmt.Errorf("social: party %s not found", partyID)
	}

	for _, pid := range p.Members {
		delete(pm.byPlayer, pid)
	}
	delete(pm.parties, partyID)

	return nil
}

func (pm *PartyManager) RecordKillContribution(partyID, playerID string, contribution uint32) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	p, ok := pm.parties[partyID]
	if !ok {
		return
	}

	for _, m := range p.Members {
		if m == playerID {
			p.KillContributions[playerID] += contribution
			return
		}
	}
}

func (pm *PartyManager) GetPartyKillContributions(partyID string) map[string]uint32 {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	p, ok := pm.parties[partyID]
	if !ok {
		return nil
	}

	result := make(map[string]uint32, len(p.KillContributions))
	for k, v := range p.KillContributions {
		result[k] = v
	}
	return result
}

func (pm *PartyManager) ResetKillContributions(partyID string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	p, ok := pm.parties[partyID]
	if !ok {
		return fmt.Errorf("social: party %s not found", partyID)
	}
	p.KillContributions = make(map[string]uint32)
	return nil
}

func (pm *PartyManager) GetParty(partyID string) *Party {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.parties[partyID]
}

func (pm *PartyManager) GetPlayerParty(playerID string) *Party {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	partyID, ok := pm.byPlayer[playerID]
	if !ok {
		return nil
	}
	return pm.parties[partyID]
}
