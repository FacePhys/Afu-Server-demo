# 多阶段构建：前端、Go 在构建机本地架构上编译(交叉编译到目标架构)，运行镜像只放二进制。
# 构建：make image(默认 linux/amd64)

# ---------- 前端 ----------
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---------- Go ----------
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/FacePhys/Afu-Server-demo/internal/console.buildRevision=${VERSION}" \
      -o /out/server ./cmd/server \
 && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w" \
      -o /out/fakeclient ./cmd/fakeclient

# ---------- 运行 ----------
FROM alpine:3.22
# ca-certificates：以后连云数据库开TLS时要用；tzdata：日志按 TZ 显示时间
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -H -u 10001 afu
WORKDIR /app
COPY --from=build /out/server /out/fakeclient /app/
USER 10001
ENV TZ=Asia/Shanghai
# 8080：SDK上报(/afu/v1/meter)；18081：内部后台，只应在宿主机本地或内网暴露
EXPOSE 8080 18081
# 配置和密钥运行时挂载进来，不打进镜像
ENTRYPOINT ["/app/server", "-config", "/app/configs/config.yaml"]
