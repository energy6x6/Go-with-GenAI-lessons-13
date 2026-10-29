# LibraTrack · gRPC-демо до Уроку 13

Робочий приклад до презентації «Мікросервісна архітектура: gRPC замість REST»:
`InventoryService` на Go з усіма чотирма типами викликів, REST-містком (grpc-gateway),
mTLS, JWT-автентифікацією, тестами та бенчмарком. Усе запускається **локально**, без Docker і без хмари.

## Зміст

1. [Що всередині](#1-що-всередині)
2. [Що потрібно встановити](#2-що-потрібно-встановити)
3. [Швидкий старт (5 хвилин)](#3-швидкий-старт-5-хвилин)
4. [Сценарії демонстрації](#4-сценарії-демонстрації)
5. [Тести та бенчмарк](#5-тести-та-бенчмарк)
6. [Перегенерація коду з .proto](#6-перегенерація-коду-з-proto)
7. [Типові проблеми](#7-типові-проблеми)
8. [Відмінності від слайдів](#8-відмінності-від-слайдів)
9. [Зв'язок із домашнім завданням](#9-зв'язок-із-домашнім-завданням)

---

## 1. Що всередині

```
proto/inventory.proto          контракт (слайди 12–14, 43)
third_party/google/api/        annotations.proto, http.proto (потрібні для grpc-gateway)
gen/inventory/                 ЗГЕНЕРОВАНИЙ код — вже в архіві, protoc для запуску не потрібен
cmd/inventory-server/          gRPC-сервер  (:50051)
cmd/inventory-client/          Go-клієнт: get, register, set-status, watch, bulk, sync
cmd/gateway/                   REST/JSON місток до gRPC (:8080)
cmd/gencerts/                  генератор сертифікатів для mTLS (без openssl)
cmd/mint-token/                генератор демо-JWT
internal/server/               реалізація всіх RPC + тести
internal/auth/                 JWT, RBAC, інтерсептори (unary + stream)
internal/middleware/           recovery і logging
internal/tlsconf/              побудова tls.Config для mTLS
internal/store/                сховище в пам'яті + підписки на зміни
bench/                         бенчмарк gRPC vs REST vs REST-через-gateway
scripts/smoke.sh               наскрізна перевірка
```

**Відповідність слайдам**

| Слайди | Де в коді |
|---|---|
| 12–14 (.proto) | `proto/inventory.proto` |
| 15–16 (protoc, згенероване) | `Makefile` → `make proto`, `gen/inventory/` |
| 19 (unary) | `GetCopy`, `RegisterCopy`, `UpdateCopyStatus` у `internal/server/server.go` |
| 20 (серверний стрімінг) | `WatchCopyStatus` |
| 21 (клієнтський стрімінг) | `BulkRegisterCopies` |
| 22 (двонапрямлений) | `SyncInventory` |
| 28, 43 (grpc-gateway) | `cmd/gateway`, HTTP-анотації в `.proto` |
| 31 (mTLS) | `internal/tlsconf`, `cmd/gencerts` |
| 32–33 (токени, інтерсептори) | `internal/auth`, `internal/middleware` |
| 44, 46 (чеклісти) | `internal/server/*_test.go`, `scripts/smoke.sh` |
| 47 (бенчмарк) | `bench/benchmark_test.go` |

---

## 2. Що потрібно встановити

| Інструмент | Обов'язково? | Навіщо |
|---|---|---|
| **Go 1.22+** | так | збірка і запуск. Перевірка: `go version` |
| **curl** | для REST-сценаріїв | виклики gateway |
| **grpcurl** | бажано | «curl для gRPC» (слайд 41) |
| **protoc** + плагіни | лише якщо міняєте `.proto` | перегенерація коду |

Потрібен інтернет **один раз**, щоб `go mod tidy` завантажив залежності.

**Go:** https://go.dev/dl/ (або `brew install go`, `winget install GoLang.Go`, `sudo apt install golang-go` — якщо версія в apt старіша за 1.22, беріть з go.dev).

**grpcurl** (будь-який варіант):
```bash
brew install grpcurl                                  # macOS
go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest   # будь-яка ОС; бінарник потрапить у $(go env GOPATH)/bin
scoop install grpcurl                                 # Windows (Scoop)
```
Переконайтеся, що `$(go env GOPATH)/bin` є у `PATH`.

**protoc** (потрібен тільки для розділу 6):
```bash
brew install protobuf                  # macOS
sudo apt install protobuf-compiler     # Ubuntu/Debian
winget install protobuf                # Windows  (або: choco install protoc)
protoc --version
```

**Windows:** найзручніше працювати у **WSL** або **Git Bash** — тоді всі команди нижче працюють без змін.
Для чистого PowerShell дивіться примітки з позначкою 🪟.

---

## 3. Швидкий старт (5 хвилин)

Відкрийте **три термінали** у каталозі проєкту.

**Один раз, у будь-якому терміналі:**
```bash
go mod tidy          # завантажить залежності й створить go.sum
go test ./... -race  # перевірка, що все збирається і працює (≈10 с)
```

**Термінал 1 — сервер:**
```bash
go run ./cmd/inventory-server
```
Очікуєте рядок `inventory server started addr=0.0.0.0:50051 ...`. Сервер пише лог кожного виклику (метод, gRPC-код, тривалість).

**Термінал 2 — клієнт:**
```bash
go run ./cmd/inventory-client get copy-1
```
Очікуваний результат:
```json
{
  "id": "copy-1",
  "book_id": "book-1",
  "inventory_number": "INV-0001",
  "status": "AVAILABLE"
}
```

**Термінал 3 — REST-місток** (потрібен сервер із терміналу 1):
```bash
go run ./cmd/gateway
curl localhost:8080/v1/copies/copy-1        # у ще одному вікні
```
🪟 PowerShell: замість `curl` пишіть `curl.exe` (інакше спрацює псевдонім `Invoke-WebRequest`).

Початкові дані: `copy-1` (AVAILABLE), `copy-2` (BORROWED), `copy-3` (LOST).
Зупинка будь-якого процесу — `Ctrl+C`.

---

## 4. Сценарії демонстрації

Усі сценарії йдуть за чекліст слайда 44. Сервер і gateway мають бути запущені (розділ 3).

### 4.1 Unary і коди помилок (слайди 19, 44)

```bash
go run ./cmd/inventory-client get copy-1      # повертає Copy
go run ./cmd/inventory-client get nope        # RPC помилка: code=NotFound message="copy nope not found"
go run ./cmd/inventory-client register book-5 INV-5001
go run ./cmd/inventory-client register book-5 INV-5001   # code=AlreadyExists
```

Те саме через **grpcurl** (слайд 41) — без жодного Go-коду, завдяки server reflection:
```bash
grpcurl -plaintext localhost:50051 list
grpcurl -plaintext localhost:50051 list inventory.v1.InventoryService
grpcurl -plaintext -d '{"id":"copy-1"}' localhost:50051 inventory.v1.InventoryService/GetCopy
grpcurl -plaintext -d '{"id":"nope"}'   localhost:50051 inventory.v1.InventoryService/GetCopy
```
🪟 PowerShell: JSON у `-d` потребує екранування: `-d '{\"id\":\"copy-1\"}'`.

> grpcurl друкує поля у camelCase (`bookId`) — це стандартне JSON-відображення Protobuf.
> Gateway і наш клієнт налаштовані на snake_case (`book_id`), як у REST API з Уроку 7.

**Що показати студентам:** помилка — це `codes.NotFound`, а не загальний `Unknown` (слайд 45).

### 4.2 Серверний стрімінг: живі оновлення (слайди 20, 42)

Потрібні два термінали. У першому:
```bash
go run ./cmd/inventory-client -max 3 watch copy-1
```
Він одразу покаже поточний статус. У другому змінюйте статус:
```bash
go run ./cmd/inventory-client set-status copy-1 BORROWED
go run ./cmd/inventory-client set-status copy-1 AVAILABLE
```
У першому терміналі зʼявляться оновлення, а після третього сервер сам закриє потік, і клієнт напише `потік завершено сервером (io.EOF) — все гаразд`.

**Скасування клієнтом (слайд 46, «чи не витікає горутина»):** запустіть `watch copy-1` без `-max` і натисніть `Ctrl+C`. У логу сервера з'явиться:
```
msg="watch finished" copy_id=copy-1 reason="client gone / cancelled"
```
Це доводить, що обробник бачить скасування через `stream.Context()` і звільняє підписку.

**Автоматичні зміни без другого терміналу:** запустіть сервер з `-simulate` (статуси міняються кожні 2 с, інтервал: `-sim-interval 1s`):
```bash
go run ./cmd/inventory-server -simulate
go run ./cmd/inventory-client -max 5 watch copy-1
```

### 4.3 Клієнтський стрімінг (слайд 21)

```bash
go run ./cmd/inventory-client bulk book-7 5     # registered: 5
go run ./cmd/inventory-client bulk book-7 5     # повторно: failed: 5 (AlreadyExists)
```
Партія реєструється одним викликом; один поганий запис не валить решту.

### 4.4 Двонапрямлений стрімінг (слайд 22)

```bash
go run ./cmd/inventory-client sync
```
Клієнт надсилає три події, сервер підтверджує кожну (`applied=true`), а подія для неіснуючого `copy-999` отримує `applied=false`. Надсилання й отримання йдуть незалежно.

### 4.5 REST через grpc-gateway (слайди 28, 43)

```bash
curl -i localhost:8080/v1/copies/copy-1                       # 200
curl -i localhost:8080/v1/copies/nope                         # 404  (було NotFound)
curl -X POST  localhost:8080/v1/copies -d '{"book_id":"book-8","inventory_number":"INV-8001"}'
curl -i -X POST localhost:8080/v1/copies -d '{"book_id":"book-8","inventory_number":"INV-8001"}'   # 409 (AlreadyExists)
curl -X PATCH localhost:8080/v1/copies/copy-1/status -d '{"status":"LOST"}'
curl -i -X PATCH localhost:8080/v1/copies/copy-1/status -d '{"status":"STATUS_UNSPECIFIED"}'      # 400
```

Стрімінг через REST (потік JSON-рядків):
```bash
curl -N "localhost:8080/v1/copies/copy-1/watch?max_updates=3"
# в іншому терміналі: curl -X PATCH localhost:8080/v1/copies/copy-1/status -d '{"status":"BORROWED"}'
```

Порівняйте з пунктом 4.1: та сама відповідь, але у форматі JSON. Клієнтський та двонапрямлений стрімінг через gateway **не** відкрито (у `.proto` для них немає HTTP-анотацій) — вони доступні лише як нативний gRPC.

🪟 PowerShell: JSON у `-d` пишіть як `-d '{\"book_id\":\"book-8\",\"inventory_number\":\"INV-8001\"}'`.

### 4.6 Токен-автентифікація (слайди 32–33)

Перезапустіть сервер із `-auth`:
```bash
go run ./cmd/inventory-server -auth
```
Згенеруйте токени:
```bash
export LIB=$(go run ./cmd/mint-token -sub anna -role librarian)
export RDR=$(go run ./cmd/mint-token -sub oleh -role reader)
```
🪟 PowerShell: `$env:LIB = go run ./cmd/mint-token -sub anna -role librarian` (і так само `$env:RDR`), далі використовуйте `$env:LIB` замість `$LIB`.

Перевірте:
```bash
go run ./cmd/inventory-client get copy-1                       # code=Unauthenticated "missing token"
go run ./cmd/inventory-client -token "$RDR" get copy-1         # ок
go run ./cmd/inventory-client -token "$RDR" register b INV-1   # code=PermissionDenied (reader не може писати)
go run ./cmd/inventory-client -token "$RDR" bulk book-3 2      # PermissionDenied: стрімінгові RPC теж захищені
go run ./cmd/inventory-client -token "$LIB" register b INV-1   # ок
```
Через grpcurl: `grpcurl -plaintext -H "authorization: Bearer $RDR" -d '{"id":"copy-1"}' localhost:50051 inventory.v1.InventoryService/GetCopy`.

Через REST (gateway сам перекладає заголовок `Authorization` у gRPC-metadata):
```bash
curl -i localhost:8080/v1/copies/copy-1                                    # 401
curl -i -H "Authorization: Bearer $RDR" localhost:8080/v1/copies/copy-1    # 200
curl -i -H "Authorization: Bearer $RDR" -X POST localhost:8080/v1/copies -d '{"book_id":"b","inventory_number":"X"}'   # 403
```

Ролі та політика (хто який метод може викликати) — `DefaultPolicy()` у `internal/auth/interceptor.go`. Метод, якого немає в політиці, заборонено за замовчуванням. Токен діє годину (`-ttl 5m` для коротшого; `-ttl=-1m` дає вже протермінований — зручно для тесту).

> Токен (`INVENTORY_TOKEN`) також можна передати змінною середовища замість `-token`.

### 4.7 mTLS (слайди 30–31)

```bash
go run ./cmd/gencerts                       # створить каталог certs/
go run ./cmd/inventory-server -tls -auth    # термінал 1
go run ./cmd/gateway -tls                   # термінал 3 (gateway теж ходить до сервера по mTLS)
```
Успішний виклик (довірений клієнтський сертифікат + токен):
```bash
go run ./cmd/inventory-client -tls -token "$LIB" get copy-1
```
Негативні сценарії — те, заради чого існує mTLS:

| Команда | Результат |
|---|---|
| `go run ./cmd/inventory-client get copy-1` (без `-tls`) | `Unavailable ... error reading server preface: EOF` |
| `... -tls -client-cert none -token "$LIB" get copy-1` | `Unavailable ... tls: certificate required` |
| `... -tls -client-cert rogue-client -token "$LIB" get copy-1` | `Unavailable ... connection reset by peer` |
| `... -tls get copy-1` (без токена) | `Unauthenticated: missing token` (mTLS пройдено, токена немає) |

Останній рядок показує, що mTLS автентифікує **сервіс**, а токен — **користувача** (слайд 31): це два незалежні рівні.

**Чому повідомлення клієнта розпливчасті?** У TLS 1.3 клієнт завершує рукостискання раніше, ніж сервер перевірить його сертифікат, тому справжню причину видно лише на сервері. Щоб побачити її, запустіть сервер так:
```bash
GRPC_GO_LOG_SEVERITY_LEVEL=info go run ./cmd/inventory-server -tls -auth       # bash / zsh
$env:GRPC_GO_LOG_SEVERITY_LEVEL="info"; go run ./cmd/inventory-server -tls -auth   # PowerShell
```
У логу з'являться причини, наприклад `x509: certificate signed by unknown authority` (для `rogue-client`) або `client didn't provide a certificate` (для `none`).

grpcurl з mTLS:
```bash
grpcurl -cacert certs/ca.pem -cert certs/client.pem -key certs/client-key.pem \
  -H "authorization: Bearer $RDR" -d '{"id":"copy-1"}' \
  localhost:50051 inventory.v1.InventoryService/GetCopy
```

> Сертифікати з `gencerts` дійсні рік і призначені **лише для локального демо**. Каталог `certs/` додано до `.gitignore`. У продакшені їх видає внутрішній CA з автоматичною ротацією (cert-manager).

---

## 5. Тести та бенчмарк

```bash
go test ./... -race -count=1            # усі тести
go test ./internal/server -run Watch -v # лише стрімінг
```
Тести відповідають чекліст слайдів 44 і 46: `NotFound` замість `Unknown`, `io.EOF` після завершення стріму, **відсутність витоку підписки після скасування клієнтом**, захист стрімінгових RPC, відхилення токена з `alg=none`.

**Бенчмарк (слайд 47):**
```bash
go test ./bench -run TestPayloadSize -v                    # розмір повідомлень
go test ./bench -run '^$' -bench=. -benchmem -benchtime=2s # швидкість
```
Бенчмарк порівнює три шляхи до тих самих даних на loopback: нативний gRPC, рукописний REST/JSON і REST через gateway. З'єднання перевикористовуються, а тіло HTTP-відповіді вичитується й закривається — інакше з'єднання не повертається в пул, і порівняння буде нечесним.

**Мої виміри** (Linux, 1 vCPU, Go 1.22, `-benchtime=3s`; у вас цифри будуть іншими):

| Що міряємо | Результат |
|---|---|
| Розмір `Copy` у мережі | Protobuf **28 байт**, JSON **85 байт** (≈3×) |
| Серіалізація + десеріалізація | Protobuf ≈ 410 нс, JSON ≈ 1850 нс (≈4,5× швидше) |
| Один виклик, gRPC | ≈ 33 мкс |
| Один виклик, рукописний REST | ≈ 24 мкс |
| Один виклик, REST через gateway | ≈ 80 мкс (додатковий «стрибок») |

**Не очікуйте, що gRPC виграє в кожному рядку.** Формат справді компактніший і швидший, але для одного крихітного повідомлення на loopback накладні витрати транспорту переважують, і рукописний REST у моєму вимірі навіть випередив gRPC. Це узгоджується зі слайдом 10 («для одного запиту це майже не має значення»). Перевага gRPC проявляється за більших повідомлень, високої паралельності на багатьох ядрах, стрімінгу та повільної мережі. Для домашнього завдання вимірюйте на власному сервісі й описуйте те, що побачили, а не те, що «має бути».

Те, що REST через gateway найповільніший, теж закономірно: запит проходить два протоколи. Тому внутрішні сервіси мають викликати gRPC напряму, оминаючи gateway.

---

## 6. Перегенерація коду з .proto

Потрібна лише якщо ви змінюєте `proto/inventory.proto`. Згенерований код уже лежить у `gen/inventory/`.

```bash
make tools      # встановить protoc-gen-go, protoc-gen-go-grpc, protoc-gen-grpc-gateway (версії зафіксовано)
make proto      # запустить protoc
```
Без `make` (наприклад, Windows) — виконайте напряму:
```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@v2.20.0

protoc -I proto -I third_party \
  --go_out=. --go_opt=module=libratrack \
  --go-grpc_out=. --go-grpc_opt=module=libratrack \
  --grpc-gateway_out=. --grpc-gateway_opt=module=libratrack \
  proto/inventory.proto
```
(у PowerShell запишіть команду `protoc` в один рядок). Плагіни мають бути в `PATH`: додайте `$(go env GOPATH)/bin`.

Після зміни `.proto` запустіть `go build ./... && go test ./...`: якщо ви змінили або видалили метод, компілятор одразу покаже, що зламалось. Це і є «контракт, що контролюється компілятором» зі слайда 27.

Згенеровані файли не редагуйте вручну (слайд 16).

---

## 7. Типові проблеми

| Симптом | Причина і рішення |
|---|---|
| `missing go.sum entry` / `no required module provides package` | Не виконано `go mod tidy`. Запустіть його (потрібен інтернет). |
| `go mod tidy` не знаходить версію `genproto` | Виконайте `go get google.golang.org/genproto/googleapis/api@latest google.golang.org/grpc@latest google.golang.org/protobuf@latest github.com/grpc-ecosystem/grpc-gateway/v2@latest`, потім `go mod tidy`. У цьому разі після оновлення перегенеруйте код (`make proto`). |
| `requires go >= 1.xx` | Стара версія Go. Оновіть з https://go.dev/dl/ |
| `code=Unavailable ... connection refused` | Сервер не запущено або інший порт. Перевірте термінал 1 і прапорець `-addr`. |
| `listen tcp :50051: bind: address already in use` | Порт зайнято (часто — старий екземпляр сервера). Закрийте його або `-addr :50052` (і `-addr localhost:50052` у клієнті). |
| gateway відповідає `503` / `Unavailable` | Gateway не бачить gRPC-сервер: перевірте `-grpc-addr` і чи збігається режим `-tls`. |
| `RPC помилка: code=Unauthenticated` | Сервер запущено з `-auth`, а токена немає або він протермінований. Згенеруйте новий (`mint-token`). Секрет має збігатися (`-jwt-secret` / `JWT_SECRET`). |
| `load server cert (запустіть go run ./cmd/gencerts)` | Немає `certs/`. Запустіть `go run ./cmd/gencerts` з кореня проєкту (клієнт і сервер шукають `certs/` відносно поточного каталогу; інший шлях — `-cert-dir`). |
| `x509: certificate is valid for ..., not ...` | Підключення за іншим ім'ям. Сертифікат сервера дійсний для `localhost`, `127.0.0.1`, `::1`. |
| `protoc-gen-go: program not found` | Плагіни не в `PATH`. Додайте `$(go env GOPATH)/bin`. |
| `google/api/annotations.proto: File not found` | Пропущено `-I third_party` у команді `protoc`. |
| `google/protobuf/timestamp.proto: File not found` | `protoc` встановлено без каталогу `include`. На Windows при ручному розпакуванні архіву покладіть папку `include` поруч із `bin`. |
| `curl` у PowerShell поводиться дивно | Це псевдонім. Використовуйте `curl.exe`. |
| `grpcurl: Failed to dial target host ... context deadline exceeded` | Сервер запущено з `-tls`, а grpcurl іде з `-plaintext` або без клієнтського сертифіката. Використайте `-cacert/-cert/-key` (розділ 4.7). |
| Сервер довго не зупиняється по `Ctrl+C` | Відкритий стрім блокує м'яку зупинку. Сервер чекає 5 с і зупиняється примусово — це нормально. |

---

## 8. Відмінності від слайдів

Усе це зроблено свідомо, щоб демо було запускним і безпечним:

- **`--go_opt=module=libratrack` замість `paths=source_relative`.** Зі `source_relative` файли лягли б у `proto/`, а `go_package` вказує на `libratrack/gen/inventory`. З `module=` код потрапляє точно у `gen/inventory/`, і шлях імпорту збігається.
- **Один `.proto`-файл** замість трьох умовних (`messages`, `inventory_service`, ...) — простіше запускати.
- **Додаткові RPC `RegisterCopy` та `UpdateCopyStatus`.** Другий потрібен, щоб було що спостерігати у `watch`; перший використовує `BulkRegisterCopies`.
- **`stream`-інтерсептор поруч з unary.** На слайді 32 показано лише unary, але без stream-версії три стрімінгові RPC залишились би без автентифікації (є тест `TestAuth_StreamingRPCsAreProtectedToo`).
- **JWT реалізовано мінімально на stdlib** (HS256), щоб не додавати залежність і показати механіку. У продакшені — `golang-jwt/jwt`.
- **Gateway на `net/http`, а не Gin.** `*runtime.ServeMux` — це `http.Handler`; приклад підключення до Gin є в коментарі `cmd/gateway/main.go`.
- **`grpc.NewClient`** замість застарілих `grpc.Dial` / `WithBlock`.
- **Вимкнений за замовчуванням `-simulate`.** Статуси змінюються лише вашими викликами, тому відповіді в README передбачувані.
- **Server reflection увімкнено** заради `grpcurl`. У продакшені її вимикають.
- Відкритий текст (`insecure.NewCredentials()`) — тільки для локальної розробки, як і сказано на слайдах 33 та 44.

**Що перевірено автором.** Код зібрано і прогнано на Linux з Go 1.22.2: `go vet`, `gofmt`, тести з `-race`, `scripts/smoke.sh`, ручні сценарії розділу 4 (включно з mTLS, токенами, grpcurl, curl). Версії залежностей зафіксовано у `go.mod` (grpc 1.64.1, protobuf 1.34.2, grpc-gateway 2.20.0). Скачування залежностей через `go mod tidy` у середовищі автора було недоступне (модульний проксі закритий), тому його відтворено збіркою з вихідників тих самих версій. Якщо `go mod tidy` у вас видасть помилку, скористайтеся рядком з розділу 7. Запуск на Windows і macOS не перевірявся.

---

## 9. Зв'язок із домашнім завданням

1. **Сервіс із 3+ методами, один зі стрімінгом.** Скопіюйте структуру: змініть `proto/inventory.proto` під власну предметну область, виконайте `make proto`, реалізуйте методи за зразком `internal/server/server.go`.
2. **Клієнт і бенчмарк gRPC vs REST.** Візьміть `bench/benchmark_test.go` як шаблон: замініть дані й методи, збережіть умови чесного порівняння (розділ 5).
3. **`.proto` від ШІ та перевірка.** Попросіть ШІ згенерувати `.proto` за описом і звірте результат із чеклістом слайдів 17 та 37: нумерація полів, нульове значення enum, `stream` лише там, де потрібно, `package` з версією, коректність кодів помилок.
