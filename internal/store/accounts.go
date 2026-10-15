package store

import (
	"context"
	"database/sql"
	"errors"
)

// UserCount 返回 users 表行数（首启引导判据）。
func (db *DB) UserCount(ctx context.Context) (int64, error) {
	var n int64
	if err := db.SQL.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// CreateUser 新增用户（username 冲突由唯一约束兜底）。
func (db *DB) CreateUser(ctx context.Context, username, passwordHash string, createdAt int64) error {
	_, err := db.SQL.ExecContext(ctx,
		"INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?)",
		username, passwordHash, createdAt)
	return err
}

// PasswordHash 取用户口令哈希；不存在返回 "", false, nil。
func (db *DB) PasswordHash(ctx context.Context, username string) (string, bool, error) {
	var hash string
	err := db.SQL.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE username = ?", username).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return hash, true, nil
}

// CreateSession 建会话。
func (db *DB) CreateSession(ctx context.Context, token, username string, createdAt, expiresAt int64) error {
	_, err := db.SQL.ExecContext(ctx,
		"INSERT INTO sessions (token, username, created_at, expires_at) VALUES (?, ?, ?, ?)",
		token, username, createdAt, expiresAt)
	return err
}

// SessionByToken 取会话；不存在返回 nil, nil。时间列兼容旧库 ISO 文本。
func (db *DB) SessionByToken(ctx context.Context, token string) (*SessionRow, error) {
	var username string
	var createdAt, expiresAt any
	err := db.SQL.QueryRowContext(ctx, "SELECT username, created_at, expires_at FROM sessions WHERE token = ?", token).
		Scan(&username, &createdAt, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &SessionRow{Token: token, Username: username, CreatedAt: normalizeTime(createdAt), ExpiresAt: normalizeTime(expiresAt)}, nil
}

// TouchSession 滑动续期：更新过期时刻。
func (db *DB) TouchSession(ctx context.Context, token string, newExpiry int64) error {
	_, err := db.SQL.ExecContext(ctx, "UPDATE sessions SET expires_at = ? WHERE token = ?", newExpiry, token)
	return err
}

// DestroySession 删会话（幂等）。
func (db *DB) DestroySession(ctx context.Context, token string) error {
	_, err := db.SQL.ExecContext(ctx, "DELETE FROM sessions WHERE token = ?", token)
	return err
}

// CleanupExpiredSessions 删除已过期会话，返回删除行数。
//
// 旧库 sessions.expires_at 为 ISO 文本列，SQL 里直接比较会按文本字典序出错，
// 故在 Go 侧解析后逐条删除（会话量小，代价可忽略）。
func (db *DB) CleanupExpiredSessions(ctx context.Context, now int64) (int64, error) {
	rows, err := db.SQL.QueryContext(ctx, "SELECT token, expires_at FROM sessions")
	if err != nil {
		return 0, err
	}
	var expired []string
	for rows.Next() {
		var token string
		var expiresAt any
		if err := rows.Scan(&token, &expiresAt); err != nil {
			rows.Close()
			return 0, err
		}
		if normalizeTime(expiresAt) <= now {
			expired = append(expired, token)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	var removed int64
	for _, token := range expired {
		res, err := db.SQL.ExecContext(ctx, "DELETE FROM sessions WHERE token = ?", token)
		if err != nil {
			return removed, err
		}
		n, _ := res.RowsAffected()
		removed += n
	}
	return removed, nil
}
