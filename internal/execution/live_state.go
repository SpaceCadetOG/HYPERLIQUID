package execution

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"hyperliquid/internal/market"
)

type LiveStateStore struct {
	mu   sync.Mutex
	path string
	data LiveState
}

type LiveState struct {
	UpdatedAt time.Time                    `json:"updated_at"`
	Positions map[string]LivePositionState `json:"positions"`
}

type LivePositionState struct {
	Symbol        string      `json:"symbol"`
	Side          market.Side `json:"side"`
	EntryPrice    float64     `json:"entry_price"`
	Size          float64     `json:"size"`
	StopPrice     float64     `json:"stop_price"`
	TargetPrice   float64     `json:"target_price"`
	EntryOrderID  string      `json:"entry_order_id"`
	StopOrderID   string      `json:"stop_order_id"`
	TargetOrderID string      `json:"target_order_id"`
	LastSeenAt    time.Time   `json:"last_seen_at"`
	Imported      bool        `json:"imported"`
}

func NewLiveStateStore(path string) *LiveStateStore {
	if strings.TrimSpace(path) == "" {
		path = "out/live_state.json"
	}
	s := &LiveStateStore{
		path: path,
		data: LiveState{Positions: map[string]LivePositionState{}},
	}
	_ = s.Load()
	return s
}

func (s *LiveStateStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.data = LiveState{Positions: map[string]LivePositionState{}}
			return nil
		}
		return err
	}
	var state LiveState
	if err := json.Unmarshal(raw, &state); err != nil {
		return err
	}
	if state.Positions == nil {
		state.Positions = map[string]LivePositionState{}
	}
	s.data = state
	return nil
}

func (s *LiveStateStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *LiveStateStore) Snapshot() LiveState {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := LiveState{
		UpdatedAt: s.data.UpdatedAt,
		Positions: make(map[string]LivePositionState, len(s.data.Positions)),
	}
	for k, v := range s.data.Positions {
		cp.Positions[k] = v
	}
	return cp
}

func (s *LiveStateStore) UpsertEntry(req market.OrderRequest, result market.OrderResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToUpper(strings.TrimSpace(req.Symbol))
	s.data.Positions[key] = LivePositionState{
		Symbol:        req.Symbol,
		Side:          req.Side,
		EntryPrice:    req.Price,
		Size:          req.Size,
		StopPrice:     req.StopPrice,
		TargetPrice:   req.TargetPrice,
		EntryOrderID:  result.OrderID,
		StopOrderID:   result.StopOrderID,
		TargetOrderID: result.TargetOrderID,
		LastSeenAt:    time.Now().UTC(),
	}
	s.data.UpdatedAt = time.Now().UTC()
	return s.saveLocked()
}

func (s *LiveStateStore) UpsertImported(pos market.Position) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToUpper(strings.TrimSpace(pos.Symbol))
	cur := s.data.Positions[key]
	cur.Symbol = pos.Symbol
	cur.Side = pos.Side
	cur.EntryPrice = pos.EntryPrice
	cur.Size = pos.Size
	cur.LastSeenAt = time.Now().UTC()
	cur.Imported = true
	s.data.Positions[key] = cur
	s.data.UpdatedAt = time.Now().UTC()
	return s.saveLocked()
}

func (s *LiveStateStore) UpdateProtection(symbol, stopID, targetID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToUpper(strings.TrimSpace(symbol))
	cur, ok := s.data.Positions[key]
	if !ok {
		return nil
	}
	if stopID != "" {
		cur.StopOrderID = stopID
	}
	if targetID != "" {
		cur.TargetOrderID = targetID
	}
	cur.LastSeenAt = time.Now().UTC()
	s.data.Positions[key] = cur
	s.data.UpdatedAt = time.Now().UTC()
	return s.saveLocked()
}

func (s *LiveStateStore) TouchPositions(positions []market.Position) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	live := make(map[string]market.Position, len(positions))
	for _, pos := range positions {
		live[strings.ToUpper(strings.TrimSpace(pos.Symbol))] = pos
	}
	for key, state := range s.data.Positions {
		pos, ok := live[key]
		if !ok {
			delete(s.data.Positions, key)
			continue
		}
		state.EntryPrice = pos.EntryPrice
		state.Size = pos.Size
		state.Side = pos.Side
		state.LastSeenAt = time.Now().UTC()
		s.data.Positions[key] = state
	}
	s.data.UpdatedAt = time.Now().UTC()
	return s.saveLocked()
}

func (s *LiveStateStore) Remove(symbol string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Positions, strings.ToUpper(strings.TrimSpace(symbol)))
	s.data.UpdatedAt = time.Now().UTC()
	return s.saveLocked()
}

func (s *LiveStateStore) saveLocked() error {
	if s.data.Positions == nil {
		s.data.Positions = map[string]LivePositionState{}
	}
	s.data.UpdatedAt = time.Now().UTC()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0o644)
}
