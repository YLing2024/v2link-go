// Package httpapi 用标准库 net/http 复刻旧实现（Express 4）的 HTTP 契约。
//
// 装配顺序与旧 server/src/app.ts 逐条对齐：
//
//	express.json({limit:'64kb'}) → /api/healthz（免鉴权）→ /api 认证入口（auth-mode 免鉴权；
//	builtin 的 login/logout/me；sso 下三者一律 404）→ /api/clash（GET 免鉴权、POST 需鉴权）→
//	/api 业务鉴权中间件 → /api/links、/api/audit、/api/regions → 静态托管 + SPA 回退 →
//	404 兜底 → 500 统一错误处理。
//
// 关键派生行为（与旧实现一致）：未登录访问任意未知 /api/* 会先被业务鉴权拦成 401，只有已认证
// 才会拿到 404 JSON；静态回退只对 GET/HEAD 生效，其余方法落 404。
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"v2link-go/internal/auth"
	"v2link-go/internal/clash"
	"v2link-go/internal/config"
	"v2link-go/internal/httperr"
	"v2link-go/internal/jsonx"
	"v2link-go/internal/links"
	"v2link-go/internal/reconcile"
	"v2link-go/internal/regions"
	"v2link-go/internal/store"
)

// maxBodyBytes 是 JSON 请求体上限（对齐 express.json({limit:'64kb'})）。
const maxBodyBytes = 64 * 1024

// 响应内容类型（字符集大小写与旧实现逐字一致）。
const (
	contentTypeJSON  = "application/json; charset=utf-8"
	contentTypeHTML  = "text/html; charset=utf-8"
	contentTypePlain = "text/plain; charset=utf-8"
	contentTypeYAML  = "text/yaml; charset=utf-8"
)

// LinkService 是 /api/links 的业务编排（internal/links.Service 实现）。
type LinkService interface {
	List(ctx context.Context) ([]store.LinkView, error)
	Create(ctx context.Context, in links.CreateInput, actor string) (store.LinkView, error)
	Revoke(ctx context.Context, id, actor string) (store.LinkView, error)
	Extend(ctx context.Context, id string, in links.ExtendInput, actor string) (store.LinkView, error)
	Remove(ctx context.Context, id, actor string) error
}

// MonitorStore 是监控/审计只读查询（internal/store.DB 实现）。
type MonitorStore interface {
	ListSamples(ctx context.Context, q store.SampleQuery) ([]store.TrafficSampleRow, error)
	ListConnections(ctx context.Context, q store.ConnectionQuery) ([]store.ConnectionRow, int64, error)
	ListAudit(ctx context.Context, from, to *int64, limit, offset int) ([]store.AuditView, int64, error)
}

// RegionProbeService 提供地区连通性内存快照（internal/regions.Service 实现）。
type RegionProbeService interface {
	Snapshot() (int64, []regions.SnapshotProbe)
	History() []regions.Round
}

// XrayStatusProvider 提供数据面内存快照（internal/reconcile.Service 实现）。
type XrayStatusProvider interface {
	Snapshot() reconcile.Status
}

// ClashService 是 Clash 订阅编排（internal/clash.Service 实现）。
type ClashService interface {
	Create(ctx context.Context, linkIds any, actor string) (clash.Created, error)
	Get(ctx context.Context, token string, mode clash.Mode) (clash.Subscription, error)
}

// Options 是装配参数；除 Config/Auth 外均可缺省（缺省时空实现/省略对应路由）。
type Options struct {
	Config      *config.Config
	Auth        *auth.Authenticator
	Links       LinkService
	Monitor     MonitorStore
	RegionProbe RegionProbeService
	Reconcile   XrayStatusProvider
	Clash       ClashService
	// StaticFS 是嵌在前端产物目录（已定位到 dist 根）。
	StaticFS staticSource
	// StaticOnly 只托管前端产物（本仓库的本地预览便利开关，与旧实现无关）。
	StaticOnly bool
	Logger     *slog.Logger
	Now        func() int64
	StartedAt  time.Time
}

// Server 是 HTTP 装配体。
type Server struct {
	config     *config.Config
	auth       *auth.Authenticator
	links      LinkService
	monitor    MonitorStore
	probe      RegionProbeService
	reconcile  XrayStatusProvider
	clash      ClashService
	static     staticSource
	staticOnly bool
	log        *slog.Logger
	now        func() int64
	startedAt  time.Time
	routes     []apiRoute
}

// New 构造服务并解析路由表。
func New(opts Options) *Server {
	if opts.Now == nil {
		opts.Now = store.NowMillis
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.StaticFS == nil {
		opts.StaticFS = emptyStatic{}
	}
	started := opts.StartedAt
	if started.IsZero() {
		started = time.Now()
	}
	s := &Server{
		config:     opts.Config,
		auth:       opts.Auth,
		links:      opts.Links,
		monitor:    opts.Monitor,
		probe:      opts.RegionProbe,
		reconcile:  opts.Reconcile,
		clash:      opts.Clash,
		static:     opts.StaticFS,
		staticOnly: opts.StaticOnly,
		log:        log,
		now:        opts.Now,
		startedAt:  started,
	}
	s.routes = s.routeTable()
	return s
}

// Context 是单次请求的处理上下文。
type Context struct {
	// Method 是原始方法（HEAD 保留 HEAD）。
	Method string
	// Path 是解码后的 URL 路径。
	Path string
	// Query 是解析后的查询参数（仅保留首个值；重复键在此不出现）。
	Query url.Values
	// Body 是解析后的 JSON 顶层对象字段；非对象/非 JSON 时为空。
	Body map[string]json.RawMessage
	// User 是当前身份；免鉴权路由为空串。
	User string
	// Now 是请求时刻（epoch ms）。
	Now int64
	// Params 是路径参数（:id / :token）。
	Params map[string]string
	// Request 是原始请求（读 Cookie / 头等）。
	Request *http.Request
}

// Response 是处理结果；RawBody 非空时按 ContentType 原样输出，否则把 Body 编码为 JSON。
type Response struct {
	Status      int
	Body        any
	RawBody     []byte
	ContentType string
	Headers     map[string]string
}

// Handler 是单条路由的处理函数。
type Handler func(*Context) (Response, error)

// Handler 返回顶层 http.Handler。
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.staticOnly {
			s.serveStaticOnly(w, r)
			return
		}
		s.serveHTTP(w, r)
	})
}

// serveHTTP 是主分发：严格按 Express 中间件挂载顺序判定。
func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	head := r.Method == http.MethodHead

	body, err := readJSONBody(r)
	if err != nil {
		s.write(w, r, head, errorJSON(http.StatusInternalServerError, err.Error()))
		return
	}
	ctx := &Context{
		Method:  r.Method,
		Path:    path,
		Query:   firstValues(r.URL.RawQuery),
		Body:    body,
		Now:     s.now(),
		Request: r,
	}

	// ① /api/healthz（免鉴权）
	if route, params, ok := s.matchStage(stageHealth, method, path); ok {
		ctx.Params = params
		s.run(w, r, head, ctx, route)
		return
	}

	// ② /api 认证入口（auth-mode 免鉴权；builtin 的 login/logout/me；sso 下三者 404）
	if route, params, ok := s.matchStage(stageAuthEntry, method, path); ok {
		if route.builtinOnly && s.config != nil && s.config.AuthMode == config.AuthSSO {
			s.write(w, r, head, errorJSON(http.StatusNotFound, "接口不存在"))
			return
		}
		ctx.Params = params
		s.run(w, r, head, ctx, route)
		return
	}

	// ③ /api/clash（GET 免鉴权；POST 自带鉴权）
	if route, params, ok := s.matchStage(stageClash, method, path); ok {
		ctx.Params = params
		if route.authRequired {
			user := s.authenticate(r)
			if user == "" {
				s.write(w, r, head, unauthorizedJSON())
				return
			}
			ctx.User = user
		}
		s.run(w, r, head, ctx, route)
		return
	}

	// ④ 业务鉴权中间件 + /api/links、/api/audit、/api/regions
	if isAPIPath(path) {
		user := s.authenticate(r)
		if user == "" {
			s.write(w, r, head, unauthorizedJSON())
			return
		}
		ctx.User = user
		if route, params, ok := s.matchStage(stageAuthed, method, path); ok {
			ctx.Params = params
			s.run(w, r, head, ctx, route)
			return
		}
		if strings.HasPrefix(path, "/api/") {
			s.write(w, r, head, errorJSON(http.StatusNotFound, "接口不存在: "+r.Method+" "+path))
			return
		}
		// path == "/api"：Express 的 SPA 回退正则 \/(?!api\/) 会命中，落到静态层
	}

	// ⑤ 静态托管 + SPA 回退（仅 GET/HEAD）
	if method == http.MethodGet {
		s.serveStatic(w, r, head)
		return
	}

	// ⑥ 404 兜底
	if strings.HasPrefix(path, "/api/") {
		s.write(w, r, head, errorJSON(http.StatusNotFound, "接口不存在: "+r.Method+" "+path))
		return
	}
	s.write(w, r, head, Response{Status: http.StatusNotFound, ContentType: contentTypeHTML, RawBody: []byte("Not Found")})
}

// authenticate 返回当前身份；未认证为空串。
func (s *Server) authenticate(r *http.Request) string {
	if s.auth == nil {
		return ""
	}
	return s.auth.User(r)
}

// run 调用路由处理函数并写出响应。
func (s *Server) run(w http.ResponseWriter, r *http.Request, head bool, ctx *Context, route *apiRoute) {
	resp, err := route.handler(ctx)
	if err != nil {
		s.write(w, r, head, errorToJSON(err))
		return
	}
	s.write(w, r, head, resp)
}

// write 统一写出响应；head 为 true 时只写头（对齐 Express 对 HEAD 只回头部）。
func (s *Server) write(w http.ResponseWriter, r *http.Request, head bool, resp Response) {
	for name, value := range resp.Headers {
		w.Header().Set(name, value)
	}
	contentType := resp.ContentType
	if contentType == "" {
		contentType = contentTypeJSON
	}
	var payload []byte
	switch {
	case resp.RawBody != nil:
		payload = resp.RawBody
	case resp.Body != nil:
		payload = marshalNoEscape(resp.Body)
	}
	w.Header().Set("Content-Type", contentType)
	if head {
		w.Header().Set("Content-Length", itoa(len(payload)))
		w.WriteHeader(resp.Status)
		return
	}
	w.WriteHeader(resp.Status)
	if len(payload) > 0 {
		_, _ = w.Write(payload)
	}
}

// --- 响应构造 ---

// dataJSON 构造 `{"ok":true,"data":<data>}`（键序与旧实现一致）。
func dataJSON(status int, data any) Response {
	payload := marshalNoEscape(data)
	raw := make([]byte, 0, len(payload)+17)
	raw = append(raw, `{"ok":true,"data":`...)
	raw = append(raw, payload...)
	raw = append(raw, '}')
	return Response{Status: status, RawBody: raw, ContentType: contentTypeJSON}
}

// okJSON 构造 `{"ok":true}`。
func okJSON(status int) Response {
	return Response{Status: status, RawBody: []byte(`{"ok":true}`), ContentType: contentTypeJSON}
}

// errorJSON 构造 `{"ok":false,"error":<msg>}`。
func errorJSON(status int, msg string) Response {
	return Response{Status: status, RawBody: []byte(`{"ok":false,"error":` + jsonx.Quote(msg) + `}`), ContentType: contentTypeJSON}
}

// unauthorizedJSON 构造业务鉴权 401（对齐旧中间件：只有 error，无 ok 字段）。
func unauthorizedJSON() Response {
	return Response{Status: http.StatusUnauthorized, RawBody: []byte(`{"error":"未登录"}`), ContentType: contentTypeJSON}
}

// errorToJSON 把处理函数返回的错误映射为响应：httperr 用其状态码，其余一律 500。
func errorToJSON(err error) Response {
	var he *httperr.Error
	if asHTTPErr(err, &he) {
		return errorJSON(he.Status, he.Message)
	}
	return errorJSON(http.StatusInternalServerError, err.Error())
}

// asHTTPErr 判断并解包 *httperr.Error。
func asHTTPErr(err error, target **httperr.Error) bool {
	for err != nil {
		if e, ok := err.(*httperr.Error); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// marshalNoEscape 与 JSON.stringify 对齐：不转义 < > & 与 U+2028/U+2029。
func marshalNoEscape(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return []byte("null")
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// itoa 是十进制整数转换（避免依赖 fmt）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// --- 路由匹配 ---

// matchStage 在当前阶段里按注册顺序找第一条命中（方法 + 路径）的路由。
func (s *Server) matchStage(stage routeStage, method, path string) (*apiRoute, map[string]string, bool) {
	for i := range s.routes {
		rt := &s.routes[i]
		if rt.stage != stage || rt.method != method {
			continue
		}
		if params, ok := matchRoutePath(rt.routePath, path); ok {
			return rt, params, true
		}
	}
	return nil, nil, false
}

// matchRoutePath 把请求路径与路由模板比对：支持 :name 段，容忍单个尾斜杠。
// 返回捕获的路径参数（已 URL 解码）。
func matchRoutePath(pattern, path string) (map[string]string, bool) {
	pseg := splitPath(pattern)
	hseg := splitPath(path)
	if len(pseg) != len(hseg) {
		return nil, false
	}
	params := map[string]string{}
	for i, want := range pseg {
		got := hseg[i]
		if strings.HasPrefix(want, ":") {
			decoded, err := url.PathUnescape(got)
			if err != nil {
				return nil, false
			}
			params[want[1:]] = decoded
			continue
		}
		if want != got {
			return nil, false
		}
	}
	return params, true
}

// splitPath 去掉首尾斜杠后按 / 切段；空路径返回空切片。
func splitPath(p string) []string {
	if p == "" || p == "/" {
		return nil
	}
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

// isAPIPath 判断路径是否进入 /api 挂载点（Express 挂载匹配：等于 /api 或以 /api/ 开头）。
func isAPIPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}

// firstValues 解析查询串，重复键只保留首个值（对齐旧实现只读单值字段的用法）。
func firstValues(rawQuery string) url.Values {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return url.Values{}
	}
	out := url.Values{}
	for key, list := range values {
		if len(list) > 0 {
			out.Set(key, list[0])
		}
	}
	return out
}

// --- 请求体 ---

// readJSONBody 解析 JSON 顶层对象字段；非 JSON 内容类型/空体返回空对象。
func readJSONBody(r *http.Request) (map[string]json.RawMessage, error) {
	empty := map[string]json.RawMessage{}
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		return empty, nil
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		return nil, errBodyRead
	}
	if int64(len(data)) > maxBodyBytes {
		return nil, errBodyTooLarge
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return empty, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err == nil {
		if obj == nil {
			return empty, nil
		}
		return obj, nil
	}
	// 顶层数组：Express strict 模式接受，但字段访问一律 undefined；据此回落空对象。
	var anyVal any
	if err := json.Unmarshal(data, &anyVal); err == nil {
		if _, isArray := anyVal.([]any); isArray {
			return empty, nil
		}
	}
	return nil, errBodyParse
}

// isJSONContentType 复刻 body-parser 的类型判定：媒介类型为 application/json 或以 +json 结尾。
func isJSONContentType(header string) bool {
	if header == "" {
		return false
	}
	media := strings.TrimSpace(strings.Split(header, ";")[0])
	media = strings.ToLower(media)
	if media == "application/json" {
		return true
	}
	return strings.HasPrefix(media, "application/") && strings.HasSuffix(media, "+json")
}

// 请求体解析错误（文案不强求与 Node 完全一致，见 docs/API.md 的「有意差异」）。
var (
	errBodyRead     = bodyError("读取请求体失败")
	errBodyTooLarge = bodyError("request entity too large")
	errBodyParse    = bodyError("请求体不是合法 JSON")
)

// bodyError 是请求体阶段错误（对外 500，与旧实现走统一错误处理一致）。
type bodyError string

func (e bodyError) Error() string { return string(e) }

// --- 静态托管的空实现 ---

// staticSource 是静态产物来源（嵌入的 dist 目录）；用标准库 io/fs.FS。
type staticSource = fs.FS

// emptyStatic 在未嵌入产物时使用：所有路径都视为不存在。
type emptyStatic struct{}

func (emptyStatic) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }
