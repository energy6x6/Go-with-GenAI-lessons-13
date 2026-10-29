package server_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	pb "libratrack/gen/inventory"
	"libratrack/internal/server"
	"libratrack/internal/store"
)

// startServer піднімає справжній gRPC-сервер у пам'яті (bufconn — без мережі й портів).
func startServer(t *testing.T, serverOpts ...grpc.ServerOption) (pb.InventoryServiceClient, *store.Store) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	st := store.New()
	srv := grpc.NewServer(serverOpts...)
	pb.RegisterInventoryServiceServer(srv, server.New(st, slog.New(slog.NewTextHandler(io.Discard, nil))))
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(); srv.Stop() })
	return pb.NewInventoryServiceClient(conn), st
}

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// --- Unary (чекліст слайда 44, рядки 1–2) ---------------------------------

func TestGetCopy_OK(t *testing.T) {
	c, _ := startServer(t)
	got, err := c.GetCopy(ctxT(t), &pb.GetCopyRequest{Id: "copy-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetInventoryNumber() != "INV-0001" || got.GetStatus() != pb.Copy_AVAILABLE {
		t.Fatalf("неочікуваний примірник: %v", got)
	}
}

func TestGetCopy_UnknownID_IsNotFound(t *testing.T) {
	c, _ := startServer(t)
	_, err := c.GetCopy(ctxT(t), &pb.GetCopyRequest{Id: "nope"})
	if got := status.Code(err); got != codes.NotFound { // а не codes.Unknown (слайд 45)
		t.Fatalf("code = %v, хотіли NotFound (err=%v)", got, err)
	}
}

func TestGetCopy_EmptyID_IsInvalidArgument(t *testing.T) {
	c, _ := startServer(t)
	_, err := c.GetCopy(ctxT(t), &pb.GetCopyRequest{})
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Fatalf("code = %v, хотіли InvalidArgument", got)
	}
}

func TestRegisterCopy_DuplicateInventoryNumber_IsAlreadyExists(t *testing.T) {
	c, _ := startServer(t)
	req := &pb.RegisterCopyRequest{BookId: "book-9", InventoryNumber: "INV-9"}
	if _, err := c.RegisterCopy(ctxT(t), req); err != nil {
		t.Fatal(err)
	}
	_, err := c.RegisterCopy(ctxT(t), req)
	if got := status.Code(err); got != codes.AlreadyExists {
		t.Fatalf("code = %v, хотіли AlreadyExists", got)
	}
}

func TestUpdateCopyStatus_Unspecified_IsInvalidArgument(t *testing.T) {
	c, _ := startServer(t)
	_, err := c.UpdateCopyStatus(ctxT(t), &pb.UpdateCopyStatusRequest{Id: "copy-1"})
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Fatalf("code = %v, хотіли InvalidArgument", got)
	}
}

// --- Серверний стрімінг (чекліст слайда 44, рядок 3) ------------------------

func TestWatchCopyStatus_ServerClosesAfterMax_ClientSeesEOF(t *testing.T) {
	c, _ := startServer(t)
	ctx := ctxT(t)

	stream, err := c.WatchCopyStatus(ctx, &pb.WatchRequest{Id: "copy-1", MaxUpdates: 3})
	if err != nil {
		t.Fatal(err)
	}
	// 1-ше повідомлення — поточний знімок (AVAILABLE). Коли ми його отримали,
	// підписка на зміни вже гарантовано існує, тож далі можна міняти статус.
	first, err := stream.Recv()
	if err != nil || first.GetStatus() != pb.Copy_AVAILABLE {
		t.Fatalf("snapshot: %v, %v", first, err)
	}
	for _, st := range []pb.Copy_Status{pb.Copy_BORROWED, pb.Copy_AVAILABLE} {
		if _, err := c.UpdateCopyStatus(ctx, &pb.UpdateCopyStatusRequest{Id: "copy-1", Status: st}); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []pb.Copy_Status{pb.Copy_BORROWED, pb.Copy_AVAILABLE} {
		upd, err := stream.Recv()
		if err != nil {
			t.Fatal(err)
		}
		if upd.GetStatus() != want {
			t.Fatalf("status = %v, хотіли %v", upd.GetStatus(), want)
		}
	}
	// Після 3-го повідомлення сервер штатно закриває потік -> чистий io.EOF.
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("очікували io.EOF, отримали %v", err)
	}
}

func TestWatchCopyStatus_UnknownID_IsNotFound(t *testing.T) {
	c, _ := startServer(t)
	stream, err := c.WatchCopyStatus(ctxT(t), &pb.WatchRequest{Id: "nope"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = stream.Recv()
	if got := status.Code(err); got != codes.NotFound {
		t.Fatalf("code = %v, хотіли NotFound", got)
	}
}

// Чекліст слайда 46: "скасуйте контекст і переконайтеся, що серверний цикл
// завершується, а горутина не витікає".
func TestWatchCopyStatus_ClientCancel_ReleasesSubscription(t *testing.T) {
	c, st := startServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := c.WatchCopyStatus(ctx, &pb.WatchRequest{Id: "copy-1"}) // без ліміту
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil { // знімок отримано => підписка активна
		t.Fatal(err)
	}
	if n := st.SubscriberCount(); n != 1 {
		t.Fatalf("під час спостереження підписок = %d, хотіли 1", n)
	}

	cancel() // клієнт "йде"

	deadline := time.Now().Add(2 * time.Second)
	for st.SubscriberCount() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("підписка не звільнилась після скасування клієнтом — витік!")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// --- Клієнтський стрімінг ----------------------------------------------------

func TestBulkRegisterCopies_CountsRegisteredAndFailed(t *testing.T) {
	c, _ := startServer(t)
	stream, err := c.BulkRegisterCopies(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	reqs := []*pb.RegisterCopyRequest{
		{BookId: "b", InventoryNumber: "X-1"},
		{BookId: "b", InventoryNumber: "X-2"},
		{BookId: "b", InventoryNumber: "X-1"}, // дубль -> failed
		{BookId: "", InventoryNumber: "X-3"},  // некоректний -> failed
	}
	for _, r := range reqs {
		if err := stream.Send(r); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := stream.CloseAndRecv()
	if err != nil {
		t.Fatal(err)
	}
	if sum.GetRegistered() != 2 || sum.GetFailed() != 2 || len(sum.GetCopyIds()) != 2 {
		t.Fatalf("підсумок: %v", sum)
	}
}

// --- Двонапрямлений стрімінг -------------------------------------------------

func TestSyncInventory_AcksEveryEvent(t *testing.T) {
	c, st := startServer(t)
	stream, err := c.SyncInventory(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	events := []*pb.SyncEvent{
		{BranchId: "A", CopyId: "copy-1", Status: pb.Copy_BORROWED, Sequence: 1},
		{BranchId: "A", CopyId: "copy-999", Status: pb.Copy_BORROWED, Sequence: 2},
	}
	wantApplied := []bool{true, false}
	for i, ev := range events {
		if err := stream.Send(ev); err != nil {
			t.Fatal(err)
		}
		ack, err := stream.Recv()
		if err != nil {
			t.Fatal(err)
		}
		if ack.GetSequence() != ev.Sequence || ack.GetApplied() != wantApplied[i] {
			t.Fatalf("ack[%d] = %v", i, ack)
		}
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("очікували io.EOF, отримали %v", err)
	}
	if got, _ := st.Get("copy-1"); got.GetStatus() != pb.Copy_BORROWED {
		t.Fatalf("подію не застосовано: %v", got)
	}
}
