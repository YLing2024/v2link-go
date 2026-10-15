package httpapi

import "net/http"

// handleAudit 审计日志分页查询。
func (s *Server) handleAudit(ctx *Context) (Response, error) {
	limitRaw, limitPresent := qval(ctx.Query, "limit")
	limit := clampInt(limitRaw, limitPresent, 50, 1, 200)
	offsetRaw, offsetPresent := qval(ctx.Query, "offset")
	offset := clampInt(offsetRaw, offsetPresent, 0, 0, maxSafeInt)

	rows, total, err := s.monitor.ListAudit(ctx.Request.Context(), optClampInt(ctx.Query, "from"), optClampInt(ctx.Query, "to"), limit, offset)
	if err != nil {
		return Response{}, err
	}
	return dataJSON(http.StatusOK, paged{Rows: rows, Total: total, Limit: limit, Offset: offset}), nil
}
