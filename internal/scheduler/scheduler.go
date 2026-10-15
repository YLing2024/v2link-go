// Package scheduler 是后台调度器：过期扫描、流量账本、保留期清理、地区探测、xray 一致性同步。
//
// 语义逐条对齐旧实现 services/scheduler.ts：定时周期来自配置；流量账本首拉不带 -reset
// 仅读值丢弃（见 collectTraffic 注释），此后才用 -reset 的增量；过期扫描只把 active 置为
// expired（防与手动吊销竞态）；地区探测与一致性同步启动即跑一轮，周期为 0 时只跑启动那一次。
package scheduler

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	"v2link-go/internal/monitor"
	"v2link-go/internal/reconcile"
	"v2link-go/internal/regions"
	"v2link-go/internal/store"
)

// Repo 是调度器读写的账本。
type Repo interface {
	FindActiveExpired(ctx context.Context, now int64) ([]store.LinkRow, error)
	ListActive(ctx context.Context) ([]store.LinkRow, error)
	AddTraffic(ctx context.Context, q store.Querier, id string, upDelta, downDelta int64) error
	ExpireLink(ctx context.Context, q store.Querier, id string) (bool, error)
}

// Monitor 是采样与清理。
type Monitor interface {
	InsertSamples(ctx context.Context, q store.Querier, items []store.SampleInput) error
	DeleteConnectionsBefore(ctx context.Context, ts int64) (int64, error)
	DeleteSamplesBefore(ctx context.Context, ts int64) (int64, error)
}

// Xray 是数据面热管理与流量查询。
type Xray interface {
	RemoveUser(ctx context.Context, email string) (int64, error)
	QueryTraffic(ctx context.Context, pattern string, reset bool) (string, error)
}

// ClashSubs 清理过期订阅。
type ClashSubs interface {
	DeleteExpiredClashSubs(ctx context.Context, now int64) (int64, error)
}

// RegionProbe 跑地区探测一轮。
type RegionProbe interface {
	RunRound(ctx context.Context) regions.Round
}

// Reconciler 跑一致性同步一轮。
type Reconciler interface {
	Run(ctx context.Context) (reconcile.Result, error)
}

// TxRunner 把一组写入放进单事务。
type TxRunner interface {
	Tx(ctx context.Context, fn func(q store.Querier) error) error
}

// Options 构造调度器。周期 <=0 表示不周期轮询（地区探测/一致性同步仍会跑启动那一轮）。
type Options struct {
	Repo    Repo
	Monitor Monitor
	Xray    Xray
	Tx      TxRunner

	ExpireInterval        time.Duration
	LedgerInterval        time.Duration
	ConnCleanupInterval   time.Duration
	SampleCleanupInterval time.Duration
	ConnRetention         time.Duration
	SampleRetention       time.Duration

	RegionProbe         RegionProbe
	RegionProbeInterval time.Duration
	Reconcile           Reconciler
	ReconcileInterval   time.Duration
	ClashSubs           ClashSubs

	Now    func() int64
	Logger *slog.Logger
}

// TrafficResult 是流量账本单轮结果。
type TrafficResult struct {
	Applied    int
	SampleRows int
}

// Scheduler 运行各周期任务。
type Scheduler struct {
	o   Options
	log *slog.Logger
	now func() int64

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	wg      sync.WaitGroup

	// firstLedger 为 true 表示尚未预热（首拉只读不 reset）。
	firstLedger bool
}

// New 构造调度器。
func New(opts Options) *Scheduler {
	if opts.Tx == nil {
		panic("scheduler: Tx 不能为空")
	}
	if opts.Now == nil {
		opts.Now = store.NowMillis
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Scheduler{o: opts, log: log, now: opts.Now, firstLedger: true}
}

// Start 预热首拉并启动各周期任务（幂等）。
func (s *Scheduler) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	// 预热首拉：失败不致命，firstLedger 保持 true，下轮仍按首拉处理。
	if _, err := s.CollectTraffic(context.Background()); err != nil {
		s.log.Warn("首次流量采集失败（将延后至下一轮）", "err", err)
	}

	s.every(s.o.ExpireInterval, func() { s.runExpire() })
	s.every(s.o.LedgerInterval, func() { s.runLedger() })
	s.every(s.o.ConnCleanupInterval, func() {
		s.CleanupConnections()
		s.CleanupClashSubs()
	})
	s.every(s.o.SampleCleanupInterval, func() { s.CleanupSamples() })

	// 启动即清一次旧数据，保持留存水位。
	s.CleanupConnections()
	s.CleanupSamples()
	s.CleanupClashSubs()

	if s.o.RegionProbe != nil {
		go func() { s.RunRegionProbes(context.Background()) }()
		if s.o.RegionProbeInterval > 0 {
			s.every(s.o.RegionProbeInterval, func() { s.RunRegionProbes(context.Background()) })
		}
	}
	// 一致性同步：启动即跑一轮自愈（不阻塞 HTTP），之后按周期。
	go func() { s.RunReconcile(context.Background()) }()
	if s.o.ReconcileInterval > 0 {
		s.every(s.o.ReconcileInterval, func() { s.RunReconcile(context.Background()) })
	}
}

// Stop 停止全部周期任务并等待在跑的轮次结束（幂等）。
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stopCh)
	s.mu.Unlock()
	s.wg.Wait()
}

// every 启动一个周期任务；interval<=0 时不起（首轮决定是否手动跑）。
func (s *Scheduler) every(interval time.Duration, fn func()) {
	if interval <= 0 {
		return
	}
	s.mu.Lock()
	stopCh := s.stopCh
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				fn()
			}
		}
	}()
}

// RunExpire 执行过期扫描单轮，返回被置为 expired 的链接 id。
func (s *Scheduler) RunExpire() []string { return s.runExpire() }

func (s *Scheduler) runExpire() []string {
	ctx := context.Background()
	now := s.now()
	rows, err := s.o.Repo.FindActiveExpired(ctx, now)
	if err != nil {
		s.log.Warn("过期扫描查询失败（下轮重试）", "err", err)
		return nil
	}
	expired := make([]string, 0)
	for _, row := range rows {
		if _, err := s.o.Xray.RemoveUser(ctx, row.Email); err != nil {
			s.log.Warn("过期链接 rmu 失败（下轮重试）", "email", row.Email, "err", err)
			continue
		}
		if err := s.o.Tx.Tx(ctx, func(q store.Querier) error {
			// 仅 active → expired（防与手动 revoke 竞态）。
			if _, err := s.o.Repo.ExpireLink(ctx, q, row.ID); err != nil {
				return err
			}
			return nil
		}); err != nil {
			s.log.Warn("标记 expired 失败（下轮重试）", "id", row.ID, "err", err)
			continue
		}
		expired = append(expired, row.ID)
	}
	if len(expired) > 0 {
		s.log.Info("过期吊销", "count", len(expired))
	}
	return expired
}

// CollectTraffic 执行流量账本单轮。首次内部自动翻转 firstLedger（见包注释）。
func (s *Scheduler) CollectTraffic(ctx context.Context) (TrafficResult, error) {
	isFirst := s.firstLedger
	raw, err := s.o.Xray.QueryTraffic(ctx, "user>>>", !isFirst)
	if err != nil {
		return TrafficResult{}, err
	}
	if isFirst {
		// 首拉仅读不 reset、值丢弃；失败时不会走到这里，firstLedger 保持 true。
		s.firstLedger = false
		return TrafficResult{}, nil
	}
	parsed := monitor.ParseStatsQuery(raw)
	ts := s.now()

	active, err := s.o.Repo.ListActive(ctx)
	if err != nil {
		return TrafficResult{}, err
	}
	byEmail := make(map[string]string, len(active))
	for _, row := range active {
		byEmail[row.Email] = row.ID
	}

	res := TrafficResult{}
	samples := make([]store.SampleInput, 0, len(parsed.ByUser))
	err = s.o.Tx.Tx(ctx, func(q store.Querier) error {
		for email, pair := range parsed.ByUser {
			linkID, ok := byEmail[email]
			if !ok {
				// 账本无对应 active 链接（xray 残留/手工 adu 用户）——忽略，避免凭空累计。
				continue
			}
			if err := s.o.Repo.AddTraffic(ctx, q, linkID, pair.Up, pair.Down); err != nil {
				return err
			}
			samples = append(samples, store.SampleInput{LinkID: linkID, TS: ts, UpDelta: pair.Up, DownDelta: pair.Down})
			res.Applied++
		}
		if len(samples) > 0 {
			if err := s.o.Monitor.InsertSamples(ctx, q, samples); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return TrafficResult{}, err
	}
	res.SampleRows = len(samples)
	return res, nil
}

func (s *Scheduler) runLedger() {
	if _, err := s.CollectTraffic(context.Background()); err != nil {
		s.log.Warn("流量账本采集失败（下轮重试）", "err", err)
	}
}

// CleanupConnections 执行 connections 滚动清理，返回删除行数。
func (s *Scheduler) CleanupConnections() int64 {
	before := s.now() - s.o.ConnRetention.Milliseconds()
	n, err := s.o.Monitor.DeleteConnectionsBefore(context.Background(), before)
	if err != nil {
		s.log.Warn("连接记录清理失败", "err", err)
		return 0
	}
	if n > 0 {
		s.log.Info("清理过期连接记录", "olderThanMs", before, "removed", n)
	}
	return n
}

// CleanupSamples 执行 traffic_samples 滚动清理，返回删除行数。
func (s *Scheduler) CleanupSamples() int64 {
	before := s.now() - s.o.SampleRetention.Milliseconds()
	n, err := s.o.Monitor.DeleteSamplesBefore(context.Background(), before)
	if err != nil {
		s.log.Warn("流量采样清理失败", "err", err)
		return 0
	}
	if n > 0 {
		s.log.Info("清理过期流量采样", "olderThanMs", before, "removed", n)
	}
	return n
}

// CleanupClashSubs 清理过期 Clash 订阅，返回删除行数。
func (s *Scheduler) CleanupClashSubs() int64 {
	if s.o.ClashSubs == nil {
		return 0
	}
	n, err := s.o.ClashSubs.DeleteExpiredClashSubs(context.Background(), s.now())
	if err != nil {
		s.log.Warn("Clash 订阅清理失败", "err", err)
		return 0
	}
	if n > 0 {
		s.log.Info("清理过期 Clash 订阅", "removed", n)
	}
	return n
}

// RunRegionProbes 触发地区探测一轮（失败不致命）。
func (s *Scheduler) RunRegionProbes(ctx context.Context) {
	if s.o.RegionProbe == nil {
		return
	}
	s.o.RegionProbe.RunRound(ctx)
}

// RunReconcile 触发一致性同步一轮（失败不致命）。
func (s *Scheduler) RunReconcile(ctx context.Context) {
	if s.o.Reconcile == nil {
		return
	}
	if _, err := s.o.Reconcile.Run(ctx); err != nil {
		s.log.Warn("xray 一致性同步失败（下轮重试）", "err", err)
	}
}
