CONFIG ?= configs/config.yaml

.PHONY: db keys admin-token run build build-go web web-dev test itest lint fakeclient

db:
	docker compose up -d mysql

# 已有私钥时不覆盖：重新生成会让已发给客户端的公钥失效
keys:
	@test ! -e keys/kid1.pem || (echo "keys/kid1.pem 已存在，不覆盖"; exit 1)
	mkdir -p keys
	openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out keys/kid1.pem
	chmod 600 keys/kid1.pem
	openssl pkey -in keys/kid1.pem -pubout -out keys/kid1.pub.pem
	openssl rsa -in keys/kid1.pem -RSAPublicKey_out -out keys/kid1.pkcs1.pub.pem

# 内部后台Token：明文只发给本人，SHA-256写进 admin.tokens
admin-token:
	@t=$$(openssl rand -hex 32); \
	echo "token(只发给本人):      $$t"; \
	echo "token_sha256(写进配置): $$(printf %s "$$t" | openssl dgst -sha256 | awk '{print $$NF}')"

run:
	go run ./cmd/server -config $(CONFIG)

# 先构建前端再编译，前端打进二进制
build: web build-go

build-go:
	go build -o bin/server ./cmd/server
	go build -o bin/fakeclient ./cmd/fakeclient

web:
	cd web && npm ci && npm run build

# Vite开发服务器，/afu/internal/api 代理到 127.0.0.1:18081
web-dev:
	cd web && npm run dev

test:
	go test ./...

# 集成测试共用一个测试库，-p 1 串行跑
itest:
	@test -n "$(AFU_TEST_DSN)" || (echo "请先设置 AFU_TEST_DSN"; exit 1)
	go test -p 1 -count=1 -run Integration ./internal/...

lint:
	go vet ./...

fakeclient:
	go run ./cmd/fakeclient
