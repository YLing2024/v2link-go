package clash

import (
	"strings"
	"testing"
)

// TestParseMode 校验模式解析：仅 "global" 为全局，其余（含空、非法）一律回落 rule。
func TestParseMode(t *testing.T) {
	cases := map[string]Mode{"": ModeRule, "rule": ModeRule, "global": ModeGlobal, "GLOBAL": ModeRule, "bogus": ModeRule}
	for in, want := range cases {
		if got := ParseMode(in); got != want {
			t.Fatalf("ParseMode(%q)=%q，期望 %q", in, got, want)
		}
	}
}

// TestDedupeNodeNames 校验重名去重与「新名已被占用」的冲突处理。
func TestDedupeNodeNames(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"a", "b", "a"}, []string{"a", "b", "a#2"}},
		{[]string{"a", "a", "a"}, []string{"a", "a#2", "a#3"}},
		{[]string{"a", "a", "a#2"}, []string{"a", "a#2", "a#2#2"}},
		{[]string{"节点"}, []string{"节点"}},
	}
	for _, c := range cases {
		got := DedupeNodeNames(c.in)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Fatalf("DedupeNodeNames(%v)=%v，期望 %v", c.in, got, c.want)
		}
	}
}

// TestBuildRuleList 校验规则顺序及 directDomains 插入位置（在 RULE-SET,direct 之前）。
func TestBuildRuleList(t *testing.T) {
	rules := BuildRuleList("v2link", []string{"example.com"})
	want := []string{
		"DOMAIN-SUFFIX,cn,DIRECT",
		"IP-CIDR,127.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,172.16.0.0/12,DIRECT,no-resolve",
		"IP-CIDR,192.168.0.0/16,DIRECT,no-resolve",
		"IP-CIDR,100.64.0.0/10,DIRECT,no-resolve",
		"DOMAIN-SUFFIX,example.com,DIRECT",
		"RULE-SET,direct,DIRECT",
		"RULE-SET,cncidr,DIRECT",
		"RULE-SET,proxy,v2link",
		"MATCH,v2link",
	}
	if strings.Join(rules, "\n") != strings.Join(want, "\n") {
		t.Fatalf("规则列表不一致\n got=%v\nwant=%v", rules, want)
	}
}

// TestBuildConfigGoldenRule 用单节点锁定 rule 模式的完整 YAML 输出（逐字节）。
func TestBuildConfigGoldenRule(t *testing.T) {
	got := BuildConfig([]Node{{UUID: "u", Name: "n", Server: "v2.example.com"}}, BuildOptions{Mode: ModeRule})
	want := `proxies:
  - name: "n"
    type: vless
    server: "v2.example.com"
    port: 443
    uuid: "u"
    udp: true
    tls: true
    servername: "v2.example.com"
    network: ws
    ws-opts:
      path: "/v2ws"
      headers:
        Host: "v2.example.com"
proxy-groups:
  - name: "v2link"
    type: select
    proxies:
      - "n"
rule-providers:
  direct:
    type: http
    behavior: domain
    url: "https://cdn.jsdelivr.net/gh/Loyalsoldier/clash-rules@release/direct.txt"
    path: "./ruleset/direct.yaml"
    interval: 86400
    proxy: DIRECT
  cncidr:
    type: http
    behavior: ipcidr
    url: "https://cdn.jsdelivr.net/gh/Loyalsoldier/clash-rules@release/cncidr.txt"
    path: "./ruleset/cncidr.yaml"
    interval: 86400
    proxy: DIRECT
  proxy:
    type: http
    behavior: domain
    url: "https://cdn.jsdelivr.net/gh/Loyalsoldier/clash-rules@release/proxy.txt"
    path: "./ruleset/proxy.yaml"
    interval: 86400
    proxy: DIRECT
rules:
  - "DOMAIN-SUFFIX,cn,DIRECT"
  - "IP-CIDR,127.0.0.0/8,DIRECT,no-resolve"
  - "IP-CIDR,10.0.0.0/8,DIRECT,no-resolve"
  - "IP-CIDR,172.16.0.0/12,DIRECT,no-resolve"
  - "IP-CIDR,192.168.0.0/16,DIRECT,no-resolve"
  - "IP-CIDR,100.64.0.0/10,DIRECT,no-resolve"
  - "RULE-SET,direct,DIRECT"
  - "RULE-SET,cncidr,DIRECT"
  - "RULE-SET,proxy,v2link"
  - "MATCH,v2link"
`
	if got != want {
		t.Fatalf("rule 模式 YAML 不一致\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestBuildConfigGlobal 校验 global 模式：无 rule-providers，rules 仅 MATCH。
func TestBuildConfigGlobal(t *testing.T) {
	got := BuildConfig([]Node{{UUID: "u", Name: "n", Server: "v2.example.com", Sni: "sni.example.com", Port: 8443, Path: "/ws"}}, BuildOptions{Mode: ModeGlobal})
	if strings.Contains(got, "rule-providers:") {
		t.Fatalf("global 模式不应含 rule-providers:\n%s", got)
	}
	if !strings.Contains(got, "rules:\n  - \"MATCH,v2link\"\n") {
		t.Fatalf("global 模式 rules 应为单条 MATCH:\n%s", got)
	}
	if !strings.Contains(got, "servername: \"sni.example.com\"") || !strings.Contains(got, "port: 8443") {
		t.Fatalf("节点字段未按预期渲染:\n%s", got)
	}
}

// TestBuildConfigEmpty 校验空节点：proxies 与 proxies 列表均为 []，结构仍完整。
func TestBuildConfigEmpty(t *testing.T) {
	got := BuildConfig(nil, BuildOptions{})
	if !strings.Contains(got, "proxies: []\n") || !strings.Contains(got, "    proxies: []\n") {
		t.Fatalf("空节点输出异常:\n%s", got)
	}
}

// TestBuildConfigQuotesSpecialAlias 校验特殊字符别名走 JSON 双引号转义，不破坏 YAML 结构。
func TestBuildConfigQuotesSpecialAlias(t *testing.T) {
	got := BuildConfig([]Node{{UUID: "u", Name: `a": b\c`, Server: "v2.example.com"}}, BuildOptions{})
	if !strings.Contains(got, `  - name: "a\": b\\c"`) {
		t.Fatalf("特殊字符别名转义异常:\n%s", got)
	}
}
