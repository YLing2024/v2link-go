// Package store 是账本（SQLite / MySQL）的统一访问层。
//
// 业务 SQL 收在本包：两库的差异（DDL、列存在性探测、自增主键回填、upsert 形态、时间列读取）
// 全部下沉到 Dialect 接口的两套实现，业务层不感知驱动。所有查询使用 `?` 占位符
// （modernc sqlite 与 go-sql-driver/mysql 均支持），不做任何字符串拼接。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Querier 是 *sql.DB 与 *sql.Tx 的公共子集，便于把读写放进事务。
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Dialect 抽象两库差异。实现见 internal/sqlite 与 internal/mysql。
type Dialect interface {
	// Name 返回 "sqlite" / "mysql"。
	Name() string
	// DriverName 返回 database/sql 驱动名。
	DriverName() string
	// Migrate 以增量方式建表 / 加列 / 加索引（幂等、事务内，禁止 DROP/TRUNCATE/重建表）。
	Migrate(ctx context.Context, db *sql.DB) error
	// ColumnExists 探测列是否存在：sqlite 用 pragma_table_info，mysql 用 information_schema.columns。
	ColumnExists(ctx context.Context, q Querier, table, column string) (bool, error)
	// InsertReturningID 执行单行 INSERT 并回填自增主键：
	// sqlite 走 `RETURNING id`，mysql 走 Exec + LastInsertId。
	InsertReturningID(ctx context.Context, q Querier, insertSQL string, args ...any) (int64, error)
	// UpsertSQL 返回按主键冲突即更新的 INSERT 语句（供跨库搬运幂等写入）。
	UpsertSQL(table string, columns, keyColumns []string) string
	// PrepareCopy 在跨库搬运开始时把连接置为「忠实拷贝」模式：关掉外键强制，
	// 使源库里既有的孤儿行（历史遗留：引用了已删除父行）被原样搬过去而非被清洗。
	// 必须在事务之外、于固定连接上执行（SQLite 的 PRAGMA foreign_keys 在事务内无效）。
	PrepareCopy(ctx context.Context, q Querier) error
}

// DB 是数据库句柄 + 方言。
type DB struct {
	SQL     *sql.DB
	dialect Dialect
}

// Dialect 返回当前方言（名称/能力）。
func (db *DB) Dialect() Dialect { return db.dialect }

// Driver 返回驱动名（sqlite / mysql）。
func (db *DB) Driver() string { return db.dialect.Name() }

// Open 打开数据库并跑增量迁移。
func Open(ctx context.Context, dialect Dialect, dsn string, maxOpenConns int) (*DB, error) {
	sqlDB, err := sql.Open(dialect.DriverName(), dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	if maxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(maxOpenConns)
	}
	sqlDB.SetMaxIdleConns(maxOpenConns)
	sqlDB.SetConnMaxLifetime(0)
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	if err := dialect.Migrate(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return &DB{SQL: sqlDB, dialect: dialect}, nil
}

// Close 关闭底层连接池。
func (db *DB) Close() error { return db.SQL.Close() }

// Tx 执行 fn 于单个事务；fn 返回错误则回滚。
func (db *DB) Tx(ctx context.Context, fn func(q Querier) error) error {
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Now 返回当前 epoch 毫秒；测试可注入。
type Clock func() int64

// NowMillis 是默认时钟。
func NowMillis() int64 { return time.Now().UnixMilli() }
