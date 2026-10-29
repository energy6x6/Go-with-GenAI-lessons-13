package server

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "libratrack/gen/inventory"
	"libratrack/internal/store"
)

// GetBookAvailability returns the inventory counts for one known book.
func (s *Server) GetBookAvailability(ctx context.Context, req *pb.GetBookAvailabilityRequest) (*pb.GetBookAvailabilityResponse, error) {
	if err := validateBook(ctx, req.GetBookId()); err != nil {
		return nil, err
	}
	value, err := s.store.GetBookAvailability(req.GetBookId())
	if err != nil {
		return nil, availabilityError(err, req.GetBookId())
	}
	return &pb.GetBookAvailabilityResponse{Availability: availabilityMessage(value)}, nil
}

// WatchBookAvailability sends an atomic snapshot followed by newer states.
func (s *Server) WatchBookAvailability(req *pb.WatchBookAvailabilityRequest, stream pb.InventoryService_WatchBookAvailabilityServer) error {
	ctx := stream.Context()
	if err := validateBook(ctx, req.GetBookId()); err != nil {
		return err
	}
	if req.GetMaxUpdates() < 0 {
		return status.Error(codes.InvalidArgument, "max_updates must not be negative")
	}
	value, updates, cancel, err := s.store.SubscribeBook(req.GetBookId())
	if err != nil {
		return availabilityError(err, req.GetBookId())
	}
	defer cancel()
	s.log.Info("book watch started", "book_id", value.BookID)
	defer s.log.Info("book watch finished", "book_id", value.BookID)
	var sent int32
	for {
		if err := ctx.Err(); err != nil {
			return status.FromContextError(err).Err()
		}
		if err := stream.Send(&pb.WatchBookAvailabilityResponse{Availability: availabilityMessage(value)}); err != nil {
			return err
		}
		sent++
		if req.GetMaxUpdates() > 0 && sent >= req.GetMaxUpdates() {
			return nil
		}
		select {
		case <-ctx.Done():
			return status.FromContextError(ctx.Err()).Err()
		case next, ok := <-updates:
			if !ok {
				return nil
			}
			value = next
		}
	}
}

func validateBook(ctx context.Context, bookID string) error {
	if err := ctx.Err(); err != nil {
		return status.FromContextError(err).Err()
	}
	if strings.TrimSpace(bookID) == "" {
		return status.Error(codes.InvalidArgument, "book_id is required")
	}
	return nil
}

func availabilityError(err error, bookID string) error {
	if errors.Is(err, store.ErrNotFound) {
		return status.Errorf(codes.NotFound, "book %s has no registered copies", bookID)
	}
	return status.Error(codes.Internal, "cannot read book availability")
}

func availabilityMessage(value store.BookAvailability) *pb.BookAvailability {
	return &pb.BookAvailability{BookId: value.BookID, Total: value.Total, Available: value.Available, Borrowed: value.Borrowed, Lost: value.Lost, Revision: value.Revision}
}
