// Package reconcile 做「账本 → xray」的单向一致性收敛：账本为权威，xray 为镜像。
//
// 背景：xray 的动态用户只存在进程内存里，xray 一重启（改配置 / 崩溃 / 重启机器）全部丢失，
// 而账本仍显示 active。本服务周期性地把应有效的补回、不应有效的清掉。
//
// 权威与副作用边界（重要）：本服务**只读账本、绝不改账本**；xray 调用失败只记日志，
// 下一轮重试即可收敛。热管理只走 adu/rmu（不 reload、不断存量连接）。addUser 对已存在返回
// false、rmu 对不存在返回 0，因此稳态每轮无动作、不产日志。
package reconcile

import (
	"context"
	"io"
	"log/slog"

	"v2link-go/internal/expiry"
	"v2link-go/internal/store"
	"v2link-go/internal/vless"
)

// LinkSource 提供全量账本行（含 revoked/expired）。
type LinkSource interface {
	LinksListAll(ctx context.Context) ([]store.LinkRow, error)
}

// UserManager 是数据面用户热管理（由 internal/xray.Client 实现）。
type UserManager interface {
	AddUser(ctx context.Context, spec vless.XrayUserSpec) (bool, error)
	RemoveUser(ctx context.Context, email string) (int64, error)
}

// Status 是 /api/healthz 暴露的数据面内存快照（零 DB/xray 往返）。
type Status struct {
	// ActiveLinks 是账本中应当有效的链接数（上一轮）。
	ActiveLinks int64 `json:"activeLinks"`
	// ReconciledAt 是上一轮完成时间（epoch ms；0 = 尚未跑过）。
	ReconciledAt int64 `json:"reconciledAt"`
	// LastFix 是上一轮实际补回 / 清理数量。
	LastFix FixCount `json:"lastFix"`
}

// FixCount 是上一轮动作计数。
type FixCount struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

// Result 是一轮收敛的结果（测试与调用方用）。
type Result struct {
	// Checked 本轮检查的账本行数。
	Checked int
	// Added 实际补回 xray 的链接 id。
	Added []string
	// Removed 实际从 xray 清理的链接 id。
	Removed []string
	// Failures xray 调用失败（账本未改，下轮重试）的链接 id。
	Failures []string
}

// ShouldBeActive 判定账本行「应当有效」：active 且未吊销 且（永久哨兵 或 尚未到期）。
func ShouldBeActive(row store.LinkRow, now int64) bool {
	return row.Status == store.StatusActive &&
		row.RevokedAt == nil &&
		(expiry.IsPermanent(row.ExpiresAt) || row.ExpiresAt > now)
}

// Service 周期收敛账本与 xray。
type Service struct {
	repo LinkSource
	xray UserManager
	log  *slog.Logger
	now  func() int64
	mu   chan struct{} // 串行化 Run，避免重叠轮次同时打 xray
	st   Status
}

// Options 构造 Service。
type Options struct {
	Repo   LinkSource
	Xray   UserManager
	Logger *slog.Logger
	Now    func() int64
}

// New 构造收敛服务。
func New(opts Options) *Service {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	now := opts.Now
	if now == nil {
		now = func() int64 { return store.NowMillis() }
	}
	return &Service{
		repo: opts.Repo,
		xray: opts.Xray,
		log:  log,
		now:  now,
		mu:   make(chan struct{}, 1),
	}
}

// Snapshot 返回内存态快照副本（调用方改不动内部状态）。
func (s *Service) Snapshot() Status {
	return Status{ActiveLinks: s.st.ActiveLinks, ReconciledAt: s.st.ReconciledAt, LastFix: s.st.LastFix}
}

// Run 跑一轮收敛。逐行处理，任一 xray 调用失败只记入 failures 并继续，绝不改账本、绝不抛出。
func (s *Service) Run(ctx context.Context) (Result, error) {
	s.mu <- struct{}{}
	defer func() { <-s.mu }()

	now := s.now()
	rows, err := s.repo.LinksListAll(ctx)
	if err != nil {
		return Result{}, err
	}
	res := Result{Checked: len(rows)}
	var activeLinks int64
	for _, row := range rows {
		if ShouldBeActive(row, now) {
			activeLinks++
			created, err := s.xray.AddUser(ctx, vless.XrayUserSpec{Email: row.Email, UUID: row.UUID})
			if err != nil {
				res.Failures = append(res.Failures, row.ID)
				continue
			}
			if created {
				res.Added = append(res.Added, row.ID)
			}
			continue
		}
		n, err := s.xray.RemoveUser(ctx, row.Email)
		if err != nil {
			res.Failures = append(res.Failures, row.ID)
			continue
		}
		if n > 0 {
			res.Removed = append(res.Removed, row.ID)
		}
	}
	s.st = Status{
		ActiveLinks:  activeLinks,
		ReconciledAt: s.now(),
		LastFix:      FixCount{Added: len(res.Added), Removed: len(res.Removed)},
	}
	if len(res.Failures) > 0 {
		s.log.Error("[reconcile] xray 调用失败（账本未改，下轮重试）", "count", len(res.Failures), "ids", res.Failures)
	}
	if len(res.Added) > 0 || len(res.Removed) > 0 {
		s.log.Info("[reconcile] 一致性同步完成", "checked", len(rows), "added", len(res.Added), "removed", len(res.Removed))
	}
	return res, nil
}
