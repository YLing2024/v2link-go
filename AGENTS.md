# AGENTS.md — v2link-go 维护约定

本仓库是 v2link 控制面的 Go 重写。旧实现 `/root/proj/v2link` 是**功能权威**（只读，不得修改）。
权威工程规范见 `/root/proj/ENGINEERING_BASELINE.md`；本文件只做本仓库补充，不降低其要求。

## 技术栈（固定）

- HTTP：仅标准库 `net/http`；不引 Web 框架与路由库（分发顺序手写在 `internal/httpapi/api.go`）。
- 数据库：`modernc.org/sqlite`（纯 Go、无 cgo）+ `github.com/go-sql-driver/mysql`；业务 SQL 全收在 `internal/store`。
- 配置：只读环境变量（不引 YAML / dotenv）。
- 加密：`internal/scrypt` 纯标准库实现（N=16384,r=8,p=1，与旧实现参数一致）；不引 `x/crypto`。
- 日志：标准库 `log/slog`。
- 前端：`frontend/` 原样搬入；产物经根包 `//go:embed all:frontend/dist` 打进二进制。

**允许的第三方依赖只有 `modernc.org/sqlite` 与 `github.com/go-sql-driver/mysql`**；新增依赖前先改本文件并说明。

## 目录

- `cmd/v2link-go/`：进程入口（装配 + 优雅退出 + `migrate-data`）。
- `internal/store/`：账本层（SQL 与方言接口）。
- `internal/sqlite/`、`internal/mysql/`：两套薄方言（DDL / upsert / 自增回填 / 列探测 / DSN）。
- `internal/httpapi/`：HTTP 契约层（分发顺序、静态托管、各路由）。
- `internal/{links,monitor,clash,reconcile,regions,auth,xray,ids,expiry,vless,httperr,jsonx}/`：业务与工具。
- `tools/fake-xray/`：验收用假 xray（不参与生产）。
- `deploy/`：systemd / env / nginx 样例（**只提交，不安装到 /etc**）。

## 三条硬约束（改动前必读）

1. **HTTP 契约逐条对齐**：路径、方法、状态码、响应体形状（`{ok,data}` / `{ok:false,error}`、
   分页 `{rows,total,limit,offset}`、`/auth-mode`、`/api/healthz`、`/api/regions/probes`）、
   分发顺序（未登录访问未知 `/api/*` 先 401，已登录才 404 JSON）不得改。改了路由必须同步 `docs/API.md`。
2. **账本 → xray 单向收敛**：账本为权威、xray 为镜像；**只读账本、绝不因 xray 失败改账本**；
   只走 `adu/rmu` 热管理，不 reload、不断存量连接；稳态无动作不打日志。
3. **数据库迁移只做增量**：禁止 DROP / TRUNCATE / 重建表；两种方言下都要能通过同一套测试。

## 红线

- 源码 / 配置 / 文档 / 提交信息**绝不**出现真实域名、公网 / 内网 IP、邮箱、密钥、真实口令、真实 `vless://` 链接；
  示例统一 `example.com` / `127.0.0.1`。
- 不写 `fmt.Println` 调试残留（`cmd/` 的一次性口令打印除外）；不留注释掉的死代码；新增 TODO/FIXME 必须为 0。
- 登录限速、会话 token、口令哈希、数据库口令一律不进日志。
- 只监听 `127.0.0.1`；生产端口 `19245`。

## 命令

- `make build` / `make test` / `make race` / `make vet` / `make check`（机检路由文档 + vet + 单测）。
- 依赖安装与前端命令一律带 `timeout`。

## 提交

小步提交、中文信息、不带署名与 TASK 号；**禁止** `git push`、`checkout -b`、`rebase`、
`reset --hard`、`merge`；不要 `git add` 运行产物（二进制、`frontend/dist`、`frontend/node_modules`、数据目录）。
