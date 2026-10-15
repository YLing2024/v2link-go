package httpapi

import (
	"net/http"
	"strings"

	"v2link-go/internal/links"
	"v2link-go/internal/monitor"
	"v2link-go/internal/store"
)

// paged 是分页信封（/connections 与 /audit 共用）。
type paged struct {
	Rows   any   `json:"rows"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

// handleLinksList 返回全量账本展示形态。
func (s *Server) handleLinksList(ctx *Context) (Response, error) {
	views, err := s.links.List(ctx.Request.Context())
	if err != nil {
		return Response{}, err
	}
	return dataJSON(http.StatusOK, views), nil
}

// handleLinksCreate 生成链接（201）。
func (s *Server) handleLinksCreate(ctx *Context) (Response, error) {
	view, err := s.links.Create(ctx.Request.Context(), links.ParseCreateInput(ctx.Body), ctx.User)
	if err != nil {
		return Response{}, err
	}
	return dataJSON(http.StatusCreated, view), nil
}

// handleLinksRevoke 吊销链接。
func (s *Server) handleLinksRevoke(ctx *Context) (Response, error) {
	view, err := s.links.Revoke(ctx.Request.Context(), ctx.Params["id"], ctx.User)
	if err != nil {
		return Response{}, err
	}
	return dataJSON(http.StatusOK, view), nil
}

// handleLinksDelete 删除链接（账本行 + 采样），成功回 { ok: true }（无 data）。
func (s *Server) handleLinksDelete(ctx *Context) (Response, error) {
	if err := s.links.Remove(ctx.Request.Context(), ctx.Params["id"], ctx.User); err != nil {
		return Response{}, err
	}
	return okJSON(http.StatusOK), nil
}

// handleLinksExtend 延长/转换有效期。
func (s *Server) handleLinksExtend(ctx *Context) (Response, error) {
	view, err := s.links.Extend(ctx.Request.Context(), ctx.Params["id"], links.ParseExtendInput(ctx.Body), ctx.User)
	if err != nil {
		return Response{}, err
	}
	return dataJSON(http.StatusOK, view), nil
}

// handleLinkTraffic 流量曲线：30s 采样按 hour|day 聚合。
func (s *Server) handleLinkTraffic(ctx *Context) (Response, error) {
	now := int(ctx.Now)
	bucket := monitor.ParseBucket(ctx.Query.Get("bucket"))
	toRaw, toPresent := qval(ctx.Query, "to")
	to := clampInt(toRaw, toPresent, now, 1, maxSafeInt)
	fromRaw, fromPresent := qval(ctx.Query, "from")
	from := clampInt(fromRaw, fromPresent, to-24*3600*1000, 1, to)

	samples, err := s.monitor.ListSamples(ctx.Request.Context(), store.SampleQuery{
		ID:   ctx.Params["id"],
		From: int64(from),
		To:   int64(to),
	})
	if err != nil {
		return Response{}, err
	}
	series := monitor.ToSeries(monitor.Aggregate(samples, bucket), int64(from), int64(to), bucket)
	return dataJSON(http.StatusOK, series), nil
}

// handleLinkConnections 连接记录分页 + host 前缀搜索。
func (s *Server) handleLinkConnections(ctx *Context) (Response, error) {
	limitRaw, limitPresent := qval(ctx.Query, "limit")
	limit := clampInt(limitRaw, limitPresent, 50, 1, 200)
	offsetRaw, offsetPresent := qval(ctx.Query, "offset")
	offset := clampInt(offsetRaw, offsetPresent, 0, 0, maxSafeInt)

	var hostPrefix *string
	if q, present := qval(ctx.Query, "q"); present {
		if trimmed := strings.TrimSpace(q); trimmed != "" {
			prefix := sliceRunes(trimmed, 200)
			hostPrefix = &prefix
		}
	}
	linkID := ctx.Params["id"]
	rows, total, err := s.monitor.ListConnections(ctx.Request.Context(), store.ConnectionQuery{
		LinkID:     &linkID,
		HostPrefix: hostPrefix,
		From:       optClampInt(ctx.Query, "from"),
		To:         optClampInt(ctx.Query, "to"),
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		return Response{}, err
	}
	return dataJSON(http.StatusOK, paged{Rows: rows, Total: total, Limit: limit, Offset: offset}), nil
}
