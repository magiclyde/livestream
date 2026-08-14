# WebRTC 核心概念与我们的信令协议

## 1. WebRTC 是什么

WebRTC（Web Real-Time Communication）是浏览器之间、或浏览器与服务器之间
进行实时音视频通信的框架，由三部分组成：

- 采集：`getUserMedia` 拿到摄像头/麦克风；
- 传输：媒体走 **RTP over SRTP**（UDP），保证低延迟；
- 连接建立：**SDP 协商** + **ICE 候选交换**，解决"怎么找到对方、怎么打通 NAT"。

## 2. 关键概念

| 概念 | 作用 | 通俗解释 |
|---|---|---|
| RTCPeerConnection | 一条媒体连接 | 负责协商、传输、状态管理的大管家 |
| SDP | 媒体会话描述文本 | 双方各自"我能编解码什么、我有哪些音视频轨道、我在哪些地址收数据" |
| Offer / Answer | 协商的两个动作 | 一方出 offer（提议），另一方回 answer（确认/修改） |
| ICE | 连通性建立 | 找出双方都可达的一条网络路径 |
| 候选 candidate | 一条可能的路径 | host（本机）、srflx（STUN 反射公网地址）、relay（TURN 中转） |
| STUN | 打洞辅助 | 帮我发现"我的公网地址是什么" |
| TURN | 中转服务器 | 打洞失败时由服务器转发媒体（本项目不搭） |
| DTLS | UDP 上的 TLS | 握手后生成 SRTP 密钥，防止窃听 |
| SRTP | 加密的 RTP | 真正承载音视频的加密包 |
| Transceiver / Track | 一条媒体通道 | audio 或 video 各一条；方向 sendonly / recvonly / sendrecv |

## 3. 协商时序（本项目，服务器始终发起 offer）

为了让模型简单，本项目约定**只有服务器发起 offer，客户端只回答**。

主播端：

```
1. 主播 ws.send { type:"join", room, role:"publisher" }
2. 服务器建 PeerConnection，预置 recvonly 音视频 transceiver
3. 服务器 createOffer → 发 offer 给主播
4. 主播 addTrack(摄像头/麦克风) → setRemoteDescription → createAnswer → 回 answer
5. 双方交换 ICE candidate
6. 主播的 RTP 包到达服务器，触发服务器 OnTrack
```

观众端：

```
1. 观众 ws.send { type:"join", room, role:"viewer" }
2. 服务器把已存在的转发轨道 addTrack 到观众的连接
3. 服务器 createOffer → 发 offer 给观众
4. 观众 setRemoteDescription → createAnswer → 回 answer
5. 交换 ICE candidate，服务器把主播 RTP 包转发过来，观众 OnTrack 播放
```

主播中途新开一条轨道、或主播离开时，服务器会向观众**重新协商**（重新发 offer），
保证轨道增删同步。

## 4. 信令消息格式（WebSocket JSON）

| 方向 | type | 关键字段 | 说明 |
|---|---|---|---|
| C→S | join | room, role | 加入房间，role 为 publisher/viewer |
| C→S | answer | sdp | 回答服务器的 offer |
| C→S | ice | candidate | 汇报本地 ICE 候选 |
| S→C | joined | room, role | 加入成功 |
| S→C | offer | sdp | 服务器发起的协商 |
| S→C | ice | candidate | 转发对方候选 |
| S→C | publisher-left | - | 主播离开，观众轨道已移除 |
| S→C | error | message | 错误提示 |

## 5. 为什么能"转发不转码"：TrackLocalStaticRTP

SFU 的核心逻辑只有两行循环：

```go
pkt, _, err := remote.ReadRTP()   // 读主播的 RTP 包
local.WriteRTP(pkt)                // 广播给所有观众
```

- `TrackRemote.ReadRTP()` 从主播连接读出原始 RTP 包；
- `TrackLocalStaticRTP.WriteRTP()` 把包广播给所有绑定它的 `RTPSender`；
- 每个观众对应一个绑定，库内部自动把 SSRC、PayloadType 改写为观众连接的参数；
- 全程不解码、不编码，所以 CPU 开销低——这正是 SFU（选择性转发）的本质。
