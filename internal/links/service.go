// Package links 是链接生成/吊销/续期/删除的业务编排层。
//
// 约定（REQUIREMENTS.md §5）：所有写操作 = 先落账本（事务）→ 再调 xray；xray 失败 → 回滚账本
// 并抛 502（保证账本与数据面一致）。状态机：active → expired | revoked；expired/revoked 不可
// 再延长；revoked 不可逆。expires_at=0 是永久哨兵（见 internal/expiry）。
//
// 仅 extend 不触 xray（expires_at 是纯控制面字段）；create/revoke 才需要数据面一致。
package links

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"strconv"
	"strings"

	"v2link-go/internal/expiry"
	"v2link-go/internal/httperr"
	"v2link-go/internal/ids"
	"v2link-go/internal/jsonx"
	"v2link-go/internal/store"
	"v2link-go/internal/vless"
)

// MaxExpireAbsDays 是绝对过期时刻允许的最大跨度（now ~ now+365 天）。
const MaxExpireAbsDays = 365

// Limits 是业务上限（默认值来自 config 注入，服务层不碰全局配置）。
type Limits struct {
	DefaultHours int
	MaxHours     int
}

// Repo 是账本读写。
type Repo interface {
	LinksList(ctx context.Context) ([]store.LinkView, error)
	LinkByID(ctx context.Context, id string) (*store.LinkRow, error)
	InsertLink(ctx context.Context, q store.Querier, r store.LinkRow) error
	RemoveLink(ctx context.Context, q store.Querier, id string) (int64, error)
	ExtendLinkExpiry(ctx context.Context, q store.Querier, id string, newExpiry int64) error
	RevokeLink(ctx context.Context, q store.Querier, id string, revokedAt int64) (bool, error)
	RollbackRevoke(ctx context.Context, q store.Querier, id string) error
}

// Monitor 是审计与采样清理。
type Monitor interface {
	InsertAudit(ctx context.Context, q store.Querier, in store.AuditInput) (int64, error)
	DeleteAuditByLink(ctx context.Context, q store.Querier, linkID string) (int64, error)
	DeleteSamplesByLink(ctx context.Context, q store.Querier, linkID string) (int64, error)
}

// Xray 是数据面热管理（internal/xray.Client 实现）。
type Xray interface {
	AddUser(ctx context.Context, spec vless.XrayUserSpec) (bool, error)
	RemoveUser(ctx context.Context, email string) (int64, error)
}

// TxRunner 把一组读写放进单事务。
type TxRunner interface {
	Tx(ctx context.Context, fn func(q store.Querier) error) error
}

// Options 构造服务。
type Options struct {
	Repo    Repo
	Monitor Monitor
	Xray    Xray
	Runner  TxRunner
	Limits  Limits
	Now     func() int64
	Logger  *slog.Logger
}

// Service 是链接业务服务。
type Service struct {
	o   Options
	log *slog.Logger
	now func() int64
}

// New 构造服务。
func New(opts Options) *Service {
	if opts.Now == nil {
		opts.Now = func() int64 { return store.NowMillis() }
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{o: opts, log: log, now: opts.Now}
}

// List 返回全量账本展示形态（按创建时间倒序）。
func (s *Service) List(ctx context.Context) ([]store.LinkView, error) {
	return s.o.Repo.LinksList(ctx)
}

// Create 生成一条链接：先落账本 + 审计（事务），再 adu；失败回滚账本与审计并返回 502。
func (s *Service) Create(ctx context.Context, in CreateInput, actor string) (store.LinkView, error) {
	norm, err := s.normalizeCreate(in)
	if err != nil {
		return store.LinkView{}, err
	}
	id := ids.NewLinkID()
	uuid := ids.RandomUUID()
	createdAt := s.now()
	expiresAtMs := int64(0)
	if !norm.permanent {
		if norm.abs != nil {
			expiresAtMs = *norm.abs
		} else {
			expiresAtMs = createdAt + int64(norm.hours)*3600*1000
		}
	}
	row := store.LinkRow{
		ID:        id,
		UUID:      uuid,
		Email:     id,
		Note:      norm.note,
		Alias:     norm.alias,
		CreatedAt: createdAt,
		ExpiresAt: expiresAtMs,
		Status:    store.StatusActive,
	}

	detail := createAuditDetail(norm, expiresAtMs)
	if err := s.o.Runner.Tx(ctx, func(q store.Querier) error {
		if err := s.o.Repo.InsertLink(ctx, q, row); err != nil {
			return err
		}
		_, err := s.o.Monitor.InsertAudit(ctx, q, store.AuditInput{
			TS: createdAt, Actor: actorOf(actor), Action: "create", LinkID: &id, Detail: detail,
		})
		return err
	}); err != nil {
		return store.LinkView{}, err
	}

	if _, err := s.o.Xray.AddUser(ctx, vless.XrayUserSpec{Email: id, UUID: uuid}); err != nil {
		// 回滚：本次 create 未生效，同事务撤销账本行与审计行。
		rbErr := s.o.Runner.Tx(ctx, func(q store.Querier) error {
			if _, err := s.o.Repo.RemoveLink(ctx, q, id); err != nil {
				return err
			}
			_, err := s.o.Monitor.DeleteAuditByLink(ctx, q, id)
			return err
		})
		if rbErr != nil {
			s.log.Error("[links] create 回滚失败", "id", id, "err", rbErr)
		}
		return store.LinkView{}, httperr.New(502, "xray 添加用户失败，已回滚: "+err.Error())
	}
	return s.viewByID(ctx, id)
}

// Revoke 吊销：先标 revoked + 审计（事务），再 rmu；失败回滚回 active 并返回 502。
func (s *Service) Revoke(ctx context.Context, id, actor string) (store.LinkView, error) {
	row, err := s.activeRow(ctx, id)
	if err != nil {
		return store.LinkView{}, err
	}
	revokedAt := s.now()
	note := nullable(row.Note)
	if err := s.o.Runner.Tx(ctx, func(q store.Querier) error {
		if _, err := s.o.Repo.RevokeLink(ctx, q, id, revokedAt); err != nil {
			return err
		}
		_, err := s.o.Monitor.InsertAudit(ctx, q, store.AuditInput{
			TS: revokedAt, Actor: actorOf(actor), Action: "revoke", LinkID: &id,
			Detail: jsonx.Object(jsonx.Field{Key: "note", Raw: note}),
		})
		return err
	}); err != nil {
		return store.LinkView{}, err
	}
	if _, err := s.o.Xray.RemoveUser(ctx, id); err != nil {
		rbErr := s.o.Runner.Tx(ctx, func(q store.Querier) error {
			return s.o.Repo.RollbackRevoke(ctx, q, id)
		})
		if rbErr != nil {
			s.log.Error("[links] revoke 回滚失败", "id", id, "err", rbErr)
		}
		return store.LinkView{}, httperr.New(502, "xray 删除用户失败，已回滚: "+err.Error())
	}
	return s.viewByID(ctx, id)
}

// Extend 延长/转换：hours / expiresAt / permanent 三选一；仅 active 可操作。
func (s *Service) Extend(ctx context.Context, id string, in ExtendInput, actor string) (store.LinkView, error) {
	row, err := s.activeRow(ctx, id)
	if err != nil {
		return store.LinkView{}, err
	}
	wasPermanent := expiry.IsPermanent(row.ExpiresAt)
	if in.Permanent.Present && !in.Permanent.IsBool {
		return store.LinkView{}, httperr.New(400, "permanent 须为布尔值")
	}
	hasHours := in.Hours.Present
	hasAbs := in.ExpiresAt.Present
	hasPerm := in.Permanent.Present && in.Permanent.Value

	provided := 0
	for _, b := range []bool{hasHours, hasAbs, hasPerm} {
		if b {
			provided++
		}
	}
	if provided != 1 {
		if provided == 0 {
			return store.LinkView{}, httperr.New(400, "请指定 hours、expiresAt 或 permanent 之一")
		}
		return store.LinkView{}, httperr.New(400, "请三选一：hours / expiresAt / permanent，不能同时传")
	}

	var newExpiry int64
	var detail string
	switch {
	case hasPerm:
		if wasPermanent {
			return store.LinkView{}, httperr.New(400, "该链接已是永久有效")
		}
		newExpiry = expiry.PermanentExpiresAt
		detail = jsonx.Object(jsonx.Field{Key: "permanent", Raw: "true"})
	case hasAbs:
		at, err := s.validateAbsolute(in.ExpiresAt.Value)
		if err != nil {
			return store.LinkView{}, err
		}
		newExpiry = at
		fields := make([]jsonx.Field, 0, 3)
		if wasPermanent {
			fields = append(fields, jsonx.Field{Key: "from", Raw: jsonx.Quote("permanent")})
		}
		fields = append(fields,
			jsonx.Field{Key: "expiresAt", Raw: jsonx.Int(at)},
			jsonx.Field{Key: "expires_at", Raw: jsonx.Int(newExpiry)})
		detail = jsonx.Object(fields...)
	default:
		h := in.Hours.Value
		if !isInteger(h) || h < 1 || h > float64(s.o.Limits.MaxHours) {
			return store.LinkView{}, httperr.New(400, "延长时长须为 1~"+strconv.Itoa(s.o.Limits.MaxHours)+" 小时的整数")
		}
		if wasPermanent {
			return store.LinkView{}, httperr.New(400, "永久链接没有到期时刻可平移，请用 expiresAt 指定绝对到期时间")
		}
		newExpiry = row.ExpiresAt + int64(h)*3600*1000
		detail = jsonx.Object(
			jsonx.Field{Key: "hours", Raw: jsonx.Int(int64(h))},
			jsonx.Field{Key: "expires_at", Raw: jsonx.Int(newExpiry)})
	}

	if err := s.o.Runner.Tx(ctx, func(q store.Querier) error {
		if err := s.o.Repo.ExtendLinkExpiry(ctx, q, id, newExpiry); err != nil {
			return err
		}
		_, err := s.o.Monitor.InsertAudit(ctx, q, store.AuditInput{
			TS: s.now(), Actor: actorOf(actor), Action: "extend", LinkID: &id, Detail: detail,
		})
		return err
	}); err != nil {
		return store.LinkView{}, err
	}
	return s.viewByID(ctx, id)
}

// Remove 删除链接（账本行 + 采样）；connections 保留。先尽力 rmu（失败不阻塞）。
func (s *Service) Remove(ctx context.Context, id, actor string) error {
	row, err := s.o.Repo.LinkByID(ctx, id)
	if err != nil {
		return err
	}
	if row == nil {
		return httperr.New(404, "链接不存在")
	}
	if _, err := s.o.Xray.RemoveUser(ctx, id); err != nil {
		// 用户点了删除就真删掉：xray 不可达不阻塞，仅记日志。
		s.log.Warn("[links] remove: rmu 失败", "id", id, "err", err)
	}
	detail := removeAuditDetail(*row)
	return s.o.Runner.Tx(ctx, func(q store.Querier) error {
		if _, err := s.o.Monitor.DeleteSamplesByLink(ctx, q, id); err != nil {
			return err
		}
		if _, err := s.o.Repo.RemoveLink(ctx, q, id); err != nil {
			return err
		}
		_, err := s.o.Monitor.InsertAudit(ctx, q, store.AuditInput{
			TS: s.now(), Actor: actorOf(actor), Action: "link_delete", LinkID: &id, Detail: detail,
		})
		return err
	})
}

// normalizeCreate 校验并归一化创建入参。
func (s *Service) normalizeCreate(in CreateInput) (normalized, error) {
	note := truncateRunes(strings.TrimSpace(in.Note), 500)
	alias := truncateRunes(strings.TrimSpace(in.Alias), 100)
	if in.Permanent.Present && !in.Permanent.IsBool {
		return normalized{}, httperr.New(400, "permanent 须为布尔值")
	}
	isPermanent := in.Permanent.Present && in.Permanent.Value
	hasHours := in.Hours.Present
	hasAbs := in.ExpiresAt.Present
	count := 0
	for _, b := range []bool{isPermanent, hasHours, hasAbs} {
		if b {
			count++
		}
	}
	if count > 1 {
		return normalized{}, httperr.New(400, "expiresAt / hours / permanent 只能指定一个")
	}
	if hasHours {
		h := in.Hours.Value
		if !isInteger(h) || h < 1 || h > float64(s.o.Limits.MaxHours) {
			return normalized{}, httperr.New(400, "时长须为 1~"+strconv.Itoa(s.o.Limits.MaxHours)+" 小时的整数")
		}
	}
	var abs *int64
	if hasAbs {
		at, err := s.validateAbsolute(in.ExpiresAt.Value)
		if err != nil {
			return normalized{}, err
		}
		abs = &at
	}
	hours := s.o.Limits.DefaultHours
	if hasHours {
		hours = int(in.Hours.Value)
	}
	return normalized{note: note, alias: alias, abs: abs, hours: hours, permanent: isPermanent}, nil
}

// validateAbsolute 校验绝对过期时刻：必须是 epoch 毫秒整数、晚于当前、且不超过 365 天。
func (s *Service) validateAbsolute(at float64) (int64, error) {
	if math.IsNaN(at) || math.IsInf(at, 0) || math.Trunc(at) != at {
		return 0, httperr.New(400, "expiresAt 须为 epoch 毫秒整数")
	}
	now := s.now()
	value := int64(at)
	if value <= now {
		return 0, httperr.New(400, "expiresAt 须晚于当前时间（不允许设置过去时刻）")
	}
	if value-now > int64(MaxExpireAbsDays)*24*3600*1000 {
		return 0, httperr.New(400, "expiresAt 超出合理范围（未来 365 天内）")
	}
	return value, nil
}

// activeRow 取 active 行；不存在 404，非 active 400。
func (s *Service) activeRow(ctx context.Context, id string) (store.LinkRow, error) {
	row, err := s.o.Repo.LinkByID(ctx, id)
	if err != nil {
		return store.LinkRow{}, err
	}
	if row == nil {
		return store.LinkRow{}, httperr.New(404, "链接不存在")
	}
	if row.Status != store.StatusActive {
		return store.LinkRow{}, httperr.New(400, "仅 active 链接可操作（当前 "+string(row.Status)+"）")
	}
	return *row, nil
}

func (s *Service) viewByID(ctx context.Context, id string) (store.LinkView, error) {
	row, err := s.o.Repo.LinkByID(ctx, id)
	if err != nil {
		return store.LinkView{}, err
	}
	if row == nil {
		return store.LinkView{}, httperr.New(404, "链接不存在")
	}
	return store.ToView(*row), nil
}

// normalized 是校验后的创建入参。
type normalized struct {
	note      string
	alias     string
	abs       *int64
	hours     int
	permanent bool
}

func createAuditDetail(n normalized, expiresAtMs int64) string {
	fields := make([]jsonx.Field, 0, 4)
	fields = append(fields, jsonx.Field{Key: "note", Raw: nullable(n.note)})
	if n.alias != "" {
		fields = append(fields, jsonx.Field{Key: "alias", Raw: jsonx.Quote(n.alias)})
	}
	switch {
	case n.permanent:
		fields = append(fields, jsonx.Field{Key: "permanent", Raw: "true"})
	case n.abs != nil:
		fields = append(fields,
			jsonx.Field{Key: "expiresAt", Raw: jsonx.Int(*n.abs)},
			jsonx.Field{Key: "expires_at", Raw: jsonx.Int(expiresAtMs)})
	default:
		fields = append(fields, jsonx.Field{Key: "hours", Raw: jsonx.Int(int64(n.hours))})
	}
	return jsonx.Object(fields...)
}

func removeAuditDetail(row store.LinkRow) string {
	statusLabel := statusLabels[row.Status]
	return jsonx.Object(
		jsonx.Field{Key: "id", Raw: jsonx.Quote(row.ID)},
		jsonx.Field{Key: "alias", Raw: nullable(row.Alias)},
		jsonx.Field{Key: "note", Raw: nullable(row.Note)},
		jsonx.Field{Key: "status", Raw: jsonx.Quote(string(row.Status))},
		jsonx.Field{Key: "text", Raw: jsonx.Quote("删除 " + row.ID + "（" + statusLabel + "）")},
	)
}

// statusLabels 是状态中文标签（审计 detail 摘要用）。
var statusLabels = map[store.LinkStatus]string{
	store.StatusActive:  "使用中",
	store.StatusExpired: "已过期",
	store.StatusRevoked: "已吊销",
}

func nullable(s string) string {
	if s == "" {
		return "null"
	}
	return jsonx.Quote(s)
}

func actorOf(actor string) string {
	if v := strings.TrimSpace(actor); v != "" {
		return v
	}
	return "dev"
}

func isInteger(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0) && math.Trunc(f) == f
}

// truncateRunes 按字符数截断（对齐 JS String.prototype.slice，按 UTF-16 码元计；中文/emoji 少见
// 且均为 BMP 内时按 rune 处理等价于按码点）。
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// --- 入参类型与解析（路由层只负责取原始 JSON，本层复刻旧实现的 Number/typeof 语义）---

// NumberField 表示一个可能缺省的数值入参。
type NumberField struct {
	Present bool
	Value   float64
}

// BoolField 表示一个可能缺省的布尔入参。
type BoolField struct {
	Present bool
	IsBool  bool
	Value   bool
}

// CreateInput 是创建入参（已按 JS Number/typeof 语义归一）。
type CreateInput struct {
	Note      string
	Alias     string
	Hours     NumberField
	ExpiresAt NumberField
	Permanent BoolField
}

// ExtendInput 是续期入参。
type ExtendInput struct {
	Hours     NumberField
	ExpiresAt NumberField
	Permanent BoolField
}

// ParseCreateInput 从原始 JSON 字段解析创建入参。
func ParseCreateInput(raw map[string]json.RawMessage) CreateInput {
	return CreateInput{
		Note:      stringField(raw["note"]),
		Alias:     stringField(raw["alias"]),
		Hours:     numberField(raw, "hours"),
		ExpiresAt: numberField(raw, "expiresAt"),
		Permanent: boolField(raw, "permanent"),
	}
}

// ParseExtendInput 从原始 JSON 字段解析续期入参。
func ParseExtendInput(raw map[string]json.RawMessage) ExtendInput {
	return ExtendInput{
		Hours:     numberField(raw, "hours"),
		ExpiresAt: numberField(raw, "expiresAt"),
		Permanent: boolField(raw, "permanent"),
	}
}

func stringField(raw json.RawMessage) string {
	if len(raw) == 0 || raw[0] != '"' {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

func numberField(raw map[string]json.RawMessage, key string) NumberField {
	v, ok := raw[key]
	if !ok {
		return NumberField{}
	}
	return NumberField{Present: true, Value: jsNumber(v)}
}

func boolField(raw map[string]json.RawMessage, key string) BoolField {
	v, ok := raw[key]
	if !ok {
		return BoolField{}
	}
	s := strings.TrimSpace(string(v))
	switch s {
	case "true":
		return BoolField{Present: true, IsBool: true, Value: true}
	case "false":
		return BoolField{Present: true, IsBool: true, Value: false}
	default:
		return BoolField{Present: true, IsBool: false}
	}
}

// jsNumber 复刻 JS Number() 的粗语义：数字直取；字符串按十进制解析（空串为 0）；
// true/false 为 1/0；null 为 0；对象/数组等为 NaN。
func jsNumber(raw json.RawMessage) float64 {
	s := strings.TrimSpace(string(raw))
	switch s {
	case "null":
		return 0
	case "true":
		return 1
	case "false":
		return 0
	case "":
		return math.NaN()
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return math.NaN()
		}
		return parseNumberString(str)
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return math.NaN()
	}
	return f
}

// parseNumberString 复刻 Number(string)：先 trim；空串为 0；否则按浮点解析；失败为 NaN。
func parseNumberString(s string) float64 {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return math.NaN()
	}
	return f
}
