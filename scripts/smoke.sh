#!/usr/bin/env bash
# Наскрізна перевірка: збирає бінарники, запускає сервер і gateway, проганяє
# сценарії зі слайда 44 і зупиняє все. Потрібні: go, curl, bash.
set -u
cd "$(dirname "$0")/.."
mkdir -p bin
go build -o bin/ ./cmd/inventory-server ./cmd/inventory-client ./cmd/gateway || exit 1

bin/inventory-server >bin/server.log 2>&1 & SRV=$!
bin/gateway          >bin/gateway.log 2>&1 & GW=$!
trap 'kill $SRV $GW 2>/dev/null' EXIT
sleep 1.5

C=bin/inventory-client
fail=0
check() { # check "опис" "очікуваний підрядок" команда...
  local name=$1 want=$2; shift 2
  local out; out=$("$@" 2>&1)
  if grep -q -- "$want" <<<"$out"; then echo "  OK   $name"; else echo "  FAIL $name"; echo "$out" | head -3; fail=1; fi
}

echo "gRPC:"
check "GetCopy повертає Copy"          '"id": "copy-1"'  $C get copy-1
check "GetCopy невідомий -> NotFound"  'code=NotFound'   $C get nope
check "Register дубль -> AlreadyExists" 'code=AlreadyExists' bash -c "$C register b SMOKE-1 >/dev/null; $C register b SMOKE-1"
check "Bulk реєструє партію"           '"registered": 3' $C bulk smoke-book 3
check "Sync підтверджує події"         'applied=true'    $C sync
check "Watch завершується io.EOF"      'io.EOF'          $C -max 1 watch copy-1
echo "REST (gateway):"
check "GET /v1/copies/copy-1 -> 200"   '"copy-1"'        curl -s localhost:8080/v1/copies/copy-1
check "GET невідомий -> 404"           '404'             curl -s -o /dev/null -w '%{http_code}' localhost:8080/v1/copies/nope
check "POST дубль -> 409"              '409'             bash -c "curl -s -o /dev/null -X POST localhost:8080/v1/copies -d '{\"book_id\":\"b\",\"inventory_number\":\"SMOKE-R\"}'; curl -s -o /dev/null -w '%{http_code}' -X POST localhost:8080/v1/copies -d '{\"book_id\":\"b\",\"inventory_number\":\"SMOKE-R\"}'"

[ $fail -eq 0 ] && echo "ВСЕ ГАРАЗД" || { echo "Є ПОМИЛКИ"; exit 1; }
