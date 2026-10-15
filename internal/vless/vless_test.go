package vless

import (
	"encoding/json"
	"testing"
)

// TestBuildURI 覆盖字段顺序、可选参数与 #fragment 规则的边界，锁死与旧实现逐字节一致的输出。
func TestBuildURI(t *testing.T) {
	cases := []struct {
		name string
		in   LinkParams
		want string
	}{
		{
			name: "缺省 TLS + 端口 0 回落 443 + 中文别名",
			in:   LinkParams{Host: "v2.example.com", Path: "/v2ws", UUID: "11111111-2222-3333-4444-555555555555", Alias: "节点一"},
			want: "vless://11111111-2222-3333-4444-555555555555@v2.example.com:443?encryption=none&type=ws&security=tls&path=%2Fv2ws&host=v2.example.com#%E8%8A%82%E7%82%B9%E4%B8%80",
		},
		{
			name: "显式端口 + security none（省略 security 参数）",
			in:   LinkParams{Host: "v2.example.com", Port: 8443, Path: "/ws", Security: "none", UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
			want: "vless://aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee@v2.example.com:8443?encryption=none&type=ws&path=%2Fws&host=v2.example.com",
		},
		{
			name: "sni 与 host 不同时追加 sni 且 host 取 sni",
			in:   LinkParams{Host: "v2.example.com", Port: 443, Path: "v2ws", Sni: "sni.example.com", UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Alias: "x"},
			want: "vless://aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee@v2.example.com:443?encryption=none&type=ws&security=tls&path=%2Fv2ws&host=sni.example.com&sni=sni.example.com#x",
		},
		{
			name: "别名两侧空白被裁剪，空别名省略 fragment",
			in:   LinkParams{Host: "v2.example.com", Path: "/v2ws", UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Alias: "   "},
			want: "vless://aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee@v2.example.com:443?encryption=none&type=ws&security=tls&path=%2Fv2ws&host=v2.example.com",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := BuildURI(c.in); got != c.want {
				t.Fatalf("BuildURI 不一致\n got=%s\nwant=%s", got, c.want)
			}
		})
	}
}

// TestDisplayAlias 校验别名回退链 alias → note → id。
func TestDisplayAlias(t *testing.T) {
	cases := []struct {
		alias, note, id, want string
	}{
		{" 客户端名 ", "备注", "lk_1", "客户端名"},
		{"", " 备注 ", "lk_1", "备注"},
		{"", "", "lk_1", "lk_1"},
		{"", "   ", "", ""},
	}
	for _, c := range cases {
		if got := DisplayAlias(c.alias, c.note, c.id); got != c.want {
			t.Fatalf("DisplayAlias(%q,%q,%q)=%q，期望 %q", c.alias, c.note, c.id, got, c.want)
		}
	}
}

// TestFormURLEncode 校验 application/x-www-form-urlencoded：保留 *-._、空格转 '+'、其余大写百分号。
func TestFormURLEncode(t *testing.T) {
	cases := map[string]string{
		"/v2ws":        "%2Fv2ws",
		"a b":          "a+b",
		"a*b-c._d":     "a*b-c._d",
		"中文":          "%E4%B8%AD%E6%96%87",
		"a=b&c":        "a%3Db%26c",
	}
	for in, want := range cases {
		if got := formURLEncode(in); got != want {
			t.Fatalf("formURLEncode(%q)=%q，期望 %q", in, got, want)
		}
	}
}

// TestEncodeURIComponent 校验 encodeURIComponent 的保留字符集（-_.!~*'() 原样）。
func TestEncodeURIComponent(t *testing.T) {
	cases := map[string]string{
		"a-b_c.d!e~f*g'h(i)": "a-b_c.d!e~f*g'h(i)",
		"a/b?c#d":            "a%2Fb%3Fc%23d",
		"中 文":                "%E4%B8%AD%20%E6%96%87",
	}
	for in, want := range cases {
		if got := EncodeURIComponent(in); got != want {
			t.Fatalf("EncodeURIComponent(%q)=%q，期望 %q", in, got, want)
		}
	}
}

// TestBuildInboundFragment 校验 adu 片段的结构与字段名（adu 命令行不接受 -tag）。
func TestBuildInboundFragment(t *testing.T) {
	frag := BuildInboundFragment(XrayUserSpec{Email: "lk_demo", UUID: "11111111-2222-3333-4444-555555555555"}, "vless-in")
	raw, err := json.Marshal(frag)
	if err != nil {
		t.Fatalf("序列化片段失败: %v", err)
	}
	want := `{"inbounds":[{"tag":"vless-in","port":7895,"protocol":"vless","settings":{"clients":[{"id":"11111111-2222-3333-4444-555555555555","email":"lk_demo","level":0}],"decryption":"none"}}]}`
	if string(raw) != want {
		t.Fatalf("片段 JSON 不一致\n got=%s\nwant=%s", raw, want)
	}
}
