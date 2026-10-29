package server_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "libratrack/gen/inventory"
	"libratrack/internal/auth"
	"libratrack/internal/store"
)

func TestGetBookAvailability(t *testing.T) {
	for _, tc := range []struct {
		name, book string
		code       codes.Code
	}{
		{"seeded", "book-1", codes.OK}, {"empty", "", codes.InvalidArgument},
		{"whitespace", "  ", codes.InvalidArgument}, {"unknown", "missing", codes.NotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := startServer(t)
			got, err := client.GetBookAvailability(ctxT(t), &pb.GetBookAvailabilityRequest{BookId: tc.book})
			wantCode(t, err, tc.code)
			if tc.code == codes.OK {
				value := got.GetAvailability()
				if value.GetBookId() != "book-1" || value.GetTotal() != 2 || value.GetAvailable() != 1 || value.GetBorrowed() != 1 || value.GetLost() != 0 || value.GetRevision() != 3 {
					t.Fatalf("unexpected snapshot: %v", value)
				}
			}
		})
	}
}

func TestWatchBookAvailabilityUpdatesAndEOF(t *testing.T) {
	client, st := startServer(t)
	ctx := ctxT(t)
	stream, err := client.WatchBookAvailability(ctx, &pb.WatchBookAvailabilityRequest{BookId: "book-1", MaxUpdates: 3})
	if err != nil {
		t.Fatal(err)
	}
	first, err := stream.Recv()
	if err != nil || first.GetAvailability().GetAvailable() != 1 {
		t.Fatalf("first: %v, %v", first, err)
	}
	if _, err := client.UpdateCopyStatus(ctx, &pb.UpdateCopyStatusRequest{Id: "copy-1", Status: pb.Copy_LOST}); err != nil {
		t.Fatal(err)
	}
	second, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if second.GetAvailability().GetLost() != 1 || second.GetAvailability().GetAvailable() != 0 || second.GetAvailability().GetRevision() != 4 {
		t.Fatalf("status update: %v", second)
	}
	if _, err := client.RegisterCopy(ctx, &pb.RegisterCopyRequest{BookId: "book-1", InventoryNumber: "WATCH-NEW"}); err != nil {
		t.Fatal(err)
	}
	third, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if third.GetAvailability().GetTotal() != 3 || third.GetAvailability().GetAvailable() != 1 || third.GetAvailability().GetRevision() != 5 {
		t.Fatalf("registration update: %v", third)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
	waitBookSubscriptions(t, st, 0)
}

func TestWatchBookAvailabilityValidation(t *testing.T) {
	for _, tc := range []struct {
		name, book string
		max        int32
		code       codes.Code
	}{
		{"empty", "", 0, codes.InvalidArgument}, {"whitespace", " ", 0, codes.InvalidArgument},
		{"negative max", "book-1", -1, codes.InvalidArgument}, {"unknown", "missing", 1, codes.NotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, st := startServer(t)
			stream, err := client.WatchBookAvailability(ctxT(t), &pb.WatchBookAvailabilityRequest{BookId: tc.book, MaxUpdates: tc.max})
			if err == nil {
				_, err = stream.Recv()
			}
			wantCode(t, err, tc.code)
			waitBookSubscriptions(t, st, 0)
		})
	}
}

func TestWatchBookAvailabilityClientCancelReleasesSubscription(t *testing.T) {
	client, st := startServer(t)
	ctx, cancel := context.WithCancel(ctxT(t))
	defer cancel()
	stream, err := client.WatchBookAvailability(ctx, &pb.WatchBookAvailabilityRequest{BookId: "book-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	waitBookSubscriptions(t, st, 1)
	cancel()
	_, err = stream.Recv()
	wantCode(t, err, codes.Canceled)
	waitBookSubscriptions(t, st, 0)
}

func TestWatchBookAvailabilityDeadlineReleasesSubscription(t *testing.T) {
	client, st := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream, err := client.WatchBookAvailability(ctx, &pb.WatchBookAvailabilityRequest{BookId: "book-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	waitBookSubscriptions(t, st, 1)
	_, err = stream.Recv()
	wantCode(t, err, codes.DeadlineExceeded)
	waitBookSubscriptions(t, st, 0)
}

func TestWatchBookAvailabilitySingleSnapshot(t *testing.T) {
	client, st := startServer(t)
	stream, err := client.WatchBookAvailability(ctxT(t), &pb.WatchBookAvailabilityRequest{BookId: "book-2", MaxUpdates: 1})
	if err != nil {
		t.Fatal(err)
	}
	value, err := stream.Recv()
	if err != nil || value.GetAvailability().GetLost() != 1 {
		t.Fatalf("got %v, %v", value, err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
	waitBookSubscriptions(t, st, 0)
}

func TestAvailabilityAuthUnaryAndStream(t *testing.T) {
	client := startAuthServer(t)
	for _, tc := range []struct {
		name, role string
		code       codes.Code
	}{
		{"no token", "", codes.Unauthenticated}, {"reader", auth.RoleReader, codes.OK},
		{"librarian", auth.RoleLibrarian, codes.OK}, {"unknown role", "guest", codes.PermissionDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := ctxT(t)
			if tc.role != "" {
				ctx = tokenCtx(t, tc.role, time.Minute)
			}
			_, err := client.GetBookAvailability(ctx, &pb.GetBookAvailabilityRequest{BookId: "book-1"})
			wantCode(t, err, tc.code)
			stream, err := client.WatchBookAvailability(ctx, &pb.WatchBookAvailabilityRequest{BookId: "book-1", MaxUpdates: 1})
			if err == nil {
				_, err = stream.Recv()
			}
			wantCode(t, err, tc.code)
		})
	}
}

func waitBookSubscriptions(t *testing.T, st *store.Store, want int) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for st.BookSubscriberCount() != want {
		select {
		case <-deadline.C:
			t.Fatalf("book subscriptions = %d, want %d", st.BookSubscriberCount(), want)
		case <-tick.C:
		}
	}
}

func TestGetBookAvailabilityCanceledContext(t *testing.T) {
	client, _ := startServer(t)
	ctx, cancel := context.WithCancel(ctxT(t))
	cancel()
	_, err := client.GetBookAvailability(ctx, &pb.GetBookAvailabilityRequest{BookId: "book-1"})
	if status.Code(err) != codes.Canceled {
		t.Fatal(err)
	}
}
