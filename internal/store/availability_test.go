package store

import (
	"errors"
	"fmt"
	pb "libratrack/gen/inventory"
	"sync"
	"testing"
)

func TestBookAvailabilityCountsAndRevisions(t *testing.T) {
	s := New()
	want := BookAvailability{BookID: "book-1", Total: 2, Available: 1, Borrowed: 1, Revision: 3}
	got, err := s.GetBookAvailability("book-1")
	if err != nil || got != want {
		t.Fatalf("initial = %+v, %v; want %+v", got, err, want)
	}
	got.Available = 999
	got, _ = s.GetBookAvailability("book-1")
	if got != want {
		t.Fatal("caller changed stored state")
	}
	copy, err := s.Create("book-1", "EXTRA")
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []pb.Copy_Status{pb.Copy_BORROWED, pb.Copy_LOST, pb.Copy_AVAILABLE} {
		if _, err := s.SetStatus(copy.Id, state); err != nil {
			t.Fatal(err)
		}
	}
	got, _ = s.GetBookAvailability("book-1")
	want.Total++
	want.Available++
	want.Revision += 4
	if got != want {
		t.Fatalf("after changes = %+v, want %+v", got, want)
	}
	if _, err := s.Create("book-1", "EXTRA"); !errors.Is(err, ErrDuplicateNumber) {
		t.Fatal(err)
	}
	if _, err := s.SetStatus(copy.Id, pb.Copy_AVAILABLE); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetBookAvailability("book-1")
	if got != want {
		t.Fatal("duplicate or unchanged status advanced the revision")
	}
}

func TestBookSubscriptionLatestStateAndCleanup(t *testing.T) {
	s := New()
	initial, updates, cancel, err := s.SubscribeBook("book-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if s.BookSubscriberCount() != 1 {
		t.Fatal("subscription missing")
	}
	if _, err := s.SetStatus("copy-3", pb.Copy_AVAILABLE); err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-updates:
		t.Fatalf("unrelated book update: %+v", value)
	default:
	}
	for i := 0; i < 100; i++ {
		state := pb.Copy_BORROWED
		if i%2 != 0 {
			state = pb.Copy_AVAILABLE
		}
		if _, err := s.SetStatus("copy-1", state); err != nil {
			t.Fatal(err)
		}
	}
	want, _ := s.GetBookAvailability("book-1")
	select {
	case got := <-updates:
		if got != want || got.Revision != initial.Revision+100 {
			t.Fatalf("pending = %+v, want latest %+v", got, want)
		}
	default:
		t.Fatal("latest state lost")
	}
	select {
	case <-updates:
		t.Fatal("unbounded pending history")
	default:
	}
	cancel()
	cancel()
	if s.BookSubscriberCount() != 0 {
		t.Fatal("subscription leaked")
	}
	if _, ok := <-updates; ok {
		t.Fatal("channel remains open after cancellation")
	}
}

func TestUnknownBookDoesNotSubscribe(t *testing.T) {
	s := New()
	if _, err := s.GetBookAvailability("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	_, updates, cancel, err := s.SubscribeBook("missing")
	if !errors.Is(err, ErrNotFound) || updates != nil || cancel != nil || s.BookSubscriberCount() != 0 {
		t.Fatal("unknown book created a subscription")
	}
}

func TestBookAvailabilityConcurrentRegistrationAndStatus(t *testing.T) {
	s := New()
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for i := 0; i < 25; i++ {
				copy, err := s.Create("concurrent-book", fmt.Sprintf("C-%d-%d", worker, i))
				if err != nil {
					t.Error(err)
					return
				}
				if _, err := s.SetStatus(copy.Id, pb.Copy_BORROWED); err != nil {
					t.Error(err)
					return
				}
			}
		}(worker)
	}
	workers.Wait()
	got, err := s.GetBookAvailability("concurrent-book")
	want := BookAvailability{BookID: "concurrent-book", Total: 200, Borrowed: 200, Revision: 400}
	if err != nil || got != want {
		t.Fatalf("got %+v, %v; want %+v", got, err, want)
	}
}
