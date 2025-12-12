# ==========================================
# 多阶段构建 - 构建阶段
# ==========================================
FROM golang:1.25-alpine AS builder

# 安装构建依赖
RUN apk add --no-cache git ca-certificates tzdata

# 设置工作目录
WORKDIR /app

# 复制 go.mod 和 go.sum
COPY go.mod go.sum ./

# 下载依赖
RUN go mod download

# 复制源代码
COPY . .

# 构建应用
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.Version=1.0.0 -X main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /app/ttsfm-server \
    ./cmd/main.go

# ==========================================
# 运行阶段 - 最小镜像
# ==========================================
FROM alpine:3.19

# 安装运行时依赖
RUN apk add --no-cache ca-certificates tzdata

# 创建非 root 用户
RUN addgroup -g 1000 ttsfm && \
    adduser -u 1000 -G ttsfm -s /bin/sh -D ttsfm

# 设置工作目录
WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=builder /app/ttsfm-server /app/ttsfm-server

# 设置文件权限
RUN chown -R ttsfm:ttsfm /app

# 切换到非 root 用户
USER ttsfm

# 暴露端口
EXPOSE 8080

# 健康检查
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# 设置环境变量
ENV TTSFM_HOST=0.0.0.0
ENV TTSFM_PORT=8080

# 启动命令
ENTRYPOINT ["/app/ttsfm-server"]
