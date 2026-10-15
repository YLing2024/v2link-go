// Package monitor 负责连接追溯与流量曲线：access log 增量解析入库、采样聚合。
package monitor

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ParsedLine 是解析出的单条 access log 记录。
//
// xray 26.3.x 的 access log 是纯文本，形如：
//
//	2026/09/04 22:53:17.938337 from 127.0.0.1:51706 accepted tcp:www.example.com:443 email: lk_test
//
// 只含连接建立事件、无字节数；仅 accepted 事件入库（避免重复计数）。
type ParsedLine struct {
	// TS 是文件内本地时间对应的 epoch 毫秒。
	TS int64
	// Event 取值 accepted/rejected/dropped。
	Event string
	// Host 是目标主机（域名或 IP）；解析失败为空串。
	Host string
	// Port 是目标端口；缺失或越界为 nil。
	Port *int64
	// Email 是 xray 用户标识；缺失为空串。
	Email string
}

var (
	headRe      = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}\.\d+) from \S+ (accepted|rejected|dropped)\b`)
	logDateRe   = regexp.MustCompile(`^(\d{4})/(\d{2})/(\d{2}) (\d{2}):(\d{2}):(\d{2})(?:\.(\d+))?$`)
	emailRe     = regexp.MustCompile(`email: (\S+)`)
	ipv6HostRe  = regexp.MustCompile(`\btcp:\[([^\]]+)\](?::(\d+))?`)
	plainHostRe = regexp.MustCompile(`\btcp:([A-Za-z0-9.-]+)(?::(\d+))?`)
)

// ParseLogDate 把 "2026/09/04 22:53:17.938337" 解析为 epoch 毫秒（按进程本地时区，与旧实现一致）。
func ParseLogDate(s string) (int64, bool) {
	m := logDateRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	year, _ := strconv.Atoi(m[1])
	month, _ := strconv.Atoi(m[2])
	day, _ := strconv.Atoi(m[3])
	hour, _ := strconv.Atoi(m[4])
	min, _ := strconv.Atoi(m[5])
	sec, _ := strconv.Atoi(m[6])
	frac := m[7]
	// 秒以下按前 3 位取毫秒（与 JS frac.padEnd(3,'0').slice(0,3) 语义一致）。
	for len(frac) < 3 {
		frac += "0"
	}
	ms, _ := strconv.Atoi(frac[:3])
	base := time.Date(year, time.Month(month), day, hour, min, sec, 0, time.Local)
	return base.UnixMilli() + int64(ms), true
}

// ParseAccessLine 解析单行；非 accepted / 缺目标或 email 时返回 false（不落库）。
func ParseAccessLine(line string) (ParsedLine, bool) {
	text := strings.TrimSpace(line)
	if text == "" {
		return ParsedLine{}, false
	}
	head := headRe.FindStringSubmatch(text)
	if head == nil {
		return ParsedLine{}, false
	}
	ts, ok := ParseLogDate(head[1])
	if !ok {
		return ParsedLine{}, false
	}
	if head[2] != "accepted" {
		return ParsedLine{}, false
	}
	emailM := emailRe.FindStringSubmatch(text)
	email := ""
	if emailM != nil {
		email = emailM[1]
	}
	if email == "" {
		return ParsedLine{}, false
	}
	host, port := extractTarget(text)
	return ParsedLine{TS: ts, Event: head[2], Host: host, Port: port, Email: email}, true
}

// extractTarget 从正文提取 `tcp:host:port`；IPv6 目标按 [::1]:443 形态解析。
func extractTarget(text string) (string, *int64) {
	if m := ipv6HostRe.FindStringSubmatch(text); m != nil {
		return m[1], parsePort(m[2])
	}
	m := plainHostRe.FindStringSubmatch(text)
	if m == nil {
		return "", nil
	}
	if m[1] == "" {
		return "", nil
	}
	return m[1], parsePort(m[2])
}

// parsePort 取值域校验：空 → nil；非数字或越界(1..65535) → nil。
func parsePort(raw string) *int64 {
	if raw == "" {
		return nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 1 || v > 65535 {
		return nil
	}
	return &v
}
