// streamtest 是一个无头端到端自测工具：
// 用两个 Pion 客户端分别扮演主播和观众，验证
// 信令协商、ICE 打通、RTP 转发整条链路是否工作。
//
// 用法：go run ./cmd/streamtest
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"livestream/internal/server"
	"livestream/internal/sfu"
)

const (
	roomName    = "e2e"
	wantPkts    = 30 // 收到多少个 RTP 包算通过
	testTimeout = 45 * time.Second
)

// testClient 封装一个测试角色（主播/观众）的 WebSocket + PeerConnection。
type testClient struct {
	ctx        context.Context
	conn       *websocket.Conn
	pc         *webrtc.PeerConnection
	role       sfu.Role
	local      *webrtc.TrackLocalStaticRTP
	answerSent chan struct{}
	answerOnce sync.Once
}

func newTestClient(ctx context.Context, url string, role sfu.Role, onTrack func(*webrtc.TrackRemote)) (*testClient, error) {
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", url, err)
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		conn.Close(websocket.StatusInternalError, "")
		return nil, fmt.Errorf("new peer connection: %w", err)
	}

	c := &testClient{
		ctx:        ctx,
		conn:       conn,
		pc:         pc,
		role:       role,
		answerSent: make(chan struct{}),
	}

	if onTrack != nil {
		pc.OnTrack(func(t *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
			onTrack(t)
		})
	}
	pc.OnICECandidate(func(cand *webrtc.ICECandidate) {
		if cand == nil {
			return
		}
		ci := cand.ToJSON()
		c.send(sfu.Message{Type: "ice", Candidate: &ci})
	})

	go c.readLoop()
	return c, nil
}

func (c *testClient) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	if err := c.conn.Write(c.ctx, websocket.MessageText, b); err != nil {
		log.Printf("[%s] write: %v", c.role, err)
	}
}

func (c *testClient) readLoop() {
	for {
		_, data, err := c.conn.Read(c.ctx)
		if err != nil {
			log.Printf("[%s] read end: %v", c.role, err)
			return
		}

		var msg sfu.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "offer":
			if msg.SDP == nil {
				continue
			}
			if err := c.handleOffer(*msg.SDP); err != nil {
				log.Printf("[%s] handle offer: %v", c.role, err)
				return
			}
		case "ice":
			if msg.Candidate != nil {
				if err := c.pc.AddICECandidate(*msg.Candidate); err != nil {
					log.Printf("[%s] add ice: %v", c.role, err)
				}
			}
		case "publisher-left":
			log.Printf("[viewer] 收到 publisher-left")
		case "error":
			log.Printf("[%s] server error: %s", c.role, msg.Message)
		}
	}
}

func (c *testClient) handleOffer(sdp webrtc.SessionDescription) error {
	if c.role == sfu.RolePublisher && c.local != nil {
		if _, err := c.pc.AddTrack(c.local); err != nil {
			return fmt.Errorf("add track: %w", err)
		}
	}

	if err := c.pc.SetRemoteDescription(sdp); err != nil {
		return fmt.Errorf("set remote description: %w", err)
	}
	answer, err := c.pc.CreateAnswer(nil)
	if err != nil {
		return fmt.Errorf("create answer: %w", err)
	}
	if err := c.pc.SetLocalDescription(answer); err != nil {
		return fmt.Errorf("set local description: %w", err)
	}
	c.send(sfu.Message{Type: "answer", SDP: c.pc.LocalDescription()})
	c.answerOnce.Do(func() { close(c.answerSent) })
	return nil
}

// startPublish 创建一条 VP8 测试轨道，并在协商完成后持续发送 RTP 包。
func (c *testClient) startPublish() error {
	track, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000},
		"video", "test-stream",
	)
	if err != nil {
		return err
	}
	c.local = track

	go func() {
		select {
		case <-c.answerSent:
		case <-c.ctx.Done():
			return
		}

		seq := uint16(0)
		ts := uint32(0)
		for {
			pkt := &rtp.Packet{
				Header: rtp.Header{
					Version:        2,
					PayloadType:    96,
					SequenceNumber: seq,
					Timestamp:      ts,
					SSRC:           0xdeadbeef,
				},
				Payload: []byte{0x10, 0x20, 0x30}, // 测试载荷，无需是合法 VP8
			}
			if err := track.WriteRTP(pkt); err != nil {
				return
			}
			seq++
			ts += 3000
			select {
			case <-c.ctx.Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}()
	return nil
}

func main() {
	log.SetPrefix("[streamtest] ")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{Handler: server.New(server.Config{}, nil)}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	base := "ws://" + ln.Addr().String() + "/ws"
	log.Printf("test server listening on %s", base)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// 1. 主播加入并推流
	pub, err := newTestClient(ctx, base, sfu.RolePublisher, nil)
	if err != nil {
		log.Fatal(err)
	}
	if err := pub.startPublish(); err != nil {
		log.Fatal(err)
	}
	pub.send(sfu.Message{Type: "join", Room: roomName, Role: sfu.RolePublisher})

	select {
	case <-pub.answerSent:
		log.Printf("publisher 协商完成")
	case <-ctx.Done():
		log.Fatal("等待 publisher 协商超时")
	}

	// 给服务器一点时间创建转发器（收到主播第一个 RTP 包后 OnTrack 触发）
	time.Sleep(500 * time.Millisecond)

	// 2. 观众加入并统计收到的 RTP 包
	var got atomic.Int64
	viewer, err := newTestClient(ctx, base, sfu.RoleViewer, func(t *webrtc.TrackRemote) {
		log.Printf("viewer 收到轨道: kind=%s id=%s", t.Kind(), t.ID())
		go func() {
			for {
				if _, _, err := t.ReadRTP(); err != nil {
					return
				}
				if n := got.Add(1); n%10 == 0 {
					log.Printf("viewer 累计收到 %d 个 RTP 包", n)
				}
			}
		}()
	})
	if err != nil {
		log.Fatal(err)
	}
	viewer.send(sfu.Message{Type: "join", Room: roomName, Role: sfu.RoleViewer})

	// 3. 等待通过条件或超时
	deadline := time.After(30 * time.Second)
	for {
		if got.Load() >= wantPkts {
			fmt.Printf("PASS: 转发链路正常，viewer 收到 %d 个 RTP 包\n", got.Load())
			return
		}
		select {
		case <-deadline:
			fmt.Fprintf(os.Stderr, "FAIL: 超时，viewer 仅收到 %d/%d 个 RTP 包\n", got.Load(), wantPkts)
			os.Exit(1)
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "FAIL:", ctx.Err())
			os.Exit(1)
		case <-time.After(200 * time.Millisecond):
		}
	}
}
