package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"v2link-go/internal/auth"
	"v2link-go/internal/config"
)

// newTestServer 构造 sso 模式的装配体：OPTIONS 与请求体解析都在路由之前，
// 与业务处理器无关，故无需注入各 Service。
func newTestServer() *Server {
	return New(Options{
		Config: &config.Config{AuthMode: config.AuthSSO},
		Auth:   auth.NewAuthenticator(config.AuthSSO, nil, config.TrustProxy{}),
	})
}

func doRequest(t *testing.T, method, target, contentType, body string, authed bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if authed {
		req.Header.Set(auth.AuthUserHeader, "dev")
	}
	rec := httptest.NewRecorder()
	newTestServer().Handler().ServeHTTP(rec, req)
	return rec
}

const (
	ctJSON     = "application/json; charset=utf-8"
	ctHTML     = "text/html; charset=utf-8"
	bodyNoAuth = `{"error":"未登录"}`
)

// TestServeOptions 锁定 Express 自动 OPTIONS 的状态码 / Allow / 正文 / 内容类型。
func TestServeOptions(t *testing.T) {
	cases := []struct {
		name       string
		target     string
		authed     bool
		wantStatus int
		wantAllow  string
		wantBody   string
		wantCT     string
	}{
		{"健康检查", "/api/healthz", false, 200, "GET,HEAD", "GET,HEAD", ctHTML},
		{"auth-mode", "/api/auth-mode", false, 200, "GET,HEAD", "GET,HEAD", ctHTML},
		{"auth login", "/api/auth/login", false, 200, "POST", "POST", ctHTML},
		{"auth logout", "/api/auth/logout", false, 200, "POST", "POST", ctHTML},
		{"auth me", "/api/auth/me", false, 200, "GET,HEAD", "GET,HEAD", ctHTML},
		{"clash 订阅正文", "/api/clash/subscriptions/abc", false, 200, "GET,HEAD", "GET,HEAD", ctHTML},
		{"clash 建订阅", "/api/clash/subscriptions", false, 200, "POST", "POST", ctHTML},
		{"links 未鉴权", "/api/links", false, 401, "", bodyNoAuth, ctJSON},
		{"links 已鉴权", "/api/links", true, 200, "GET,HEAD,POST", "GET,HEAD,POST", ctHTML},
		{"尾斜杠", "/api/links/", true, 200, "GET,HEAD,POST", "GET,HEAD,POST", ctHTML},
		{"带查询串", "/api/links?x=1", true, 200, "GET,HEAD,POST", "GET,HEAD,POST", ctHTML},
		{"单条删除", "/api/links/abc", true, 200, "DELETE", "DELETE", ctHTML},
		{"吊销", "/api/links/abc/revoke", true, 200, "POST", "POST", ctHTML},
		{"延长", "/api/links/abc/extend", true, 200, "POST", "POST", ctHTML},
		{"流量", "/api/links/abc/traffic", true, 200, "GET,HEAD", "GET,HEAD", ctHTML},
		{"连接", "/api/links/abc/connections", true, 200, "GET,HEAD", "GET,HEAD", ctHTML},
		{"审计", "/api/audit", true, 200, "GET,HEAD", "GET,HEAD", ctHTML},
		{"地区", "/api/regions/probes", true, 200, "GET,HEAD", "GET,HEAD", ctHTML},
		{"未知 api 未鉴权", "/api/nope", false, 401, "", bodyNoAuth, ctJSON},
		{"未知 api 已鉴权", "/api/nope", true, 404, "", `{"ok":false,"error":"接口不存在: OPTIONS /api/nope"}`, ctJSON},
		{"裸 /api 已鉴权", "/api", true, 404, "", "Not Found", ctHTML},
		{"根路径", "/", false, 404, "", "Not Found", ctHTML},
		{"非 api 路径", "/foo", false, 404, "", "Not Found", ctHTML},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(t, http.MethodOptions, tc.target, "", "", tc.authed)
			if rec.Code != tc.wantStatus {
				t.Fatalf("状态码 = %d, want %d", rec.Code, tc.wantStatus)
			}
			if got := rec.Header().Get("Allow"); got != tc.wantAllow {
				t.Fatalf("Allow = %q, want %q", got, tc.wantAllow)
			}
			if got := rec.Header().Get("Content-Type"); got != tc.wantCT {
				t.Fatalf("Content-Type = %q, want %q", got, tc.wantCT)
			}
			if got := rec.Body.String(); got != tc.wantBody {
				t.Fatalf("正文 = %q, want %q", got, tc.wantBody)
			}
		})
	}
}
