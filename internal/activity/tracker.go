package activity

import "sync"

type Tracker struct {
	mu      sync.RWMutex
	players map[string]map[string]int
}

func NewTracker() *Tracker {
	return &Tracker{players: make(map[string]map[string]int)}
}

func (t *Tracker) Enter(gameID, playerID string) {
	if t == nil || gameID == "" || playerID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.players[gameID] == nil {
		t.players[gameID] = make(map[string]int)
	}
	t.players[gameID][playerID]++
}

func (t *Tracker) Leave(gameID, playerID string) {
	if t == nil || gameID == "" || playerID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	players := t.players[gameID]
	if players == nil {
		return
	}
	players[playerID]--
	if players[playerID] <= 0 {
		delete(players, playerID)
	}
	if len(players) == 0 {
		delete(t.players, gameID)
	}
}

func (t *Tracker) Count(gameID string) int {
	if t == nil || gameID == "" {
		return 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.players[gameID])
}
