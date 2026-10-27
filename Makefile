CONFIG ?= configs/config.yaml
# 镜像版本：git提交号，有未提交改动时带 -dirty
VERSION ?= $(shell git describe --always --dirty 2>/dev/null || echo dev)
PLATFORM ?= linux/amd64
IMAGE ?= afu-server:$(VERSION)
ARCH_SUFFIX := $(subst /,-,$(PLATFORM))
RELEASE_NAME := afu-server-$(VERSION)

.PHONY: db keys admin-token run build build-go web web-dev test itest lint fakeclient image package

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

# 构建 Docker 镜像(默认 linux/amd64，在本机交叉编译)
image:
	docker buildx build --platform $(PLATFORM) --build-arg VERSION=$(VERSION) -t $(IMAGE) --load .

# 打部署包：镜像 + docker-compose + 配置模板 + 建表SQL + 脚本，输出到 release/
package: image
	rm -rf release/$(RELEASE_NAME) && mkdir -p release/$(RELEASE_NAME)/migrations
	docker save $(IMAGE) | gzip > release/$(RELEASE_NAME)/$(RELEASE_NAME)-$(ARCH_SUFFIX).image.tar.gz
	cp deploy/docker-compose.yml deploy/.env.example deploy/config.yaml.example deploy/gen-keys.sh \
	   deploy/gen-admin-token.sh deploy/nginx.conf.example deploy/README.md release/$(RELEASE_NAME)/
	cp migrations/*.sql release/$(RELEASE_NAME)/migrations/
	sed -i.bak 's/^AFU_VERSION=.*/AFU_VERSION=$(VERSION)/' release/$(RELEASE_NAME)/.env.example && rm release/$(RELEASE_NAME)/.env.example.bak
	tar -C release -czf release/$(RELEASE_NAME)-$(ARCH_SUFFIX).tar.gz $(RELEASE_NAME)
	@echo "部署包：release/$(RELEASE_NAME)-$(ARCH_SUFFIX).tar.gz"
