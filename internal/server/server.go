// Package server 提供 HTTP 服务：静态页面 + WebSocket 信令。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"sync/atomic"

	"github.com/coder/websocket"
	"github.com/pion/webrtc/v4"

	"livestream/internal/sfu"
)

// Config 是服务配置。
type Config struct {
	STUNURIs []string
	Logf     func(format string, args ...any)
}

// Server 持有 SFU Hub 和配置。
type Server struct {
	hub      *sfu.Hub
	stunURIs []string
	logf     func(format string, args ...any)
	nextID   atomic.Int64
}

// New 构造 http.Handler；staticFS 为空时只提供 /ws 与 /api/config。
func New(cfg Config, staticFS fs.FS) http.Handler {
	s := &Server{
		hub:      sfu.NewHub(cfg.Logf),
		stunURIs: cfg.STUNURIs,
		logf:     cfg.Logf,
	}
	if s.logf == nil {
		s.logf = log.Printf
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/api/config", s.handleConfig)
	if staticFS != nil {
		mux.Handle("/", http.FileServer(http.FS(staticFS)))
	}
	return mux
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"stun": s.stunURIs})
}

// handleWS 是信令入口：一条 WebSocket 连接 = 一个对端。
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// 本地学习项目放宽 Origin 校验；生产环境应改用 OriginPatterns。
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	out := make(chan []byte, 64)
	go s.writeLoop(ctx, c, out)

	send := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		select {
		case out <- b:
		default: // 队列满则丢弃，直播信令可接受
		}
	}

	var (
		peer *sfu.Peer
		room *sfu.Room
	)
	defer func() {
		if room != nil && peer != nil {
			room.RemovePeer(peer)
		}
	}()

	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			s.logf("ws read: %v", err)
			return
		}

		var msg sfu.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			send(sfu.Message{Type: "error", Message: "bad json: " + err.Error()})
			continue
		}

		switch msg.Type {
		case "join":
			if peer != nil {
				send(sfu.Message{Type: "error", Message: "already joined"})
				continue
			}
			if msg.Room == "" {
				send(sfu.Message{Type: "error", Message: "room is required"})
				continue
			}
			role := sfu.Role(msg.Role)
			if role != sfu.RolePublisher && role != sfu.RoleViewer {
				send(sfu.Message{Type: "error", Message: "role must be publisher or viewer"})
				continue
			}

			room = s.hub.GetOrCreateRoom(msg.Room)
			pc, err := s.newPC(role, msg.Room, send)
			if err != nil {
				send(sfu.Message{Type: "error", Message: "create peer connection: " + err.Error()})
				room = nil
				continue
			}

			peer = sfu.NewPeer(s.peerID(), role, pc, send)
			peerCopy := peer
			roomCopy := room
			if role == sfu.RolePublisher {
				pc.OnTrack(func(remote *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
					if peerCopy.Closed() {
						return
					}
					roomCopy.HandlePublisherTrack(peerCopy, remote)
				})
			}
			pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
				s.logf("peer %s (%s) state=%s", peerCopy.ID, role, st)
			})

			switch role {
			case sfu.RolePublisher:
				if err := room.AttachPublisher(peer); err != nil {
					send(sfu.Message{Type: "error", Message: err.Error()})
					peer.Close()
					peer = nil
					room = nil
					continue
				}
			case sfu.RoleViewer:
				room.AddViewer(peer)
			}

			peer.Send(sfu.Message{Type: "joined", Room: msg.Room, Role: role})
			go room.Negotiate(peer)

		case "answer":
			if peer == nil || msg.SDP == nil {
				send(sfu.Message{Type: "error", Message: "answer before join"})
				continue
			}
			room.HandleAnswer(peer, *msg.SDP)

		case "ice":
			if peer == nil || msg.Candidate == nil {
				continue
			}
			if err := peer.PC.AddICECandidate(*msg.Candidate); err != nil {
				s.logf("add ice candidate for %s: %v", peer.ID, err)
			}

		case "leave":
			if peer != nil {
				room.RemovePeer(peer)
				peer = nil
				room = nil
			}

		default:
			send(sfu.Message{Type: "error", Message: "unknown message type: " + msg.Type})
		}
	}
}

// newPC 创建 PeerConnection：
// - 主播角色预置 recvonly 音视频 transceiver，等待主播把轨道挂上来；
// - 注册 ICE 候选回调，把服务器侧候选转发给客户端。
func (s *Server) newPC(role sfu.Role, roomID string, send sfu.SendFunc) (*webrtc.PeerConnection, error) {
	cfg := webrtc.Configuration{}
	for _, uri := range s.stunURIs {
		cfg.ICEServers = append(cfg.ICEServers, webrtc.ICEServer{URLs: []string{uri}})
	}

	pc, err := webrtc.NewPeerConnection(cfg)
	if err != nil {
		return nil, err
	}

	if role == sfu.RolePublisher {
		for _, kind := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeAudio, webrtc.RTPCodecTypeVideo} {
			if _, err := pc.AddTransceiverFromKind(kind, webrtc.RTPTransceiverInit{
				Direction: webrtc.RTPTransceiverDirectionRecvonly,
			}); err != nil {
				_ = pc.Close()
				return nil, err
			}
		}
	}

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		cand := c.ToJSON()
		send(sfu.Message{Type: "ice", Room: roomID, Candidate: &cand})
	})

	return pc, nil
}

func (s *Server) peerID() string {
	return fmt.Sprintf("peer-%d", s.nextID.Add(1))
}

func (s *Server) writeLoop(ctx context.Context, c *websocket.Conn, out <-chan []byte) {
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-out:
			if err := c.Write(ctx, websocket.MessageText, b); err != nil {
				return
			}
		}
	}
}
