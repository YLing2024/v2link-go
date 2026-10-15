package auth

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"v2link-go/internal/config"
)

// AuthUserHeader 是网关注入头：仅 sso 模式信任该头名。
const AuthUserHeader = "X-Auth-User"

// SessionCookie 是自带会话 cookie 名（与旧实现一致；不采用 __Host- 前缀以保持契约不变）。
const SessionCookie = "v2link_session"

// Authenticator 是业务侧唯一鉴权入口。
type Authenticator struct {
	mode    config.AuthMode
	acct    *Accounts
	trust   config.TrustProxy
	limiter *loginLimiter
}

// NewAuthenticator 构造鉴权器。limiter 由同一实例共享（登录限速按客户端 IP 计数）。
func NewAuthenticator(mode config.AuthMode, acct *Accounts, trust config.TrustProxy) *Authenticator {
	return &Authenticator{mode: mode, acct: acct, trust: trust, limiter: newLoginLimiter()}
}

// Mode 返回当前模式。
func (a *Authenticator) Mode() config.AuthMode { return a.mode }

// Accounts 返回账号服务（auth 路由直接用）。
func (a *Authenticator) Accounts() *Accounts { return a.acct }

// Limiter 返回登录限速器。
func (a *Authenticator) Limiter() *loginLimiter { return a.limiter }

// User 返回当前请求身份；未认证返回空串。builtin 忽略 X-Auth-User，sso 忽略 cookie/Bearer。
func (a *Authenticator) User(r *http.Request) string {
	if a.mode == config.AuthSSO {
		if v := strings.TrimSpace(r.Header.Get(AuthUserHeader)); v != "" {
			return v
		}
		return ""
	}
	token := ReadSessionToken(r)
	if token == "" {
		return ""
	}
	user, err := a.acct.VerifySession(r.Context(), token)
	if err != nil {
		// 会话表读取失败按未登录处理：不因库抖动 500（鉴权失败即可，业务请求随后自会再报错）。
		return ""
	}
	return user
}

// ParseCookies 极简 cookie 解析（不引第三方）：只取一层，同名取首个，值做一次 URL 解码。
func ParseCookies(header string) map[string]string {
	out := map[string]string{}
	if header == "" {
		return out
	}
	for _, part := range strings.Split(header, ";") {
		eq := strings.IndexByte(part, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(part[:eq])
		if key == "" {
			continue
		}
		if _, dup := out[key]; dup {
			continue
		}
		val := strings.TrimSpace(part[eq+1:])
		if decoded, err := url.QueryUnescape(val); err == nil {
			val = decoded
		}
		out[key] = val
	}
	return out
}

// ReadSessionToken 取请求凭证：优先 `Authorization: Bearer <token>`，其次 cookie `v2link_session`。
func ReadSessionToken(r *http.Request) string {
	if header := r.Header.Get("Authorization"); header != "" {
		if token := bearerToken(header); token != "" {
			return token
		}
	}
	if v := strings.TrimSpace(ParseCookies(r.Header.Get("Cookie"))[SessionCookie]); v != "" {
		return v
	}
	return ""
}

// bearerToken 复刻 /^Bearer\s+(.+)$/i 的匹配。
func bearerToken(header string) string {
	h := strings.TrimSpace(header)
	if len(h) < 7 || !strings.EqualFold(h[:7], "Bearer ") {
		// 允许 Bearer 与 token 之间是多空白（\s+）；先按前缀 "Bearer" 判断再切。
		if len(h) < 6 || !strings.EqualFold(h[:6], "Bearer") {
			return ""
		}
		rest := strings.TrimLeft(h[6:], " \t\r\n")
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(h[7:])
}

// randomHex 生成 n 字节随机数据的十六进制串。
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// --- 登录限速（按客户端 IP，内存计数，进程重启清零）---

const (
	loginWindowMS   = 15 * 60 * 1000
	loginMaxFailure = 10
)

type failureEntry struct {
	count   int
	resetAt int64
}

// loginLimiter 记录各 IP 窗口内的登录失败次数。
type loginLimiter struct {
	entries map[string]failureEntry
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{entries: map[string]failureEntry{}}
}

// BlockedSeconds 返回剩余封禁秒数；0 表示未封禁（并顺带清理过期窗口）。
func (l *loginLimiter) BlockedSeconds(ip string, now int64) int {
	entry, ok := l.entries[ip]
	if !ok {
		return 0
	}
	if entry.resetAt <= now {
		delete(l.entries, ip)
		return 0
	}
	if entry.count < loginMaxFailure {
		return 0
	}
	return int((entry.resetAt - now + 999) / 1000)
}

// RecordFailure 记一次失败；窗口过期则重置。
func (l *loginLimiter) RecordFailure(ip string, now int64) {
	entry, ok := l.entries[ip]
	if !ok || entry.resetAt <= now {
		l.entries[ip] = failureEntry{count: 1, resetAt: now + loginWindowMS}
		return
	}
	entry.count++
	l.entries[ip] = entry
}

// Clear 清除某 IP 的失败计数（登录成功）。
func (l *loginLimiter) Clear(ip string) { delete(l.entries, ip) }

// --- Cookie 写入 ---

// SessionSetCookie 生成登录会话的 Set-Cookie 头值。
//
// 属性与顺序逐字对齐 Express `res.cookie(name, token, {path:'/', httpOnly, sameSite:'lax', secure, maxAge})`
// （底层 cookie.serialize 的输出顺序：Max-Age; Path; Expires; HttpOnly; [Secure]; SameSite）。
func SessionSetCookie(token string, ttl time.Duration, secure bool) string {
	seconds := int64(ttl / time.Second)
	expires := time.Now().Add(ttl).UTC().Format(http.TimeFormat)
	header := SessionCookie + "=" + token +
		"; Max-Age=" + strconv.FormatInt(seconds, 10) +
		"; Path=/; Expires=" + expires +
		"; HttpOnly"
	if secure {
		header += "; Secure"
	}
	return header + "; SameSite=Lax"
}

// SessionClearCookie 生成登出时清除会话 cookie 的 Set-Cookie 头值。
//
// 对齐 Express `res.clearCookie(name, {path:'/'})`：只带 Path 与 1970 过期时刻，不带 HttpOnly/SameSite。
func SessionClearCookie() string {
	return SessionCookie + "=; Path=/; Expires=" + time.Unix(0, 0).UTC().Format(http.TimeFormat)
}

// ClientIP 复刻 Express trust proxy 下的 req.ip：按信任范围决定是否采信 X-Forwarded-For。
//
// 说明：验收在本机直连（无反向代理）下两种实现都是 127.0.0.1；有反代时按信任范围取最左
// 非信任地址的近似实现，语义与 Express 的 forward 链求值一致（见 docs/API.md 说明）。
func ClientIP(r *http.Request, trust config.TrustProxy) string {
	remote := hostOnly(r.RemoteAddr)
	xff := forwardedFor(r.Header.Get("X-Forwarded-For"))
	switch trust.Kind {
	case config.TrustNone:
		return remote
	case config.TrustAll:
		if len(xff) > 0 {
			return xff[0]
		}
		return remote
	case config.TrustHops:
		// 信任最近 Hops 跳：从右往左数第 Hops 个是客户端，越界取最左。
		chain := append([]string{remote}, xff...)
		idx := len(chain) - 1 - trust.Hops
		if idx < 0 {
			idx = 0
		}
		return chain[idx]
	default: // TrustLoopback
		if isLoopback(remote) && len(xff) > 0 {
			return xff[0]
		}
		return remote
	}
}

// IsSecureRequest 判定请求是否走 HTTPS（直连或反代 x-forwarded-proto）。
func IsSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		return false
	}
	first := strings.TrimSpace(strings.Split(proto, ",")[0])
	return first == "https"
}

func forwardedFor(header string) []string {
	if header == "" {
		return nil
	}
	parts := strings.Split(header, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// hostOnly 去掉 host:port 里的端口，保留 IPv6 方括号内地址。
func hostOnly(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if strings.HasPrefix(addr, "[") {
		if end := strings.IndexByte(addr, ']'); end > 0 {
			return addr[:end+1]
		}
	}
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		if _, err := strconv.Atoi(addr[i+1:]); err == nil {
			return addr[:i]
		}
	}
	return addr
}

func isLoopback(addr string) bool {
	host := strings.Trim(hostOnly(addr), "[]")
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}
