// Package mysql 是账本层的 MySQL 方言（github.com/go-sql-driver/mysql）。
//
// 与 SQLite 方言的差异全部收在这里：DDL 类型（VARCHAR/BIGINT/AUTO_INCREMENT）、
// 列存在性探测（information_schema.columns）、自增主键回填（LastInsertId）、
// upsert 形态（ON DUPLICATE KEY UPDATE）、字符集与排序规则（utf8mb4/utf8mb4_unicode_ci）。
// 迁移只做增量（CREATE TABLE IF NOT EXISTS / ADD COLUMN），绝不 DROP、TRUNCATE 或重建表。
package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"v2link-go/internal/store"
)

// Dialect 实现 store.Dialect。
type Dialect struct{}

// New 返回 MySQL 方言单例。
func New() *Dialect { return &Dialect{} }

// Name 返回方言名。
func (d *Dialect) Name() string { return "mysql" }

// DriverName 返回 go-sql-driver/mysql 注册的驱动名。
func (d *Dialect) DriverName() string { return "mysql" }

// BuildDSN 由分段参数拼出 go-sql-driver 的 DSN。口令只进返回值，绝不落日志。
//
// 参数取舍：utf8mb4 保中文/emoji；parseTime=false（时间列一律 epoch 毫秒整数，不交给驱动解析）；
// timeout 控制建连、read/writeTimeout 防止慢查询挂死调度器；loc=Local 对整数时间列无影响。
func BuildDSN(host string, port int, name, user, password string) string {
	cfg := mysql.NewConfig()
	cfg.User = user
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("%s:%d", host, port)
	cfg.DBName = name
	cfg.ParseTime = false
	cfg.Collation = "utf8mb4_unicode_ci"
	cfg.Loc = time.Local
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 15 * time.Second
	cfg.WriteTimeout = 15 * time.Second
	return cfg.FormatDSN()
}

// ddl 是建表语句（幂等）。时间列统一 epoch 毫秒 BIGINT；外键列类型与排序规则和 links.id 严格一致。
var ddl = []string{
	`CREATE TABLE IF NOT EXISTS links (
		id          VARCHAR(64)  NOT NULL,
		uuid        VARCHAR(64)  NOT NULL,
		email       VARCHAR(190) NOT NULL,
		note        VARCHAR(500) NOT NULL DEFAULT '',
		alias       VARCHAR(128) NOT NULL DEFAULT '',
		up_bytes    BIGINT NOT NULL DEFAULT 0,
		down_bytes  BIGINT NOT NULL DEFAULT 0,
		created_at  BIGINT NOT NULL,
		expires_at  BIGINT NOT NULL,
		revoked_at  BIGINT NULL,
		status      VARCHAR(16) NOT NULL DEFAULT 'active',
		PRIMARY KEY (id),
		UNIQUE KEY uk_links_uuid (uuid),
		UNIQUE KEY uk_links_email (email),
		KEY idx_links_status (status),
		KEY idx_links_expires (expires_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS traffic_samples (
		id          BIGINT NOT NULL AUTO_INCREMENT,
		link_id     VARCHAR(64) NOT NULL,
		ts          BIGINT NOT NULL,
		up_delta    BIGINT NOT NULL DEFAULT 0,
		down_delta  BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (id),
		KEY idx_samples_link_ts (link_id, ts),
		CONSTRAINT fk_samples_link FOREIGN KEY (link_id) REFERENCES links (id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS connections (
		id           BIGINT NOT NULL AUTO_INCREMENT,
		link_id      VARCHAR(64) NULL,
		email        VARCHAR(190) NOT NULL,
		ts           BIGINT NOT NULL,
		host         VARCHAR(255) NULL,
		port         INT NULL,
		up_bytes     BIGINT NULL,
		down_bytes   BIGINT NULL,
		duration_ms  BIGINT NULL,
		PRIMARY KEY (id),
		KEY idx_conn_link_ts (link_id, ts),
		KEY idx_conn_ts (ts)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS audit_log (
		id       BIGINT NOT NULL AUTO_INCREMENT,
		ts       BIGINT NOT NULL,
		actor    VARCHAR(190) NOT NULL,
		action   VARCHAR(32) NOT NULL,
		link_id  VARCHAR(64) NULL,
		detail   TEXT NULL,
		PRIMARY KEY (id),
		KEY idx_audit_ts (ts)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS clash_subs (
		token      VARCHAR(64) NOT NULL,
		link_ids   TEXT NOT NULL,
		created_at BIGINT NOT NULL,
		expires_at BIGINT NOT NULL,
		created_by VARCHAR(190) NOT NULL DEFAULT '',
		PRIMARY KEY (token),
		KEY idx_clash_subs_expires (expires_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS users (
		id            BIGINT NOT NULL AUTO_INCREMENT,
		username      VARCHAR(190) NOT NULL,
		password_hash VARCHAR(255) NOT NULL,
		created_at    BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (id),
		UNIQUE KEY uk_users_username (username)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS sessions (
		token      VARCHAR(64) NOT NULL,
		username   VARCHAR(190) NOT NULL,
		created_at BIGINT NOT NULL DEFAULT 0,
		expires_at BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (token),
		KEY idx_sessions_expires (expires_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS schema_version (
		version    INT NOT NULL,
		applied_at BIGINT NOT NULL,
		PRIMARY KEY (version)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
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
		"INSERT IGNORE INTO schema_version (version, applied_at) VALUES (?, ?)",
		migrateVersion, time.Now().UnixMilli()); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("记录 schema_version 失败: %w", err)
	}
	return tx.Commit()
}

// ColumnExists 用 information_schema.columns 探测列（限定当前库）。
func (d *Dialect) ColumnExists(ctx context.Context, q store.Querier, table, column string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.columns "+
			"WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?",
		table, column).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("读取表 %s 结构失败: %w", table, err)
	}
	return n > 0, nil
}

// InsertReturningID 用 Exec + LastInsertId 回填自增主键（MySQL 无 RETURNING）。
func (d *Dialect) InsertReturningID(ctx context.Context, q store.Querier, insertSQL string, args ...any) (int64, error) {
	res, err := q.ExecContext(ctx, insertSQL, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpsertSQL 生成 ON DUPLICATE KEY UPDATE 形态（跨库搬运幂等写入）。
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
		assignments = append(assignments, c+" = VALUES("+c+")")
	}
	base := "INSERT INTO " + table + " (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	if len(assignments) == 0 {
		return base
	}
	return base + " ON DUPLICATE KEY UPDATE " + strings.Join(assignments, ", ")
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
