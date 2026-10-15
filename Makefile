BINARY := v2link-go
GOFLAGS := -trimpath
LDFLAGS := -s -w
# 前端产物入口；构建后由根包 //go:embed 打进二进制。
FRONTEND_DIST := frontend/dist/index.html
# 只测本仓库的 Go 包（root + cmd + internal），避免误扫前端依赖目录。
PKGS := . ./cmd/... ./internal/...

.PHONY: all web build test race vet fmt check clean docker run

all: build

# 前端产物（存在则跳过；删除 frontend/dist 可强制重建）。
$(FRONTEND_DIST):
	cd frontend && npm install --no-audit --no-fund && npm run build

web: $(FRONTEND_DIST)

build: web
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/v2link-go

# vet / 单测都需要嵌入的前端产物目录存在。
vet: web
	go vet ./...

test: web
	go test $(PKGS) -count=1

race: web
	go test $(PKGS) -count=1 -race

# 聚合门禁：路由↔文档机检 + vet + 单测。退出码 0 才算过。
check: web
	node scripts/check-api-docs.mjs
	go vet ./...
	go test $(PKGS) -count=1

fmt:
	gofmt -l .

run: build
	./$(BINARY)

docker:
	docker build -t v2link-go:latest .

clean:
	rm -f $(BINARY)
	rm -rf frontend/dist
