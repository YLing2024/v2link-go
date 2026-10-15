// Package vless 构造 vless:// URI 与 xray adu 所需的 inbound 配置片段。
//
// 字段顺序、参数与 #fragment 生成规则逐字节对齐旧实现 lib/vless.ts：二维码/复制/安卓/桌面
// 客户端都在用，格式不能变。
package vless

import (
	"strings"

	"v2link-go/internal/expiry"
)

// LinkParams 是构造 vless URI 的入参（与旧实现 VlessLinkParams 对应）。
type LinkParams struct {
	Host     string
	Sni      string
	Port     int
	Path     string
	Security string
	UUID     string
	Alias    string
}

// BuildURI 按旧实现顺序拼装 vless:// URI：
// encryption=none&type=ws[&security=tls]&path=<form-urlencoded>[&host=..][&sni=..]#<encodeURIComponent(alias)>。
func BuildURI(p LinkParams) string {
	security := p.Security
	if security == "" {
		security = "tls"
	}
	type kv struct{ k, v string }
	params := []kv{{"encryption", "none"}, {"type", "ws"}}
	if security != "none" {
		params = append(params, kv{"security", security})
	}
	wsPath := p.Path
	if !strings.HasPrefix(wsPath, "/") {
		wsPath = "/" + wsPath
	}
	params = append(params, kv{"path", wsPath})
	wsHost := strings.TrimSpace(p.Sni)
	if wsHost == "" {
		wsHost = strings.TrimSpace(p.Host)
	}
	params = append(params, kv{"host", wsHost})
	if wsHost != p.Host {
		params = append(params, kv{"sni", wsHost})
	}
	port := p.Port
	if port == 0 {
		port = 443
	}
	var qs strings.Builder
	for i, item := range params {
		if i > 0 {
			qs.WriteByte('&')
		}
		qs.WriteString(formURLEncode(item.k))
		qs.WriteByte('=')
		qs.WriteString(formURLEncode(item.v))
	}
	uri := "vless://" + p.UUID + "@" + p.Host + ":" + itoa(port) + "?" + qs.String()
	if alias := strings.TrimSpace(p.Alias); alias != "" {
		uri += "#" + encodeURIComponent(alias)
	}
	return uri
}

// DisplayAlias 别名回退链：alias（客户端名）→ note（备注）→ id。
func DisplayAlias(alias, note, id string) string {
	for _, v := range []string{alias, note, id} {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

// XrayUserSpec 是 adu 片段里的单个用户。
type XrayUserSpec struct {
	Email string
	UUID  string
}

// inboundFragment 是 adu 输入片段（顶层含 inbounds 数组；adu 命令行不接受 -tag）。
type inboundFragment struct {
	Inbounds []inbound `json:"inbounds"`
}

type inbound struct {
	Tag      string   `json:"tag"`
	Port     int      `json:"port"`
	Protocol string   `json:"protocol"`
	Settings settings `json:"settings"`
}

type settings struct {
	Clients    []client `json:"clients"`
	Decryption string   `json:"decryption"`
}

type client struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Level int    `json:"level"`
}

// BuildInboundFragment 构造 adu 片段：port 固定 7895（xray 配置解析所需，非私有地址），
// 不限速（xray VLESS 无 per-user 限速原生字段）。
func BuildInboundFragment(spec XrayUserSpec, inboundTag string) inboundFragment {
	return inboundFragment{Inbounds: []inbound{{
		Tag:      inboundTag,
		Port:     7895,
		Protocol: "vless",
		Settings: settings{
			Clients:    []client{{ID: spec.UUID, Email: spec.Email, Level: 0}},
			Decryption: "none",
		},
	}}}
}

// PublicHost/PublicSni/PublicPath 由配置注入，这里只做纯函数。
var (
	// PublicHostDefault 缺省公开地址（占位，绝不硬编码真实域名）。
	PublicHostDefault = "v2.example.com"
	// PublicPathDefault 缺省 WS 路径。
	PublicPathDefault = "/v2ws"
)

// IsPermanent 便于上层复用到期哨兵判定。
func IsPermanent(expiresAt int64) bool { return expiry.IsPermanent(expiresAt) }

// formURLEncode 复刻 JS URLSearchParams 的 application/x-www-form-urlencoded 序列化：
// 字母数字与 *-._ 原样，空格转 '+'，其余按 UTF-8 百分号编码（大写十六进制）。
func formURLEncode(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '*', c == '-', c == '.', c == '_':
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
		}
	}
	return b.String()
}

// encodeURIComponent 复刻 JS encodeURIComponent：保留 A-Za-z0-9 与 -_.!~*'()，其余百分号编码。
func encodeURIComponent(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '!', c == '~', c == '*', c == '\'', c == '(', c == ')':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
		}
	}
	return b.String()
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
