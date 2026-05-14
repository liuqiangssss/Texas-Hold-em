package table

import (
	"context"
	"sync"
)

// Manager maintains the live set of tables and basic matchmaking.
// MVP strategy: per blinds level, keep one table; create a new one if full.
type Manager struct {
	mu     sync.Mutex
	ctx    context.Context
	tables map[string]*Table
}

func NewManager(ctx context.Context) *Manager {
	return &Manager{
		ctx:    ctx,
		tables: make(map[string]*Table),
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
	m.tables[t.ID] = t
	go t.Run(m.ctx)
	return t
}
