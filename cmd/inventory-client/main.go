// inventory-client — Go-клієнт, що демонструє всі чотири патерни викликів.
//
//	go run ./cmd/inventory-client get copy-1                  # unary
//	go run ./cmd/inventory-client register book-9 INV-9001    # unary
//	go run ./cmd/inventory-client set-status copy-1 BORROWED  # unary
//	go run ./cmd/inventory-client watch copy-1                # серверний стрімінг
//	go run ./cmd/inventory-client -max 3 watch copy-1         # ...сервер завершить після 3 повідомлень
//	go run ./cmd/inventory-client bulk book-7 5               # клієнтський стрімінг
//	go run ./cmd/inventory-client sync                        # двонапрямлений стрімінг
//
// Глобальні прапорці (-addr, -tls, -token, ...) ставте ПЕРЕД командою.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "libratrack/gen/inventory"
	"libratrack/internal/auth"
	"libratrack/internal/tlsconf"
)

var (
	addr       = flag.String("addr", "localhost:50051", "адреса gRPC-сервера")
	useTLS     = flag.Bool("tls", false, "підключатися через (m)TLS")
	certDir    = flag.String("cert-dir", "certs", "каталог із сертифікатами")
	clientCert = flag.String("client-cert", "client", "ім'я клієнтського сертифіката: client | rogue-client | none")
	token      = flag.String("token", os.Getenv("INVENTORY_TOKEN"), "JWT-токен (або змінна середовища INVENTORY_TOKEN)")
	timeout    = flag.Duration("timeout", 5*time.Second, "таймаут для unary-викликів")
	maxUpdates = flag.Int("max", 0, "watch: сервер завершить потік після N повідомлень (0 — без ліміту)")
)

func main() {
	flag.Usage = usage
	flag.Parse()
	os.Exit(run(flag.Args()))
}

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}

	conn, err := dial()
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial:", err)
		return 1
	}
	defer conn.Close()
	client := pb.NewInventoryServiceClient(conn)

	// Ctrl+C скасовує контекст — так демонструємо скасування стріму клієнтом.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "get":
		err = needArgs(rest, 1, "get <id>")
		if err == nil {
			err = cmdGet(ctx, client, rest[0])
		}
	case "register":
		err = needArgs(rest, 2, "register <book_id> <inventory_number>")
		if err == nil {
			err = cmdRegister(ctx, client, rest[0], rest[1])
		}
	case "set-status":
		err = needArgs(rest, 2, "set-status <id> <AVAILABLE|BORROWED|LOST>")
		if err == nil {
			err = cmdSetStatus(ctx, client, rest[0], rest[1])
		}
	case "watch":
		err = needArgs(rest, 1, "watch <id>")
		if err == nil {
			err = cmdWatch(ctx, client, rest[0])
		}
	case "bulk":
		err = needArgs(rest, 2, "bulk <book_id> <count>")
		if err == nil {
			err = cmdBulk(ctx, client, rest[0], rest[1])
		}
	case "sync":
		err = cmdSync(ctx, client)
	default:
		usage()
		return 2
	}

	if err != nil {
		printRPCError(err)
		return 1
	}
	return 0
}

// ---------------------------------------------------------------------------
// Unary (слайд 19)
// ---------------------------------------------------------------------------

func cmdGet(ctx context.Context, c pb.InventoryServiceClient, id string) error {
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	cp, err := c.GetCopy(ctx, &pb.GetCopyRequest{Id: id})
	if err != nil {
		return err
	}
	printMsg(cp)
	return nil
}

func cmdRegister(ctx context.Context, c pb.InventoryServiceClient, bookID, inv string) error {
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	cp, err := c.RegisterCopy(ctx, &pb.RegisterCopyRequest{BookId: bookID, InventoryNumber: inv})
	if err != nil {
		return err
	}
	printMsg(cp)
	return nil
}

func cmdSetStatus(ctx context.Context, c pb.InventoryServiceClient, id, statusName string) error {
	v, ok := pb.Copy_Status_value[strings.ToUpper(statusName)]
	if !ok {
		return fmt.Errorf("невідомий статус %q (AVAILABLE, BORROWED, LOST)", statusName)
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	cp, err := c.UpdateCopyStatus(ctx, &pb.UpdateCopyStatusRequest{Id: id, Status: pb.Copy_Status(v)})
	if err != nil {
		return err
	}
	printMsg(cp)
	return nil
}

// ---------------------------------------------------------------------------
// Серверний стрімінг: отримання потоку (слайд 42)
// ---------------------------------------------------------------------------

func cmdWatch(ctx context.Context, c pb.InventoryServiceClient, id string) error {
	// Без таймауту: потік живе, доки сервер його не закриє або ми не натиснемо Ctrl+C.
	stream, err := c.WatchCopyStatus(ctx, &pb.WatchRequest{Id: id, MaxUpdates: int32(*maxUpdates)})
	if err != nil {
		return err
	}
	fmt.Printf("слідкуємо за %s (Ctrl+C — вийти)...\n", id)
	for {
		update, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			// io.EOF — це УСПІХ: сервер штатно закрив потік (слайд 42).
			fmt.Println("потік завершено сервером (io.EOF) — все гаразд")
			return nil
		}
		if err != nil {
			if status.Code(err) == codes.Canceled {
				fmt.Println("потік скасовано клієнтом (Ctrl+C)")
				return nil
			}
			return err
		}
		fmt.Printf("%s  %s -> %s\n",
			update.GetChangedAt().AsTime().Local().Format("15:04:05.000"),
			update.GetCopyId(), update.GetStatus())
	}
}

// ---------------------------------------------------------------------------
// Клієнтський стрімінг: N -> 1 (слайд 21)
// ---------------------------------------------------------------------------

func cmdBulk(ctx context.Context, c pb.InventoryServiceClient, bookID, countStr string) error {
	var n int
	if _, err := fmt.Sscanf(countStr, "%d", &n); err != nil || n <= 0 {
		return fmt.Errorf("count має бути додатним числом, отримано %q", countStr)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	stream, err := c.BulkRegisterCopies(ctx)
	if err != nil {
		return err
	}
	for i := 1; i <= n; i++ {
		// Номери детерміновані: повторний запуск дасть AlreadyExists -> failed=N.
		req := &pb.RegisterCopyRequest{BookId: bookID, InventoryNumber: fmt.Sprintf("BULK-%s-%03d", bookID, i)}
		if err := stream.Send(req); err != nil {
			if errors.Is(err, io.EOF) {
				break // сервер уже закрив потік; справжню причину поверне CloseAndRecv
			}
			return err
		}
	}
	summary, err := stream.CloseAndRecv() // сигналізує серверу "я все надіслав" і чекає підсумок
	if err != nil {
		return err
	}
	printMsg(summary)
	return nil
}

// ---------------------------------------------------------------------------
// Двонапрямлений стрімінг: N <-> N (слайд 22)
// ---------------------------------------------------------------------------

func cmdSync(ctx context.Context, c pb.InventoryServiceClient) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	stream, err := c.SyncInventory(ctx)
	if err != nil {
		return err
	}

	events := []*pb.SyncEvent{
		{BranchId: "branch-A", CopyId: "copy-1", Status: pb.Copy_BORROWED, Sequence: 1},
		{BranchId: "branch-A", CopyId: "copy-1", Status: pb.Copy_AVAILABLE, Sequence: 2},
		{BranchId: "branch-A", CopyId: "copy-999", Status: pb.Copy_BORROWED, Sequence: 3}, // неіснуючий
	}

	// Надсилання і отримання йдуть НЕЗАЛЕЖНО: відправник — в окремій горутині.
	sendErr := make(chan error, 1)
	go func() {
		for _, ev := range events {
			if err := stream.Send(ev); err != nil {
				sendErr <- err
				return
			}
			fmt.Printf("-> надіслано seq=%d copy=%s status=%s\n", ev.Sequence, ev.CopyId, ev.Status)
			time.Sleep(300 * time.Millisecond)
		}
		sendErr <- stream.CloseSend() // "більше нічого не надсилаю"; приймати можна й далі
	}()

	for {
		ack, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		fmt.Printf("<- ack      seq=%d copy=%s applied=%v note=%q\n",
			ack.GetSequence(), ack.GetCopyId(), ack.GetApplied(), ack.GetNote())
	}
	return <-sendErr
}

// ---------------------------------------------------------------------------
// Підключення та допоміжне
// ---------------------------------------------------------------------------

func dial() (*grpc.ClientConn, error) {
	var opts []grpc.DialOption
	if *useTLS {
		cfg, err := tlsconf.ClientConfig(*certDir, *clientCert)
		if err != nil {
			return nil, err
		}
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(cfg)))
	} else {
		// Відкритий текст — ЛИШЕ для локальної розробки (слайд 33).
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	if *token != "" {
		// Токен додається до кожного виклику. Через TLS — обов'язково; тут для локального
		// демо дозволяємо й відкритий канал (requireTLS = useTLS).
		opts = append(opts, grpc.WithPerRPCCredentials(auth.NewPerRPCToken(*token, *useTLS)))
	}
	return grpc.NewClient(*addr, opts...) // NewClient не з'єднується одразу — перший RPC це зробить
}

func needArgs(args []string, n int, usageLine string) error {
	if len(args) != n {
		return fmt.Errorf("використання: inventory-client %s", usageLine)
	}
	return nil
}

func printMsg(m proto.Message) {
	fmt.Println(protojson.MarshalOptions{Multiline: true, UseProtoNames: true, EmitUnpopulated: true}.Format(m))
}

func printRPCError(err error) {
	if st, ok := status.FromError(err); ok {
		fmt.Fprintf(os.Stderr, "RPC помилка: code=%s message=%q\n", st.Code(), st.Message())
		return
	}
	fmt.Fprintln(os.Stderr, "помилка:", err)
}

func usage() {
	fmt.Fprint(os.Stderr, `inventory-client [прапорці] <команда> [аргументи]

Команди:
  get <id>                              unary: отримати примірник
  register <book_id> <inventory_number> unary: зареєструвати примірник
  set-status <id> <STATUS>              unary: змінити статус (AVAILABLE|BORROWED|LOST)
  watch <id>                            серверний стрімінг: живі оновлення статусу
  bulk <book_id> <count>                клієнтський стрімінг: масова реєстрація
  sync                                  двонапрямлений стрімінг: синхронізація філій

Прапорці:
`)
	flag.PrintDefaults()
}
