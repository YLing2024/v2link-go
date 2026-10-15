package httpapi

import (
	"net/http"

	"v2link-go/internal/clash"
	"v2link-go/internal/httperr"
)

// handleClashGet 抓取订阅正文（免鉴权，token 即凭据）；错误按需求返回纯文本中文并禁用缓存。
func (s *Server) handleClashGet(ctx *Context) (Response, error) {
	sub, err := s.clash.Get(ctx.Request.Context(), ctx.Params["token"], clash.ParseMode(ctx.Query.Get("mode")))
	if err != nil {
		var he *httperr.Error
		if asHTTPErr(err, &he) {
			return Response{Status: he.Status, ContentType: contentTypePlain, RawBody: []byte(he.Message)}, nil
		}
		return Response{Status: http.StatusInternalServerError, ContentType: contentTypePlain, RawBody: []byte("服务器内部错误")}, nil
	}
	headers := map[string]string{
		"Content-Disposition": `attachment; filename="v2link.yaml"`,
		"Cache-Control":       "no-store",
	}
	if sub.Userinfo != nil {
		headers["subscription-userinfo"] = *sub.Userinfo
	}
	return Response{Status: http.StatusOK, ContentType: contentTypeYAML, RawBody: []byte(sub.YAML), Headers: headers}, nil
}

// handleClashCreate 创建临时订阅（201）。
func (s *Server) handleClashCreate(ctx *Context) (Response, error) {
	created, err := s.clash.Create(ctx.Request.Context(), decodeAny(ctx.Body["linkIds"]), ctx.User)
	if err != nil {
		return Response{}, err
	}
	return dataJSON(http.StatusCreated, created), nil
}
