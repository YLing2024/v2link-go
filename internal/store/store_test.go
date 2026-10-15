package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"v2link-go/internal/sqlite"
	"v2link-go/internal/store"
)

func openTestSQLite(t *testing.T) *store.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(context.Background(), sqlite.New(), dsn, 4)
	if err != nil {
		t.Fatalf("打开 SQLite 失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSQLiteMigrateAndColumnProbe(t *testing.T) {
	ctx := context.Background()
	db := openTestSQLite(t)

	// 建表后每张表都可用（Migrate 幂等，重复跑不报错）。
	if err := db.Dialect().Migrate(ctx, db.SQL); err != nil {
		t.Fatalf("重复迁移失败: %v", err)
	}
	if err := db.Dialect().Migrate(ctx, db.SQL); err != nil {
		t.Fatalf("再次重复迁移失败: %v", err)
	}

	// 列存在性探测（pragma_table_info 带参数绑定）。
	for _, tc := range []struct {
		table, col string
		want       bool
	}{
		{"links", "alias", true},
		{"links", "speed_mbps", false},
		{"users", "created_at", true},
	} {
		got, err := db.Dialect().ColumnExists(ctx, db.SQL, tc.table, tc.col)
		if err != nil {
			t.Fatalf("探测列 %s.%s 失败: %v", tc.table, tc.col, err)
		}
		if got != tc.want {
			t.Errorf("列 %s.%s 存在性 = %v, 期望 %v", tc.table, tc.col, got, tc.want)
		}
	}
}

func TestSQLiteInsertReturningAndUpsert(t *testing.T) {
	ctx := context.Background()
	db := openTestSQLite(t)

	// InsertReturningID 回填自增主键。
	id, err := db.Dialect().InsertReturningID(ctx, db.SQL,
		"INSERT INTO audit_log (ts, actor, action, link_id, detail) VALUES (?, ?, ?, ?, ?)",
		1, "dev", "create", nil, nil)
	if err != nil {
		t.Fatalf("插入 audit_log 失败: %v", err)
	}
	if id != 1 {
		t.Fatalf("自增主键 = %d, 期望 1", id)
	}

	// Upsert 幂等：同键重复写不报错且更新值。
	cols := store.CopyColumns("links")
	upsert := db.Dialect().UpsertSQL("links", cols, store.KeyColumns("links"))
	row := []any{"lk_test", "uuid-1", "lk_test", "n", "a", 0, 0, 100, 200, nil, "active"}
	if _, err := db.SQL.ExecContext(ctx, upsert, row...); err != nil {
		t.Fatalf("首次 upsert 失败: %v", err)
	}
	row[5] = int64(42) // up_bytes
	if _, err := db.SQL.ExecContext(ctx, upsert, row...); err != nil {
		t.Fatalf("重复 upsert 失败: %v", err)
	}
	var up int64
	if err := db.SQL.QueryRowContext(ctx, "SELECT up_bytes FROM links WHERE id = ?", "lk_test").Scan(&up); err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	if up != 42 {
		t.Fatalf("upsert 未更新: up_bytes = %d", up)
	}
}
