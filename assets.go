// Package v2link 承载嵌入式前端产物，供二进制自带前端形态使用。
package v2link

import (
	"embed"
	"io/fs"
)

// distFS 嵌入前端构建产物；以 all: 前缀包含点文件与下划线开头的文件。
//
//go:embed all:frontend/dist
var distFS embed.FS

// DistFS 返回定位到 dist 根的文件系统；嵌入失败时退回原 FS（调用方按不存在处理）。
func DistFS() fs.FS {
	sub, err := fs.Sub(distFS, "frontend/dist")
	if err != nil {
		return distFS
	}
	return sub
}
