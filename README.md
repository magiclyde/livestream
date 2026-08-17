# Livestream — Go WebRTC 直播学习项目

纯 Go 实现的 WebRTC 直播服务（SFU 模式）：
浏览器推流 → Go 服务转发 → 浏览器播放。不依赖 ffmpeg / nginx-rtmp 等外部进程。

## 快速开始

```bash
go run .
```

然后打开两个浏览器标签页访问 http://localhost:8080 ：

1. 标签页 A：输入房间号（如 `demo`），点「开始直播（主播）」，授权摄像头/麦克风；
2. 标签页 B：输入相同房间号，点「观看直播（观众）」；
3. 标签页 A 的画面会出现在标签页 B 的「直播间画面」里。

> 默认启用 Google 公共 STUN（跨网络时帮助打通 NAT）。仅本机/局域网演示可
> 用 `go run . -stun ""` 关闭。

## 构建与版本信息

```bash
make build                 # 输出 bin/livestream（注入 git commit / 构建时间）
bin/livestream -version    # 查看构建信息
```

构建信息由 Makefile 通过 `-ldflags` 注入 `main` 包变量：

- `version`：git tag 描述（无 tag 时为短 commit，工作区有改动时带 `-dirty`）；
- `git commit`：当前提交的短哈希；
- `build date`：UTC 构建时间；
- `go version`：编译所用的 Go 版本（运行时读取，无需注入）。

`make release` 额外开启静态链接并裁剪调试信息（`-s -w`）。

## 手机/局域网测试（HTTPS）

浏览器（尤其 iPhone Safari）只在 HTTPS 或 localhost 下开放摄像头/麦克风，
用 `http://局域网IP:8080` 访问会直接失败。推荐用 mkcert 签一张 IP 证书：

```bash
mkcert 192.168.x.x          # 生成证书，需先 mkcert -install 并信任根证书
go run . -addr 192.168.x.x:8443 -cert 192.168.x.x+1.pem -key 192.168.x.x+1-key.pem
```

手机先安装并信任 mkcert 的根证书（`~/Library/Application Support/mkcert/rootCA.pem`），
再访问 `https://192.168.x.x:8443`。`-cert`/`-key` 缺一不可，同时提供时自动切 HTTPS。

## 端到端自测（无头）

```bash
go run ./cmd/streamtest
```

用两个 Pion 客户端扮演主播与观众，走真实信令/ICE/RTP 链路，
观众收到 30 个 RTP 包即 PASS。

## 目录结构

| 路径 | 职责 |
|---|---|
| `main.go` | 入口：命令行参数、嵌入 `web/` 静态资源 |
| `internal/sfu/` | 核心：Hub（房间表）、Room（房间）、Peer（对端）、TrackRelay（转发器） |
| `internal/server/` | HTTP 服务：静态页面、`/api/config`、WebSocket 信令入口 |
| `web/` | 前端：原生 HTML/CSS/JS 推流与播放页 |
| `cmd/streamtest/` | 无头端到端自测 |
| `docs/` | 概念与架构文档 |
| `progress.md` | 开发计划、决策与日志 |

## 学习路径

1. [docs/01-直播链路与协议.md](docs/01-直播链路与协议.md)：采集→编码→传输→播放全链路、协议对比、选型理由；
2. [docs/02-WebRTC核心概念.md](docs/02-WebRTC核心概念.md)：SDP/ICE/DTLS/SRTP、协商时序、信令消息格式、转发原理；
3. [docs/03-系统架构与代码走读.md](docs/03-系统架构与代码走读.md)：代码结构、关键并发点、踩坑记录；
4. 自己动手跑通自测，然后按「扩展练习」改造代码。

## 扩展练习（后续方向）

- 录制：把 RTP 流转成 HLS（ffmpeg 或 `pion/rtp` + TS muxer）；
- RTMP 接入：支持 OBS 推 RTMP 再转 WebRTC 分发；
- 弹幕/聊天：复用信令连接做房间内广播；
- 多主播：把 Room 改成支持多路轨道并存；
- 带宽自适应：读取 RTCP 反馈（丢包率、往返时延）控制码率；
- TURN 部署：跨对称 NAT 场景。
