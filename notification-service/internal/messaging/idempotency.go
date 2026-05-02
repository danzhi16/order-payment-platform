package messaging

import "sync"

// IdempotencyStore tracks message IDs that have already been processed.
// In-memory is sufficient per the assignment spec; we cap the set size so a
// long-running consumer doesn't grow without bound. A real system would back
// this with Redis or a uniqueness index in the DB.
type IdempotencyStore struct {
	mu      sync.Mutex
	seen    map[string]struct{}
	order   []string // FIFO eviction tracking
	maxSize int
}

func NewIdempotencyStore(maxSize int) *IdempotencyStore {
	if maxSize <= 0 {
		maxSize = 10_000
	}
	return &IdempotencyStore{
		seen:    make(map[string]struct{}, maxSize),
		order:   make([]string, 0, maxSize),
		maxSize: maxSize,
	}
}

// Forget removes an id from the seen set. Used when a handler fails after
// the id was marked: we want a retry to actually reprocess.
func (s *IdempotencyStore) Forget(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[id]; !ok {
		return
	}
	delete(s.seen, id)
	for i, v := range s.order {
		if v == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			return
		}
	}
}

// MarkIfNew returns true if the id was unseen (and records it), false if it
// was already processed. Single call so the check-and-set is atomic.
func (s *IdempotencyStore) MarkIfNew(id string) bool {
	if id == "" {
		return true // nothing to dedupe on; let the caller decide
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[id]; ok {
		return false
	}
	s.seen[id] = struct{}{}
	s.order = append(s.order, id)
	if len(s.order) > s.maxSize {
		evict := s.order[0]
		s.order = s.order[1:]
		delete(s.seen, evict)
	}
	return true
}
