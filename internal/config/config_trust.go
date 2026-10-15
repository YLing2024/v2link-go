package config

import (
	"strconv"
	"strings"
)

// TrustKind 反代信任范围类别（对齐 Express trust proxy 的常用取值）。
type TrustKind int

const (
	// TrustNone 不信任任何转发头（TRUST_PROXY=false/0/off）。
	TrustNone TrustKind = iota
	// TrustAll 信任全部转发头（仅在服务不直接对外时使用）。
	TrustAll
	// TrustLoopback 只信本机反代（默认）。
	TrustLoopback
	// TrustHops 信任最近 N 跳的转发链。
	TrustHops
)

// TrustProxy 反代信任范围配置。
type TrustProxy struct {
	Kind TrustKind
	Hops int
}

// ParseTrustProxy 解析 TRUST_PROXY：false/0/off → TrustNone；true/1/on → TrustAll；
// 纯数字 → TrustHops；其余（含空、loopback）→ TrustLoopback。
func ParseTrustProxy(raw string) TrustProxy {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "false", "0", "off":
		return TrustProxy{Kind: TrustNone}
	case "true", "1", "on":
		return TrustProxy{Kind: TrustAll}
	}
	if v := strings.TrimSpace(raw); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return TrustProxy{Kind: TrustHops, Hops: n}
		}
	}
	return TrustProxy{Kind: TrustLoopback}
}
