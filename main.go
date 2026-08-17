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
	cert := flag.String("cert", "", "TLS 证书路径（PEM）；与 -key 同时提供则启用 HTTPS")
	key := flag.String("key", "", "TLS 私钥路径（PEM）；与 -cert 同时提供则启用 HTTPS")
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

	scheme := "http"
	if *cert != "" || *key != "" {
		if *cert == "" || *key == "" {
			log.Fatal("-cert 与 -key 必须同时提供")
		}
		scheme = "https"
	}

	log.Printf("livestream listening on %s://%s", scheme, *addr)
	log.Printf("STUN: %v", stunURIs)
	if scheme == "https" {
		err = http.ListenAndServeTLS(*addr, *cert, *key, handler)
	} else {
		err = http.ListenAndServe(*addr, handler)
	}
	if err != nil {
		log.Fatal(err)
	}
}
