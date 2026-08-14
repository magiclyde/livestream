// livestream 是一个纯 Go 的 WebRTC 直播学习项目：
// 浏览器推流 -> Go SFU 转发 -> 浏览器播放。
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"livestream/internal/server"
)

//go:embed web
var webFS embed.FS

func main() {
	addr := flag.String("addr", ":8080", "HTTP 监听地址")
	stun := flag.String("stun", "stun:stun.l.google.com:19302", "逗号分隔的 STUN 地址；留空则禁用")
	flag.Parse()

	staticFS, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}

	var stunURIs []string
	for _, uri := range strings.Split(*stun, ",") {
		if uri = strings.TrimSpace(uri); uri != "" {
			stunURIs = append(stunURIs, uri)
		}
	}

	handler := server.New(server.Config{STUNURIs: stunURIs}, staticFS)

	log.Printf("livestream listening on http://%s", *addr)
	log.Printf("STUN: %v", stunURIs)
	if err := http.ListenAndServe(*addr, handler); err != nil {
		log.Fatal(err)
	}
}
