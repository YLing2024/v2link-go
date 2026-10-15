# v2link-go

临时 VLESS 链接管理控制面的 **Go 重写版**。行为逐条对齐旧实现（`/root/proj/v2link`，Node 24 + Express 4 + SQLite），
默认监听 `127.0.0.1:19245`（旧实现的 `7897` 不动，两者可并存）。

- **控制面 / 数据面分离**：控制面（本服务）管账本与 HTTP；数据面 xray-core 走 `xray api adu/rmu` 热管理，
  不 reload、不断存量连接。
- **单二进制**：前端产物经 `//go:embed` 打进二进制；无外部运行时依赖（SQLite 为纯 Go 实现，无需 cgo）。
- **双库支持**：`DB_DRIVER=sqlite`（默认）或 `mysql`，配置切换；`migrate-data` 子命令做跨库拷贝。

## 构建与运行

```bash
make build          # 构建前端产物 + 编译二进制（./v2link-go）
make test           # go test（-count=1）
make race           # 关键并发路径加 -race
make check          # 聚合门禁：路由↔文档机检 + go vet + go test（退出码 0 才算过）

make run            # 构建后直接运行（读环境变量）
./v2link-go --static-only            # 只托管嵌入的前端产物（本地预览）
./v2link-go migrate-data --help      # 跨库搬运
```

## 配置（环境变量）

配置**只读环境变量**（对齐旧实现的 systemd `EnvironmentFile` 部署口径），不读 YAML / `.env` 文件。
键名与默认值逐条对齐旧实现（27 个键），唯一差异是新增 `DB_DRIVER` 等数据库选择键；
默认端口由 `7897` 改为 `19245`。全部键见 `deploy/env.example`。

- 数据面：`XRAY_API`、`XRAY_BIN`、`XRAY_INBOUND_TAG`、`XRAY_API_TIMEOUT_S`、`XRAY_API_RETRIES`
- 时限：`DEFAULT_HOURS`、`MAX_HOURS`
- 调度：`EXPIRE_SCAN_INTERVAL_S`、`LEDGER_INTERVAL_S`、`XRAY_RECONCILE_INTERVAL_S`、
  `CONN_CLEANUP_INTERVAL_S`、`CONN_RETENTION_S`、`SAMPLE_CLEANUP_INTERVAL_S`、`SAMPLE_RETENTION_S`、
  `REGION_PROBE_INTERVAL_S`、`REGION_PROBES`
- 采集：`ACCESS_LOG_PATH`
- 数据库：`DB_DRIVER`、`DB_PATH`、`DB_HOST`、`DB_PORT`、`DB_NAME`、`DB_USER`、`DB_PASSWORD`、`DB_DSN`
- Clash：`CLASH_SUB_TTL_S`、`PUBLIC_BASE_URL`、`CLASH_DIRECT_DOMAINS`
- 认证：`AUTH_MODE`、`V2LINK_ADMIN_USER`、`V2LINK_ADMIN_PASSWORD`、`SESSION_TTL_HOURS`、`TRUST_PROXY`
- 公开地址（生成 `vless://`）：`PUBLIC_HOST`、`PUBLIC_PATH`、`PUBLIC_SNI`

## 目录

```
cmd/v2link-go/       进程入口（装配 / 优雅退出 / migrate-data）
internal/
  config/            环境变量解析
  store/             账本访问层 + 跨库搬运
  sqlite/ mysql/     两套薄方言（DDL / upsert / 自增回填 / 列探测）
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
deploy/              systemd / env / nginx 样例（不安装）
docs/API.md          接口契约（权威，机检）
docs/DB.md           双库映射与迁移
docs/COMPONENTS.md   前端组件登记
```

## 接口契约

见 `docs/API.md`（权威）。`scripts/check-api-docs.mjs` 机检「源码路由 ↔ 文档标题」一致，随 `make check` 一起跑。

## 数据库

见 `docs/DB.md`。默认 SQLite；`DB_DRIVER=mysql` 需 `DB_HOST`/`DB_NAME`/`DB_USER`（或 `DB_DSN`）。
迁移只做增量（禁止 DROP / TRUNCATE / 重建表），老库原地升级、数据保留。

## 与旧实现的有意差异

见交付报告；要点：

1. 默认端口 `7897` → `19245`（新旧并存）。
2. 新增数据库选择与 `migrate-data`（需求书 §3 要求）。
3. `users`/`sessions` 时间列由 ISO 文本统一为 epoch 毫秒整数（读旧库时兼容）。
4. 配置只读环境变量（不读 `.env` / YAML）。
5. 前端目录沿用 `frontend/`（需求书 §5 用语；§7 目录清单里的 `web/` 与此冲突，以 §5 为准）。

## 许可

MIT（与旧仓库一致），见 `LICENSE`。
