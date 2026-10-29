// Package bench — базовий бенчмарк gRPC vs REST (слайд 47, пункт 2 домашнього завдання).
//
// Умови "чесного порівняння":
//   - та сама машина, ті самі дані (copy-1), реальний TCP на loopback (не in-memory);
//   - обидва клієнти перевикористовують з'єднання (keep-alive / мультиплексування HTTP/2);
//   - тіло HTTP-відповіді завжди вичитується й закривається (інакше з'єднання не
//     повертається в пул — типова помилка наївного http.Get у бенчмарках).
//
// Запуск:
//
//	go test ./bench -bench=. -benchmem -benchtime=2000x
//	go test ./bench -run TestPayloadSize -v          # розмір повідомлень
package bench

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "libratrack/gen/inventory"
	"libratrack/internal/server"
	"libratrack/internal/store"
)

// restCopy — те саме, що Copy, але у вигляді "ручного" REST/JSON (як у REST API з Уроку 7).
type restCopy struct {
	ID              string `json:"id"`
	BookID          string `json:"book_id"`
	InventoryNumber string `json:"inventory_number"`
	Status          string `json:"status"`
}

type env struct {
	grpcClient pb.InventoryServiceClient
	restURL    string // ручний REST:       GET /copies/copy-1
	gwURL      string // REST через gateway: GET /v1/copies/copy-1
	httpClient *http.Client
}

func setup(tb testing.TB) *env {
	tb.Helper()
	st := store.New()

	// 1) gRPC-сервер на реальному TCP-порту loopback.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	grpcSrv := grpc.NewServer()
	pb.RegisterInventoryServiceServer(grpcSrv, server.New(st, slog.New(slog.NewTextHandler(io.Discard, nil))))
	go func() { _ = grpcSrv.Serve(lis) }()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		tb.Fatal(err)
	}

	// 2) "Ручний" REST поверх тих самих даних.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /copies/{id}", func(w http.ResponseWriter, r *http.Request) {
		c, err := st.Get(r.PathValue("id"))
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(restCopy{
			ID: c.Id, BookID: c.BookId, InventoryNumber: c.InventoryNumber, Status: c.Status.String(),
		})
	})
	restSrv := httptest.NewServer(mux)

	// 3) REST через grpc-gateway (додатковий "стрибок" REST -> gRPC).
	gwMux := runtime.NewServeMux(runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
		MarshalOptions: protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true},
	}))
	if err := pb.RegisterInventoryServiceHandler(context.Background(), gwMux, conn); err != nil {
		tb.Fatal(err)
	}
	gwSrv := httptest.NewServer(gwMux)

	tb.Cleanup(func() {
		gwSrv.Close()
		restSrv.Close()
		conn.Close()
		grpcSrv.Stop()
	})

	return &env{
		grpcClient: pb.NewInventoryServiceClient(conn),
		restURL:    restSrv.URL + "/copies/copy-1",
		gwURL:      gwSrv.URL + "/v1/copies/copy-1",
		httpClient: &http.Client{Transport: &http.Transport{
			MaxIdleConns:        512,
			MaxIdleConnsPerHost: 512, // за замовчуванням лише 2 — це б спотворило паралельний тест
		}},
	}
}

func (e *env) httpGet(tb testing.TB, url string) {
	resp, err := e.httpClient.Get(url)
	if err != nil {
		tb.Error(err)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body) // обов'язково вичитати
	resp.Body.Close()                     // ...і закрити, щоб з'єднання повернулось у пул
	if resp.StatusCode != http.StatusOK {
		tb.Errorf("HTTP %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Послідовні виклики
// ---------------------------------------------------------------------------

func BenchmarkGetCopy_gRPC(b *testing.B) {
	e := setup(b)
	ctx := context.Background()
	req := &pb.GetCopyRequest{Id: "copy-1"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.grpcClient.GetCopy(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGetCopy_REST(b *testing.B) {
	e := setup(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.httpGet(b, e.restURL)
	}
}

func BenchmarkGetCopy_RESTviaGateway(b *testing.B) {
	e := setup(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.httpGet(b, e.gwURL)
	}
}

// ---------------------------------------------------------------------------
// Паралельні виклики: тут проявляється мультиплексування HTTP/2 (слайд 7)
// ---------------------------------------------------------------------------

func BenchmarkGetCopyParallel_gRPC(b *testing.B) {
	e := setup(b)
	ctx := context.Background()
	req := &pb.GetCopyRequest{Id: "copy-1"}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb_ *testing.PB) {
		for pb_.Next() {
			if _, err := e.grpcClient.GetCopy(ctx, req); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

func BenchmarkGetCopyParallel_REST(b *testing.B) {
	e := setup(b)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb_ *testing.PB) {
		for pb_.Next() {
			e.httpGet(b, e.restURL)
		}
	})
}

// ---------------------------------------------------------------------------
// Лише серіалізація, без мережі: відокремлюємо вартість ФОРМАТУ від вартості ТРАНСПОРТУ
// ---------------------------------------------------------------------------

func BenchmarkSerialize_Protobuf(b *testing.B) {
	c := &pb.Copy{Id: "copy-1", BookId: "book-1", InventoryNumber: "INV-0001", Status: pb.Copy_AVAILABLE}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		data, err := proto.Marshal(c)
		if err != nil {
			b.Fatal(err)
		}
		var out pb.Copy
		if err := proto.Unmarshal(data, &out); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSerialize_JSON(b *testing.B) {
	c := restCopy{ID: "copy-1", BookID: "book-1", InventoryNumber: "INV-0001", Status: "AVAILABLE"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		data, err := json.Marshal(c)
		if err != nil {
			b.Fatal(err)
		}
		var out restCopy
		if err := json.Unmarshal(data, &out); err != nil {
			b.Fatal(err)
		}
	}
}

// ---------------------------------------------------------------------------
// Розмір повідомлення на дроті (слайд 8): Protobuf vs JSON
// ---------------------------------------------------------------------------

func TestPayloadSize(t *testing.T) {
	c := &pb.Copy{Id: "copy-1", BookId: "book-1", InventoryNumber: "INV-0001", Status: pb.Copy_AVAILABLE}

	protoBytes, err := proto.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	jsonBytes, err := json.Marshal(restCopy{
		ID: c.Id, BookID: c.BookId, InventoryNumber: c.InventoryNumber, Status: c.Status.String(),
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Protobuf: %3d байт  (у мережі — номери полів, без назв)", len(protoBytes))
	t.Logf("JSON:     %3d байт  (назви полів повторюються в кожному повідомленні)", len(jsonBytes))
	t.Logf("JSON більший у %.1f раза", float64(len(jsonBytes))/float64(len(protoBytes)))

	if len(protoBytes) >= len(jsonBytes) {
		t.Fatalf("очікували, що Protobuf компактніший за JSON")
	}
}
