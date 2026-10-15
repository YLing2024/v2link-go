# v2link-go

临时 VLESS 链接管理控制面的 **Go 重写版**。行为逐条对齐旧实现（`/root/proj/v2link`，Node 24 + Express 4 + SQLite），
默认监听 `127.0.0.1:19245`（旧实现的 `7897` 不动，两者可并存）。

- **控制面 / 数据面分离**：控制面（本服务）管账本与 HTTP；数据面 xray-core 走 `xray api adu/rmu` 热管理，
  不 reload、不断存量连接。
- **单二进制**：前端产物经 `//go:embed` 打进二进制；运行时不依赖外部语言运行时。
- **仅 MySQL**：新版只支持 MySQL（`github.com/go-sql-driver/mysql`），没有 SQLite 依赖与 `DB_DRIVER` 开关；
  旧 SQLite 数据用独立工具 `tools/migrate-sqlite-to-mysql/` 一次性搬运。

## 构建与运行

```bash
make build          # 构建前端产物 + 编译二进制（./v2link-go）
make test           # go test（-count=1）
make race           # 关键并发路径加 -race
make check          # 聚合门禁：路由↔文档机检 + go vet + go test（退出码 0 才算过）

make run            # 构建后直接运行（读环境变量）
./v2link-go --static-only            # 只托管嵌入的前端产物（本地预览）
```

## 配置（环境变量）

配置**只读环境变量**（对齐旧实现的 systemd `EnvironmentFile` 部署口径），不读 YAML / `.env` 文件。
旧实现的业务键名与默认值逐条对齐，新增的是数据库连接键（MySQL）与默认端口由 `7897` 改为 `19245`。
全部键见 `deploy/env.example`。

- 数据面：`XRAY_API`、`XRAY_BIN`、`XRAY_INBOUND_TAG`、`XRAY_API_TIMEOUT_S`、`XRAY_API_RETRIES`
- 时限：`DEFAULT_HOURS`、`MAX_HOURS`
- 调度：`EXPIRE_SCAN_INTERVAL_S`、`LEDGER_INTERVAL_S`、`XRAY_RECONCILE_INTERVAL_S`、
  `CONN_CLEANUP_INTERVAL_S`、`CONN_RETENTION_S`、`SAMPLE_CLEANUP_INTERVAL_S`、`SAMPLE_RETENTION_S`、
  `REGION_PROBE_INTERVAL_S`、`REGION_PROBES`
- 采集：`ACCESS_LOG_PATH`
- 数据库：`DB_HOST`、`DB_PORT`、`DB_NAME`、`DB_USER`、`DB_PASSWORD`、`DB_DSN`（仅 MySQL）
- Clash：`CLASH_SUB_TTL_S`、`PUBLIC_BASE_URL`、`CLASH_DIRECT_DOMAINS`
- 认证：`AUTH_MODE`、`V2LINK_ADMIN_USER`、`V2LINK_ADMIN_PASSWORD`、`SESSION_TTL_HOURS`、`TRUST_PROXY`
- 公开地址（生成 `vless://`）：`PUBLIC_HOST`、`PUBLIC_PATH`、`PUBLIC_SNI`

## 目录

```
cmd/v2link-go/       进程入口（装配 / 优雅退出）
internal/
  config/            环境变量解析
  store/             账本访问层 + MySQL 建表/增量迁移
  ids/ expiry/ vless/ id、UUID、有效期、vless URI
  xray/              xray api 客户端（os/exec，超时 + 退避重试）
  links/             链接业务编排（创建/吊销/延长/删除）
  monitor/           access log 采集、流量解析/聚合
  clash/             Clash 订阅（YAML 自实现）
  reconcile/         账本 → xray 单向一致性收敛
  regions/           地区连通性探测
  auth/              scrypt 口令、会话 Cookie、登录限速
  httpapi/           HTTP 契约层（标准库 net/http）
frontend/            前端源码（原样搬入；产物嵌入）
tools/fake-xray/     验收用假 xray（不参与生产）
tools/migrate-sqlite-to-mysql/  一次性迁移工具（独立 module，不参与服务运行时）
deploy/              systemd / env / nginx 样例（不安装）
docs/API.md          接口契约（权威，机检）
docs/DB.md           MySQL 表结构与旧库迁移映射
docs/COMPONENTS.md   前端组件登记
docs/ACCEPTANCE.md    验收方法与真实结果
```

## 接口契约

见 `docs/API.md`（权威）。`scripts/check-api-docs.mjs` 机检「源码路由 ↔ 文档标题」一致，随 `make check` 一起跑。

## 数据库

见 `docs/DB.md`。只支持 MySQL：给一条 `DB_DSN`，或 `DB_HOST`/`DB_PORT`/`DB_NAME`/`DB_USER`/`DB_PASSWORD`
（`DB_NAME`/`DB_USER` 必填）。迁移只做增量（禁止 DROP / TRUNCATE / 重建表），老库原地升级、数据保留。
旧 SQLite 数据用 `tools/migrate-sqlite-to-mysql/` 一次性搬入。

## 与旧实现的有意差异

见交付报告；要点：

1. 默认端口 `7897` → `19245`（新旧并存）。
2. 存储由 SQLite 改为**仅 MySQL**，并提供独立迁移工具（需求书 §3）。
3. 配置只读环境变量（不读 `.env` / YAML）。
4. 前端目录沿用 `frontend/`（需求书 §5 用语；§7 目录清单里的 `web/` 与此冲突，以 §5 为准）。

## 许可

MIT（与旧仓库一致），见 `LICENSE`。
