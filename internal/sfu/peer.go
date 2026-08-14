package sfu

import (
	"sync"

	"github.com/pion/webrtc/v4"
)

// Role 表示一个客户端在房间里的身份。
type Role string

const (
	RolePublisher Role = "publisher"
	RoleViewer    Role = "viewer"
)

// Message 是信令通道（WebSocket）上传输的 JSON 消息，服务端与客户端共用。
type Message struct {
	Type      string                     `json:"type"`
	Room      string                     `json:"room,omitempty"`
	Role      Role                       `json:"role,omitempty"`
	SDP       *webrtc.SessionDescription `json:"sdp,omitempty"`
	Candidate *webrtc.ICECandidateInit   `json:"candidate,omitempty"`
	Message   string                     `json:"message,omitempty"`
}

// SendFunc 异步投递一条信令消息给对端（非阻塞）。
type SendFunc func(v any)

// Peer 代表一个已建立的 WebRTC 对端（主播或观众）。
type Peer struct {
	ID   string
	Role Role
	PC   *webrtc.PeerConnection
	Send SendFunc

	negMu     sync.Mutex // 串行化协商，避免并发 offer 叠加
	answerCh  chan struct{}
	closedCh  chan struct{}
	closeOnce sync.Once

	sendersMu sync.Mutex
	senders   map[string]*webrtc.RTPSender // relayID -> RTPSender（仅观众角色使用）
}

// NewPeer 构造一个对端。
func NewPeer(id string, role Role, pc *webrtc.PeerConnection, send SendFunc) *Peer {
	return &Peer{
		ID:       id,
		Role:     role,
		PC:       pc,
		Send:     send,
		answerCh: make(chan struct{}, 1),
		closedCh: make(chan struct{}),
		senders:  make(map[string]*webrtc.RTPSender),
	}
}

// Close 幂等地关闭对端连接。
func (p *Peer) Close() {
	p.closeOnce.Do(func() {
		close(p.closedCh)
		_ = p.PC.Close()
	})
}

func (p *Peer) addSender(relayID string, s *webrtc.RTPSender) {
	p.sendersMu.Lock()
	defer p.sendersMu.Unlock()
	p.senders[relayID] = s
}

// takeSenders 取出并清空全部 RTPSender，用于主播离开时移除观众轨道。
func (p *Peer) takeSenders() []*webrtc.RTPSender {
	p.sendersMu.Lock()
	defer p.sendersMu.Unlock()
	out := make([]*webrtc.RTPSender, 0, len(p.senders))
	for _, s := range p.senders {
		out = append(out, s)
	}
	p.senders = make(map[string]*webrtc.RTPSender)
	return out
}

// markAnswered 记录一次 answer 已收到，解除 negotiate 的等待。
func (p *Peer) markAnswered() {
	select {
	case p.answerCh <- struct{}{}:
	default:
	}
}
