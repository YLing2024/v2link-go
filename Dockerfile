# 多阶段构建：先构建前端与 Go 二进制，再产出最小运行镜像。
# 前端产物经 go:embed 打进二进制，运行镜像只需二进制本身。
FROM node:24-bookworm AS web
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
# 构建期公开地址注入用占位值；真实部署值由运行时（nginx 反代 + PUBLIC_* 环境变量）决定。
RUN VITE_PUBLIC_HOST=example.com VITE_PUBLIC_PATH=/v2ws VITE_API_PROXY_TARGET=http://127.0.0.1:19245 npm run build

FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/dist ./frontend/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/v2link-go ./cmd/v2link-go

FROM debian:bookworm-slim
RUN useradd --system --create-home --home-dir /var/lib/v2link-go v2link
USER v2link
WORKDIR /var/lib/v2link-go
COPY --from=build /out/v2link-go /usr/local/bin/v2link-go
ENV HOST=127.0.0.1 PORT=19245 DB_DRIVER=sqlite DB_PATH=/var/lib/v2link-go/v2link.db
EXPOSE 19245
ENTRYPOINT ["/usr/local/bin/v2link-go"]
