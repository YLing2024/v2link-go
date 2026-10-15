// Package sqlite 是账本层的 SQLite 方言（modernc.org/sqlite，纯 Go、无 cgo）。
//
// 与 MySQL 方言的差异全部收在这里：DDL 类型、列存在性探测（pragma_table_info）、
// 自增主键回填（RETURNING id）、upsert 形态（ON CONFLICT ... DO UPDATE）。
// 迁移只做增量（CREATE TABLE IF NOT EXISTS / ADD COLUMN），绝不 DROP、TRUNCATE 或重建表 —
// 现网 v0.1 老库（含历史 speed_mbps 列、ISO 文本时间列）必须原地继续可用。
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"v2link-go/internal/store"

	// 注册 database/sql 驱动名 "sqlite"（modernc 纯 Go 实现，无 cgo）。
	_ "modernc.org/sqlite"
)

// Dialect 实现 store.Dialect。
type Dialect struct{}

// New 返回 SQLite 方言单例。
func New() *Dialect { return &Dialect{} }

// Name 返回方言名。
func (d *Dialect) Name() string { return "sqlite" }

// DriverName 返回 modernc.org/sqlite 注册的驱动名。
func (d *Dialect) DriverName() string { return "sqlite" }

// ddl 是建表语句（幂等）。列定义与旧实现 init.ts 逐字对齐，但**不**含 speed_mbps，
// 且 users/sessions 的时间列统一为 INTEGER（旧库为 TEXT，靠 normalizeTime 兼容读取）。
var ddl = []string{
	`CREATE TABLE IF NOT EXISTS links (
		id          TEXT PRIMARY KEY,
		uuid        TEXT NOT NULL UNIQUE,
		email       TEXT NOT NULL UNIQUE,
		note        TEXT NOT NULL DEFAULT '',
		alias       TEXT NOT NULL DEFAULT '',
		up_bytes    INTEGER NOT NULL DEFAULT 0,
		down_bytes  INTEGER NOT NULL DEFAULT 0,
		created_at  INTEGER NOT NULL,
		expires_at  INTEGER NOT NULL,
		revoked_at  INTEGER,
		status      TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','expired','revoked'))
	)`,
	`CREATE INDEX IF NOT EXISTS idx_links_status ON links(status)`,
	`CREATE INDEX IF NOT EXISTS idx_links_expires ON links(expires_at)`,
	`CREATE TABLE IF NOT EXISTS traffic_samples (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		link_id     TEXT NOT NULL REFERENCES links(id),
		ts          INTEGER NOT NULL,
		up_delta    INTEGER NOT NULL DEFAULT 0,
		down_delta  INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS idx_samples_link_ts ON traffic_samples(link_id, ts)`,
	`CREATE TABLE IF NOT EXISTS connections (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		link_id      TEXT,
		email        TEXT NOT NULL,
		ts           INTEGER NOT NULL,
		host         TEXT,
		port         INTEGER,
		up_bytes     INTEGER,
		down_bytes   INTEGER,
		duration_ms  INTEGER
	)`,
	`CREATE INDEX IF NOT EXISTS idx_conn_link_ts ON connections(link_id, ts)`,
	`CREATE INDEX IF NOT EXISTS idx_conn_ts ON connections(ts)`,
	`CREATE TABLE IF NOT EXISTS audit_log (
		id       INTEGER PRIMARY KEY AUTOINCREMENT,
		ts       INTEGER NOT NULL,
		actor    TEXT NOT NULL,
		action   TEXT NOT NULL,
		link_id  TEXT,
		detail   TEXT
	)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_log(ts)`,
	`CREATE TABLE IF NOT EXISTS clash_subs (
		token      TEXT PRIMARY KEY,
		link_ids   TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		created_by TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX IF NOT EXISTS idx_clash_subs_expires ON clash_subs(expires_at)`,
	`CREATE TABLE IF NOT EXISTS users (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		username      TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		created_at    INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS sessions (
		token      TEXT PRIMARY KEY,
		username   TEXT NOT NULL,
		created_at INTEGER NOT NULL DEFAULT 0,
		expires_at INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at)`,
	`CREATE TABLE IF NOT EXISTS schema_version (
		version    INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`,
}

// migrateVersion 是记录进 schema_version 的版本号（DDL 幂等，重复执行无副作用）。
const migrateVersion = 1

// Migrate 建表 / 建索引 / 补列（幂等、事务内），并记录 schema_version。
func (d *Dialect) Migrate(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启迁移事务失败: %w", err)
	}
	for _, stmt := range ddl {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("执行建表语句失败: %w", err)
		}
	}
	for _, table := range store.MigrateTables() {
		if err := store.EnsureAlterColumns(ctx, d, tx, table); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (?, ?)",
		migrateVersion, time.Now().UnixMilli()); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("记录 schema_version 失败: %w", err)
	}
	return tx.Commit()
}

// ColumnExists 用 pragma_table_info 探测列；表名为受控常量，无注入面。
func (d *Dialect) ColumnExists(ctx context.Context, q store.Querier, table, column string) (bool, error) {
	rows, err := q.QueryContext(ctx, "SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return false, fmt.Errorf("读取表 %s 结构失败: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}

// InsertReturningID 用 SQLite 的 RETURNING 子句一次性拿回自增主键。
func (d *Dialect) InsertReturningID(ctx context.Context, q store.Querier, insertSQL string, args ...any) (int64, error) {
	var id int64
	if err := q.QueryRowContext(ctx, insertSQL+" RETURNING id", args...).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// UpsertSQL 生成 ON CONFLICT(key) DO UPDATE 形态（跨库搬运幂等写入）。
func (d *Dialect) UpsertSQL(table string, columns, keyColumns []string) string {
	placeholders := make([]string, len(columns))
	for i := range columns {
		placeholders[i] = "?"
	}
	assignments := make([]string, 0, len(columns))
	for _, c := range columns {
		if contains(keyColumns, c) {
			continue
		}
		assignments = append(assignments, c+" = excluded."+c)
	}
	base := "INSERT INTO " + table + " (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	if len(assignments) == 0 {
		return base
	}
	return base + " ON CONFLICT(" + strings.Join(keyColumns, ", ") + ") DO UPDATE SET " + strings.Join(assignments, ", ")
}

// PrepareCopy 关闭本连接的 SQLite 外键强制。源库存在历史遗留的孤儿行（引用了已删除的父行），
// 忠实拷贝要求这些行原样落到目标库，而不是被外键约束挡下或清洗。PRAGMA foreign_keys 是连接级
// 开关且不能在事务内切换，故必须在固定连接上、于任何写事务之前执行。
func (d *Dialect) PrepareCopy(ctx context.Context, q store.Querier) error {
	if _, err := q.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return fmt.Errorf("关闭 SQLite 外键强制失败: %w", err)
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
