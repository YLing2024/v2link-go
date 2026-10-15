package store

import "context"

// ColumnSpec 描述一个可增量补齐的列：两库类型显式给出（不下沉成公共子集）。
type ColumnSpec struct {
	Name       string
	SQLiteType string
	MySQLType  string
}

// alterSpecs 是各表用于「增量补列」的列定义。只列需要防老库缺列的列；
// 建表语句见各方言 Migrate（CREATE TABLE IF NOT EXISTS）。
var alterSpecs = map[string][]ColumnSpec{
	"links": {
		{Name: "alias", SQLiteType: "TEXT NOT NULL DEFAULT ''", MySQLType: "VARCHAR(128) NOT NULL DEFAULT ''"},
	},
	"clash_subs": {
		{Name: "created_by", SQLiteType: "TEXT NOT NULL DEFAULT ''", MySQLType: "VARCHAR(190) NOT NULL DEFAULT ''"},
	},
	"users": {
		{Name: "created_at", SQLiteType: "INTEGER NOT NULL DEFAULT 0", MySQLType: "BIGINT NOT NULL DEFAULT 0"},
	},
	"sessions": {
		{Name: "created_at", SQLiteType: "INTEGER NOT NULL DEFAULT 0", MySQLType: "BIGINT NOT NULL DEFAULT 0"},
		{Name: "expires_at", SQLiteType: "INTEGER NOT NULL DEFAULT 0", MySQLType: "BIGINT NOT NULL DEFAULT 0"},
	},
}

// ensureOrder 是执行「增量补列」的表顺序（确定性，便于复现）。
var ensureOrder = []string{"links", "clash_subs", "users", "sessions"}

// MigrateTables 返回需要执行增量补列的表（顺序确定）。
func MigrateTables() []string { return ensureOrder }

// EnsureAlterColumns 逐表补齐缺失列（幂等）。name 为方言名。
func EnsureAlterColumns(ctx context.Context, d Dialect, q Querier, table string) error {
	for _, spec := range alterSpecs[table] {
		exists, err := d.ColumnExists(ctx, q, table, spec.Name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		typ := spec.MySQLType
		if d.Name() == "sqlite" {
			typ = spec.SQLiteType
		}
		if _, err := q.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+spec.Name+" "+typ); err != nil {
			return err
		}
	}
	return nil
}

// CopyTables 是跨库搬运的表顺序（先父后子，满足外键）。
var CopyTables = []string{"links", "traffic_samples", "connections", "audit_log", "clash_subs", "users", "sessions"}

// copyColumns 是各表的显式列顺序（与建表列一致，供逐行拷贝）。
var copyColumns = map[string][]string{
	"links":           {"id", "uuid", "email", "note", "alias", "up_bytes", "down_bytes", "created_at", "expires_at", "revoked_at", "status"},
	"traffic_samples": {"id", "link_id", "ts", "up_delta", "down_delta"},
	"connections":     {"id", "link_id", "email", "ts", "host", "port", "up_bytes", "down_bytes", "duration_ms"},
	"audit_log":       {"id", "ts", "actor", "action", "link_id", "detail"},
	"clash_subs":      {"token", "link_ids", "created_at", "expires_at", "created_by"},
	"users":           {"id", "username", "password_hash", "created_at"},
	"sessions":        {"token", "username", "created_at", "expires_at"},
}

// timeColumns 是跨库搬运时需要归一化为 epoch 毫秒的列（旧库 users/sessions 为 ISO 文本）。
var timeColumns = map[string]bool{
	"users.created_at":    true,
	"sessions.created_at": true,
	"sessions.expires_at": true,
}

// keyColumns 是 upsert 的冲突键。
var keyColumns = map[string][]string{
	"links":           {"id"},
	"traffic_samples": {"id"},
	"connections":     {"id"},
	"audit_log":       {"id"},
	"clash_subs":      {"token"},
	"users":           {"id"},
	"sessions":        {"token"},
}

// CopyColumns 返回某表的拷贝列顺序。
func CopyColumns(table string) []string { return copyColumns[table] }

// KeyColumns 返回某表的 upsert 键列。
func KeyColumns(table string) []string { return keyColumns[table] }

// IsTimeColumn 判断某列是否为跨库需归一化的时间列。
func IsTimeColumn(table, column string) bool { return timeColumns[table+"."+column] }
