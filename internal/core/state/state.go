package state

import (
	"sync"

	"github.com/sorokin-vladimir/tele/internal/store"
)

// State is the domain state of the account. Every mutation on the update path
// goes through one of its operations, and every operation funnels through
// commit, so there is exactly one place a state change can be observed from.
//
// Operations are named for what happened rather than for the field they set:
// ApplyIncoming, not AppendMessageAndBumpUnread. Callers describe events; the
// state decides what that means.
type State struct {
	st        store.Store
	mu        sync.RWMutex
	listeners []func(Change)
}

func New(st store.Store) *State {
	return &State{st: st}
}

// Store exposes the underlying store. Its readers are the owner's own paths —
// building projections, resolving a peer for a fetch — and the command call
// sites #198 converts. It is not a mutation path: writing through it bypasses
// commit, so nothing downstream hears about it.
func (s *State) Store() store.Store { return s.st }

// OnChange registers fn to run for every committed Change. The owner registers
// the one that rebuilds projections.
//
// A callback rather than a channel: a mutation can originate on any goroutine —
// the update loop, a history backfill — and a channel would either need a
// drainer running before the first mutation or would block the mutator when the
// buffer filled. A callback has neither failure mode, and the work behind it
// rebuilds the subscribed windows and enqueues their deltas without blocking.
func (s *State) OnChange(fn func(Change)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners = append(s.listeners, fn)
}

// commit is the single chokepoint every mutation passes through. Persistence is
// already handled write-through by the store, so commit only publishes; it is
// the one place projections hook into.
func (s *State) commit(c Change) {
	s.mu.RLock()
	listeners := s.listeners
	s.mu.RUnlock()
	for _, fn := range listeners {
		fn(c)
	}
}
