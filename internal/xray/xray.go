// Package xray 是控制面访问数据面（xray-core）的唯一通道。
//
// 全部经官方 `xray api <sub>` 子命令完成 adu / rmu / statsquery，零 gRPC、零 reload、
// 不断存量连接。逐行复刻旧实现 services/xrayClient.ts 的语义：
//   - 统一带 `-s=<api 地址>`；每次调用带超时，失败按 200ms * 2^attempt 指数退避重试。
//   - adu 输入是「顶层含 inbounds 数组」的完整配置片段（写临时文件传参）；
//     **adu 命令行不接受 -tag**，inbound tag 写在片段的 inbounds[0].tag 里。
//   - adu 的退出码对业务失败不敏感：必须以 stdout 的 `Added N user(s)` 判定成功；
//     email 已存在时 stdout 报 `already exists` 且 Added 0，按幂等成功返回 false（无实际新增）。
//   - rmu 删不存在的用户返回 `Removed 0 user(s)`（exit 0），幂等无害。
package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"v2link-go/internal/vless"
)

// Runner 执行一次外部命令并返回 stdout；测试可注入假实现（stub 脚本或纯内存）。
type Runner func(ctx context.Context, name string, args ...string) (string, error)

// Options 构造 Client 的依赖；零值有安全默认（生产由 config 注入）。
type Options struct {
	// APIAddr 形如 127.0.0.1:8081，作为 `-s=` 传入。
	APIAddr string
	// Bin 是 xray 可执行文件（XRAY_BIN；验收用 stub 脚本）。
	Bin string
	// InboundTag 是 adu 片段里的 inbound tag（也是 rmu 的 -tag）。
	InboundTag string
	// Timeout 是单次调用的超时（含进程被杀）。
	Timeout time.Duration
	// Retries 是失败后的额外重试次数（不含首次）。
	Retries int
	// Runner 缺省用 os/exec 真跑；测试注入。
	Runner Runner
	// Logger 记重试等可观测事件；缺省丢弃。
	Logger *slog.Logger
}

// Client 是数据面热管理客户端。
type Client struct {
	apiAddr    string
	bin        string
	inboundTag string
	timeout    time.Duration
	retries    int
	runner     Runner
	log        *slog.Logger
}

// New 构造客户端。
func New(opts Options) *Client {
	c := &Client{
		apiAddr:    opts.APIAddr,
		bin:        opts.Bin,
		inboundTag: opts.InboundTag,
		timeout:    opts.Timeout,
		retries:    opts.Retries,
		runner:     opts.Runner,
		log:        opts.Logger,
	}
	if c.bin == "" {
		c.bin = "xray"
	}
	if c.inboundTag == "" {
		c.inboundTag = "vless-in"
	}
	if c.timeout <= 0 {
		c.timeout = 3 * time.Second
	}
	if c.runner == nil {
		c.runner = execRunner
	}
	if c.log == nil {
		c.log = slog.New(slog.NewTextHandler(discard{}, nil))
	}
	return c
}

// InboundTag 返回当前 inbound tag（clash 片段与 rmu 共用）。
func (c *Client) InboundTag() string { return c.inboundTag }

var (
	addedRe   = regexp.MustCompile(`Added\s+(\d+)\s+user`)
	removedRe = regexp.MustCompile(`Removed\s+(\d+)\s+user`)
)

// run 组装 `xray api <sub> -s=<addr> <args...>`，带超时与指数退避重试。
func (c *Client) run(ctx context.Context, sub string, args []string) (string, error) {
	argv := append([]string{"api", sub, "-s=" + c.apiAddr}, args...)
	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, c.timeout)
		out, err := c.runner(callCtx, c.bin, argv...)
		cancel()
		if err == nil {
			return out, nil
		}
		lastErr = err
		if attempt < c.retries {
			select {
			case <-ctx.Done():
				return "", fmt.Errorf("xray api %s 调用被取消: %w", sub, ctx.Err())
			case <-time.After(time.Duration(200*(1<<attempt)) * time.Millisecond):
			}
		}
	}
	return "", fmt.Errorf("xray api %s 调用失败: %w", sub, lastErr)
}

// AddUser 热加用户。返回 true 表示真正新增；false 表示已存在（幂等，无实际动作）。
//
// 成功判据是 stdout 的 `Added N user(s)`（exit code 不可靠）；`already exists` 按幂等成功；
// 其余 Added 0（如结构错误）仍视为失败返回 error。
func (c *Client) AddUser(ctx context.Context, spec vless.XrayUserSpec) (bool, error) {
	dir, err := writeTempFragment(spec, c.inboundTag)
	if err != nil {
		return false, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	out, err := c.run(ctx, "adu", []string{filepath.Join(dir, "user.json")})
	if err != nil {
		return false, err
	}
	added := 0
	if m := addedRe.FindStringSubmatch(out); m != nil {
		added, _ = strconv.Atoi(m[1])
	}
	if added >= 1 {
		return true, nil
	}
	if strings.Contains(out, "already exists") {
		return false, nil
	}
	return false, fmt.Errorf("adu 未新增用户(可能结构/上游问题): %s", truncate(out, 200))
}

// RemoveUser 热删用户（幂等：不存在返回 0 不报错）。返回实际删除数。
func (c *Client) RemoveUser(ctx context.Context, email string) (int64, error) {
	out, err := c.run(ctx, "rmu", []string{"-tag=" + c.inboundTag, email})
	if err != nil {
		return 0, err
	}
	if m := removedRe.FindStringSubmatch(out); m != nil {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		return n, nil
	}
	return 0, nil
}

// QueryTraffic 查询（累计/重置）流量，原样返回 stdout 交给上层解析。
// reset=true 时返回的是「重置前」的累计值，并与统计计数器清零。
func (c *Client) QueryTraffic(ctx context.Context, pattern string, reset bool) (string, error) {
	args := []string{"-pattern", pattern}
	if reset {
		args = append(args, "-reset")
	}
	return c.run(ctx, "statsquery", args)
}

// writeTempFragment 写 adu 片段到系统临时目录（不落 data/，不污染账本目录），返回目录供清理。
func writeTempFragment(spec vless.XrayUserSpec, inboundTag string) (string, error) {
	dir, err := os.MkdirTemp("", "v2link-adu-")
	if err != nil {
		return "", fmt.Errorf("创建 adu 临时目录失败: %w", err)
	}
	payload, err := json.Marshal(vless.BuildInboundFragment(spec, inboundTag))
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("序列化 adu 片段失败: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "user.json"), payload, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("写入 adu 片段失败: %w", err)
	}
	return dir, nil
}

// execRunner 是真跑的默认实现；命令失败时把 stderr 一并带进错误，便于排障。
func execRunner(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("%s", msg)
	}
	return stdout.String(), nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// discard 是 slog 的静默输出。
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
