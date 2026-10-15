package clash

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"strings"
	"time"

	"v2link-go/internal/httperr"
	"v2link-go/internal/ids"
	"v2link-go/internal/reconcile"
	"v2link-go/internal/store"
	"v2link-go/internal/vless"
)

// MinLinks / MaxLinks 是订阅可选的链接条数范围。
const (
	MinLinks = 1
	MaxLinks = 50
)

// tokenPrefixLen 是审计 detail 里 token 前缀长度（可追溯同一订阅，又不泄漏完整凭证）。
const tokenPrefixLen = 8

// Created 是创建订阅的返回体（字段名与旧实现逐字一致）。
type Created struct {
	Token      string    `json:"token"`
	URL        string    `json:"url"`
	ImportURL  string    `json:"importUrl"`
	ExpiresAt  int64     `json:"expiresAt"`
	TTLSeconds int64     `json:"ttlSeconds"`
	Count      int       `json:"count"`
	Skipped    []string  `json:"skipped"`
	Nodes      []NodeRef `json:"nodes"`
}

// NodeRef 是订阅里一个节点的最小标识。
type NodeRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Subscription 是抓取订阅的结果。
type Subscription struct {
	// YAML 是订阅正文。
	YAML string
	// Userinfo 是 subscription-userinfo 头内容；信息不足时为 nil。
	Userinfo *string
	// Count 是本次实际包含的节点数。
	Count int
	// ExpiresAt 是订阅到期时刻（epoch ms）。
	ExpiresAt int64
}

// LinkLookup 提供按 id 读账本行。
type LinkLookup interface {
	LinkByID(ctx context.Context, id string) (*store.LinkRow, error)
}

// SubStore 是订阅指针的读写。
type SubStore interface {
	InsertClashSub(ctx context.Context, q store.Querier, row store.ClashSubRow) error
	ClashSubByToken(ctx context.Context, token string) (*store.ClashSubRow, error)
}

// AuditStore 写审计。
type AuditStore interface {
	InsertAudit(ctx context.Context, q store.Querier, in store.AuditInput) (int64, error)
}

// ServiceOptions 构造订阅服务。
type ServiceOptions struct {
	Links LinkLookup
	Subs  SubStore
	Audit AuditStore
	Q     store.Querier
	// PublicBaseURL 是订阅地址前缀（PUBLIC_BASE_URL）。
	PublicBaseURL string
	// TTL 是订阅有效期。
	TTL time.Duration
	// DirectDomains 是规则分流额外直连域名后缀（默认空）。
	DirectDomains []string
	// Host/Sni/Path 是连接地址、TLS SNI/WS Host、WS 路径（生产注入 PUBLIC_HOST/PUBLIC_SNI/PUBLIC_PATH）。
	Host string
	Sni  string
	Path string
	// Now 缺省系统时钟；测试注入固定值。
	Now func() int64
	// Token 生成器；缺省 32 字节随机 base64url。测试注入可复现 token。
	Token func() string
	// Logger 缺省丢弃。
	Logger *slog.Logger
}

// Service 是 Clash 订阅编排。
type Service struct {
	o   ServiceOptions
	log *slog.Logger
	now func() int64
}

// New 构造订阅服务。
func New(opts ServiceOptions) *Service {
	if opts.Now == nil {
		opts.Now = func() int64 { return store.NowMillis() }
	}
	if opts.Token == nil {
		opts.Token = func() string { return randomToken() }
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{o: opts, log: log, now: opts.Now}
}

// Create 生成订阅指针：保留存在顺序、忽略不存在并回报 skipped，落库 + 审计。
func (s *Service) Create(ctx context.Context, linkIds any, actor string) (Created, error) {
	ids, err := normalizeLinkIDs(linkIds)
	if err != nil {
		return Created{}, err
	}
	accepted := make([]store.LinkRow, 0, len(ids))
	skipped := make([]string, 0)
	seen := make(map[string]bool)
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		row, err := s.o.Links.LinkByID(ctx, id)
		if err != nil {
			return Created{}, err
		}
		if row != nil {
			accepted = append(accepted, *row)
			continue
		}
		skipped = append(skipped, id)
	}
	if len(accepted) == 0 {
		return Created{}, httperr.New(400, "所选链接均不存在")
	}

	token := s.o.Token()
	now := s.now()
	expiresAt := now + s.o.TTL.Milliseconds()
	who := strings.TrimSpace(actor)
	if who == "" {
		who = "dev"
	}
	linkIDs, err := json.Marshal(nodeIDs(accepted))
	if err != nil {
		return Created{}, err
	}
	if err := s.o.Subs.InsertClashSub(ctx, s.o.Q, store.ClashSubRow{
		Token:     token,
		LinkIDs:   string(linkIDs),
		CreatedAt: now,
		ExpiresAt: expiresAt,
		CreatedBy: who,
	}); err != nil {
		return Created{}, err
	}
	detail, err := json.Marshal(map[string]any{"count": len(accepted), "token_prefix": tokenPrefix(token)})
	if err != nil {
		return Created{}, err
	}
	if _, err := s.o.Audit.InsertAudit(ctx, s.o.Q, store.AuditInput{
		TS:     now,
		Actor:  who,
		Action: "clash_sub_create",
		LinkID: nil,
		Detail: string(detail),
	}); err != nil {
		return Created{}, err
	}

	url := s.buildURL(token)
	return Created{
		Token:      token,
		URL:        url,
		ImportURL:  "clash://install-config?url=" + vless.EncodeURIComponent(url) + "&name=v2link",
		ExpiresAt:  expiresAt,
		TTLSeconds: int64(math.Round(float64(s.o.TTL.Milliseconds()) / 1000)),
		Count:      len(accepted),
		Skipped:    skipped,
		Nodes:      nodeRefs(accepted),
	}, nil
}

// Get 抓取订阅正文；按链接**当前状态**重新生成 YAML，全失效返回 404。
func (s *Service) Get(ctx context.Context, token string, mode Mode) (Subscription, error) {
	row, err := s.o.Subs.ClashSubByToken(ctx, token)
	if err != nil {
		return Subscription{}, err
	}
	if row == nil {
		return Subscription{}, httperr.New(404, "订阅不存在")
	}
	now := s.now()
	if row.ExpiresAt <= now {
		return Subscription{}, httperr.New(404, "订阅已过期")
	}

	var ids []string
	var parsed []any
	if err := json.Unmarshal([]byte(row.LinkIDs), &parsed); err == nil {
		for _, v := range parsed {
			if id, ok := v.(string); ok {
				ids = append(ids, id)
			}
		}
	}

	live := make([]store.LinkRow, 0, len(ids))
	for _, id := range ids {
		link, err := s.o.Links.LinkByID(ctx, id)
		if err != nil {
			return Subscription{}, err
		}
		if link != nil && reconcile.ShouldBeActive(*link, now) {
			live = append(live, *link)
		}
	}
	if len(live) == 0 {
		return Subscription{}, httperr.New(404, "所选节点均已失效")
	}

	nodes := make([]Node, len(live))
	for i, r := range live {
		nodes[i] = Node{
			UUID:   r.UUID,
			Name:   vless.DisplayAlias(r.Alias, r.Note, r.ID),
			Server: s.o.Host,
			Sni:    s.o.Sni,
			Path:   s.o.Path,
		}
	}
	return Subscription{
		YAML:      BuildConfig(nodes, BuildOptions{Mode: mode, DirectDomains: s.o.DirectDomains}),
		Userinfo:  buildUserinfo(live),
		Count:     len(live),
		ExpiresAt: row.ExpiresAt,
	}, nil
}

func (s *Service) buildURL(token string) string {
	base := strings.TrimRight(strings.TrimSpace(s.o.PublicBaseURL), "/")
	return base + "/api/clash/subscriptions/" + token
}

// normalizeLinkIDs 复刻旧实现的入参校验：必须是数组、1~50 条、每项为非空字符串。
func normalizeLinkIDs(raw any) ([]string, error) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, httperr.New(400, "linkIds 须为数组")
	}
	if len(arr) < MinLinks || len(arr) > MaxLinks {
		return nil, httperr.New(400, "请选择 1~50 条链接（当前 "+itoa(len(arr))+" 条）")
	}
	ids := make([]string, 0, len(arr))
	for _, v := range arr {
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, httperr.New(400, "linkIds 含非法项")
		}
		ids = append(ids, strings.TrimSpace(s))
	}
	return ids, nil
}

// buildUserinfo 生成 subscription-userinfo：上传/下载字节合计 + 最早到期（秒）；全永久则省略 expire。
func buildUserinfo(rows []store.LinkRow) *string {
	if len(rows) == 0 {
		return nil
	}
	var up, down int64
	expires := make([]int64, 0, len(rows))
	for _, r := range rows {
		up += r.UpBytes
		down += r.DownBytes
		if r.ExpiresAt > 0 {
			expires = append(expires, r.ExpiresAt)
		}
	}
	parts := []string{"upload=" + itoa64(up), "download=" + itoa64(down)}
	if len(expires) > 0 {
		min := expires[0]
		for _, e := range expires[1:] {
			if e < min {
				min = e
			}
		}
		parts = append(parts, "expire="+itoa64(min/1000))
	}
	out := strings.Join(parts, "; ")
	return &out
}

func nodeIDs(rows []store.LinkRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func nodeRefs(rows []store.LinkRow) []NodeRef {
	out := make([]NodeRef, len(rows))
	for i, r := range rows {
		out[i] = NodeRef{ID: r.ID, Name: vless.DisplayAlias(r.Alias, r.Note, r.ID)}
	}
	return out
}

func tokenPrefix(token string) string {
	if len(token) <= tokenPrefixLen {
		return token
	}
	return token[:tokenPrefixLen]
}

// randomToken 生成 32 字节随机数据的 base64url（对齐 crypto.randomBytes(32).toString('base64url')）。
func randomToken() string { return ids.RandomToken(32) }

func itoa64(n int64) string {
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
