package sfu

import (
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

// keyframeInterval 周期性向主播要关键帧的间隔。
const keyframeInterval = 2 * time.Second

// TrackRelay 把主播的一条轨道转发给所有观众，是 SFU 的核心单元。
//
// 原理：remote.ReadRTP() 从主播连接读出原始 RTP 包，
// local.WriteRTP() 把包广播给所有绑定它的 RTPSender（每个观众一个绑定），
// 全程不解码、不编码，这就是"选择性转发"。
type TrackRelay struct {
	ID     string
	remote *webrtc.TrackRemote
	local  *webrtc.TrackLocalStaticRTP
	room   *Room

	stop chan struct{}
	done chan struct{}
	once sync.Once
}

func (t *TrackRelay) start() { go t.loop() }

func (t *TrackRelay) loop() {
	defer close(t.done)
	t.room.Logf("relay %s started", t.ID)
	if t.remote.Kind() == webrtc.RTPCodecTypeVideo {
		go t.requestKeyframes()
	}
	for {
		pkt, _, err := t.remote.ReadRTP()
		if err != nil {
			t.room.Logf("relay %s read end: %v", t.ID, err)
			return
		}
		if err := t.local.WriteRTP(pkt); err != nil {
			// 单个观众失败不应中断整个转发
			t.room.Logf("relay %s write: %v", t.ID, err)
		}
		select {
		case <-t.stop:
			return
		default:
		}
	}
}

// requestKeyframes 周期性向主播发送 PLI（关键帧请求）：
// 观众中途加入或丢包后需要新的 I 帧才能恢复画面，而当前 relay
// 不转发观众侧的 RTCP 反馈，这里用轮询兜底，保证恢复时间有上界。
func (t *TrackRelay) requestKeyframes() {
	ticker := time.NewTicker(keyframeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-t.stop:
			return
		case <-ticker.C:
			t.room.RequestKeyframe(t.remote)
		}
	}
}

// stopForwarding 请求停止转发（loop 可能在 ReadRTP 上阻塞，
// 需要关闭主播 PeerConnection 才能让它退出）。
func (t *TrackRelay) stopForwarding() {
	t.once.Do(func() { close(t.stop) })
}

// waitDone 等待转发 goroutine 退出。
func (t *TrackRelay) waitDone() { <-t.done }
