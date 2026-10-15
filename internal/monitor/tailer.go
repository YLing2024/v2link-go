package monitor

import (
	"container/list"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"syscall"
	"time"

	"v2link-go/internal/store"
)

// BatchSize 是攒批落库阈值（一批一事务），与旧实现 accessLogTailer 一致。
const BatchSize = 100

// maxReadBytes 是单轮读取上限，防止一次轮询吃掉整个大文件。
const maxReadBytes = 1 << 20

// PollResult 是单轮采集统计。
type PollResult struct {
	// Bytes 本轮读取字节数。
	Bytes int64
	// Parsed 本轮解析出的有效行数。
	Parsed int
	// Skipped 跳过行数（非 accepted / 无 email / 去重命中）。
	Skipped int
	// Flushed 本轮落库行数。
	Flushed int
}

// TailerOptions 构造 Tailer。
type TailerOptions struct {
	// Path 是 xray access log 路径。
	Path string
	// Interval 是轮询周期；<=0 表示不自动轮询（测试手动 Poll）。
	Interval time.Duration
	// ResolveLinkID 关联 email → links.id；返回 nil 表示库中无匹配（记 link_id=NULL）。
	ResolveLinkID func(email string) *string
	// Sink 攒批落库；返回错误时该批丢弃并记日志（防内存无限增长）。
	Sink func(rows []store.ConnectionInput) error
	// Logger 缺省丢弃。
	Logger *slog.Logger
}

// Tailer 增量读 access log、解析 accepted 事件、关联后攒批落库。
//
// 自愈设计（配合 logrotate copytruncate）：首轮只建基线（从文件尾开始）；同 inode 变小即
// 视为截断、offset 归零重读；inode 变化即视为轮转、重开新文件并以行指纹去重；
// 解析失败/无 email 的行跳过，不因脏行中断。
type Tailer struct {
	opts    TailerOptions
	offset  int64
	size    int64
	ino     uint64
	hasStat bool
	seen    *orderedSet
	partial string
	pending []store.ConnectionInput
	timer   *time.Ticker
	stopCh  chan struct{}
	running bool
}

// NewTailer 构造采集器。
func NewTailer(opts TailerOptions) *Tailer {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Tailer{opts: opts, seen: newOrderedSet(1024)}
}

// Start 建基线并起周期轮询（Interval<=0 时只建 running 标记，由测试手动 Poll）。
func (t *Tailer) Start() {
	if t.running {
		return
	}
	t.running = true
	if t.opts.Interval <= 0 {
		return
	}
	t.Poll()
	t.stopCh = make(chan struct{})
	t.timer = time.NewTicker(t.opts.Interval)
	go func() {
		for {
			select {
			case <-t.stopCh:
				return
			case <-t.timer.C:
				t.Poll()
			}
		}
	}()
}

// Stop 停止周期轮询（幂等）。
func (t *Tailer) Stop() {
	if !t.running {
		return
	}
	t.running = false
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	if t.stopCh != nil {
		close(t.stopCh)
		t.stopCh = nil
	}
}

// Poll 单轮增量读 + 攒批 + flush；任何错误都降级为日志，不向上抛（下轮重试）。
func (t *Tailer) Poll() PollResult {
	var res PollResult
	fi, err := os.Stat(t.opts.Path)
	if err != nil {
		t.opts.Logger.Warn("读取 access log 失败（下轮重试）", "path", t.opts.Path, "err", err)
		return res
	}
	curIno := inodeOf(fi)
	if !t.hasStat {
		// 首次：建基线（从文件尾开始），不灌历史。
		t.offset = fi.Size()
		t.size = fi.Size()
		t.ino = curIno
		t.hasStat = true
		return res
	}
	inodeChanged := curIno != t.ino
	truncated := !inodeChanged && fi.Size() < t.size
	if inodeChanged {
		t.opts.Logger.Info("检测到日志轮转（inode 变化），重开新文件", "ino", curIno)
		t.offset = 0
		t.partial = ""
	} else if truncated {
		t.opts.Logger.Info("检测到日志截断（copytruncate），offset 归零重读")
		t.offset = 0
		t.partial = ""
	}
	if fi.Size() > t.offset {
		want := fi.Size() - t.offset
		if want > maxReadBytes {
			want = maxReadBytes
		}
		chunk, n, err := readAt(t.opts.Path, t.offset, want)
		if err != nil {
			t.opts.Logger.Warn("读取 access log 内容失败（下轮重试）", "path", t.opts.Path, "err", err)
			return res
		}
		t.offset += n
		res.Bytes = n
		t.consumeChunk(chunk, &res)
	}
	t.size = fi.Size()
	t.ino = curIno
	res.Flushed = t.flush()
	return res
}

// consumeChunk 按行切分并入库；末段无换行符结尾视为半行留待下轮拼接。
func (t *Tailer) consumeChunk(chunk string, res *PollResult) {
	text := t.partial + chunk
	lines := strings.Split(text, "\n")
	t.partial = lines[len(lines)-1]
	for _, raw := range lines[:len(lines)-1] {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		parsed, ok := ParseAccessLine(line)
		if !ok {
			res.Skipped++
			continue
		}
		row := store.ConnectionInput{
			LinkID: t.opts.ResolveLinkID(parsed.Email),
			Email:  parsed.Email,
			TS:     parsed.TS,
			Host:   emptyToNil(parsed.Host),
			Port:   parsed.Port,
		}
		if t.isDuplicate(row) {
			res.Skipped++
			continue
		}
		t.pending = append(t.pending, row)
		res.Parsed++
		if len(t.pending) >= BatchSize {
			res.Flushed += t.flush()
		}
	}
}

// flush 把积压缓冲落库；失败时丢弃该批并记录指纹，防内存无限增长。
func (t *Tailer) flush() int {
	if len(t.pending) == 0 {
		return 0
	}
	batch := t.pending
	t.pending = nil
	if err := t.opts.Sink(batch); err != nil {
		t.opts.Logger.Error("connections 落库失败（丢弃本批，下次重试）", "err", err, "rows", len(batch))
	}
	for _, r := range batch {
		t.seen.add(fingerprint(r))
	}
	return len(batch)
}

func (t *Tailer) isDuplicate(r store.ConnectionInput) bool {
	return t.seen.has(fingerprint(r))
}

// fingerprint 是行指纹（email|host|port|ts）；同一毫秒的不同连接因 host/email 不同不会误删。
func fingerprint(r store.ConnectionInput) string {
	host := ""
	if r.Host != nil {
		host = *r.Host
	}
	port := ""
	if r.Port != nil {
		port = fmt.Sprintf("%d", *r.Port)
	}
	return fmt.Sprintf("%s|%s|%s|%d", r.Email, host, port, r.TS)
}

func emptyToNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// readAt 从 offset 读取至多 want 字节；返回内容与实读字节数。
func readAt(path string, offset, want int64) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	buf := make([]byte, want)
	n, err := f.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return "", 0, err
	}
	return string(buf[:n]), int64(n), nil
}

func inodeOf(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}

// orderedSet 是带上限的「最近使用」集合：重复 add 会把键移到队尾，超上限丢队首（复刻 JS Map 语义）。
type orderedSet struct {
	cap   int
	ll    *list.List
	index map[string]*list.Element
}

func newOrderedSet(capacity int) *orderedSet {
	return &orderedSet{cap: capacity, ll: list.New(), index: make(map[string]*list.Element)}
}

func (s *orderedSet) add(key string) {
	if el, ok := s.index[key]; ok {
		s.ll.MoveToBack(el)
		return
	}
	s.index[key] = s.ll.PushBack(key)
	if s.ll.Len() > s.cap {
		front := s.ll.Front()
		if front != nil {
			s.ll.Remove(front)
			delete(s.index, front.Value.(string))
		}
	}
}

func (s *orderedSet) has(key string) bool {
	_, ok := s.index[key]
	return ok
}
