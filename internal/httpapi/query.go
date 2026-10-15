package httpapi

import (
	"math"
	"net/url"
	"strconv"
	"strings"
)

// maxSafeInt 是 JS Number.MAX_SAFE_INTEGER（旧实现分页上限用的哨兵）。
const maxSafeInt = 9007199254740991

// qval 取查询参数首个值；键不存在时 present=false。
func qval(values url.Values, key string) (string, bool) {
	list, ok := values[key]
	if !ok || len(list) == 0 {
		return "", false
	}
	return list[0], true
}

// clampInt 复刻旧 clampInt(v, fallback, min, max)：
// Number(v) 非有限 → fallback；否则截断后夹到 [min, max]。
func clampInt(raw string, present bool, fallback, min, max int) int {
	if !present {
		return fallback
	}
	n := jsNumber(raw)
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return fallback
	}
	v := int(math.Trunc(n))
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// optClampInt 复刻「q.x === undefined ? undefined : clampInt(q.x, 0, 0, MAX_SAFE_INTEGER)」。
func optClampInt(values url.Values, key string) *int64 {
	raw, present := qval(values, key)
	if !present {
		return nil
	}
	v := int64(clampInt(raw, true, 0, 0, maxSafeInt))
	return &v
}

// jsNumber 复刻 JS Number(string) 的常用语义：trim；空串为 0；支持 0x/0b/0o 与 Infinity；
// 其余按十进制浮点解析，整串不合法即 NaN。
func jsNumber(raw string) float64 {
	s := strings.TrimSpace(raw)
	switch s {
	case "":
		return 0
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	case "NaN":
		return math.NaN()
	}
	sign := 1.0
	body := s
	if strings.HasPrefix(body, "+") {
		body = body[1:]
	} else if strings.HasPrefix(body, "-") {
		sign = -1
		body = body[1:]
	}
	lower := strings.ToLower(body)
	if strings.HasPrefix(lower, "0x") {
		if n, err := strconv.ParseInt(lower[2:], 16, 64); err == nil {
			return sign * float64(n)
		}
		return math.NaN()
	}
	if strings.HasPrefix(lower, "0b") {
		if n, err := strconv.ParseInt(lower[2:], 2, 64); err == nil {
			return sign * float64(n)
		}
		return math.NaN()
	}
	if strings.HasPrefix(lower, "0o") {
		if n, err := strconv.ParseInt(lower[2:], 8, 64); err == nil {
			return sign * float64(n)
		}
		return math.NaN()
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return f
}

// sliceRunes 按字符数截断（近似 JS String.prototype.slice 的 UTF-16 码元语义）。
func sliceRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
