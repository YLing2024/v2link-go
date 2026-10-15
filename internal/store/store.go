// Package store 是账本（MySQL）的统一访问层。
//
// 2026-10-16 口径变更后本服务只支持 MySQL：驱动固定为 github.com/go-sql-driver/mysql，
// 不再有 SQLite 依赖、DB_DRIVER 开关或方言分支。建表与增量迁移一并收在本包（见 schema.go）。
// 所有查询一律使用 `?` 占位符，不做字符串拼接。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	// 注册 database/sql 驱动名 "mysql"。
	_ "github.com/go-sql-driver/mysql"
)

// Querier 是 *sql.DB 与 *sql.Tx 的公共子集，便于把读写放进事务。
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// DB 是 MySQL 连接池句柄。
type DB struct {
	SQL *sql.DB
}

// Open 打开 MySQL 连接池并跑一次幂等迁移（建表 / 补列 / 记 schema_version）。
func Open(ctx context.Context, dsn string, maxOpenConns int) (*DB, error) {
	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	if maxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(maxOpenConns)
		sqlDB.SetMaxIdleConns(maxOpenConns)
	}
	sqlDB.SetConnMaxLifetime(0)
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	if err := Migrate(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return &DB{SQL: sqlDB}, nil
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

// insertReturningID 执行单行 INSERT 并回填自增主键（MySQL 用 LastInsertId）。
func (db *DB) insertReturningID(ctx context.Context, q Querier, insertSQL string, args ...any) (int64, error) {
	res, err := q.ExecContext(ctx, insertSQL, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Clock 返回当前 epoch 毫秒；测试可注入。
type Clock func() int64

// NowMillis 是默认时钟。
func NowMillis() int64 { return time.Now().UnixMilli() }
