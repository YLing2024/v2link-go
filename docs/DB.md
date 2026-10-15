# v2link-go 数据库（SQLite / MySQL 双支持）

本服务用同一套业务代码支持 SQLite（默认）与 MySQL，方言差异全部下沉到
`internal/sqlite` 与 `internal/mysql` 两套薄方言实现，业务层（`internal/store` 以上）不感知驱动。
**不为了兼容两库把业务 SQL 降级成公共子集**：DDL、upsert、自增主键回填、列存在性探测、字符集排序规则
都由方言层显式分支。

## 1. 选择与配置

- `DB_DRIVER=sqlite`（默认）| `mysql`。非法值启动即报错（fail fast）。
- SQLite：`DB_PATH`（默认 `./data/v2link.db`）。打开时注入 pragma：`journal_mode=WAL`、
  `synchronous=NORMAL`、`foreign_keys=ON`、`busy_timeout=5000`（对齐旧 `connection.ts` 口径）。
- MySQL：`DB_HOST` / `DB_PORT`(3306) / `DB_NAME` / `DB_USER` / `DB_PASSWORD`，或一条 `DB_DSN`
  （go-sql-driver 原生 DSN，优先生效）。
- **未配置 MySQL 时，`go-sql-driver/mysql` 不予使用、不建连接、不影响启动**：只有 `DB_DRIVER=mysql`
  分支才会拼 DSN 并打开连接。连接串与口令只进返回值，**绝不落日志**（`Describe()` 只输出库名）。
- 连接池上限：SQLite 4、MySQL 16（SQLite 写锁敏感，池更小）。

## 2. 表与类型映射

7 张业务表 + `schema_version`。时间列统一为 **epoch 毫秒整数**（旧实现 `users`/`sessions` 原为 ISO 文本，
本次统一为整数，属需求书 §3 允许的重设计；读旧库时靠 `normalizeTime` 容忍文本）。

| 逻辑字段 | SQLite | MySQL | 备注 |
|---|---|---|---|
| TEXT 主键（`links.id`、`clash_subs.token`、`sessions.token`） | `TEXT` | `VARCHAR(64)` | 业务键，字符串 |
| 自增主键（`traffic_samples/connections/audit_log/users.id`） | `INTEGER PRIMARY KEY AUTOINCREMENT` | `BIGINT AUTO_INCREMENT` | 回填方式见 §4 |
| 计数/字节/时间（`up_bytes`、`ts`、`created_at` 等） | `INTEGER` | `BIGINT` | |
| 短文本（`note`、`alias`、`status`、`action`） | `TEXT` | `VARCHAR(500/128/16/32)` | |
| 长文本（`detail`、`link_ids`） | `TEXT` | `TEXT` | |
| 字符集 / 排序规则 | — | `utf8mb4` / `utf8mb4_unicode_ci` | 外键列类型与排序规则严格一致 |

外键：`traffic_samples.link_id → links.id`（MySQL 显式 `CONSTRAINT`，SQLite 由 `REFERENCES` 声明）。
`connections` 不对 `links` 建外键（`link_id` 可为 NULL 且连接历史在链接删除后保留）。

### 表清单

| 表 | 用途 | 键 |
|---|---|---|
| `links` | 链接账本（权威） | PK `id`；UNIQUE `uuid`、`email` |
| `traffic_samples` | 30s 流量采样（保留 30 天） | PK 自增；索引 `(link_id, ts)` |
| `connections` | access log 连接追溯（保留 7 天） | PK 自增；索引 `(link_id, ts)`、`(ts)` |
| `audit_log` | 操作审计 | PK 自增；索引 `(ts)` |
| `clash_subs` | Clash 临时订阅指针 | PK `token`；索引 `(expires_at)` |
| `users` | 自带账号（builtin） | PK 自增；UNIQUE `username` |
| `sessions` | 会话（12h 滑动续期） | PK `token`；索引 `(expires_at)` |
| `schema_version` | 迁移版本记录 | PK `version` |

## 3. 迁移（只增量）

- 建表 / 建索引 / 补列全部幂等（`CREATE TABLE IF NOT EXISTS`、`CREATE INDEX IF NOT EXISTS`、
  `ALTER TABLE ADD COLUMN`），**绝不 DROP / TRUNCATE / 重建表**。事务内执行并写 `schema_version`。
- 补列前先探测列是否存在：
  - SQLite：`SELECT name FROM pragma_table_info(?)`（参数绑定，无注入面）。
  - MySQL：查询 `information_schema.columns`。
- 现状只需为老库补：`links.alias`、`clash_subs.created_by`、`users.created_at`、
  `sessions.created_at`、`sessions.expires_at`（老库可能缺）。
- **老库（含历史 `speed_mbps` 列）原地继续可用**：本服务不删该列，也不依赖它；数据原地保留。

## 4. 两库差异的显式分支

| 能力 | SQLite | MySQL |
|---|---|---|
| DDL 自增主键 | `INTEGER PRIMARY KEY AUTOINCREMENT` | `BIGINT AUTO_INCREMENT` |
| 自增主键回填 | INSERT + `RETURNING id` | `Exec` + `LastInsertId()` |
| Upsert | `ON CONFLICT(<keys>) DO UPDATE SET …=excluded.…` | `ON DUPLICATE KEY UPDATE …=VALUES(…)` |
| 列存在性 | `pragma_table_info` | `information_schema.columns` |
| 时间语义 | 整数 epoch 毫秒 | 整数 epoch 毫秒（`parseTime=false`） |

## 5. 跨库搬运：`migrate-data`

把源库逐表拷贝到目标库：**拷贝不是清洗**（保留原始值，仅把旧库 ISO 文本时间列归一为 epoch 毫秒）。

```bash
# 源库只读（OpenRaw，只发 SELECT）；目标库跑增量迁移
v2link-go migrate-data --from sqlite:./data/v2link.db --to "mysql:user:pass@tcp(127.0.0.1:3306)/v2link" --verify
```

- `--from` / `--to`：DSN 前缀 `sqlite:` / `mysql:` 选驱动。`sqlite:` 后接文件路径；`mysql:` 后接
  go-sql-driver 原生 DSN。
- `--dry-run`：只统计与校验，**不写目标库**。
- `--verify`：搬运后逐表比对目标条数与源条数，并抽样比对行；不一致 → 非 0 退出。
- 顺序：`links → traffic_samples → connections → audit_log → clash_subs → users → sessions`（先父后子）。
- 每表单事务 upsert，天然幂等，可重复执行。
- 源库**绝不删除、绝不清空、绝不改结构**（用 `store.OpenRaw` 打开，不跑迁移）。
- 写入固定在同一条目标连接上，写前调用方言 `PrepareCopy` 关掉该连接的外键强制
  （SQLite `PRAGMA foreign_keys=OFF` / MySQL `SET FOREIGN_KEY_CHECKS=0`）。现网源库存在历史
  遗留的孤儿行（引用了已删除父行），忠实拷贝要求其原样落地而非被约束挡下或清洗。

## 6. 有意差异

- 旧实现只有 SQLite；本服务新增 `DB_DRIVER=mysql` 与 `migrate-data`（需求书 §3 明确要求的加强）。
- `users`/`sessions` 时间列由 ISO 文本改为 epoch 毫秒整数（需求书 §3 允许的重设计）；
  读旧库时 `normalizeTime` 容忍两种形态，搬运时归一为整数。
- 旧库 `links.speed_mbps` 历史列保留不动（不删、不用）。
