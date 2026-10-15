package monitor

import (
	"encoding/json"
	"math"
	"regexp"
)

// TrafficByUser 是 xray statsquery 解析结果：email → {上行,下行} 字节增量。
type TrafficByUser struct {
	ByUser map[string]TrafficPair
}

// TrafficPair 是某用户的上下行累计值。
type TrafficPair struct {
	Up   int64
	Down int64
}

// statNameRe 匹配 `user>>><email>>>traffic>>>uplink|downlink`。
var statNameRe = regexp.MustCompile(`^user>>>(.+?)>>>traffic>>>(uplink|downlink)$`)

// ParseStatsQuery 解析 xray statsquery 的原始 stdout，逐行对齐旧实现 lib/xrayStats.ts：
//   - 非 JSON（空/警告）→ 视为无流量，不抛异常；
//   - stat 非数组 → 无流量；
//   - value 缺省或非有限数 → 按 0；<=0 的项跳过（xray 省略 0 值字段）。
func ParseStatsQuery(raw string) TrafficByUser {
	out := TrafficByUser{ByUser: map[string]TrafficPair{}}
	var parsed struct {
		Stat []struct {
			Name  string          `json:"name"`
			Value json.RawMessage `json:"value"`
		} `json:"stat"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return out
	}
	for _, item := range parsed.Stat {
		m := statNameRe.FindStringSubmatch(item.Name)
		if m == nil {
			continue
		}
		email := m[1]
		dir := m[2]
		v := rawNumber(item.Value)
		if v <= 0 {
			continue
		}
		cur := out.ByUser[email]
		if dir == "uplink" {
			cur.Up += v
		} else {
			cur.Down += v
		}
		out.ByUser[email] = cur
	}
	return out
}

// rawNumber 只接受 JSON 数字字面量（string/bool/null 一律 0，对齐 JS typeof === 'number'）。
func rawNumber(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return int64(f)
}
