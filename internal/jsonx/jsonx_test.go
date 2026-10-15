package jsonx

import "testing"

func TestQuote(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"普通串", "hello", `"hello"`},
		{"双引号与反斜杠", `a"b\c`, `"a\"b\\c"`},
		{"控制字符", "a\tb\nc", `"a\tb\nc"`},
		{"其它控制字符按 u00XX", "a\x01b", `"a\u0001b"`},
		{"不转义尖括号与和号", "<a>&b", `"<a>&b"`},
		{"中文原样", "未登录", `"未登录"`},
		{"BMP 字符原样", "é", `"é"`},
		{"补充平面字符原样", "😀", `"😀"`},
		// 孤立代理项（UTF-16 高位）在 WTF-8 里编成 3 字节，JSON.stringify 会转义成 \uXXXX。
		{"孤立高位代理项转义", "\xed\xa0\xbd", `"\ud83d"`},
		{"孤立低位代理项转义", "\xed\xb0\x80", `"\udc00"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Quote(tc.in); got != tc.want {
				t.Fatalf("Quote(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestQuoteBodyErrorSurrogate 锁定「报错文案经 JSON 序列化」的最终字节形态：
// V8 的 token 只含高位代理项，序列化后必须是 \ud83d（与旧实现逐字节一致）。
func TestQuoteBodyErrorSurrogate(t *testing.T) {
	msg, isErr := BodyJSONError("😀")
	if !isErr {
		t.Fatal("BodyJSONError(\"😀\") 应为解析错误")
	}
	const want = `"Unexpected token '\ud83d', \"😀\" is not valid JSON"`
	if got := Quote(msg); got != want {
		t.Fatalf("Quote(msg) = %q, want %q", got, want)
	}
}
