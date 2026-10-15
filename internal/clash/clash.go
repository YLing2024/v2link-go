// Package clash 生成 Clash / mihomo 订阅配置（纯函数）与订阅编排服务。
//
// 输出形态：手写 YAML，字符串标量一律走 jsonx.Quote 产出的双引号标量（JSON 双引号转义是
// YAML 双引号转义的子集），别名里的 : # " \ 、中文、emoji 都不会破坏结构。
// 结构与旧实现 lib/clash.ts 逐字节一致，客户端解析不能变。
package clash

import (
	"strings"

	"v2link-go/internal/jsonx"
)

// GroupName 是订阅代理组名（固定）；rules 末尾 MATCH 到它。
const GroupName = "v2link"

// DefaultPort 是对外端口（nginx 终结 TLS 后回源）。
const DefaultPort = 443

// DefaultPath 是默认 WS 路径（与控制面 / 数据面一致）。
const DefaultPath = "/v2ws"

// RuleProviderURLPrefix 是规则集 CDN 前缀（jsdelivr；禁 raw.githubusercontent.com）。
const RuleProviderURLPrefix = "https://cdn.jsdelivr.net/gh/Loyalsoldier/clash-rules@release/"

// RuleProviderIntervalS 是规则集刷新间隔（秒）。
const RuleProviderIntervalS = 86400

// Mode 是生成模式：rule = 规则分流（默认）；global = 全部走代理（旧行为）。
type Mode string

const (
	// ModeRule 规则分流。
	ModeRule Mode = "rule"
	// ModeGlobal 全局代理。
	ModeGlobal Mode = "global"
)

// ParseMode 把查询参数转成模式；非法/缺省一律回落 rule。
func ParseMode(raw string) Mode {
	if raw == "global" {
		return ModeGlobal
	}
	return ModeRule
}

// RuleProvider 是一条规则集定义。
type RuleProvider struct {
	// Name 是 provider 名，也是 rules 里 RULE-SET 的引用名。
	Name string
	// Behavior 是规则集类型（domain / ipcidr）。
	Behavior string
	// URL 是规则集下载地址。
	URL string
	// Path 是本地缓存相对路径。
	Path string
	// Proxy 是拉取规则集时的出口（固定 DIRECT，否则拉不下来的失败是静默的）。
	Proxy string
}

// RuleProviders 只引 3 个集合：大陆站点直连、大陆 IP 直连、GFW 列表走代理。
var RuleProviders = []RuleProvider{
	{Name: "direct", Behavior: "domain", URL: RuleProviderURLPrefix + "direct.txt", Path: "./ruleset/direct.yaml", Proxy: "DIRECT"},
	{Name: "cncidr", Behavior: "ipcidr", URL: RuleProviderURLPrefix + "cncidr.txt", Path: "./ruleset/cncidr.yaml", Proxy: "DIRECT"},
	{Name: "proxy", Behavior: "domain", URL: RuleProviderURLPrefix + "proxy.txt", Path: "./ruleset/proxy.yaml", Proxy: "DIRECT"},
}

// inlineDirectRules 是不依赖远程集合也能正确分流的兜底规则（顺序是硬要求）。
var inlineDirectRules = []string{
	"DOMAIN-SUFFIX,cn,DIRECT",
	"IP-CIDR,127.0.0.0/8,DIRECT,no-resolve",
	"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
	"IP-CIDR,172.16.0.0/12,DIRECT,no-resolve",
	"IP-CIDR,192.168.0.0/16,DIRECT,no-resolve",
	"IP-CIDR,100.64.0.0/10,DIRECT,no-resolve",
}

// Node 是订阅里的一个节点。
type Node struct {
	// UUID 是 xray 用户 uuid。
	UUID string
	// Name 是原始节点名（通常来自别名回退链）；BuildConfig 负责去重。
	Name string
	// Server 是连接地址（域名或 IP）。
	Server string
	// Sni 是 TLS SNI / WS Host；空串回退 Server。
	Sni string
	// Port 缺省 443。
	Port int
	// Path 缺省 /v2ws。
	Path string
}

// BuildOptions 控制生成。
type BuildOptions struct {
	// Mode 缺省/非法 = rule。
	Mode Mode
	// DirectDomains 是额外直连域名后缀（CLASH_DIRECT_DOMAINS；默认空）。
	DirectDomains []string
}

// BuildRuleList 生成有序规则（从上到下匹配即停）；directDomains 插入到 RULE-SET,direct 之前。
func BuildRuleList(groupName string, directDomains []string) []string {
	rules := make([]string, 0, len(inlineDirectRules)+len(directDomains)+4)
	rules = append(rules, inlineDirectRules...)
	for _, domain := range directDomains {
		rules = append(rules, "DOMAIN-SUFFIX,"+domain+",DIRECT")
	}
	rules = append(rules, "RULE-SET,direct,DIRECT")
	rules = append(rules, "RULE-SET,cncidr,DIRECT")
	rules = append(rules, "RULE-SET,proxy,"+groupName)
	rules = append(rules, "MATCH,"+groupName)
	return rules
}

// DedupeNodeNames 去重节点名：重名依次追加 #2、#3…，保证全局唯一。
//
// 同时处理「新名恰好已被占用」的情况（如 a / a / a#2 → a / a#2 / a#2#2 不冲突）。
func DedupeNodeNames(names []string) []string {
	used := make(map[string]bool)
	nextSuffix := make(map[string]int)
	out := make([]string, 0, len(names))
	for _, raw := range names {
		candidate := raw
		if used[candidate] {
			n := nextSuffix[raw]
			if n == 0 {
				n = 1
			}
			for {
				n++
				candidate = raw + "#" + itoa(n)
				if !used[candidate] {
					break
				}
			}
			nextSuffix[raw] = n
		} else {
			nextSuffix[raw] = 1
		}
		used[candidate] = true
		out = append(out, candidate)
	}
	return out
}

// BuildConfig 生成 Clash / mihomo 配置 YAML 文本（末尾带换行）。
func BuildConfig(nodes []Node, opts BuildOptions) string {
	mode := opts.Mode
	if mode != ModeGlobal {
		mode = ModeRule
	}
	names := DedupeNodeNames(nodeNames(nodes))
	lines := make([]string, 0, 32+len(nodes)*13)

	if len(nodes) == 0 {
		lines = append(lines, "proxies: []")
	} else {
		lines = append(lines, "proxies:")
		for i, node := range nodes {
			name := node.Name
			if i < len(names) {
				name = names[i]
			}
			server := node.Server
			sni := strings.TrimSpace(node.Sni)
			if sni == "" {
				sni = server
			}
			port := node.Port
			if port == 0 {
				port = DefaultPort
			}
			path := node.Path
			if path == "" {
				path = DefaultPath
			}
			lines = append(lines,
				"  - name: "+jsonx.Quote(name),
				"    type: vless",
				"    server: "+jsonx.Quote(server),
				"    port: "+itoa(port),
				"    uuid: "+jsonx.Quote(node.UUID),
				"    udp: true",
				"    tls: true",
				"    servername: "+jsonx.Quote(sni),
				"    network: ws",
				"    ws-opts:",
				"      path: "+jsonx.Quote(path),
				"      headers:",
				"        Host: "+jsonx.Quote(sni),
			)
		}
	}

	lines = append(lines, "proxy-groups:")
	lines = append(lines, "  - name: "+jsonx.Quote(GroupName))
	lines = append(lines, "    type: select")
	if len(names) == 0 {
		lines = append(lines, "    proxies: []")
	} else {
		lines = append(lines, "    proxies:")
		for _, name := range names {
			lines = append(lines, "      - "+jsonx.Quote(name))
		}
	}

	if mode == ModeRule {
		lines = append(lines, "rule-providers:")
		for _, p := range RuleProviders {
			lines = append(lines,
				"  "+p.Name+":",
				"    type: http",
				"    behavior: "+p.Behavior,
				"    url: "+jsonx.Quote(p.URL),
				"    path: "+jsonx.Quote(p.Path),
				"    interval: "+itoa(RuleProviderIntervalS),
				"    proxy: "+p.Proxy,
			)
		}
	}

	lines = append(lines, "rules:")
	var rules []string
	if mode == ModeGlobal {
		rules = []string{"MATCH," + GroupName}
	} else {
		rules = BuildRuleList(GroupName, opts.DirectDomains)
	}
	for _, rule := range rules {
		lines = append(lines, "  - "+jsonx.Quote(rule))
	}

	return strings.Join(lines, "\n") + "\n"
}

func nodeNames(nodes []Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.Name
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
