package social

import (
	"sort"
	"sync"
	"time"
)

type LeaderboardCategory int

const (
	CategoryXP      LeaderboardCategory = 0
	CategoryGold    LeaderboardCategory = 1
	CategoryGuildXP LeaderboardCategory = 2
	CategoryKills   LeaderboardCategory = 3
)

type LeaderboardEntry struct {
	Rank  int
	ID    string
	Name  string
	Score uint64
}

type Leaderboard struct {
	Category   LeaderboardCategory
	GameID     string
	Entries    []*LeaderboardEntry
	UpdatedAt  int64
	MaxEntries int
}

type LeaderboardManager struct {
	mu           sync.RWMutex
	leaderboards map[LeaderboardCategory]*Leaderboard
}

func NewLeaderboardManager() *LeaderboardManager {
	lm := &LeaderboardManager{
		leaderboards: make(map[LeaderboardCategory]*Leaderboard),
	}

	for _, cat := range []LeaderboardCategory{CategoryXP, CategoryGold, CategoryGuildXP, CategoryKills} {
		lm.leaderboards[cat] = &Leaderboard{
			Category:   cat,
			GameID:     "default",
			Entries:    make([]*LeaderboardEntry, 0),
			UpdatedAt:  time.Now().Unix(),
			MaxEntries: 100,
		}
	}

	return lm
}

func (lm *LeaderboardManager) UpdateScore(category LeaderboardCategory, id, name string, score uint64) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	lb, ok := lm.leaderboards[category]
	if !ok {
		lb = &Leaderboard{
			Category:   category,
			GameID:     "default",
			Entries:    make([]*LeaderboardEntry, 0),
			UpdatedAt:  time.Now().Unix(),
			MaxEntries: 100,
		}
		lm.leaderboards[category] = lb
	}

	var entry *LeaderboardEntry
	for _, e := range lb.Entries {
		if e.ID == id {
			entry = e
			break
		}
	}

	if entry != nil {
		entry.Score = score
		entry.Name = name
	} else {
		entry = &LeaderboardEntry{
			Rank:  0,
			ID:    id,
			Name:  name,
			Score: score,
		}
		lb.Entries = append(lb.Entries, entry)
	}

	lb.UpdatedAt = time.Now().Unix()
}

func (lm *LeaderboardManager) GetTopN(category LeaderboardCategory, n int) []*LeaderboardEntry {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	lb, ok := lm.leaderboards[category]
	if !ok {
		return nil
	}

	sorted := lm.sortedEntries(lb)

	if n <= 0 {
		n = 10
	}
	if n > len(sorted) {
		n = len(sorted)
	}

	return sorted[:n]
}

func (lm *LeaderboardManager) GetRank(category LeaderboardCategory, id string) int {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	lb, ok := lm.leaderboards[category]
	if !ok {
		return 0
	}

	sorted := lm.sortedEntries(lb)
	for _, e := range sorted {
		if e.ID == id {
			return e.Rank
		}
	}
	return 0
}

func (lm *LeaderboardManager) GetLeaderboard(category LeaderboardCategory) *Leaderboard {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	return lm.leaderboards[category]
}

func (lm *LeaderboardManager) RefreshLeaderboard(category LeaderboardCategory) []*LeaderboardEntry {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	lb, ok := lm.leaderboards[category]
	if !ok {
		return nil
	}

	sorted := lm.sortedEntries(lb)
	lb.Entries = sorted

	if len(lb.Entries) > lb.MaxEntries {
		lb.Entries = lb.Entries[:lb.MaxEntries]
	}

	lb.UpdatedAt = time.Now().Unix()
	return lb.Entries
}

func (lm *LeaderboardManager) sortedEntries(lb *Leaderboard) []*LeaderboardEntry {
	entries := make([]*LeaderboardEntry, len(lb.Entries))
	copy(entries, lb.Entries)

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Score > entries[j].Score
	})

	for i, e := range entries {
		e.Rank = i + 1
	}

	return entries
}
