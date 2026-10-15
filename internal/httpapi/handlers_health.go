package httpapi

import (
	"encoding/json"
	"time"

	"v2link-go/internal/reconcile"
)

// healthData 是 /api/healthz 的 data 形态（字段顺序与旧实现一致）。
type healthData struct {
	Status string            `json:"status"`
	Now    int64             `json:"now"`
	Uptime int64             `json:"uptime"`
	Xray   *reconcile.Status `json:"xray,omitempty"`
}

// handleHealth 健康检查：只报告进程存活与数据面内存快照，不做 DB/xray 往返。
func (s *Server) handleHealth(ctx *Context) (Response, error) {
	data := healthData{
		Status: "ok",
		Now:    ctx.Now,
		Uptime: int64(time.Since(s.startedAt) / time.Second),
	}
	if s.reconcile != nil {
		snapshot := s.reconcile.Snapshot()
		data.Xray = &snapshot
	}
	return dataJSON(200, data), nil
}

// stringField 取 JSON 顶层字符串字段；非字符串/缺失返回空串（对齐 typeof === 'string' 判定）。
func stringField(raw json.RawMessage) string {
	if len(raw) == 0 || raw[0] != '"' {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// decodeAny 把 JSON 字段解析为通用值；缺失/非法返回 nil。
func decodeAny(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}
