# LibraTrack gRPC Inventory

Навчальний Go-сервіс обліку бібліотечних примірників для Task 13.
Початкове демо збережено в `main`; розширення доступності книг розробляється в `develop` і подається через pull request у `main`.

## Можливості

| RPC | Тип | Призначення |
| --- | --- | --- |
| `GetCopy` | Unary | Примірник за ID |
| `RegisterCopy` | Unary | Реєстрація примірника |
| `UpdateCopyStatus` | Unary | Зміна стану примірника |
| `WatchCopyStatus` | Server streaming | Живі зміни одного примірника |
| `BulkRegisterCopies` | Client streaming | Пакетна реєстрація |
| `SyncInventory` | Bidirectional streaming | Події та підтвердження |
| **`GetBookAvailability`** | **Unary** | **Власне розширення: підсумок примірників книги** |
| **`WatchBookAvailability`** | **Server streaming** | **Власне розширення: зміни підсумку в реальному часі** |

Для нових методів визначено окремі Request/Response. Початкові RPC, поля, номери та HTTP-маршрути збережено; сумісність перевіряє `TestOriginalSchemaCompatibility`.

## Вимоги та структура

Go 1.22+, Git; для smoke-перевірки — Python 3.8+, bash. Для ручних HTTP-викликів потрібен curl; grpcurl необов'язковий. Protoc 26.0 та плагіни потрібні лише для генерації. Docker потрібен лише для додаткового локального SonarQube.

```text
cmd/inventory-server/   gRPC-сервер
cmd/inventory-client/   Go CLI для всіх восьми RPC
cmd/gateway/            REST/JSON gateway
cmd/gencerts/           локальні mTLS-сертифікати
cmd/mint-token/         демонстраційні JWT
proto/                 контракт inventory.v1.InventoryService
gen/inventory/         згенеровані messages, stubs і gateway
internal/store/        сховище в пам'яті, підсумки та підписки
internal/server/       обробники RPC та транспортні тести
internal/auth/         JWT/RBAC для unary і streaming
internal/middleware/   логування та recovery
internal/tlsconf/      mTLS-конфігурація
bench/                 порівняння gRPC, REST і gateway
scripts/               відтворювані перевірки
```

## Швидкий запуск

```bash
git clone --branch develop git@github.com:energy6x6/Go-with-GenAI-lessons-13.git
cd Go-with-GenAI-lessons-13
go mod download
go test -race -count=1 ./...
```

Термінал 1 — сервер:

```bash
go run ./cmd/inventory-server -addr 127.0.0.1:50051
```

Термінал 2 — власний unary RPC:

```bash
go run ./cmd/inventory-client availability book-1
```

Після свіжого запуску: `total=2`, `available=1`, `borrowed=1`, `lost=0`, `revision=3`. Protobuf JSON представляє `uint64 revision` рядком.

## Власний server streaming

Після свіжого запуску сервера в терміналі 2:

```bash
go run ./cmd/inventory-client -max 3 -stream-timeout 60s watch-availability book-1
```

У терміналі 3 виконуйте наступну команду після появи попереднього оновлення:

```bash
go run ./cmd/inventory-client set-status copy-1 LOST
go run ./cmd/inventory-client register book-1 DEMO-NEW
```

Очікуваний потік:

```text
book=book-1 total=2 available=1 borrowed=1 lost=0 revision=3
book=book-1 total=2 available=0 borrowed=1 lost=1 revision=4
book=book-1 total=3 available=1 borrowed=1 lost=1 revision=5
потік завершено сервером (io.EOF) — все гаразд
```

Перший знімок і підписка створюються атомарно. Після реєстрації або фактичної зміни стану `revision` зростає; повторний запис того самого стану оновлення не створює. Зміни іншої книги не надсилаються.

Це потік **актуального стану**, а не журнал подій: один очікуваний знімок у буфері замінюється новішим для повільного клієнта. Проміжні ревізії можуть пропускатися. Дані та ревізії скидаються після перезапуску сервера.

`-max 1` повертає один знімок і EOF. `-max 0` працює до Ctrl+C або дедлайну; `-stream-timeout` типово становить 30 секунд. Скасування, дедлайн і завершення звільняють підписку; сервер має обмежену в часі м'яку зупинку.

## Початкові приклади та помилки

```bash
go run ./cmd/inventory-client get copy-1
go run ./cmd/inventory-client get missing                  # NotFound
go run ./cmd/inventory-client register book-9 DEMO-9001
go run ./cmd/inventory-client register book-9 DEMO-9001     # AlreadyExists
go run ./cmd/inventory-client bulk book-7 3                # registered=3
go run ./cmd/inventory-client bulk book-7 3                # failed=3
go run ./cmd/inventory-client sync                         # applied=true та applied=false
go run ./cmd/inventory-client -max 1 watch copy-1           # знімок і EOF
go run ./cmd/inventory-client availability missing         # NotFound
go run ./cmd/inventory-client availability ''              # InvalidArgument
```

Усі прапорці ставте перед командою. Unary-виклики мають типовий таймаут 5 секунд (`-timeout`). Для нових RPC порожній/пробільний `book_id` та від'ємний `max_updates` некоректні. Книга без зареєстрованих примірників повертає `NotFound`.

## REST gateway

В окремому терміналі:

```bash
go run ./cmd/gateway -http-addr 127.0.0.1:8080 -grpc-addr 127.0.0.1:50051
```

```bash
curl -i http://127.0.0.1:8080/v1/copies/copy-1
curl -i http://127.0.0.1:8080/v1/books/book-1/availability
curl -N 'http://127.0.0.1:8080/v1/books/book-1/availability/watch?max_updates=1'
curl -i http://127.0.0.1:8080/v1/books/missing/availability  # 404
```

Unary-відповідь gateway за змістом збігається з нативним gRPC. Server streaming доступний як JSON-потік; client streaming та bidi доступні лише через gRPC.

```bash
grpcurl -plaintext 127.0.0.1:50051 list inventory.v1.InventoryService
grpcurl -plaintext -d '{"book_id":"book-1"}' 127.0.0.1:50051 inventory.v1.InventoryService/GetBookAvailability
```

## mTLS та JWT/RBAC

Зупиніть попередні сервер і gateway. Сертифікати створюйте поза репозиторієм:

```bash
export CERT_DIR="$(mktemp -d /tmp/libratrack-certs.XXXXXX)"
go run ./cmd/gencerts -dir "$CERT_DIR"
go run ./cmd/inventory-server -tls -auth -cert-dir "$CERT_DIR"
```

В іншому терміналі задайте `CERT_DIR` тим самим абсолютним шляхом і виконайте:

```bash
export INVENTORY_TOKEN="$(go run ./cmd/mint-token -role reader)"
go run ./cmd/inventory-client -tls -cert-dir "$CERT_DIR" availability book-1
go run ./cmd/inventory-client -tls -cert-dir "$CERT_DIR" -max 1 watch-availability book-1
go run ./cmd/inventory-client -tls -cert-dir "$CERT_DIR" register book-1 DENIED  # PermissionDenied
```

`reader` читає та спостерігає; `librarian` також змінює інвентар. Методи без правила заборонено. Без JWT — `Unauthenticated`; без довіреного клієнтського сертифіката — відмова mTLS. Smoke-перевірка відтворює ці сценарії без виводу токенів.

Без TLS застосунок призначений лише для локального запуску; типові адреси прив'язані до loopback. Вихідний JWT-код HS256 і сертифікати є демонстраційними. Reflection увімкнено для grpcurl. Це навчальний приклад, а не конфігурація для production.

## Перевірки та генерація

```bash
go build ./...
go vet ./...
go test -race -count=1 ./...
bash scripts/smoke.sh
```

Smoke запускає окремі процеси, виконує 31 сценарій, прибирає власні процеси та друкує шлях до зовнішнього каталогу доказів. Порти 50051 і 8080 мають бути вільними. Можна явно передати `--evidence-dir /absolute/path/outside/repository`.

```bash
make tools
export PATH="$(go env GOPATH)/bin:$PATH"
make proto
git diff -- gen/
```

Версії плагінів зафіксовано в Makefile. Для точного відтворення generated-файлів використовується protoc 26.0. Повторна генерація не має змінювати файли. GitHub Actions перевіряє module-файли, форматування, build, vet, race tests, сумісність із `main`, генерацію та smoke на Linux з Go 1.22.12.

## Бенчмарк

```bash
go test ./bench -run TestPayloadSize -v
go test ./bench -run '^$' -bench=. -benchmem -benchtime=1s -count=3
```

Локальна перевірка 29.10.2026, macOS amd64, Go 1.27.1: медіани трьох послідовних прогонів — gRPC **95,003 мкс**, REST **81,052 мкс**, REST через gateway **213,133 мкс**. Серіалізоване тіло прикладу: Protobuf **28 байт**, JSON **85 байт**, без транспортного framing.

Це виміри невеликого повідомлення на loopback, зі з'єднаннями, що перевикористовуються. Паралельні прогони мали помітний розкид; результати не є гарантією швидкості протоколу. Повні логи вимірів зберігаються окремо від коду.

## Локальний SonarQube

Додаткова статична перевірка виконується в SonarQube Community 26.10 через Docker. Контейнер доступний лише через http://127.0.0.1:9000; H2 використовується для локального навчального запуску. Образи сервера та scanner зафіксовано за digest.

```bash
docker compose -p lesson13-quality -f compose.sonarqube.yml up -d
```

Створіть у локальному SonarQube проєкт `libratrack-lesson13` та analysis token для нього. Передайте токен змінною `SONAR_TOKEN`; не записуйте його до Git. У наведених командах задайте власний абсолютний зовнішній каталог:

```bash
export SONAR_EVIDENCE_DIR=/absolute/path/outside/repository/quality
mkdir -p "$SONAR_EVIDENCE_DIR"
go test -race -count=1 -coverprofile="$SONAR_EVIDENCE_DIR/unit.out" ./...
bash scripts/smoke.sh --coverage --evidence-dir "$SONAR_EVIDENCE_DIR/smoke"
python3 scripts/merge-coverage.py "$SONAR_EVIDENCE_DIR/unit.out" "$SONAR_EVIDENCE_DIR/smoke/coverage.out" --output "$SONAR_EVIDENCE_DIR/coverage.out"
bash scripts/sonar.sh
```

Покриття об'єднує unit/transport-тести та інструментовані запуски CLI, сервера й gateway. `sonar-project.properties` виключає generated-код і сторонні схеми. Scanner читає код через read-only mount, а його робочий каталог та звіт покриття залишаються ззовні. Локальний результат не позначається як SonarQube Cloud.

Зупинка зі збереженням результатів:

```bash
docker compose -p lesson13-quality -f compose.sonarqube.yml stop
```

## Діагностика

| Симптом | Що перевірити |
| --- | --- |
| `Unavailable` | Чи запущений сервер, адреса та режим TLS |
| Зайнятий порт | Зупиніть власний попередній процес або змініть `-addr` / `-http-addr` |
| `Unauthenticated` | Токен, його строк дії та однаковий секрет сервера й генератора |
| `PermissionDenied` | Роль і правила для відповідного unary/stream RPC |
| Помилка сертифіката | Спільний `CERT_DIR`, довірений CA, ім'я localhost/127.0.0.1 |
| Потік чекає наступного повідомлення | Змініть стан, задайте `-max 1` або натисніть Ctrl+C |
| `DeadlineExceeded` у watch-availability | Збільште `-stream-timeout` або задайте `-max` |

Перевірено локально на macOS amd64; Linux перевіряється CI. Для Windows використовуйте WSL/Git Bash; нативний PowerShell окремо не перевірявся.
