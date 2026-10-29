package store

import pb "libratrack/gen/inventory"

// BookAvailability is a value snapshot; callers cannot mutate stored state.
type BookAvailability struct {
	BookID                           string
	Total, Available, Borrowed, Lost int32
	Revision                         uint64
}

// GetBookAvailability returns one consistent snapshot, or ErrNotFound.
func (s *Store) GetBookAvailability(bookID string) (BookAvailability, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.availability[bookID]
	if !ok {
		return BookAvailability{}, ErrNotFound
	}
	return value, nil
}

// SubscribeBook atomically reads the snapshot and registers for later revisions.
// The single pending update is replaced with the latest state for slow consumers.
func (s *Store) SubscribeBook(bookID string) (BookAvailability, <-chan BookAvailability, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.availability[bookID]
	if !ok {
		return BookAvailability{}, nil, nil, ErrNotFound
	}
	updates := make(chan BookAvailability, 1)
	if s.bookSubs[bookID] == nil {
		s.bookSubs[bookID] = make(map[chan BookAvailability]struct{})
	}
	s.bookSubs[bookID][updates] = struct{}{}
	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, exists := s.bookSubs[bookID][updates]; !exists {
			return
		}
		delete(s.bookSubs[bookID], updates)
		close(updates)
		if len(s.bookSubs[bookID]) == 0 {
			delete(s.bookSubs, bookID)
		}
	}
	return value, updates, cancel, nil
}

// BookSubscriberCount reports active availability subscriptions for diagnostics.
func (s *Store) BookSubscriberCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, subscribers := range s.bookSubs {
		count += len(subscribers)
	}
	return count
}

func (s *Store) recordCreatedLocked(bookID string) {
	value := s.availability[bookID]
	value.BookID = bookID
	value.Total++
	value.Available++
	s.publishBookLocked(value)
}

func (s *Store) recordStatusLocked(bookID string, previous, next pb.Copy_Status) {
	value := s.availability[bookID]
	changeCount(&value, previous, -1)
	changeCount(&value, next, 1)
	s.publishBookLocked(value)
}

func changeCount(value *BookAvailability, state pb.Copy_Status, delta int32) {
	switch state {
	case pb.Copy_AVAILABLE:
		value.Available += delta
	case pb.Copy_BORROWED:
		value.Borrowed += delta
	case pb.Copy_LOST:
		value.Lost += delta
	}
}

// Called with mu held: publishing and cancellation cannot race or send on a closed channel.
func (s *Store) publishBookLocked(value BookAvailability) {
	value.Revision++
	s.availability[value.BookID] = value
	for updates := range s.bookSubs[value.BookID] {
		select {
		case updates <- value:
		default:
			select {
			case <-updates:
			default:
			}
			updates <- value
		}
	}
}
