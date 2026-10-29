# Зручні скорочення. Без `make` (Windows) просто виконуйте команди з README.
.PHONY: tools proto certs server gateway test bench smoke clean

tools:   ## встановити protoc-плагіни (потрібні лише для перегенерації коду)
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
	go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@v2.20.0

proto:   ## перегенерувати gen/inventory з proto/inventory.proto
	protoc -I proto -I third_party \
	  --go_out=. --go_opt=module=libratrack \
	  --go-grpc_out=. --go-grpc_opt=module=libratrack \
	  --grpc-gateway_out=. --grpc-gateway_opt=module=libratrack \
	  proto/inventory.proto

certs:   ## сертифікати для mTLS
	go run ./cmd/gencerts

server:
	go run ./cmd/inventory-server

gateway:
	go run ./cmd/gateway

test:
	go test ./... -race -count=1

bench:
	go test ./bench -run '^$$' -bench=. -benchmem -benchtime=2s

smoke:   ## наскрізна перевірка (Linux/macOS/Git Bash/WSL; потрібен curl)
	bash scripts/smoke.sh

clean:
	rm -rf certs bin
