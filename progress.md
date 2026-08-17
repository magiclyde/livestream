# 开发进度（progress）

> 本文件记录开发计划、技术决策和开发日志，随开发持续更新。

## 目标

在当前目录开发一个用于学习的直播推流应用：Go 后端 + Web 前端，
以 WebRTC 实现低延迟直播（浏览器推流 → Go 服务转发 → 浏览器播放）。

## 技术选型

| 项 | 选择 | 理由 |
|---|---|---|
| 后端 | Go 1.26 | 用户主语言，重点学 Go 并发与网络编程 |
| 媒体 | Pion WebRTC v4 | 纯 Go 实现，无需 ffmpeg / nginx-rtmp 等外部进程 |
| 信令 | coder/websocket（WebSocket） | 现代、context API；注意：`pion/websocket` 仓库不存在，已修正 |
| 前端 | 原生 HTML/JS | 学习项目，不引入框架 |
| 协议 | WebRTC（SFU 模式） | 低延迟、链路直观，可深入学习 SDP/ICE/DTLS/SRTP |

## 计划

| # | 任务 | 状态 | 备注 |
|---|---|---|---|
| 1 | 初始化 Go 模块与依赖 | ✅ 完成 | `go mod init livestream`；webrtc v4.2.18、coder/websocket v1.8.15 |
| 2 | 概念文档：直播链路/协议 + WebRTC 基础 | ✅ 完成 | `docs/01-直播链路与协议.md`、`docs/02-WebRTC核心概念.md` |
| 3 | 信令服务 + SFU 核心 | ✅ 完成 | `internal/sfu`（房间/轨道转发）、`internal/server`（HTTP/WS 信令） |
| 4 | Web 前端推流/播放页面 | ✅ 完成 | `web/`，浏览器原生 WebRTC API |
| 5 | 无头端到端自测 | ✅ 完成 | `cmd/streamtest`，PASS：viewer 收到 30+ RTP 包 |
| 6 | README 与收尾 | ✅ 完成 | README + docs/03；主程序冒烟测试通过（/ 200、/api/config 正常） |

## 架构设计（v1）

- 每个房间 1 个主播（publisher）+ N 个观众（viewer），按房间号加入。
- 服务器始终是 offer 发起方，客户端只回答（简化协商模型）。
- 转发不转码：SFU 收到主播的 RTP 包后原样写入本地轨道，
  由 `TrackLocalStaticRTP` 广播给每个观众（自动按观众改写 SSRC/PayloadType）。
- 主播离开时：停止转发，从每个观众的连接中移除对应轨道并重新协商；
  观众连接保持，可继续等下一个主播。

## 开发日志

- 2026-08-14 初始化：`go mod init livestream`；拉取 `pion/webrtc v4.2.18`。
  `pion/websocket` 仓库不存在（报 Repository not found），改用 `coder/websocket v1.8.15`。
- 2026-08-14 API 确认（读 Pion 源码）：
  `TrackRemote.ReadRTP() (*rtp.Packet, interceptor.Attributes, error)`；
  `TrackLocalStaticRTP.WriteRTP` 在无绑定接收者时返回 nil（可安全忽略写错误继续转发）。
- 2026-08-14 环境确认：Go 1.26.5、Node 24 可用；本机无 ffmpeg（不影响本项目）。
- 2026-08-14 编译修正（编译器兜底，均记录在 `docs/03`）：
  `OnTrack` 双参数、`OnConnectionStateChange` 方法名、`NewTrackLocalStaticRTP` 返回 error、
  `ToJSON()` 返回值取地址。
- 2026-08-14 端到端自测 PASS：publisher/viewer 两个 Pion 客户端，
  两个 PeerConnection 均 connected，viewer 收到 33 个 RTP 包；
  `go build ./...`、`go vet ./...`、`gofmt -l` 全部通过。
- 2026-08-14 主程序冒烟测试：`go run . -addr 127.0.0.1:8099 -stun ""`
  启动正常，`GET /` 返回 200（embed 静态页生效），`GET /api/config` 返回 JSON。
- 2026-08-14 v1 完成。待办：浏览器实机验证（需本机打开两个标签页）、
  后续扩展见 README「扩展练习」。
- 2026-08-17 修 bug：离开房间时 panic（`server.go` 连接状态回调空指针）。
  原因：`OnConnectionStateChange`/`OnTrack` 闭包捕获了外层会变的 `peer`/`room`
  变量，`leave` 后二者被置 nil，PC 关闭触发回调即空指针解引用。
  修法：注册回调时用局部副本 `p`/`r` 固定引用，`OnTrack` 增加
  `Peer.Closed()` 守卫避免离开后误挂新轨道。教训：Pion 回调是异步的，
  闭包里永远不要引用后续会被置 nil 的外层变量。
