// Package regions 做地区连通性探测：服务器直连各主流地区知名 HTTPS 端点测 RTT（HTTP，非 ICMP），
// 结果只存内存（最新快照 + 最近 N 轮历史），不落库。
//
// 语义逐条对齐旧实现 services/regionProbe.ts：GET 直连、5s 超时、收到响应头即记 RTT 并中止 body；
// res.ok（HTTP 200–299）= ok=true；失败原因区分 timeout 与其它；并发测全部端点；防重入。
package regions

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"v2link-go/internal/config"
)

// 默认参数（与旧实现一致）。
const (
	defaultTimeoutMs = 5000
	defaultMaxRounds = 12
)

// Spec 是一个探测端点。
type Spec struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Flag string `json:"flag"`
	URL  string `json:"url"`
}

// Result 是单个地区一轮的探测结果。
type Result struct {
	Key   string  `json:"key"`
	OK    bool    `json:"ok"`
	RTTMs *int64  `json:"rttMs"`
	Error *string `json:"error"`
	TS    int64   `json:"ts"`
}

// Round 是一轮探测（全部地区的并发结果）。
type Round struct {
	TS      int64    `json:"ts"`
	Results []Result `json:"results"`
}

// SnapshotProbe 是快照里的地区条目（带端点元信息，前端直接渲染）。
type SnapshotProbe struct {
	Result
	Flag string `json:"flag"`
	Name string `json:"name"`
}

// Doer 抽象一次 HTTP 请求（测试注入 mock）。
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Options 构造探测服务。
type Options struct {
	// Probes 探测端点；缺省用内置列表。
	Probes []Spec
	// Timeout 单次探测超时；<=0 用 5s。
	Timeout time.Duration
	// MaxRounds 内存保留轮数；<=0 用 12。
	MaxRounds int
	// Client 注入的 HTTP 客户端；缺省无超时（超时由 context 控制）。
	Client Doer
	// Now 缺省系统时钟；测试注入固定值。
	Now func() int64
	// Logger 缺省丢弃。
	Logger *slog.Logger
}

// Service 维护最新快照与历史轮次。
type Service struct {
	probes    []Spec
	timeout   time.Duration
	maxRounds int
	client    Doer
	now       func() int64
	log       *slog.Logger

	mu       sync.Mutex
	rounds   []Round
	inFlight chan struct{}
}

// New 构造探测服务。
func New(opts Options) *Service {
	probes := opts.Probes
	if len(probes) == 0 {
		probes = toSpecs(config.DefaultRegionProbes)
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeoutMs * time.Millisecond
	}
	maxRounds := opts.MaxRounds
	if maxRounds <= 0 {
		maxRounds = defaultMaxRounds
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{}
	}
	now := opts.Now
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{
		probes:    probes,
		timeout:   timeout,
		maxRounds: maxRounds,
		client:    client,
		now:       now,
		log:       log,
	}
}

// Snapshot 返回最新快照；从未跑过 → updatedAt=0、空列表。与端点配置同序返回。
func (s *Service) Snapshot() (int64, []SnapshotProbe) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.rounds) == 0 {
		return 0, []SnapshotProbe{}
	}
	latest := s.rounds[0]
	byKey := make(map[string]Result, len(latest.Results))
	for _, r := range latest.Results {
		byKey[r.Key] = r
	}
	out := make([]SnapshotProbe, 0, len(s.probes))
	for _, p := range s.probes {
		base, ok := byKey[p.Key]
		if !ok {
			errMsg := "尚未探测"
			base = Result{Key: p.Key, OK: false, Error: &errMsg, TS: latest.TS}
		}
		out = append(out, SnapshotProbe{Result: base, Flag: p.Flag, Name: p.Name})
	}
	return latest.TS, out
}

// History 返回最近 N 轮历史（newest first，最多 maxRounds 轮）。
func (s *Service) History() []Round {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Round, len(s.rounds))
	copy(out, s.rounds)
	return out
}

// RunRound 探测一轮（并发测全部端点）；已有进行中的一轮时复用同一轮，避免叠加探测。
func (s *Service) RunRound(ctx context.Context) Round {
	s.mu.Lock()
	if s.inFlight != nil {
		done := s.inFlight
		s.mu.Unlock()
		<-done
		s.mu.Lock()
		r := Round{}
		if len(s.rounds) > 0 {
			r = s.rounds[0]
		}
		s.mu.Unlock()
		return r
	}
	done := make(chan struct{})
	s.inFlight = done
	s.mu.Unlock()

	round := s.doTick(ctx)

	s.mu.Lock()
	s.rounds = append([]Round{round}, s.rounds...)
	if len(s.rounds) > s.maxRounds {
		s.rounds = s.rounds[:s.maxRounds]
	}
	s.inFlight = nil
	s.mu.Unlock()
	close(done)
	return round
}

// doTick 并发探测全部端点并汇总。
func (s *Service) doTick(ctx context.Context) Round {
	ts := s.now()
	results := make([]Result, len(s.probes))
	var wg sync.WaitGroup
	for i, p := range s.probes {
		wg.Add(1)
		go func(i int, p Spec) {
			defer wg.Done()
			results[i] = s.probeOne(ctx, p, ts)
		}(i, p)
	}
	wg.Wait()

	okCount := 0
	for _, r := range results {
		if r.OK {
			okCount++
		}
	}
	s.log.Info("地区探测完成", "ok", okCount, "total", len(results), "ts", ts)
	return Round{TS: ts, Results: results}
}

// probeOne 探测单个端点：GET，收到响应头即记 RTT，随即关 body；失败区分 timeout。
func (s *Service) probeOne(ctx context.Context, p Spec, ts int64) Result {
	started := s.now()
	callCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, p.URL, nil)
	if err != nil {
		return errorResult(p.Key, ts, err.Error())
	}
	resp, err := s.client.Do(req)
	if err != nil {
		if callCtx.Err() == context.DeadlineExceeded || ctx.Err() == context.DeadlineExceeded {
			return errorResult(p.Key, ts, "timeout")
		}
		return errorResult(p.Key, ts, err.Error())
	}
	rtt := s.now() - started
	_ = resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return Result{Key: p.Key, OK: true, RTTMs: &rtt, Error: nil, TS: ts}
	}
	msg := "HTTP " + itoa(resp.StatusCode)
	return Result{Key: p.Key, OK: false, RTTMs: &rtt, Error: &msg, TS: ts}
}

func errorResult(key string, ts int64, msg string) Result {
	return Result{Key: key, OK: false, RTTMs: nil, Error: &msg, TS: ts}
}

func toSpecs(probes []config.RegionProbe) []Spec {
	out := make([]Spec, len(probes))
	for i, p := range probes {
		out[i] = Spec{Key: p.Key, Name: p.Name, Flag: p.Flag, URL: p.URL}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
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
