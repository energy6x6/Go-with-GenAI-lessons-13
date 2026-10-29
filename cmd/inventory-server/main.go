// inventory-server — gRPC InventoryService (слайд 41: "слухає порт :50051").
//
// Приклади запуску:
//
//	go run ./cmd/inventory-server                         # відкритий текст, без автентифікації
//	go run ./cmd/inventory-server -simulate               # + автоматичні зміни статусів
//	go run ./cmd/inventory-server -auth                   # + токен-автентифікація (JWT)
//	go run ./cmd/inventory-server -tls -auth              # + mTLS (спершу: go run ./cmd/gencerts)
package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"

	pb "libratrack/gen/inventory"
	"libratrack/internal/auth"
	"libratrack/internal/middleware"
	"libratrack/internal/server"
	"libratrack/internal/store"
	"libratrack/internal/tlsconf"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:50051", "адреса, яку слухає gRPC-сервер")
	useTLS := flag.Bool("tls", false, "увімкнути mTLS (потрібні сертифікати з `go run ./cmd/gencerts`)")
	certDir := flag.String("cert-dir", "certs", "каталог із сертифікатами")
	useAuth := flag.Bool("auth", false, "вимагати JWT-токен у metadata (authorization: Bearer ...)")
	secret := flag.String("jwt-secret", envOr("JWT_SECRET", auth.DefaultDemoSecret), "HMAC-секрет для JWT")
	simulate := flag.Bool("simulate", false, "імітувати зміни статусів примірників")
	simEvery := flag.Duration("sim-interval", 2*time.Second, "інтервал симуляції")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Ланцюжок інтерсепторів (слайд 33): recovery -> logging -> auth.
	// recovery зовні, щоб ловити panic усіх наступних; logging зовні від auth,
	// щоб відмови автентифікації теж потрапляли в лог.
	unary := []grpc.UnaryServerInterceptor{middleware.UnaryRecovery(log), middleware.UnaryLogging(log)}
	stream := []grpc.StreamServerInterceptor{middleware.StreamRecovery(log), middleware.StreamLogging(log)}
	if *useAuth {
		a := auth.NewAuthenticator([]byte(*secret), auth.DefaultPolicy())
		unary = append(unary, a.Unary())
		stream = append(stream, a.Stream()) // без цього стрімінгові RPC лишились би відкритими
	}
	opts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(unary...),
		grpc.ChainStreamInterceptor(stream...),
	}

	if *useTLS {
		cfg, err := tlsconf.ServerConfig(*certDir)
		if err != nil {
			log.Error("tls config", "err", err)
			os.Exit(1)
		}
		opts = append(opts, grpc.Creds(credentials.NewTLS(cfg)))
	}

	st := store.New()
	srv := grpc.NewServer(opts...)
	pb.RegisterInventoryServiceServer(srv, server.New(st, log))
	reflection.Register(srv) // дозволяє grpcurl працювати без .proto-файлів (у проді — вимкнути)

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Error("listen", "addr", *addr, "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *simulate {
		go st.Simulate(ctx, *simEvery)
	}

	log.Info("inventory server started",
		"addr", lis.Addr().String(), "mtls", *useTLS, "auth", *useAuth, "simulate", *simulate)
	if !*useTLS {
		log.Warn("TLS вимкнено — відкритий текст лише для локальної розробки (слайд 33)")
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(lis) }()

	select {
	case err := <-serveErr:
		log.Error("serve", "err", err)
		os.Exit(1)
	case <-ctx.Done():
	}

	// GracefulStop чекає завершення всіх викликів, а довгі стріми самі не завершаться —
	// тому даємо 5 секунд і примусово зупиняємо.
	log.Info("shutting down...")
	done := make(chan struct{})
	go func() { srv.GracefulStop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		log.Warn("graceful stop timed out, forcing")
		srv.Stop()
	}
	log.Info("bye")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
