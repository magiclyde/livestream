// livestream 是一个纯 Go 的 WebRTC 直播学习项目：
// 浏览器推流 -> Go SFU 转发 -> 浏览器播放。
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"runtime"
	"strings"

	"livestream/internal/server"
)

//go:embed web
var webFS embed.FS

// 构建信息，由 Makefile 通过 -ldflags "-X main.xxx=..." 注入；
// 直接 go run / go build 时保留零值（unknown / 空）。
var (
	version   = "dev"
	gitCommit = "unknown"
	buildDate = "unknown"
	goVersion = runtime.Version()
)

func printVersion() {
	log.Printf("livestream %s", version)
	log.Printf("  git commit: %s", gitCommit)
	log.Printf("  build date: %s", buildDate)
	log.Printf("  go version: %s", goVersion)
}

func main() {
	addr := flag.String("addr", ":8080", "HTTP 监听地址")
	stun := flag.String("stun", "stun:stun.l.google.com:19302", "逗号分隔的 STUN 地址；留空则禁用")
	cert := flag.String("cert", "", "TLS 证书路径（PEM）；与 -key 同时提供则启用 HTTPS")
	key := flag.String("key", "", "TLS 私钥路径（PEM）；与 -cert 同时提供则启用 HTTPS")
	showVersion := flag.Bool("version", false, "打印构建信息后退出")
	flag.Parse()

	if *showVersion {
		printVersion()
		return
	}

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
