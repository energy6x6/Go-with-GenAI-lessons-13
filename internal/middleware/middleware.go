// Package middleware — окремі інтерсептори: recovery та logging (слайд 33).
// Порядок у ланцюжку має значення; див. cmd/inventory-server/main.go.
package middleware

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// UnaryRecovery перетворює panic у обробнику на codes.Internal замість падіння процесу.
func UnaryRecovery(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic recovered", "method", info.FullMethod, "panic", r, "stack", string(debug.Stack()))
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return h(ctx, req)
	}
}

func StreamRecovery(log *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, h grpc.StreamHandler) (err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic recovered", "method", info.FullMethod, "panic", r, "stack", string(debug.Stack()))
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return h(srv, ss)
	}
}

// UnaryLogging логує метод, gRPC-код, тривалість і адресу клієнта.
func UnaryLogging(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := h(ctx, req)
		logCall(log, ctx, "unary", info.FullMethod, start, err)
		return resp, err
	}
}

func StreamLogging(log *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, h grpc.StreamHandler) error {
		start := time.Now()
		err := h(srv, ss)
		logCall(log, ss.Context(), "stream", info.FullMethod, start, err)
		return err
	}
}

func logCall(log *slog.Logger, ctx context.Context, kind, method string, start time.Time, err error) {
	remote := ""
	if p, ok := peer.FromContext(ctx); ok {
		remote = p.Addr.String()
	}
	log.Info("rpc",
		"kind", kind,
		"method", method,
		"code", status.Code(err).String(),
		"duration", time.Since(start).Round(time.Microsecond).String(),
		"remote", remote,
	)
}
