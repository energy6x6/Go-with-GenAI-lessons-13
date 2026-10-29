// Package server — реалізація InventoryServiceServer (слайди 16, 19–22).
//
// Правила, які тут дотримано (слайди 45–46):
//   - помилки повертаємо через status.Errorf(codes.X, ...), а не errors.New;
//   - у стрімінгових обробниках працюємо зі stream.Context(), а не context.Background();
//   - io.EOF від Recv() — це штатне завершення, а не помилка.
package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "libratrack/gen/inventory"
	"libratrack/internal/store"
)

type Server struct {
	// Вбудовування Unimplemented... забезпечує пряму сумісність (слайд 16):
	// новий RPC у .proto не зламає збірку, поки його не реалізовано.
	pb.UnimplementedInventoryServiceServer

	store *store.Store
	log   *slog.Logger
}

func New(st *store.Store, log *slog.Logger) *Server {
	return &Server{store: st, log: log}
}

// ---------------------------------------------------------------------------
// Unary (слайд 19)
// ---------------------------------------------------------------------------

func (s *Server) GetCopy(_ context.Context, req *pb.GetCopyRequest) (*pb.Copy, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	c, err := s.store.Get(req.GetId())
	if err != nil {
		return nil, toStatus(err, req.GetId())
	}
	return c, nil
}

func (s *Server) RegisterCopy(_ context.Context, req *pb.RegisterCopyRequest) (*pb.Copy, error) {
	return s.register(req)
}

func (s *Server) UpdateCopyStatus(_ context.Context, req *pb.UpdateCopyStatusRequest) (*pb.Copy, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	if !validStatus(req.GetStatus()) {
		return nil, status.Errorf(codes.InvalidArgument,
			"status must be AVAILABLE, BORROWED or LOST (got %s)", req.GetStatus())
	}
	c, err := s.store.SetStatus(req.GetId(), req.GetStatus())
	if err != nil {
		return nil, toStatus(err, req.GetId())
	}
	return c, nil
}

// ---------------------------------------------------------------------------
// Серверний стрімінг: 1 -> N (слайд 20)
// ---------------------------------------------------------------------------

func (s *Server) WatchCopyStatus(req *pb.WatchRequest, stream pb.InventoryService_WatchCopyStatusServer) error {
	ctx := stream.Context() // НЕ context.Background(): так ми бачимо скасування клієнтом
	id := req.GetId()
	if id == "" {
		return status.Error(codes.InvalidArgument, "id is required")
	}

	// Спочатку підписуємось, потім читаємо знімок — щоб не пропустити зміну між цими кроками.
	changes, cancel := s.store.Subscribe(id)
	defer cancel() // знімає підписку за будь-якого виходу з функції: без витоків

	current, err := s.store.Get(id)
	if err != nil {
		return toStatus(err, id)
	}

	s.log.Info("watch started", "copy_id", id, "max_updates", req.GetMaxUpdates())
	var sent int32
	send := func(st pb.Copy_Status, at time.Time) error {
		if err := stream.Send(&pb.StatusUpdate{
			CopyId:    id,
			Status:    st,
			ChangedAt: timestamppb.New(at),
		}); err != nil {
			return err
		}
		sent++
		return nil
	}
	done := func() bool { return req.GetMaxUpdates() > 0 && sent >= req.GetMaxUpdates() }

	// 1-ше повідомлення — поточний статус (знімок).
	if err := send(current.GetStatus(), time.Now()); err != nil {
		return err
	}
	if done() {
		s.log.Info("watch finished", "copy_id", id, "reason", "max_updates reached", "sent", sent)
		return nil // клієнт отримає чистий io.EOF
	}

	for {
		select {
		case <-ctx.Done():
			s.log.Info("watch finished", "copy_id", id, "reason", "client gone / cancelled", "sent", sent)
			return status.FromContextError(ctx.Err()).Err()
		case ch, ok := <-changes:
			if !ok {
				return nil
			}
			if err := send(ch.Status, ch.At); err != nil {
				return err
			}
			if done() {
				s.log.Info("watch finished", "copy_id", id, "reason", "max_updates reached", "sent", sent)
				return nil
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Клієнтський стрімінг: N -> 1 (слайд 21)
// ---------------------------------------------------------------------------

func (s *Server) BulkRegisterCopies(stream pb.InventoryService_BulkRegisterCopiesServer) error {
	sum := &pb.BulkRegisterSummary{}
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			// Клієнт завершив надсилання — час повернути підсумок.
			s.log.Info("bulk register done", "registered", sum.Registered, "failed", sum.Failed)
			return stream.SendAndClose(sum)
		}
		if err != nil {
			return err
		}
		c, err := s.register(req)
		if err != nil {
			// Один поганий запис не має валити всю партію: рахуємо й продовжуємо.
			sum.Failed++
			s.log.Warn("bulk register: item rejected",
				"inventory_number", req.GetInventoryNumber(), "err", err)
			continue
		}
		sum.Registered++
		sum.CopyIds = append(sum.CopyIds, c.GetId())
	}
}

// ---------------------------------------------------------------------------
// Двонапрямлений стрімінг: N <-> N (слайд 22)
// ---------------------------------------------------------------------------

func (s *Server) SyncInventory(stream pb.InventoryService_SyncInventoryServer) error {
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(s.applyEvent(event)); err != nil {
			return err
		}
	}
}

// applyEvent застосовує подію синхронізації та повертає підтвердження (ack).
func (s *Server) applyEvent(ev *pb.SyncEvent) *pb.SyncEvent {
	ack := &pb.SyncEvent{
		BranchId: ev.GetBranchId(),
		CopyId:   ev.GetCopyId(),
		Status:   ev.GetStatus(),
		Sequence: ev.GetSequence(),
	}
	if !validStatus(ev.GetStatus()) {
		ack.Note = "rejected: invalid status"
		return ack
	}
	if _, err := s.store.SetStatus(ev.GetCopyId(), ev.GetStatus()); err != nil {
		ack.Note = "rejected: " + err.Error()
		return ack
	}
	ack.Applied = true
	ack.Note = "applied"
	return ack
}

// ---------------------------------------------------------------------------
// Допоміжне
// ---------------------------------------------------------------------------

func (s *Server) register(req *pb.RegisterCopyRequest) (*pb.Copy, error) {
	if req.GetBookId() == "" || req.GetInventoryNumber() == "" {
		return nil, status.Error(codes.InvalidArgument, "book_id and inventory_number are required")
	}
	c, err := s.store.Create(req.GetBookId(), req.GetInventoryNumber())
	if err != nil {
		return nil, toStatus(err, req.GetInventoryNumber())
	}
	return c, nil
}

func validStatus(st pb.Copy_Status) bool {
	switch st {
	case pb.Copy_AVAILABLE, pb.Copy_BORROWED, pb.Copy_LOST:
		return true
	}
	return false
}

// toStatus перекладає доменні помилки у gRPC-коди (аналог мапінгу на HTTP-статуси).
func toStatus(err error, subject string) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return status.Errorf(codes.NotFound, "copy %s not found", subject)
	case errors.Is(err, store.ErrDuplicateNumber):
		return status.Errorf(codes.AlreadyExists, "inventory number %s already registered", subject)
	default:
		return status.Errorf(codes.Internal, "internal error")
	}
}
