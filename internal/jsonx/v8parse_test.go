package jsonx

import "testing"

// 期望值全部取自旧实现（Node 24 + Express 4 + body-parser）的原始 500 error 文案，
// 由 /root/.hermes/cache/scratch/v2link-opt 的对照脚本采集，不许手改猜测。

func TestBodyJSONError(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		// 空体是 body-parser 的特例：直接当 {} 处理，不算错误。
		{"空体当空对象", "", "", false},
		{"合法对象", `{"a":1}`, "", false},
		{"合法对象带空白", "  {\n\"a\":1}\t", "", false},
		{"合法数组（strict 接受）", `[1,2,3]`, "", false},
		{"合法嵌套", `{"a":[{"b":null}]}`, "", false},

		// strict 首字符校验：非 { / [ 一律 createStrictSyntaxError。
		{"纯空白", "   ", "Unexpected end of JSON input", true},
		{"纯换行", "\r\n", "Unexpected end of JSON input", true},
		{"strict 数字", `123`, "Unexpected token '1', \"123\" is not valid JSON", true},
		{"strict 前导零", `01`, "Unexpected token '0', \"01\" is not valid JSON", true},
		{"strict 字符串", `"hi"`, "Unexpected token '\"', \"\"hi\"\" is not valid JSON", true},
		{"strict true", `true`, "Unexpected token 't', \"true\" is not valid JSON", true},
		{"strict false", `false`, "Unexpected token 'f', \"false\" is not valid JSON", true},
		{"strict null", `null`, "Unexpected token 'n', \"null\" is not valid JSON", true},
		{"strict 截断字面", `nul`, "Unexpected token 'n', \"nul\" is not valid JSON", true},
		{"strict NaN", `NaN`, "Unexpected token 'N', \"NaN\" is not valid JSON", true},
		{"strict Infinity", `Infinity`, "Unexpected token 'I', \"Infinity\" is not valid JSON", true},
		{"strict undefined", `undefined`, "Unexpected token 'u', \"undefined\" is not valid JSON", true},
		{"strict 非 ASCII", "ééé", "Unexpected token 'é', \"ééé\" is not valid JSON", true},
		{"strict 加号", `+1`, "Unexpected token '+', \"+1\" is not valid JSON", true},
		{"strict 前导点", `.5`, "Unexpected token '.', \".5\" is not valid JSON", true},
		{"strict 坏转义", `"a\x"`, "Unexpected token '\"', \"\"a\\x\"\" is not valid JSON", true},
		{"strict 坏 Unicode", `"a\u12`, "Unexpected token '\"', \"\"a\\u12\" is not valid JSON", true},
		{"strict 指数缺数字", `1e+`, "Unexpected token '1', \"1e+\" is not valid JSON", true},
		{"strict 小数缺数字", `1.`, "Unexpected token '1', \"1.\" is not valid JSON", true},
		{"strict 负号后无数", `-x`, "Unexpected token '-', \"-x\" is not valid JSON", true},
		// 非 BMP 首字符：V8 的 token 只取高位代理项，经 JSON.stringify 转义成 \ud83d。
		{"strict 非 BMP", "😀x", "Unexpected token '\xed\xa0\xbd', \"😀x\" is not valid JSON", true},
		{"strict 非 BMP 字符串", `"😀"x`, "Unexpected token '\"', \"\"😀\"x\" is not valid JSON", true},

		// strict 通过（首字符 { / [）后走 JSON.parse 原文。
		{"对象缺属性名", "{", "Expected property name or '}' in JSON at position 1 (line 1 column 2)", true},
		{"数字键不是字符串", `{1:2}`, "Expected property name or '}' in JSON at position 1 (line 1 column 2)", true},
		{"单引号键", `{'a':1}`, "Expected property name or '}' in JSON at position 1 (line 1 column 2)", true},
		{"属性值为空", `{"a":}`, "Unexpected token '}', \"{\"a\":}\" is not valid JSON", true},
		{"尾随逗号", `{"a":1,}`, "Expected double-quoted property name in JSON at position 7 (line 1 column 8)", true},
		{"缺冒号", `{"a" 1}`, "Expected ':' after property name in JSON at position 5 (line 1 column 6)", true},
		{"对象截断", `{"a":1`, "Expected ',' or '}' after property value in JSON at position 6 (line 1 column 7)", true},
		{"数组截断", `[1,2`, "Expected ',' or ']' after array element in JSON at position 4 (line 1 column 5)", true},
		{"尾随垃圾", `{"a":1}x`, "Unexpected non-whitespace character after JSON at position 7 (line 1 column 8)", true},
		{"尾随空白后垃圾", "{\"a\":1}   x", "Unexpected non-whitespace character after JSON at position 10 (line 1 column 11)", true},
		{"字符串内控制字符", "{\"a\":\"\u0001\"}", "Bad control character in string literal in JSON at position 6 (line 1 column 7)", true},
		{"数字后导零", `{"a":01}`, "Unexpected number in JSON at position 6 (line 1 column 7)", true},
		{"特殊串 object", `[object Object]`, `"[object Object]" is not valid JSON`, true},

		// 位置按 UTF-16 码元计（非 BMP 字符占 2）。
		{"多行位置", "{\n\"a\":1,\n}", "Expected double-quoted property name in JSON at position 9 (line 3 column 1)", true},
		{"跨行数组", "[1,2,\n3", "Expected ',' or ']' after array element in JSON at position 7 (line 2 column 2)", true},
		{"CRLF 位置", "{\r\n\"a\":}", "Unexpected token '}', \"{\r\n\"a\":}\" is not valid JSON", true},
		{"非 BMP 位置", "{\"😀\":1,}", "Expected double-quoted property name in JSON at position 8 (line 1 column 9)", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, isErr := BodyJSONError(tc.in)
			if isErr != tc.wantErr || got != tc.want {
				t.Fatalf("BodyJSONError(%q) = (%q, %v), want (%q, %v)", tc.in, got, isErr, tc.want, tc.wantErr)
			}
		})
	}
}

func TestV8ParseError(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"对象缺属性名", "{", "Expected property name or '}' in JSON at position 1 (line 1 column 2)"},
		{"属性值为空", `{"a":}`, "Unexpected token '}', \"{\"a\":}\" is not valid JSON"},
		{"数组缺元素", `[1,]`, "Unexpected token ']', \"[1,]\" is not valid JSON"},
		{"到结尾", `[`, "Unexpected end of JSON input"},
		{"空串", ``, "Unexpected end of JSON input"},
		{"坏转义", `"a\x"`, "Bad escaped character in JSON at position 3 (line 1 column 4)"},
		{"数字后导零", `01`, "Unexpected number in JSON at position 1 (line 1 column 2)"},
		{"长串省略号", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", `Unexpected token 'x', "xxxxxxxxxx"... is not valid JSON`},
		{"居中省略号", "abcdefghijklmnopqrstuvwxyz0123456789", `Unexpected token 'a', "abcdefghij"... is not valid JSON`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := V8ParseError(tc.in); got != tc.want {
				t.Fatalf("V8ParseError(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestV8ParseErrorAcceptsValid(t *testing.T) {
	valid := []string{
		`{}`, `[]`, `{"a":1}`, `[1,2,3]`, `"x"`, `123`, `-1.5e+3`,
		`true`, `false`, `null`, `{"a":[{"b":null}],"c":"\u0041"}`,
		"{\n\t\"a\": 1\n}", `"\uD800"`, `{"é":"😀"}`,
	}
	for _, in := range valid {
		if got := V8ParseError(in); got != "" {
			t.Fatalf("V8ParseError(%q) = %q, want empty", in, got)
		}
	}
}
