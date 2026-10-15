package httpapi

import (
	"net/http"
	"strings"

	"v2link-go/internal/auth"
	"v2link-go/internal/jsonx"
)

// handleAuthMode 免鉴权返回当前认证模式：{"authMode": "builtin"|"sso"}。
func (s *Server) handleAuthMode(*Context) (Response, error) {
	body := `{"authMode":` + jsonx.Quote(string(s.config.AuthMode)) + `}`
	return Response{Status: http.StatusOK, RawBody: []byte(body), ContentType: contentTypeJSON}, nil
}

// handleLogin 校验口令 → 建会话 + 写 HttpOnly cookie（按 IP 限速）。
func (s *Server) handleLogin(ctx *Context) (Response, error) {
	ip := auth.ClientIP(ctx.Request, s.config.TrustProxy)
	now := ctx.Now
	if wait := s.auth.Limiter().BlockedSeconds(ip, now); wait > 0 {
		body := `{"ok":false,"error":"尝试过于频繁，请稍后再试","retryAfter":` + itoa(wait) + `}`
		return Response{Status: http.StatusTooManyRequests, RawBody: []byte(body), ContentType: contentTypeJSON}, nil
	}

	username := strings.TrimSpace(stringField(ctx.Body["username"]))
	password := stringField(ctx.Body["password"])
	if username == "" || password == "" {
		s.auth.Limiter().RecordFailure(ip, now)
		return errorJSON(http.StatusUnauthorized, "账号或密码错误"), nil
	}
	ok, err := s.auth.Accounts().VerifyPassword(ctx.Request.Context(), username, password)
	if err != nil {
		return Response{}, err
	}
	if !ok {
		s.auth.Limiter().RecordFailure(ip, now)
		return errorJSON(http.StatusUnauthorized, "账号或密码错误"), nil
	}

	s.auth.Limiter().Clear(ip)
	token, err := s.auth.Accounts().CreateSession(ctx.Request.Context(), username)
	if err != nil {
		return Response{}, err
	}
	secure := auth.IsSecureRequest(ctx.Request)
	cookie := auth.SessionSetCookie(token, s.auth.Accounts().TTL(), secure)
	body := `{"ok":true,"user":{"name":` + jsonx.Quote(username) + `}}`
	return Response{
		Status:      http.StatusOK,
		RawBody:     []byte(body),
		ContentType: contentTypeJSON,
		Headers:     map[string]string{"Set-Cookie": cookie},
	}, nil
}

// handleLogout 删会话 + 清 cookie（幂等）。
func (s *Server) handleLogout(ctx *Context) (Response, error) {
	if token := auth.ReadSessionToken(ctx.Request); token != "" {
		if err := s.auth.Accounts().DestroySession(ctx.Request.Context(), token); err != nil {
			return Response{}, err
		}
	}
	return Response{
		Status:      http.StatusOK,
		RawBody:     []byte(`{"ok":true}`),
		ContentType: contentTypeJSON,
		Headers:     map[string]string{"Set-Cookie": auth.SessionClearCookie()},
	}, nil
}

// handleMe 返回当前用户名；未登录 401。
func (s *Server) handleMe(ctx *Context) (Response, error) {
	user := ""
	if token := auth.ReadSessionToken(ctx.Request); token != "" {
		name, err := s.auth.Accounts().VerifySession(ctx.Request.Context(), token)
		if err != nil {
			return Response{}, err
		}
		user = name
	}
	if user == "" {
		return errorJSON(http.StatusUnauthorized, "未登录"), nil
	}
	return Response{Status: http.StatusOK, RawBody: []byte(`{"ok":true,"name":` + jsonx.Quote(user) + `}`), ContentType: contentTypeJSON}, nil
}
