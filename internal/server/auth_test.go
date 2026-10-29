package server_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "libratrack/gen/inventory"
	"libratrack/internal/auth"
)

var testSecret = []byte("test-secret")

// startAuthServer — сервер з тими самими інтерсепторами, що й у cmd/inventory-server.
func startAuthServer(t *testing.T) pb.InventoryServiceClient {
	t.Helper()
	a := auth.NewAuthenticator(testSecret, auth.DefaultPolicy())
	c, _ := startServer(t,
		grpc.ChainUnaryInterceptor(a.Unary()),
		grpc.ChainStreamInterceptor(a.Stream()),
	)
	return c
}

func tokenCtx(t *testing.T, role string, ttl time.Duration) context.Context {
	t.Helper()
	tok, err := auth.Sign(testSecret, auth.Claims{Subject: "tester", Role: role, ExpiresAt: time.Now().Add(ttl).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+tok)
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Fatalf("code = %v, хотіли %v (err=%v)", got, want, err)
	}
}

func TestAuth_NoToken_Unauthenticated(t *testing.T) {
	c := startAuthServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := c.GetCopy(ctx, &pb.GetCopyRequest{Id: "copy-1"})
	wantCode(t, err, codes.Unauthenticated)
}

func TestAuth_ExpiredToken_Unauthenticated(t *testing.T) {
	c := startAuthServer(t)
	_, err := c.GetCopy(tokenCtx(t, auth.RoleLibrarian, -time.Minute), &pb.GetCopyRequest{Id: "copy-1"})
	wantCode(t, err, codes.Unauthenticated)
}

func TestAuth_Reader_CanRead_CannotWrite(t *testing.T) {
	c := startAuthServer(t)
	ctx := tokenCtx(t, auth.RoleReader, time.Hour)

	if _, err := c.GetCopy(ctx, &pb.GetCopyRequest{Id: "copy-1"}); err != nil {
		t.Fatalf("reader має читати: %v", err)
	}
	_, err := c.RegisterCopy(ctx, &pb.RegisterCopyRequest{BookId: "b", InventoryNumber: "N-1"})
	wantCode(t, err, codes.PermissionDenied)
}

func TestAuth_Librarian_CanWrite(t *testing.T) {
	c := startAuthServer(t)
	_, err := c.RegisterCopy(tokenCtx(t, auth.RoleLibrarian, time.Hour),
		&pb.RegisterCopyRequest{BookId: "b", InventoryNumber: "N-2"})
	if err != nil {
		t.Fatal(err)
	}
}

// Саме тут видно, навіщо потрібен Stream()-інтерсептор: unary-інтерсептор
// стрімінгові RPC не перехоплює.
func TestAuth_StreamingRPCsAreProtectedToo(t *testing.T) {
	c := startAuthServer(t)

	// Без токена: стрім відхиляється.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, err := c.WatchCopyStatus(ctx, &pb.WatchRequest{Id: "copy-1", MaxUpdates: 1})
	if err == nil {
		_, err = ws.Recv()
	}
	wantCode(t, err, codes.Unauthenticated)

	// reader не має права на client-streaming запис.
	bs, err := c.BulkRegisterCopies(tokenCtx(t, auth.RoleReader, time.Hour))
	if err == nil {
		_ = bs.Send(&pb.RegisterCopyRequest{BookId: "b", InventoryNumber: "N-3"})
		_, err = bs.CloseAndRecv()
	}
	wantCode(t, err, codes.PermissionDenied)

	// reader може слідкувати.
	ws, err = c.WatchCopyStatus(tokenCtx(t, auth.RoleReader, time.Hour), &pb.WatchRequest{Id: "copy-1", MaxUpdates: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Recv(); err != nil {
		t.Fatalf("reader має слідкувати: %v", err)
	}
}
