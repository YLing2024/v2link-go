package store

import (
	"context"
	"database/sql"
	"errors"
)

// InsertClashSub 写入一条订阅指针。
func (db *DB) InsertClashSub(ctx context.Context, q Querier, row ClashSubRow) error {
	_, err := q.ExecContext(ctx,
		"INSERT INTO clash_subs (token, link_ids, created_at, expires_at, created_by) VALUES (?, ?, ?, ?, ?)",
		row.Token, row.LinkIDs, row.CreatedAt, row.ExpiresAt, row.CreatedBy)
	return err
}

// ClashSubByToken 按 token 取订阅；不存在返回 nil, nil。
func (db *DB) ClashSubByToken(ctx context.Context, token string) (*ClashSubRow, error) {
	var r ClashSubRow
	err := db.SQL.QueryRowContext(ctx,
		"SELECT token, link_ids, created_at, expires_at, created_by FROM clash_subs WHERE token = ?", token).
		Scan(&r.Token, &r.LinkIDs, &r.CreatedAt, &r.ExpiresAt, &r.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// DeleteExpiredClashSubs 删除已过期订阅，返回删除行数。
func (db *DB) DeleteExpiredClashSubs(ctx context.Context, now int64) (int64, error) {
	res, err := db.SQL.ExecContext(ctx, "DELETE FROM clash_subs WHERE expires_at <= ?", now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
