package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

// TestRequestBodyParseError 锁定请求体解析失败的 500 文案（V8 / body-parser 原文）。
func TestRequestBodyParseError(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantCode int
		wantBody string
	}{
		{"对象缺属性名", "{", 500, `{"ok":false,"error":"Expected property name or '}' in JSON at position 1 (line 1 column 2)"}`},
		{"strict 数字", "123", 500, `{"ok":false,"error":"Unexpected token '1', \"123\" is not valid JSON"}`},
		{"尾随逗号", `{"a":1,}`, 500, `{"ok":false,"error":"Expected double-quoted property name in JSON at position 7 (line 1 column 8)"}`},
		{"非 BMP 字符", "😀", 500, `{"ok":false,"error":"Unexpected token '\ud83d', \"😀\" is not valid JSON"}`},
		{"合法 JSON 落到路由鉴权", `{"a":1}`, 401, bodyNoAuth},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(t, http.MethodPost, "/api/links", "application/json", tc.body, false)
			if rec.Code != tc.wantCode {
				t.Fatalf("状态码 = %d, want %d", rec.Code, tc.wantCode)
			}
			if got := rec.Body.String(); got != tc.wantBody {
				t.Fatalf("正文 = %q, want %q", got, tc.wantBody)
			}
		})
	}
}

// TestRequestBodyTooLarge 覆盖 64kb 上限：超限在解析前即拦下，文案与旧实现一致。
func TestRequestBodyTooLarge(t *testing.T) {
	big := `{"a":"` + strings.Repeat("x", 70*1024) + `"}`
	rec := doRequest(t, http.MethodPost, "/api/links", "application/json", big, false)
	if rec.Code != 500 {
		t.Fatalf("状态码 = %d, want 500", rec.Code)
	}
	if got, want := rec.Body.String(), `{"ok":false,"error":"request entity too large"}`; got != want {
		t.Fatalf("正文 = %q, want %q", got, want)
	}
}

// TestOptionsParsesBodyFirst 锁定解析顺序：OPTIONS 也会先过请求体解析（与 express.json 一致）。
func TestOptionsParsesBodyFirst(t *testing.T) {
	rec := doRequest(t, http.MethodOptions, "/api/healthz", "application/json", "{", false)
	if rec.Code != 500 {
		t.Fatalf("状态码 = %d, want 500", rec.Code)
	}
	if got, want := rec.Body.String(), `{"ok":false,"error":"Expected property name or '}' in JSON at position 1 (line 1 column 2)"}`; got != want {
		t.Fatalf("正文 = %q, want %q", got, want)
	}
}
