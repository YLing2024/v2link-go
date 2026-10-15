package config

import (
	"encoding/json"
	"strings"
)

// parseProbeJSON 解析 REGION_PROBES 的 JSON 数组；只接受 key/name 非空且 url 以 https:// 开头
// 的条目（与旧实现 parseRegionProbes 一致）。返回 ok=false 表示整段不可用，调用方回落内置。
func parseProbeJSON(raw string) ([]RegionProbe, bool) {
	var arr []map[string]any
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return nil, false
	}
	out := make([]RegionProbe, 0, len(arr))
	for _, item := range arr {
		key := asString(item["key"])
		name := asString(item["name"])
		url := asString(item["url"])
		if strings.TrimSpace(key) == "" || strings.TrimSpace(name) == "" || !strings.HasPrefix(url, "https://") {
			continue
		}
		out = append(out, RegionProbe{
			Key:  strings.TrimSpace(key),
			Name: strings.TrimSpace(name),
			Flag: asString(item["flag"]),
			URL:  url,
		})
	}
	return out, true
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
