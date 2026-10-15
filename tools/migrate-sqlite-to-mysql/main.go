// Command migrate-sqlite-to-mysql 是一次性迁移工具：把旧 Node 版（SQLite）的 v2link 账本搬到 MySQL。
//
// 它是独立交付物，**不得被服务主程序 import**（本工具是独立 Go module，服务模块里没有它的依赖；
// 反而服务主程序只依赖 MySQL 驱动，不带 SQLite）。
//
// 三种模式：
//
//	默认        逐表「源计数 → 拷贝 → 目标计数 → 抽样比对」，可重复执行（upsert 幂等）
//	--dry-run   只读源库、只报计划，不连目标库、不写任何数据
//	--verify    只校验：不拷贝，只比对目标库与源库的条数与抽样行
//
// 源库一律以只读方式打开（mode=ro + query_only），绝不删改旧文件。
//
// 表结构（列名/类型/索引）与 docs/mysql-schema-cloud.md 的云库 `v2link` 对齐；本工具的建表
// 语句是服务 internal/store 那份的等价副本（独立 module 无法 import internal 包），改动需两处同步。
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	_ "modernc.org/sqlite"
)

// ddl 与 internal/store 的建表语句一致：对齐 docs/mysql-schema-cloud.md 的云库 v2link。
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
}

// tables 是搬运顺序（先父后子，便于阅读与排错；目标库无外键约束）。逐表列顺序显式给出。
var tables = []string{"links", "traffic_samples", "connections", "audit_log", "clash_subs", "users", "sessions"}

var columns = map[string][]string{
	"links":           {"id", "uuid", "email", "note", "alias", "up_bytes", "down_bytes", "created_at", "expires_at", "revoked_at", "status"},
	"traffic_samples": {"id", "link_id", "ts", "up_delta", "down_delta"},
	"connections":     {"id", "link_id", "email", "ts", "host", "port", "up_bytes", "down_bytes", "duration_ms"},
	"audit_log":       {"id", "ts", "actor", "action", "link_id", "detail"},
	"clash_subs":      {"token", "link_ids", "created_at", "expires_at", "created_by"},
	"users":           {"id", "username", "password_hash", "created_at"},
	"sessions":        {"token", "username", "created_at", "expires_at"},
}

// keys 是各表 upsert 的冲突键（拷贝可重复执行、幂等）。
var keys = map[string][]string{
	"links":           {"id"},
	"traffic_samples": {"id"},
	"connections":     {"id"},
	"audit_log":       {"id"},
	"clash_subs":      {"token"},
	"users":           {"id"},
	"sessions":        {"token"},
}

type options struct {
	sqlitePath string
	dryRun     bool
	verifyOnly bool
	sample     int

	mysqlDSN string
	host     string
	port     int
	name     string
	user     string
	password string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "迁移失败："+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("migrate-sqlite-to-mysql", flag.ContinueOnError)
	var opt options
	fs.StringVar(&opt.sqlitePath, "sqlite", "", "旧 SQLite 文件路径（只读打开）")
	fs.StringVar(&opt.mysqlDSN, "mysql-dsn", "", "目标 MySQL 的 go-sql-driver DSN（与分段参数二选一）")
	fs.StringVar(&opt.host, "mysql-host", "127.0.0.1", "目标 MySQL 主机")
	fs.IntVar(&opt.port, "mysql-port", 3306, "目标 MySQL 端口")
	fs.StringVar(&opt.name, "mysql-name", "", "目标 MySQL 库名")
	fs.StringVar(&opt.user, "mysql-user", "", "目标 MySQL 用户")
	fs.StringVar(&opt.password, "mysql-password", "", "目标 MySQL 口令（也可用 MIGRATE_MYSQL_PASSWORD 环境变量）")
	fs.BoolVar(&opt.dryRun, "dry-run", false, "只读源库、只报计划，不写目标库")
	fs.BoolVar(&opt.verifyOnly, "verify", false, "只校验：不拷贝，只比对目标库与源库")
	fs.IntVar(&opt.sample, "sample", 5, "每表抽样比对行数")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if opt.dryRun && opt.verifyOnly {
		return errors.New("--dry-run 与 --verify 不能同时使用")
	}
	if strings.TrimSpace(opt.sqlitePath) == "" {
		return errors.New("需要 --sqlite <旧库文件路径>")
	}
	if opt.sample <= 0 {
		opt.sample = 5
	}
	if opt.password == "" {
		opt.password = os.Getenv("MIGRATE_MYSQL_PASSWORD")
	}

	ctx := context.Background()
	src, err := openSQLiteReadOnly(ctx, opt.sqlitePath)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()

	if opt.dryRun {
		return dryRun(ctx, src, opt)
	}

	dst, err := openMySQL(ctx, opt)
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()

	if opt.verifyOnly {
		return report(ctx, src, dst, opt, true)
	}
	if err := createSchema(ctx, dst); err != nil {
		return err
	}
	return report(ctx, src, dst, opt, false)
}

// openSQLiteReadOnly 以只读方式打开旧库：mode=ro 只读文件，query_only 再从会话层禁止写入。
func openSQLiteReadOnly(ctx context.Context, path string) (*sql.DB, error) {
	dsn := "file:" + path + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开源库失败: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接源库失败: %w", err)
	}
	return db, nil
}

// openMySQL 打开目标库：DB_DSN 优先，否则由分段参数拼装。
func openMySQL(ctx context.Context, opt options) (*sql.DB, error) {
	dsn := strings.TrimSpace(opt.mysqlDSN)
	if dsn == "" {
		if strings.TrimSpace(opt.name) == "" || strings.TrimSpace(opt.user) == "" {
			return nil, errors.New("目标 MySQL 需要 --mysql-dsn，或 --mysql-name/--mysql-user")
		}
		cfg := mysql.NewConfig()
		cfg.User = opt.user
		cfg.Passwd = opt.password
		cfg.Net = "tcp"
		cfg.Addr = fmt.Sprintf("%s:%d", opt.host, opt.port)
		cfg.DBName = opt.name
		cfg.ParseTime = false
		cfg.Collation = "utf8mb4_0900_ai_ci"
		cfg.Timeout = 5 * time.Second
		dsn = cfg.FormatDSN()
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开目标库失败: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接目标库失败: %w", err)
	}
	return db, nil
}

// createSchema 在目标库建表补齐（CREATE TABLE IF NOT EXISTS，绝不破坏既有数据）。
func createSchema(ctx context.Context, dst *sql.DB) error {
	for _, stmt := range ddl {
		if _, err := dst.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("在目标库建表失败: %w", err)
		}
	}
	return nil
}

// dryRun 只读源库并报计划（每表源行数与合计），不连目标库、不写任何数据。
func dryRun(ctx context.Context, src *sql.DB, opt options) error {
	fmt.Printf("演练（dry-run）：只读源库 %s，不写目标库\n", opt.sqlitePath)
	var total int64
	for _, table := range tables {
		n, err := countRows(ctx, src, table)
		if err != nil {
			return err
		}
		total += n
		fmt.Printf("  %-16s 源 %5d 行\n", table, n)
	}
	fmt.Printf("  合计 源 %d 行\n", total)
	return nil
}

// report 执行「源计数 → （可选）拷贝 → 目标计数 → 抽样比对」，逐表打印结果。
func report(ctx context.Context, src, dst *sql.DB, opt options, verifyOnly bool) error {
	if verifyOnly {
		fmt.Printf("校验（verify）：源 %s → 目标 MySQL\n", opt.sqlitePath)
	} else {
		fmt.Printf("搬运：源 %s → 目标 MySQL\n", opt.sqlitePath)
	}
	var totalSrc, totalWritten int64
	mismatches := 0
	for _, table := range tables {
		cols := columns[table]
		rows, err := readTable(ctx, src, table, cols)
		if err != nil {
			return err
		}
		srcCount := int64(len(rows))
		written := int64(0)
		if !verifyOnly {
			if err := writeTable(ctx, dst, table, cols, rows); err != nil {
				return err
			}
			written = int64(len(rows))
		}
		dstCount, err := countRows(ctx, dst, table)
		if err != nil {
			return err
		}
		bad, err := sampleCompare(ctx, dst, table, cols, rows, opt.sample)
		if err != nil {
			return err
		}
		totalSrc += srcCount
		totalWritten += written
		status := "OK"
		if dstCount != srcCount || len(bad) > 0 {
			status = "不一致"
			mismatches++
		}
		line := fmt.Sprintf("  %-16s 源 %5d", table, srcCount)
		if !verifyOnly {
			line += fmt.Sprintf("  写入 %5d", written)
		}
		line += fmt.Sprintf("  目标 %5d  抽样 %d  %s", dstCount, min(opt.sample, len(rows)), status)
		fmt.Println(line)
		if dstCount != srcCount {
			fmt.Printf("      - 条数不一致: 源 %d / 目标 %d\n", srcCount, dstCount)
		}
		for _, m := range bad {
			fmt.Println("      - " + m)
		}
	}
	if verifyOnly {
		fmt.Printf("  合计 源 %d 行\n", totalSrc)
	} else {
		fmt.Printf("  合计 源 %d 行，写入 %d 行\n", totalSrc, totalWritten)
	}
	if mismatches > 0 {
		return fmt.Errorf("有 %d 张表条数或抽样不一致", mismatches)
	}
	return nil
}

// readTable 读出整表（源库只发 SELECT），[]byte 归一为 string。
func readTable(ctx context.Context, src *sql.DB, table string, cols []string) ([][]any, error) {
	rows, err := src.QueryContext(ctx, "SELECT "+strings.Join(cols, ", ")+" FROM "+table)
	if err != nil {
		return nil, fmt.Errorf("读取源表 %s 失败: %w", table, err)
	}
	defer rows.Close()
	out := make([][]any, 0)
	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("扫描源表 %s 失败: %w", table, err)
		}
		out = append(out, normalizeRow(raw))
	}
	return out, rows.Err()
}

// normalizeRow 把驱动返回的裸值转成可安全写入 MySQL 的形态（[]byte → string）。
func normalizeRow(raw []any) []any {
	out := make([]any, len(raw))
	for i, v := range raw {
		if b, ok := v.([]byte); ok {
			out[i] = string(b)
			continue
		}
		out[i] = v
	}
	return out
}

// writeTable 单表事务内逐行 upsert（幂等）。保留原始主键值，忠实搬运。
func writeTable(ctx context.Context, dst *sql.DB, table string, cols []string, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	stmt := upsertSQL(table, cols, keys[table])
	tx, err := dst.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启目标表 %s 事务失败: %w", table, err)
	}
	for _, row := range rows {
		if _, err := tx.ExecContext(ctx, stmt, row...); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("写入目标表 %s 失败: %w", table, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交目标表 %s 事务失败: %w", table, err)
	}
	return nil
}

// upsertSQL 生成 MySQL 的 INSERT ... ON DUPLICATE KEY UPDATE（按冲突键更新其余列）。
func upsertSQL(table string, cols, keyCols []string) string {
	placeholders := make([]string, len(cols))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	assignments := make([]string, 0, len(cols))
	for _, c := range cols {
		if contains(keyCols, c) {
			continue
		}
		assignments = append(assignments, c+" = VALUES("+c+")")
	}
	base := "INSERT INTO " + table + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	if len(assignments) == 0 {
		return base
	}
	return base + " ON DUPLICATE KEY UPDATE " + strings.Join(assignments, ", ")
}

// sampleCompare 取源表前 sample 行，按冲突键读回目标行并逐列比对；返回差异描述。
func sampleCompare(ctx context.Context, dst *sql.DB, table string, cols []string, rows [][]any, sample int) ([]string, error) {
	keyCols := keys[table]
	var bad []string
	for i, srcRow := range rows {
		if i >= sample {
			break
		}
		keyVals := make([]any, len(keyCols))
		for j, k := range keyCols {
			keyVals[j] = srcRow[indexOf(cols, k)]
		}
		where := make([]string, len(keyCols))
		for j, k := range keyCols {
			where[j] = k + " = ?"
		}
		query := "SELECT " + strings.Join(cols, ", ") + " FROM " + table +
			" WHERE " + strings.Join(where, " AND ")
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for j := range raw {
			ptrs[j] = &raw[j]
		}
		err := dst.QueryRowContext(ctx, query, keyVals...).Scan(ptrs...)
		if errors.Is(err, sql.ErrNoRows) {
			bad = append(bad, fmt.Sprintf("抽样缺行: %s=%v", keyCols[0], keyVals[0]))
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("抽样读取目标表 %s 失败: %w", table, err)
		}
		dstRow := normalizeRow(raw)
		if !rowsEqual(srcRow, dstRow) {
			bad = append(bad, fmt.Sprintf("抽样不一致: %s=%v", keyCols[0], keyVals[0]))
		}
	}
	return bad, nil
}

// rowsEqual 逐列比两行；数值统一按十进制字符串比，消除驱动类型差异。
func rowsEqual(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if scalarString(a[i]) != scalarString(b[i]) {
			return false
		}
	}
	return true
}

// scalarString 把标量归一为字符串：nil → 空，数字去尾零，[]byte → string。
func scalarString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	case int64:
		return fmt.Sprintf("%d", t)
	case int:
		return fmt.Sprintf("%d", t)
	case float64:
		return fmt.Sprintf("%g", t)
	case bool:
		return fmt.Sprintf("%t", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

func countRows(ctx context.Context, db *sql.DB, table string) (int64, error) {
	var n int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		return 0, fmt.Errorf("统计表 %s 失败: %w", table, err)
	}
	return n, nil
}

func indexOf(list []string, v string) int {
	for i, item := range list {
		if item == v {
			return i
		}
	}
	return -1
}

func contains(list []string, v string) bool { return indexOf(list, v) >= 0 }
