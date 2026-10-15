package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ddl 是全部表的建表语句，逐列对齐权威 schema docs/mysql-schema-cloud.md 的云库 `v2link`：
// 表名、列名、列类型、索引名一致。全部用 CREATE TABLE IF NOT EXISTS，对已存在的库是空操作，
// 绝不 DROP / TRUNCATE / 重建表，也不改动既有数据。
//
// 标识符不写反引号：这里用到的列名都不是 MySQL 保留字，建表后 SHOW CREATE TABLE 的列/索引
// 定义与权威文档一致（权威文档里的反引号是它导出时的引用形式）。云库见了 AUTO_INCREMENT=<行数>
// 这类随数据变化的表选项，这里刻意省略，避免给新库强加历史自增值。
var ddl = []string{
	`CREATE TABLE IF NOT EXISTS links (
		id          varchar(256) NOT NULL,
		uuid        varchar(256) NOT NULL,
		email       varchar(256) NOT NULL,
		note        longtext NOT NULL,
		up_bytes    bigint NOT NULL DEFAULT '0',
		down_bytes  bigint NOT NULL DEFAULT '0',
		created_at  bigint NOT NULL,
		expires_at  bigint NOT NULL,
		revoked_at  bigint DEFAULT NULL,
		status      varchar(256) NOT NULL DEFAULT 'active',
		alias       longtext NOT NULL,
		PRIMARY KEY (id),
		UNIQUE KEY uq_links_email (email),
		UNIQUE KEY uq_links_uuid (uuid),
		KEY idx_links_expires (expires_at),
		KEY idx_links_status (status)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
	`CREATE TABLE IF NOT EXISTS traffic_samples (
		id          bigint NOT NULL AUTO_INCREMENT,
		link_id     varchar(256) NOT NULL,
		ts          bigint NOT NULL,
		up_delta    bigint NOT NULL DEFAULT '0',
		down_delta  bigint NOT NULL DEFAULT '0',
		PRIMARY KEY (id),
		KEY idx_samples_link_ts (link_id, ts)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
	`CREATE TABLE IF NOT EXISTS connections (
		id           bigint NOT NULL AUTO_INCREMENT,
		link_id      varchar(256) DEFAULT NULL,
		email        longtext NOT NULL,
		ts           bigint NOT NULL,
		host         longtext,
		port         bigint DEFAULT NULL,
		up_bytes     bigint DEFAULT NULL,
		down_bytes   bigint DEFAULT NULL,
		duration_ms  bigint DEFAULT NULL,
		PRIMARY KEY (id),
		KEY idx_conn_ts (ts),
		KEY idx_conn_link_ts (link_id, ts)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
	`CREATE TABLE IF NOT EXISTS audit_log (
		id       bigint NOT NULL AUTO_INCREMENT,
		ts       bigint NOT NULL,
		actor    longtext NOT NULL,
		action   longtext NOT NULL,
		link_id  longtext,
		detail   longtext,
		PRIMARY KEY (id),
		KEY idx_audit_ts (ts)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
	`CREATE TABLE IF NOT EXISTS clash_subs (
		token      varchar(256) NOT NULL,
		link_ids   longtext NOT NULL,
		created_at bigint NOT NULL,
		expires_at bigint NOT NULL,
		created_by longtext NOT NULL,
		PRIMARY KEY (token),
		KEY idx_clash_subs_expires (expires_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
	`CREATE TABLE IF NOT EXISTS users (
		id            bigint NOT NULL AUTO_INCREMENT,
		username      varchar(256) NOT NULL,
		password_hash longtext NOT NULL,
		created_at    longtext NOT NULL,
		PRIMARY KEY (id),
		UNIQUE KEY uq_users_username (username)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
	`CREATE TABLE IF NOT EXISTS sessions (
		token      varchar(256) NOT NULL,
		username   longtext NOT NULL,
		created_at longtext NOT NULL,
		expires_at varchar(256) NOT NULL,
		PRIMARY KEY (token),
		KEY idx_sessions_expires (expires_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
	// schema_version 是迁移记录表，业务表之外的本服务内部表（权威 schema 不含它）。
	`CREATE TABLE IF NOT EXISTS schema_version (
		version    int NOT NULL,
		applied_at bigint NOT NULL,
		PRIMARY KEY (version)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
}

// migrateVersion 是记录进 schema_version 的版本号（DDL 幂等，重复执行无副作用）。
const migrateVersion = 1

// alterSpec 描述一个可增量补齐的列：类型字符串与云库 schema 逐字一致。
type alterSpec struct {
	column  string
	sqlType string
}

// alterSpecs 是「增量补列」清单：只列老库可能缺、且不影响存量数据的列。
// 类型对齐云库：长文本列（note/alias/created_by 等）在云库是 longtext。
var alterSpecs = map[string][]alterSpec{
	"links":      {{column: "alias", sqlType: "longtext NOT NULL"}},
	"clash_subs": {{column: "created_by", sqlType: "longtext NOT NULL"}},
	"users":      {{column: "created_at", sqlType: "longtext NOT NULL"}},
	"sessions": {
		{column: "created_at", sqlType: "longtext NOT NULL"},
		{column: "expires_at", sqlType: "varchar(256) NOT NULL"},
	},
}

// alterOrder 是执行「增量补列」的表顺序（确定性，便于复现）。
var alterOrder = []string{"links", "clash_subs", "users", "sessions"}

// Migrate 幂等建表、补齐缺失列，并在事务内记录 schema_version。
//
// 只做 CREATE TABLE IF NOT EXISTS 与 ALTER TABLE ADD COLUMN，**绝不** DROP / TRUNCATE / 重建表。
// 说明：MySQL 的 DDL 会隐式提交，真正的安全保证来自「每条语句都幂等 + 只增量」；空库首次启动
// 即可自建全部表（DDL 与 docs/mysql-schema-cloud.md 对齐）。
func Migrate(ctx context.Context, db *sql.DB) error {
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
	for _, table := range alterOrder {
		if err := ensureAlterColumns(ctx, tx, table); err != nil {
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

// ensureAlterColumns 逐列补齐缺失列（幂等）：先探测列是否已存在，缺了才 ADD COLUMN。
func ensureAlterColumns(ctx context.Context, q Querier, table string) error {
	for _, spec := range alterSpecs[table] {
		exists, err := columnExists(ctx, q, table, spec.column)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := q.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+spec.column+" "+spec.sqlType); err != nil {
			return fmt.Errorf("为表 %s 补列 %s 失败: %w", table, spec.column, err)
		}
	}
	return nil
}

// columnExists 用 information_schema.columns 探测列（限定当前库）。
func columnExists(ctx context.Context, q Querier, table, column string) (bool, error) {
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
