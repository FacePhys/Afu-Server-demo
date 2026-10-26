// Package web 把前端构建产物打进二进制，由内部后台端口提供。
// web/dist 只提交占位文件 .keep，没构建前端时 go build、go test 也能通过。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist 返回 web/dist 目录。
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist 是编译期固定的目录，不会失败
	}
	return sub
}
