package auth

import (
	"context"
	"strings"
	"time"

	"v2link-go/internal/store"
)

// tokenBytes 是会话 token 的随机字节数（十六进制后 64 字符）。
const tokenBytes = 32

// Accounts 是自带账号体系（AUTH_MODE=builtin）的读写门面：SQL 全在 store，这里只编排。
type Accounts struct {
	db    *store.DB
	ttlMs int64
	now   func() int64
}

// NewAccounts 构造账号服务；ttlMs 为会话 TTL（命中滑动续期）。
func NewAccounts(db *store.DB, ttlMs int64) *Accounts {
	return &Accounts{db: db, ttlMs: ttlMs, now: store.NowMillis}
}

// UserCount 返回 users 表行数（首启引导判据）。
func (a *Accounts) UserCount(ctx context.Context) (int64, error) {
	return a.db.UserCount(ctx)
}

// CreateUser 新增用户（username 冲突由唯一约束兜底）。
func (a *Accounts) CreateUser(ctx context.Context, username, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return a.db.CreateUser(ctx, username, hash, a.now())
}

// BootstrapResult 是首启引导结果。Password 仅在随机生成时返回一次（调用方打印后即丢）。
type BootstrapResult struct {
	Created  bool
	Password string
}

// BootstrapAdmin 首启引导：users 为空才建管理员；已有用户不覆盖口令。password 为空则随机。
func (a *Accounts) BootstrapAdmin(ctx context.Context, username, password string) (BootstrapResult, error) {
	n, err := a.db.UserCount(ctx)
	if err != nil {
		return BootstrapResult{}, err
	}
	if n > 0 {
		return BootstrapResult{Created: false}, nil
	}
	provided := ""
	if trimmed := strings.TrimSpace(password); trimmed != "" {
		provided = trimmed
	}
	initial := provided
	generated := false
	if initial == "" {
		initial = RandomPassword()
		generated = true
	}
	if err := a.CreateUser(ctx, username, initial); err != nil {
		return BootstrapResult{}, err
	}
	res := BootstrapResult{Created: true}
	if generated {
		res.Password = initial
	}
	return res, nil
}

// VerifyPassword 校验账号口令（用户不存在 / 口令错均为 false）。
func (a *Accounts) VerifyPassword(ctx context.Context, username, password string) (bool, error) {
	hash, ok, err := a.db.PasswordHash(ctx, username)
	if err != nil || !ok {
		return false, err
	}
	return VerifyPasswordHash(password, hash), nil
}

// CreateSession 建会话，返回 token。
func (a *Accounts) CreateSession(ctx context.Context, username string) (string, error) {
	token := randomHex(tokenBytes)
	now := a.now()
	if err := a.db.CreateSession(ctx, token, username, now, now+a.ttlMs); err != nil {
		return "", err
	}
	return token, nil
}

// VerifySession 校验会话；命中滑动续期，返回用户名；未命中/过期返回空串（过期行顺带删除）。
func (a *Accounts) VerifySession(ctx context.Context, token string) (string, error) {
	row, err := a.db.SessionByToken(ctx, token)
	if err != nil {
		return "", err
	}
	if row == nil {
		return "", nil
	}
	now := a.now()
	if row.ExpiresAt <= now {
		if err := a.db.DestroySession(ctx, token); err != nil {
			return "", err
		}
		return "", nil
	}
	if err := a.db.TouchSession(ctx, token, now+a.ttlMs); err != nil {
		return "", err
	}
	return row.Username, nil
}

// DestroySession 删会话（幂等）。
func (a *Accounts) DestroySession(ctx context.Context, token string) error {
	return a.db.DestroySession(ctx, token)
}

// CleanupExpiredSessions 清理已过期会话，返回删除行数。
func (a *Accounts) CleanupExpiredSessions(ctx context.Context) (int64, error) {
	return a.db.CleanupExpiredSessions(ctx, a.now())
}

// TTL 返回会话 TTL（Cookie maxAge 用）。
func (a *Accounts) TTL() time.Duration { return time.Duration(a.ttlMs) * time.Millisecond }
