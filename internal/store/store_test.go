package store_test

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"testing"

	"v2link-go/internal/store"
)

// testDSN 构造临时 MySQL 的 DSN：优先 TEST_DB_DSN，否则由 TEST_DB_* 分段拼装。
// 未提供时跳过（无库环境下 make check 仍可全绿；有库时这些用例真跑在 MySQL 上）。
func testDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("TEST_DB_DSN"); dsn != "" {
		return dsn
	}
	name := os.Getenv("TEST_DB_NAME")
	user := os.Getenv("TEST_DB_USER")
	if name == "" || user == "" {
		t.Skip("未设置 TEST_DB_DSN（或 TEST_DB_NAME/TEST_DB_USER），跳过 MySQL 集成测试")
	}
	host := os.Getenv("TEST_DB_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := 3306
	if raw := os.Getenv("TEST_DB_PORT"); raw != "" {
		p, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("TEST_DB_PORT 非法: %v", err)
		}
		port = p
	}
	return store.BuildDSN(host, port, name, user, os.Getenv("TEST_DB_PASSWORD"))
}

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), testDSN(t), 4)
	if err != nil {
		t.Fatalf("打开 MySQL 失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// resetSchema 清空全部表，让每个用例从确定的空库起跑。
func resetSchema(t *testing.T, db *store.DB) {
	t.Helper()
	for _, table := range []string{
		"traffic_samples", "connections", "audit_log", "clash_subs",
		"users", "sessions", "links", "schema_version",
	} {
		if _, err := db.SQL.Exec("DROP TABLE IF EXISTS " + table); err != nil {
			t.Fatalf("清空表 %s 失败: %v", table, err)
		}
	}
}

func TestMigrateBuildsSchemaAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	resetSchema(t, db)

	if err := store.Migrate(ctx, db.SQL); err != nil {
		t.Fatalf("空库建表失败: %v", err)
	}
	if err := store.Migrate(ctx, db.SQL); err != nil {
		t.Fatalf("重复迁移失败: %v", err)
	}

	wantTables := map[string]bool{
		"links": true, "traffic_samples": true, "connections": true,
		"audit_log": true, "clash_subs": true, "users": true,
		"sessions": true, "schema_version": true,
	}
	gotTables := queryStrings(t, db.SQL,
		"SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()")
	for name := range wantTables {
		if !gotTables[name] {
			t.Errorf("缺表 %s", name)
		}
	}
	for name := range gotTables {
		if !wantTables[name] {
			t.Errorf("多出表 %s", name)
		}
	}

	// 索引名与权威 schema 一致。
	wantIndexes := []string{
		"uq_links_email", "uq_links_uuid", "idx_links_expires", "idx_links_status",
		"idx_samples_link_ts", "idx_conn_ts", "idx_conn_link_ts", "idx_audit_ts",
		"idx_clash_subs_expires", "uq_users_username", "idx_sessions_expires",
	}
	gotIndexes := queryStrings(t, db.SQL,
		"SELECT DISTINCT index_name FROM information_schema.statistics WHERE table_schema = DATABASE()")
	for _, name := range wantIndexes {
		if !gotIndexes[name] {
			t.Errorf("缺索引 %s", name)
		}
	}

	// links 列集合与权威 schema 一致。
	wantLinkCols := map[string]bool{
		"id": true, "uuid": true, "email": true, "note": true, "alias": true,
		"up_bytes": true, "down_bytes": true, "created_at": true,
		"expires_at": true, "revoked_at": true, "status": true,
	}
	gotLinkCols := queryStrings(t, db.SQL,
		"SELECT column_name FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'links'")
	for name := range wantLinkCols {
		if !gotLinkCols[name] {
			t.Errorf("links 缺列 %s", name)
		}
	}
	for name := range gotLinkCols {
		if !wantLinkCols[name] {
			t.Errorf("links 多出列 %s", name)
		}
	}
}

func TestMigrateAddsMissingColumnWithoutTouchingData(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	resetSchema(t, db)

	// 造一个缺 alias 列的老 links 表并写入一行。
	if _, err := db.SQL.Exec(`CREATE TABLE links (
		id varchar(256) NOT NULL,
		uuid varchar(256) NOT NULL,
		email varchar(256) NOT NULL,
		note longtext NOT NULL,
		up_bytes bigint NOT NULL DEFAULT 0,
		down_bytes bigint NOT NULL DEFAULT 0,
		created_at bigint NOT NULL,
		expires_at bigint NOT NULL,
		revoked_at bigint DEFAULT NULL,
		status varchar(256) NOT NULL DEFAULT 'active',
		PRIMARY KEY (id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`); err != nil {
		t.Fatalf("建老表失败: %v", err)
	}
	if _, err := db.SQL.Exec(
		"INSERT INTO links (id, uuid, email, note, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
		"lk_legacy", "uuid-legacy", "legacy@example.com", "n", int64(1), int64(2)); err != nil {
		t.Fatalf("写老表失败: %v", err)
	}

	if err := store.Migrate(ctx, db.SQL); err != nil {
		t.Fatalf("补列迁移失败: %v", err)
	}

	cols := queryStrings(t, db.SQL,
		"SELECT column_name FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'links'")
	if !cols["alias"] {
		t.Fatalf("alias 列未被增量补上")
	}

	row, err := db.LinkByID(ctx, "lk_legacy")
	if err != nil {
		t.Fatalf("读回老行失败: %v", err)
	}
	if row == nil || row.Email != "legacy@example.com" || row.Alias != "" {
		t.Fatalf("老行数据未按预期保留: %+v", row)
	}
}

func TestLinkInsertAndReadBack(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	resetSchema(t, db)
	if err := store.Migrate(ctx, db.SQL); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	row := store.LinkRow{
		ID: "lk_round", UUID: "uuid-round", Email: "round@example.com",
		Note: "备注", Alias: "别名", UpBytes: 10, DownBytes: 20,
		CreatedAt: 1000, ExpiresAt: 2000, Status: store.StatusActive,
	}
	if err := db.InsertLink(ctx, db.SQL, row); err != nil {
		t.Fatalf("写入链接失败: %v", err)
	}
	got, err := db.LinkByID(ctx, "lk_round")
	if err != nil {
		t.Fatalf("读回链接失败: %v", err)
	}
	if got == nil || got.UUID != "uuid-round" || got.Note != "备注" || got.UpBytes != 10 {
		t.Fatalf("读回不一致: %+v", got)
	}
}

func TestInsertAuditReturnsID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	resetSchema(t, db)
	if err := store.Migrate(ctx, db.SQL); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	id, err := db.InsertAudit(ctx, db.SQL, store.AuditInput{TS: 1, Actor: "dev", Action: "create"})
	if err != nil {
		t.Fatalf("写审计失败: %v", err)
	}
	if id <= 0 {
		t.Fatalf("自增主键未回填: %d", id)
	}
}

// queryStrings 把单列查询结果收成集合。
func queryStrings(t *testing.T, db *sql.DB, query string) map[string]bool {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("扫描失败: %v", err)
		}
		out[s] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历失败: %v", err)
	}
	return out
}
