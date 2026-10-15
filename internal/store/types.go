package store

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"v2link-go/internal/expiry"
)

// LinkStatus 链接状态机取值。
type LinkStatus string

const (
	// StatusActive 使用中。
	StatusActive LinkStatus = "active"
	// StatusExpired 已过期。
	StatusExpired LinkStatus = "expired"
	// StatusRevoked 已吊销。
	StatusRevoked LinkStatus = "revoked"
)

// LinkRow 与 links 表逐列对应（时间列为 epoch 毫秒）。
type LinkRow struct {
	ID        string
	UUID      string
	Email     string
	Note      string
	Alias     string
	UpBytes   int64
	DownBytes int64
	CreatedAt int64
	ExpiresAt int64
	RevokedAt *int64
	Status    LinkStatus
}

// LinkView 是对外展示形态（含派生字段 permanent），字段名与旧实现逐字一致。
type LinkView struct {
	ID        string     `json:"id"`
	UUID      string     `json:"uuid"`
	Note      string     `json:"note"`
	Alias     string     `json:"alias"`
	UpBytes   int64      `json:"upBytes"`
	DownBytes int64      `json:"downBytes"`
	CreatedAt int64      `json:"createdAt"`
	ExpiresAt int64      `json:"expiresAt"`
	Permanent bool       `json:"permanent"`
	RevokedAt *int64     `json:"revokedAt"`
	Status    LinkStatus `json:"status"`
}

// ToView 把账本行转成展示形态（permanent 由 expires_at 哨兵推导）。
func ToView(r LinkRow) LinkView {
	return LinkView{
		ID:        r.ID,
		UUID:      r.UUID,
		Note:      r.Note,
		Alias:     r.Alias,
		UpBytes:   r.UpBytes,
		DownBytes: r.DownBytes,
		CreatedAt: r.CreatedAt,
		ExpiresAt: r.ExpiresAt,
		Permanent: expiry.IsPermanent(r.ExpiresAt),
		RevokedAt: r.RevokedAt,
		Status:    r.Status,
	}
}

// TrafficSampleRow 是 traffic_samples 行。
type TrafficSampleRow struct {
	ID        int64
	LinkID    string
	TS        int64
	UpDelta   int64
	DownDelta int64
}

// ConnectionRow 是 connections 行（JSON 字段名保持 snake_case，与旧实现一致）。
type ConnectionRow struct {
	ID         int64   `json:"id"`
	LinkID     *string `json:"link_id"`
	Email      string  `json:"email"`
	TS         int64   `json:"ts"`
	Host       *string `json:"host"`
	Port       *int64  `json:"port"`
	UpBytes    *int64  `json:"up_bytes"`
	DownBytes  *int64  `json:"down_bytes"`
	DurationMs *int64  `json:"duration_ms"`
}

// AuditRow 是 audit_log 行（detail 为原始 JSON 文本）。
type AuditRow struct {
	ID     int64
	TS     int64
	Actor  string
	Action string
	LinkID *string
	Detail string
}

// AuditView 是审计对外形态：detail 原样透传为 JSON 对象（或 null）。
type AuditView struct {
	ID     int64           `json:"id"`
	TS     int64           `json:"ts"`
	Actor  string          `json:"actor"`
	Action string          `json:"action"`
	LinkID *string         `json:"link_id"`
	Detail json.RawMessage `json:"detail"`
}

// ToAuditView 把审计行转成展示形态；detail 非法时回落 null（对齐旧实现 safeJson）。
func ToAuditView(r AuditRow) AuditView {
	detail := json.RawMessage("null")
	if r.Detail != "" && json.Valid([]byte(r.Detail)) {
		detail = json.RawMessage(r.Detail)
	}
	return AuditView{ID: r.ID, TS: r.TS, Actor: r.Actor, Action: r.Action, LinkID: r.LinkID, Detail: detail}
}

// ClashSubRow 是 clash_subs 行（link_ids 为 JSON 数组文本）。
type ClashSubRow struct {
	Token     string
	LinkIDs   string
	CreatedAt int64
	ExpiresAt int64
	CreatedBy string
}

// UserRow 是 users 行。
type UserRow struct {
	ID           int64
	Username     string
	PasswordHash string
	CreatedAt    int64
}

// SessionRow 是 sessions 行。
type SessionRow struct {
	Token     string
	Username  string
	CreatedAt int64
	ExpiresAt int64
}

// normalizeTime 把驱动返回的时间列统一为 epoch 毫秒：
// 新库为整数；旧库 users/sessions 是 ISO 文本，这里兼容解析。
func normalizeTime(v any) int64 {
	switch t := v.(type) {
	case nil:
		return 0
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case []byte:
		return parseTimeString(string(t))
	case string:
		return parseTimeString(t)
	default:
		return 0
	}
}

func parseTimeString(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		return ts.UnixMilli()
	}
	if ts, err := time.Parse("2006-01-02T15:04:05.000Z", s); err == nil {
		return ts.UnixMilli()
	}
	return 0
}

func nullableStr(v any) *string {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if t == "" {
			return nil
		}
		return &t
	case []byte:
		s := string(t)
		if s == "" {
			return nil
		}
		return &s
	default:
		return nil
	}
}

func nullableInt(v any) *int64 {
	switch t := v.(type) {
	case nil:
		return nil
	case int64:
		return &t
	case int:
		n := int64(t)
		return &n
	case float64:
		n := int64(t)
		return &n
	case []byte:
		if n, err := strconv.ParseInt(string(t), 10, 64); err == nil {
			return &n
		}
		return nil
	case string:
		if n, err := strconv.ParseInt(t, 10, 64); err == nil {
			return &n
		}
		return nil
	default:
		return nil
	}
}
