package store

import (
	"context"
	"database/sql"
	"strings"
)

const (
	connCols  = "id, link_id, email, ts, host, port, up_bytes, down_bytes, duration_ms"
	auditCols = "id, ts, actor, action, link_id, detail"
)

// SampleQuery 描述流量采样查询范围。
type SampleQuery struct {
	ID    string
	Email string
	From  int64
	To    int64
}

// ConnectionQuery 描述连接记录查询范围（hostPrefix 仅在 link_id/email 子集内做前缀匹配）。
type ConnectionQuery struct {
	LinkID     *string
	Email      *string
	HostPrefix *string
	From       *int64
	To         *int64
	Limit      int
	Offset     int
}

// SampleInput 是采样写入项。
type SampleInput struct {
	LinkID    string
	TS        int64
	UpDelta   int64
	DownDelta int64
}

// ConnectionInput 是连接记录写入项。
type ConnectionInput struct {
	LinkID     *string
	Email      string
	TS         int64
	Host       *string
	Port       *int64
	UpBytes    *int64
	DownBytes  *int64
	DurationMs *int64
}

// AuditInput 是审计写入项。
type AuditInput struct {
	TS     int64
	Actor  string
	Action string
	LinkID *string
	Detail string
}

// ListSamples 读取区间内的采样明细；email 非空时按邮箱关联账号查询。
func (db *DB) ListSamples(ctx context.Context, q SampleQuery) ([]TrafficSampleRow, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if q.Email != "" {
		rows, err = db.SQL.QueryContext(ctx,
			"SELECT ts.id, ts.link_id, ts.ts, ts.up_delta, ts.down_delta FROM traffic_samples ts "+
				"JOIN links l ON l.id = ts.link_id WHERE l.email = ? AND ts.ts >= ? AND ts.ts < ? ORDER BY ts.ts ASC",
			q.Email, q.From, q.To)
	} else {
		rows, err = db.SQL.QueryContext(ctx,
			"SELECT id, link_id, ts, up_delta, down_delta FROM traffic_samples WHERE link_id = ? AND ts >= ? AND ts < ? ORDER BY ts ASC",
			q.ID, q.From, q.To)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TrafficSampleRow, 0)
	for rows.Next() {
		var r TrafficSampleRow
		if err := rows.Scan(&r.ID, &r.LinkID, &r.TS, &r.UpDelta, &r.DownDelta); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// InsertSamples 批量写入采样。
func (db *DB) InsertSamples(ctx context.Context, q Querier, items []SampleInput) error {
	for _, it := range items {
		if _, err := q.ExecContext(ctx,
			"INSERT INTO traffic_samples (link_id, ts, up_delta, down_delta) VALUES (?, ?, ?, ?)",
			it.LinkID, it.TS, it.UpDelta, it.DownDelta); err != nil {
			return err
		}
	}
	return nil
}

// DeleteSamplesBefore 删除指定时刻之前的采样，返回删除行数。
func (db *DB) DeleteSamplesBefore(ctx context.Context, ts int64) (int64, error) {
	res, err := db.SQL.ExecContext(ctx, "DELETE FROM traffic_samples WHERE ts < ?", ts)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteSamplesByLink 删除某链接的全部采样，返回删除行数。
func (db *DB) DeleteSamplesByLink(ctx context.Context, q Querier, linkID string) (int64, error) {
	res, err := q.ExecContext(ctx, "DELETE FROM traffic_samples WHERE link_id = ?", linkID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ListConnections 分页读连接记录（新到旧）。
func (db *DB) ListConnections(ctx context.Context, q ConnectionQuery) ([]ConnectionRow, int64, error) {
	where, params := connWhere(q)
	rows, err := db.SQL.QueryContext(ctx,
		"SELECT "+connCols+" FROM connections "+where+" ORDER BY ts DESC, id DESC LIMIT ? OFFSET ?",
		append(params, q.Limit, q.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]ConnectionRow, 0)
	for rows.Next() {
		r, err := scanConnection(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int64
	if err := db.SQL.QueryRowContext(ctx, "SELECT COUNT(*) FROM connections "+where, params...).Scan(&total); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func connWhere(c ConnectionQuery) (string, []any) {
	parts := make([]string, 0, 4)
	params := make([]any, 0, 4)
	if c.LinkID != nil {
		parts = append(parts, "link_id = ?")
		params = append(params, *c.LinkID)
	} else if c.Email != nil {
		parts = append(parts, "email = ?")
		params = append(params, *c.Email)
	}
	if c.HostPrefix != nil && *c.HostPrefix != "" {
		parts = append(parts, "host LIKE ?")
		params = append(params, *c.HostPrefix+"%")
	}
	if c.From != nil {
		parts = append(parts, "ts >= ?")
		params = append(params, *c.From)
	}
	if c.To != nil {
		parts = append(parts, "ts < ?")
		params = append(params, *c.To)
	}
	if len(parts) == 0 {
		return "", params
	}
	return "WHERE " + strings.Join(parts, " AND "), params
}

// InsertConnections 批量写入连接记录。
func (db *DB) InsertConnections(ctx context.Context, q Querier, items []ConnectionInput) error {
	for _, it := range items {
		if _, err := q.ExecContext(ctx,
			"INSERT INTO connections (link_id, email, ts, host, port, up_bytes, down_bytes, duration_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			it.LinkID, it.Email, it.TS, it.Host, it.Port, it.UpBytes, it.DownBytes, it.DurationMs); err != nil {
			return err
		}
	}
	return nil
}

// DeleteConnectionsBefore 删除指定时刻之前的连接记录，返回删除行数。
func (db *DB) DeleteConnectionsBefore(ctx context.Context, ts int64) (int64, error) {
	res, err := db.SQL.ExecContext(ctx, "DELETE FROM connections WHERE ts < ?", ts)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// InsertAudit 写入一条审计并回填自增主键。
func (db *DB) InsertAudit(ctx context.Context, q Querier, in AuditInput) (int64, error) {
	var detail any
	if in.Detail != "" {
		detail = in.Detail
	}
	return db.dialect.InsertReturningID(ctx, q,
		"INSERT INTO audit_log (ts, actor, action, link_id, detail) VALUES (?, ?, ?, ?, ?)",
		in.TS, in.Actor, in.Action, in.LinkID, detail)
}

// ListAudit 分页读审计（新到旧）。
func (db *DB) ListAudit(ctx context.Context, from, to *int64, limit, offset int) ([]AuditView, int64, error) {
	parts := make([]string, 0, 2)
	params := make([]any, 0, 2)
	if from != nil {
		parts = append(parts, "ts >= ?")
		params = append(params, *from)
	}
	if to != nil {
		parts = append(parts, "ts < ?")
		params = append(params, *to)
	}
	where := ""
	if len(parts) > 0 {
		where = "WHERE " + strings.Join(parts, " AND ")
	}
	rows, err := db.SQL.QueryContext(ctx, "SELECT "+auditCols+" FROM audit_log "+where+" ORDER BY ts DESC, id DESC LIMIT ? OFFSET ?",
		append(params, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]AuditView, 0)
	for rows.Next() {
		r, err := scanAudit(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, ToAuditView(r))
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int64
	if err := db.SQL.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log "+where, params...).Scan(&total); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func scanConnection(rows *sql.Rows) (ConnectionRow, error) {
	var (
		r                   ConnectionRow
		linkID, host        sql.NullString
		port, up, down, dur sql.NullInt64
	)
	if err := rows.Scan(&r.ID, &linkID, &r.Email, &r.TS, &host, &port, &up, &down, &dur); err != nil {
		return ConnectionRow{}, err
	}
	if linkID.Valid {
		v := linkID.String
		r.LinkID = &v
	}
	if host.Valid {
		v := host.String
		r.Host = &v
	}
	if port.Valid {
		v := port.Int64
		r.Port = &v
	}
	if up.Valid {
		v := up.Int64
		r.UpBytes = &v
	}
	if down.Valid {
		v := down.Int64
		r.DownBytes = &v
	}
	if dur.Valid {
		v := dur.Int64
		r.DurationMs = &v
	}
	return r, nil
}

func scanAudit(rows *sql.Rows) (AuditRow, error) {
	var (
		r              AuditRow
		linkID, detail sql.NullString
	)
	if err := rows.Scan(&r.ID, &r.TS, &r.Actor, &r.Action, &linkID, &detail); err != nil {
		return AuditRow{}, err
	}
	if linkID.Valid {
		v := linkID.String
		r.LinkID = &v
	}
	if detail.Valid {
		r.Detail = detail.String
	}
	return r, nil
}
