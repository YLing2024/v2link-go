package store

import (
	"context"
	"database/sql"
	"errors"
)

// linkCols 是显式列清单：不依赖 SELECT *，老库多出的历史列（如 speed_mbps）不会打乱扫描。
const linkCols = "id, uuid, email, note, alias, up_bytes, down_bytes, created_at, expires_at, revoked_at, status"

// LinksList 返回全量账本（按创建时间倒序）的展示形态。
func (db *DB) LinksList(ctx context.Context) ([]LinkView, error) {
	rows, err := db.SQL.QueryContext(ctx, "SELECT "+linkCols+" FROM links ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LinkView, 0)
	for rows.Next() {
		row, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ToView(row))
	}
	return out, rows.Err()
}

// LinksListAll 返回全量账本原始行（含 revoked/expired），reconcile 用。
func (db *DB) LinksListAll(ctx context.Context) ([]LinkRow, error) {
	rows, err := db.SQL.QueryContext(ctx, "SELECT "+linkCols+" FROM links ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LinkRow, 0)
	for rows.Next() {
		row, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// LinkByID 按 id 取行；不存在返回 nil, nil。
func (db *DB) LinkByID(ctx context.Context, id string) (*LinkRow, error) {
	row := db.SQL.QueryRowContext(ctx, "SELECT "+linkCols+" FROM links WHERE id = ?", id)
	r, err := scanLinkRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// InsertLink 写入一行账本。
func (db *DB) InsertLink(ctx context.Context, q Querier, r LinkRow) error {
	_, err := q.ExecContext(ctx,
		"INSERT INTO links (id, uuid, email, note, alias, up_bytes, down_bytes, created_at, expires_at, revoked_at, status) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.ID, r.UUID, r.Email, r.Note, r.Alias, r.UpBytes, r.DownBytes, r.CreatedAt, r.ExpiresAt, r.RevokedAt, string(r.Status))
	return err
}

// RemoveLink 删除账本行，返回实际删除行数。
func (db *DB) RemoveLink(ctx context.Context, q Querier, id string) (int64, error) {
	res, err := q.ExecContext(ctx, "DELETE FROM links WHERE id = ?", id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// LinkStatusOf 返回某链接状态；不存在返回空串。
func (db *DB) LinkStatusOf(ctx context.Context, id string) (LinkStatus, error) {
	var s string
	err := db.SQL.QueryRowContext(ctx, "SELECT status FROM links WHERE id = ?", id).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return LinkStatus(s), nil
}

// AddTraffic 把增量累加进账本。
func (db *DB) AddTraffic(ctx context.Context, q Querier, id string, upDelta, downDelta int64) error {
	_, err := q.ExecContext(ctx,
		"UPDATE links SET up_bytes = up_bytes + ?, down_bytes = down_bytes + ? WHERE id = ?",
		upDelta, downDelta, id)
	return err
}

// FindActiveExpired 找已过期但仍 active 的链接（永久哨兵 0 不参与）。
func (db *DB) FindActiveExpired(ctx context.Context, now int64) ([]LinkRow, error) {
	rows, err := db.SQL.QueryContext(ctx,
		"SELECT "+linkCols+" FROM links WHERE status = 'active' AND expires_at > 0 AND expires_at < ?", now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LinkRow, 0)
	for rows.Next() {
		row, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// FindActiveByEmail 按 email 找仍 active 的链接（access log 关联）。
func (db *DB) FindActiveByEmail(ctx context.Context, email string) (*LinkRow, error) {
	row := db.SQL.QueryRowContext(ctx, "SELECT "+linkCols+" FROM links WHERE email = ? AND status = 'active'", email)
	r, err := scanLinkRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ListActive 返回全部 active 行。
func (db *DB) ListActive(ctx context.Context) ([]LinkRow, error) {
	rows, err := db.SQL.QueryContext(ctx, "SELECT "+linkCols+" FROM links WHERE status = 'active'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LinkRow, 0)
	for rows.Next() {
		row, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ExtendLinkExpiry 设置新的到期时刻。
func (db *DB) ExtendLinkExpiry(ctx context.Context, q Querier, id string, newExpiry int64) error {
	_, err := q.ExecContext(ctx, "UPDATE links SET expires_at = ? WHERE id = ?", newExpiry, id)
	return err
}

// ExpireLink 仅当仍 active 时标 expired，返回是否发生转移。
func (db *DB) ExpireLink(ctx context.Context, q Querier, id string) (bool, error) {
	res, err := q.ExecContext(ctx, "UPDATE links SET status = 'expired' WHERE id = ? AND status = 'active'", id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RevokeLink 仅当仍 active 时标 revoked，返回是否发生转移。
func (db *DB) RevokeLink(ctx context.Context, q Querier, id string, revokedAt int64) (bool, error) {
	res, err := q.ExecContext(ctx, "UPDATE links SET status = 'revoked', revoked_at = ? WHERE id = ? AND status = 'active'", revokedAt, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RollbackRevoke 把链接恢复为 active（吊销时 xray 失败的回滚路径）。
func (db *DB) RollbackRevoke(ctx context.Context, q Querier, id string) error {
	_, err := q.ExecContext(ctx, "UPDATE links SET status = 'active', revoked_at = NULL WHERE id = ?", id)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanLink(rows *sql.Rows) (LinkRow, error) {
	return scanLinkFrom(rows)
}

func scanLinkRow(row *sql.Row) (LinkRow, error) {
	return scanLinkFrom(row)
}

func scanLinkFrom(s scanner) (LinkRow, error) {
	var (
		r         LinkRow
		status    string
		revokedAt sql.NullInt64
	)
	if err := s.Scan(&r.ID, &r.UUID, &r.Email, &r.Note, &r.Alias, &r.UpBytes, &r.DownBytes, &r.CreatedAt, &r.ExpiresAt, &revokedAt, &status); err != nil {
		return LinkRow{}, err
	}
	if revokedAt.Valid {
		v := revokedAt.Int64
		r.RevokedAt = &v
	}
	r.Status = LinkStatus(status)
	return r, nil
}
