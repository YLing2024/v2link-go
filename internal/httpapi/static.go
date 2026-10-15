package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// staticMime 对齐 express.static（底层 mime-types）的映射；字符集统一大写 UTF-8。
var staticMime = map[string]string{
	".html":        "text/html; charset=UTF-8",
	".js":          "application/javascript; charset=UTF-8",
	".mjs":         "application/javascript; charset=UTF-8",
	".css":         "text/css; charset=UTF-8",
	".json":        "application/json; charset=UTF-8",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".gif":         "image/gif",
	".webp":        "image/webp",
	".ico":         "image/vnd.microsoft.icon",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".ttf":         "font/ttf",
	".txt":         "text/plain; charset=UTF-8",
	".webmanifest": "application/manifest+json",
	".map":         "application/json; charset=UTF-8",
	".xml":         "application/xml; charset=UTF-8",
}

// staticContentType 返回静态文件内容类型；未知扩展名回落 application/octet-stream。
func staticContentType(name string) string {
	if value, ok := staticMime[strings.ToLower(path.Ext(name))]; ok {
		return value
	}
	return "application/octet-stream"
}

// serveStaticOnly 是纯静态托管（本地预览便利开关）：/api 一律 404 JSON，其余走静态/SPA。
func (s *Server) serveStaticOnly(w http.ResponseWriter, r *http.Request) {
	head := r.Method == http.MethodHead
	if r.Method != http.MethodGet && !head {
		s.write(w, r, head, Response{Status: http.StatusNotFound, ContentType: contentTypeHTML, RawBody: []byte("Not Found")})
		return
	}
	if isAPIPath(r.URL.Path) {
		s.write(w, r, head, errorJSON(http.StatusNotFound, "接口不存在: "+r.Method+" "+r.URL.Path))
		return
	}
	s.serveStatic(w, r, head)
}

// serveStatic 静态托管 + SPA 回退（仅 GET/HEAD）。命中文件按扩展名决定缓存；未命中回退
// index.html（Cache-Control: public, max-age=0，与旧实现 res.sendFile 的默认一致）。
func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request, head bool) {
	if name, ok := resolveStaticName(r.URL.Path); ok {
		if s.writeStaticFile(w, head, name, false) {
			return
		}
	}
	if s.writeStaticFile(w, head, "index.html", true) {
		return
	}
	s.write(w, r, head, Response{Status: http.StatusNotFound, ContentType: contentTypeHTML, RawBody: []byte("Not Found")})
}

// resolveStaticName 把 URL 路径映射为产物内的相对文件名；目录路径（含尾斜杠）补 index.html。
func resolveStaticName(urlPath string) (string, bool) {
	if urlPath == "" {
		urlPath = "/"
	}
	clean := path.Clean(urlPath)
	if clean == "." {
		clean = "/"
	}
	trimmed := strings.TrimPrefix(clean, "/")
	// path.Clean 不会产生越界的 ".."，但仍防御性拒绝。
	if strings.HasPrefix(trimmed, "..") {
		return "", false
	}
	if trimmed == "" {
		return "index.html", true
	}
	if strings.HasSuffix(urlPath, "/") {
		return path.Join(trimmed, "index.html"), true
	}
	return trimmed, true
}

// writeStaticFile 写出静态文件；不存在返回 false（交回上层的 SPA 回退）。
func (s *Server) writeStaticFile(w http.ResponseWriter, head bool, name string, spaFallback bool) bool {
	info, err := fs.Stat(s.static, name)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	content, err := fs.ReadFile(s.static, name)
	if err != nil {
		return false
	}
	cache := "public, max-age=31536000, immutable"
	switch {
	case spaFallback:
		cache = "public, max-age=0"
	case strings.HasSuffix(strings.ToLower(name), ".html"):
		cache = "no-cache"
	}
	w.Header().Set("Content-Type", staticContentType(name))
	w.Header().Set("Cache-Control", cache)
	w.Header().Set("Accept-Ranges", "bytes")
	w.WriteHeader(http.StatusOK)
	if head {
		w.Header().Set("Content-Length", itoa(len(content)))
		return true
	}
	_, _ = w.Write(content)
	return true
}
