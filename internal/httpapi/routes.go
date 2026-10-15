package httpapi

import "strings"

// routeStage 是路由所属的中间件阶段（决定它在 Express 挂载链里的位置）。
type routeStage int

const (
	// stageHealth 是 /api/healthz（最前，免鉴权）。
	stageHealth routeStage = iota
	// stageAuthEntry 是 /api 认证入口（auth-mode 免鉴权；login/logout/me 仅 builtin）。
	stageAuthEntry
	// stageClash 是 /api/clash（GET 免鉴权、POST 需鉴权）。
	stageClash
	// stageAuthed 是业务鉴权后的 /api/links、/api/audit、/api/regions。
	stageAuthed
)

// apiRoute 是一条路由。pattern 既是匹配依据，也是 scripts/check-api-docs.mjs 与
// docs/API.md 机检对照的字面量锚点（形如 "METHOD /path"）。
type apiRoute struct {
	pattern      string
	method       string
	routePath    string
	mount        string
	stage        routeStage
	authRequired bool
	builtinOnly  bool
	handler      Handler
}

// Express 里的 Router 挂载点。OPTIONS 自动响应按挂载点边界汇总 Allow（见 serveOptions）。
const (
	mountHealth  = "/api/healthz"
	mountAuth    = "/api"
	mountClash   = "/api/clash"
	mountLinks   = "/api/links"
	mountAudit   = "/api/audit"
	mountRegions = "/api/regions"
)

// routeTable 是全部 API 路由，按旧 server/src/app.ts 的挂载顺序排列；顺序即优先级。
func (s *Server) routeTable() []apiRoute {
	routes := []apiRoute{
		// ① 健康检查（免鉴权，带数据面内存快照）
		{pattern: "GET /api/healthz", mount: mountHealth, stage: stageHealth, handler: s.handleHealth},

		// ② 认证入口（auth-mode 免鉴权；login/logout/me 仅 builtin，sso 下 404）
		{pattern: "GET /api/auth-mode", mount: mountAuth, stage: stageAuthEntry, handler: s.handleAuthMode},
		{pattern: "POST /api/auth/login", mount: mountAuth, stage: stageAuthEntry, builtinOnly: true, handler: s.handleLogin},
		{pattern: "POST /api/auth/logout", mount: mountAuth, stage: stageAuthEntry, builtinOnly: true, handler: s.handleLogout},
		{pattern: "GET /api/auth/me", mount: mountAuth, stage: stageAuthEntry, builtinOnly: true, handler: s.handleMe},

		// ③ Clash 订阅（GET 免鉴权；POST 自带鉴权）
		{pattern: "GET /api/clash/subscriptions/:token", mount: mountClash, stage: stageClash, handler: s.handleClashGet},
		{pattern: "POST /api/clash/subscriptions", mount: mountClash, stage: stageClash, authRequired: true, handler: s.handleClashCreate},

		// ④ 业务 API（业务鉴权中间件之后）
		{pattern: "GET /api/links", mount: mountLinks, stage: stageAuthed, handler: s.handleLinksList},
		{pattern: "POST /api/links", mount: mountLinks, stage: stageAuthed, handler: s.handleLinksCreate},
		{pattern: "POST /api/links/:id/revoke", mount: mountLinks, stage: stageAuthed, handler: s.handleLinksRevoke},
		{pattern: "DELETE /api/links/:id", mount: mountLinks, stage: stageAuthed, handler: s.handleLinksDelete},
		{pattern: "POST /api/links/:id/extend", mount: mountLinks, stage: stageAuthed, handler: s.handleLinksExtend},
		{pattern: "GET /api/links/:id/traffic", mount: mountLinks, stage: stageAuthed, handler: s.handleLinkTraffic},
		{pattern: "GET /api/links/:id/connections", mount: mountLinks, stage: stageAuthed, handler: s.handleLinkConnections},
		{pattern: "GET /api/audit", mount: mountAudit, stage: stageAuthed, handler: s.handleAudit},
		{pattern: "GET /api/regions/probes", mount: mountRegions, stage: stageAuthed, handler: s.handleRegions},
	}
	for i := range routes {
		routes[i].method, routes[i].routePath = splitPattern(routes[i].pattern)
	}
	return routes
}

// splitPattern 把 "METHOD /path" 拆成方法与路径。
func splitPattern(pattern string) (string, string) {
	if idx := strings.IndexByte(pattern, ' '); idx >= 0 {
		return pattern[:idx], pattern[idx+1:]
	}
	return "", pattern
}
