// Package jsonx 复刻 JS `JSON.stringify` 的转义与排版，用于需要与旧实现逐字节一致的场景
// （审计 detail 文本、Clash YAML 标量、订阅 token 相关片段）。
//
// 与 Go encoding/json 的差别：不转义 `<` `>` `&`，也不转义 U+2028/U+2029；控制字符按
// `\u00XX` 小写十六进制输出，与 JSON.stringify 一致。
package jsonx

import (
	"strconv"
	"strings"
)

// Field 是一个预先编码好的 JSON 对象字段（Raw 必须是合法 JSON 片段）。
type Field struct {
	Key string
	Raw string
}

// Quote 把字符串编码为 JSON 字符串字面量（含首尾双引号），语义同 JSON.stringify。
func Quote(s string) string {
	const hex = "0123456789abcdef"
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				b.WriteString(`\u00`)
				b.WriteByte(hex[c>>4])
				b.WriteByte(hex[c&0x0f])
				continue
			}
			// WTF-8 编码的孤立代理项（U+D800..U+DFFF，见 jsonx 内部 v8 报错复刻）：
			// JSON.stringify 会把它转义为 \uXXXX，这里保持一致。
			if c == 0xED && i+2 < len(s) {
				b1, b2 := s[i+1], s[i+2]
				if b1 >= 0xA0 && b1 <= 0xBF && b2 >= 0x80 && b2 <= 0xBF {
					u := uint16(c&0x0F)<<12 | uint16(b1&0x3F)<<6 | uint16(b2&0x3F)
					b.WriteString(`\u`)
					b.WriteByte(hex[u>>12&0x0F])
					b.WriteByte(hex[u>>8&0x0F])
					b.WriteByte(hex[u>>4&0x0F])
					b.WriteByte(hex[u&0x0F])
					i += 2
					continue
				}
			}
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// String 返回字符串的 JSON 字面量（Quote 的别名，语义更直白）。
func String(s string) string { return Quote(s) }

// Int 返回整数的 JSON 字面量。
func Int(n int64) string { return strconv.FormatInt(n, 10) }

// Bool 返回布尔值的 JSON 字面量。
func Bool(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// Null 返回 JSON null。
func Null() string { return "null" }

// Object 按给定字段顺序拼出 JSON 对象（保持插入顺序，与 JSON.stringify 一致）。
func Object(fields ...Field) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(Quote(f.Key))
		b.WriteByte(':')
		b.WriteString(f.Raw)
	}
	b.WriteByte('}')
	return b.String()
}
