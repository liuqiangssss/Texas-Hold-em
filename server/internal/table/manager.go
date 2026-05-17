package table

import (
	"context"
	"sync"

	"github.com/liuqiangssss/texas-holdem/server/internal/store"
)

// Manager maintains the live set of tables and basic matchmaking.
// MVP strategy: per blinds level, keep one table; create a new one if full.
type Manager struct {
	mu     sync.Mutex
	ctx    context.Context
	tables map[string]*Table
	store  store.HandHistoryStore
}

// NewManager builds the table manager. `s` receives a HandRecord at the end
// of every hand; pass store.NewNoopStore() (or nil, which auto-falls back)
// when no persistence is configured.
func NewManager(ctx context.Context, s store.HandHistoryStore) *Manager {
	if s == nil {
		s = store.NewNoopStore()
	}
	return &Manager{
		ctx:    ctx,
		tables: make(map[string]*Table),
		store:  s,
	}
}

// FindOrCreate returns a table with an open seat for the given blinds.
// The returned table's actor goroutine is guaranteed to be running.
func (m *Manager) FindOrCreate(blinds [2]int) *Table {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tables {
		if t.Blinds == blinds && t.seatedCount.Load() < MaxSeats {
			return t
		}
	}
	t := New(blinds)
	t.store = m.store
	m.tables[t.ID] = t
	go t.Run(m.ctx)
	return t
}
