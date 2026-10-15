# v2link-go 数据库（仅 MySQL）

自 2026-10-16 口径变更起，本服务**只支持 MySQL**，不再有 SQLite 依赖、`DB_DRIVER` 开关或方言分支。
建表与增量迁移收在 `internal/store`；旧 SQLite 数据由独立工具 `tools/migrate-sqlite-to-mysql/`
一次性搬运（该工具不进服务运行时）。

权威 schema：`docs/mysql-schema-cloud.md` 里 `v2link` 库的建表语句。云库历史数据已按它迁好，
服务启动时**原样对齐**这份 schema（表名、列名、列类型、索引名一致），不得另立一套。

## 1. 配置

运行时只有 MySQL 一条路径，二选一：

- 分段参数：`DB_HOST`（默认 `127.0.0.1`）、`DB_PORT`（默认 `3306`）、`DB_NAME`（必填）、
  `DB_USER`（必填）、`DB_PASSWORD`（可空）。
- 或一条 `DB_DSN`（go-sql-driver 原生 DSN，给出即整串生效，覆盖上面的分段参数）。

`DB_NAME`/`DB_USER` 缺失会启动即报错（fail fast）。口令只进 DSN，**绝不落日志**
（启动摘要 `Describe()` 只输出库名）。连接池上限 16。示例见 `deploy/env.example`。

连接参数：`utf8mb4` + `utf8mb4_0900_ai_ci`（与云库一致）；`parseTime=false`（时间列一律
epoch 毫秒整数往返）；建连 5s、读写 15s 超时。

## 2. 表结构

7 张业务表 + 本服务内部表 `schema_version`（迁移版本记录）。时间列统一为 epoch 毫秒整数；
`users.created_at`、`sessions.created_at`、`sessions.expires_at` 在云库是文本列（旧数据忠实迁移的
形态），读取侧 `normalizeTime` 同时容忍整数与文本。

| 表 | 用途 | 键与索引 |
|---|---|---|
| `links` | 链接账本（权威） | PK `id`；UNIQUE `uq_links_email`/`uq_links_uuid`；`idx_links_expires`、`idx_links_status` |
| `traffic_samples` | 30s 流量采样（保留 30 天） | PK `id`(自增)；`idx_samples_link_ts(link_id, ts)` |
| `connections` | access log 连接追溯（保留 7 天） | PK `id`(自增)；`idx_conn_ts(ts)`、`idx_conn_link_ts(link_id, ts)` |
| `audit_log` | 操作审计 | PK `id`(自增)；`idx_audit_ts(ts)` |
| `clash_subs` | Clash 临时订阅指针 | PK `token`；`idx_clash_subs_expires(expires_at)` |
| `users` | 自带账号（builtin） | PK `id`(自增)；UNIQUE `uq_users_username` |
| `sessions` | 会话（12h 滑动续期） | PK `token`；`idx_sessions_expires(expires_at)` |
| `schema_version` | 迁移版本记录 | PK `version` |

逐列类型以 `internal/store/schema.go` 的 `ddl` 为准，与云库 `SHOW CREATE TABLE` 对齐
（字符集 `utf8mb4`、排序 `utf8mb4_0900_ai_ci`、引擎 InnoDB）。`traffic_samples`/`connections`
**不建外键**（与云库一致），连接历史在链接删除后保留。

## 3. 迁移（只增量，绝不破坏）

启动时 `store.Open` 调用一次 `store.Migrate`：

- 全部 `CREATE TABLE IF NOT EXISTS`：**空库首次启动即可自建全部表**。
- 缺列时 `ALTER TABLE ... ADD COLUMN`：先查 `information_schema.columns` 探测，缺了才补。
  当前补列清单：`links.alias`、`clash_subs.created_by`、`users.created_at`、`sessions.created_at`、
  `sessions.expires_at`。
- 写入 `schema_version`（`INSERT IGNORE`）。
- **绝不** `DROP` / `TRUNCATE` / 重建表；只读既有数据、只做增量。MySQL 的 DDL 会隐式提交，
  真正的安全保证来自「每条语句都幂等 + 只增量」。
- 老库多出的历史列（如旧 SQLite 的 `speed_mbps`）不删、不用，数据原地保留。

## 4. 一次性迁移工具：`tools/migrate-sqlite-to-mysql`

把旧 Node 版 SQLite 账本搬进 MySQL（跑一次即可）。独立 Go module，**不得被服务主程序 import**。

```bash
cd tools/migrate-sqlite-to-mysql

# 只报计划（只读源库，不连目标库）
go run . --sqlite /path/to/v2link.db --dry-run

# 搬运：目标库建表补齐后逐表「源计数 → 拷贝 → 目标计数 → 抽样比对」
go run . --sqlite /path/to/v2link.db \
  --mysql-host 127.0.0.1 --mysql-port 3306 \
  --mysql-name v2link --mysql-user v2link --mysql-password '***'

# 只校验（不拷贝）
go run . --sqlite /path/to/v2link.db --mysql-host 127.0.0.1 --mysql-name v2link --mysql-user v2link --verify
```

- **源库只读**：`mode=ro` + `query_only(1)` 打开，只发 `SELECT`，绝不删改旧文件。
- 默认模式可重复执行（`INSERT ... ON DUPLICATE KEY UPDATE` 幂等）。
- `--dry-run` 只报计划；`--verify` 只校验。口令也可用 `MIGRATE_MYSQL_PASSWORD` 注入。
- 目标库只做 `CREATE TABLE IF NOT EXISTS`（DDL 与 §2 一致），绝不 `DROP` / `TRUNCATE` / 重建表。

### 表 / 列映射

工具逐表按固定列顺序搬运（保留原始主键值），SQLite 旧列名与 MySQL 新列名**同名**，
因此映射就是「同表同列，仅类型由 SQLite 动态类型映射到 MySQL 显式类型」：

| 表 | SQLite 源列 | MySQL 目标列 |
|---|---|---|
| `links` | `id, uuid, email, note, alias, up_bytes, down_bytes, created_at, expires_at, revoked_at, status` | 同名；`id/uuid/email/status` → `varchar(256)`，`note/alias` → `longtext`，计数/时间 → `bigint` |
| `traffic_samples` | `id, link_id, ts, up_delta, down_delta` | 同名；`id` → `bigint AUTO_INCREMENT`，`link_id` → `varchar(256)`，其余 → `bigint` |
| `connections` | `id, link_id, email, ts, host, port, up_bytes, down_bytes, duration_ms` | 同名；`id` → `bigint AUTO_INCREMENT`，`link_id` → `varchar(256)`，`email/host` → `longtext`，其余 → `bigint` |
| `audit_log` | `id, ts, actor, action, link_id, detail` | 同名；`id` → `bigint AUTO_INCREMENT`，`actor/action/link_id/detail` → `longtext`，`ts` → `bigint` |
| `clash_subs` | `token, link_ids, created_at, expires_at, created_by` | 同名；`token` → `varchar(256)`，`link_ids/created_by` → `longtext`，时间 → `bigint` |
| `users` | `id, username, password_hash, created_at` | 同名；`id` → `bigint AUTO_INCREMENT`，`username` → `varchar(256)`，`password_hash/created_at` → `longtext` |
| `sessions` | `token, username, created_at, expires_at` | 同名；`token/expires_at` → `varchar(256)`，`username/created_at` → `longtext` |

冲突键（upsert）：`links.id`、`traffic_samples.id`、`connections.id`、`audit_log.id`、
`clash_subs.token`、`users.id`、`sessions.token`。

## 5. 有意差异

- 旧实现只有 SQLite；新版只支持 MySQL，并提供独立迁移工具（需求书 §3）。
- `users`/`sessions` 时间列在云库是文本、在新库补齐时也用文本列；读取侧 `normalizeTime`
  同时容忍文本与整数，写出新会话时按文本落库，语义与原实现一致。
