package sfu

import (
	"fmt"
	"sync"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

const negotiationTimeout = 15 * time.Second

// Room 是一个直播间：每房间 1 个主播 + N 个观众。
type Room struct {
	ID  string
	hub *Hub

	mu      sync.RWMutex
	pub     *Peer
	viewers map[string]*Peer
	relays  []*TrackRelay
}

func newRoom(id string, hub *Hub) *Room {
	return &Room{ID: id, hub: hub, viewers: make(map[string]*Peer)}
}

func (r *Room) Logf(format string, args ...any) {
	r.hub.Logf("room %s: "+format, append([]any{r.ID}, args...)...)
}

func (r *Room) isEmpty() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.pub == nil && len(r.viewers) == 0
}

// AttachPublisher 绑定主播；房间已有主播则报错。
func (r *Room) AttachPublisher(p *Peer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pub != nil {
		return fmt.Errorf("room %s already has a publisher", r.ID)
	}
	r.pub = p
	return nil
}

// AddViewer 添加观众，并为其挂上已存在的转发轨道。
func (r *Room) AddViewer(p *Peer) {
	r.mu.Lock()
	r.viewers[p.ID] = p
	relays := append([]*TrackRelay(nil), r.relays...)
	r.mu.Unlock()

	for _, relay := range relays {
		sender, err := p.PC.AddTrack(relay.local)
		if err != nil {
			r.Logf("attach relay %s to viewer %s: %v", relay.ID, p.ID, err)
			continue
		}
		p.addSender(relay.ID, sender)
	}
}

// HandlePublisherTrack 在主播新轨道到达时创建转发器，并挂到所有在线观众上。
func (r *Room) HandlePublisherTrack(p *Peer, remote *webrtc.TrackRemote) {
	r.Logf("publisher track: %s/%s (%s)", remote.StreamID(), remote.ID(), remote.Codec().MimeType)
	local, err := webrtc.NewTrackLocalStaticRTP(remote.Codec().RTPCodecCapability, remote.ID(), remote.StreamID())
	if err != nil {
		r.Logf("create local track for %s: %v", remote.ID(), err)
		return
	}

	relay := &TrackRelay{
		ID:     fmt.Sprintf("%s/%s", remote.StreamID(), remote.ID()),
		remote: remote,
		local:  local,
		room:   r,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}

	r.mu.Lock()
	r.relays = append(r.relays, relay)
	viewers := make([]*Peer, 0, len(r.viewers))
	for _, v := range r.viewers {
		viewers = append(viewers, v)
	}
	r.mu.Unlock()

	for _, v := range viewers {
		sender, err := v.PC.AddTrack(relay.local)
		if err != nil {
			r.Logf("attach relay %s to viewer %s: %v", relay.ID, v.ID, err)
			continue
		}
		v.addSender(relay.ID, sender)
		go r.negotiate(v)
	}

	relay.start()
}

// RemovePeer 处理对端离开：主播离开时停止转发、
// 移除观众上的轨道并重新协商，让观众连接保持在线。
func (r *Room) RemovePeer(p *Peer) {
	r.mu.Lock()
	if p.Role == RolePublisher && r.pub == p {
		r.pub = nil
		relays := r.relays
		r.relays = nil
		viewers := make([]*Peer, 0, len(r.viewers))
		for _, v := range r.viewers {
			viewers = append(viewers, v)
		}
		r.mu.Unlock()

		for _, relay := range relays {
			relay.stopForwarding()
		}
		_ = p.PC.Close() // 让 ReadRTP 退出阻塞
		for _, relay := range relays {
			relay.waitDone()
		}

		for _, v := range viewers {
			for _, s := range v.takeSenders() {
				if err := v.PC.RemoveTrack(s); err != nil {
					r.Logf("remove track from viewer %s: %v", v.ID, err)
				}
			}
			go r.negotiate(v)
			v.Send(Message{Type: "publisher-left", Room: r.ID})
		}
	} else {
		if _, ok := r.viewers[p.ID]; ok {
			delete(r.viewers, p.ID)
		}
		r.mu.Unlock()
	}

	p.Close()
	if r.isEmpty() {
		r.hub.removeRoom(r.ID)
	}
}

// Negotiate 发起一次协商（创建 offer 并等待 answer），
// 供连接层在 join 后调用。
func (r *Room) Negotiate(p *Peer) { r.negotiate(p) }

// negotiate 串行化协商：同一对端同一时刻只允许一个 offer 在途。
func (r *Room) negotiate(p *Peer) {
	p.negMu.Lock()
	defer p.negMu.Unlock()

	select {
	case <-p.closedCh:
		return
	default:
	}

	offer, err := p.PC.CreateOffer(nil)
	if err != nil {
		r.Logf("create offer for %s: %v", p.ID, err)
		return
	}
	if err := p.PC.SetLocalDescription(offer); err != nil {
		r.Logf("set local description for %s: %v", p.ID, err)
		return
	}
	p.Send(Message{Type: "offer", Room: r.ID, SDP: &offer})

	select {
	case <-p.answerCh:
	case <-p.closedCh:
	case <-time.After(negotiationTimeout):
		r.Logf("negotiation with %s timed out", p.ID)
	}
}

// HandleAnswer 处理客户端 answer。
func (r *Room) HandleAnswer(p *Peer, answer webrtc.SessionDescription) {
	if err := p.PC.SetRemoteDescription(answer); err != nil {
		r.Logf("set remote description for %s: %v", p.ID, err)
		return
	}
	p.markAnswered()
}

// RequestKeyframe 向主播发送 PLI，请编码器尽快输出一个关键帧。
func (r *Room) RequestKeyframe(remote *webrtc.TrackRemote) {
	r.mu.RLock()
	pub := r.pub
	r.mu.RUnlock()
	if pub == nil {
		return
	}
	if err := pub.PC.WriteRTCP([]rtcp.Packet{
		&rtcp.PictureLossIndication{MediaSSRC: uint32(remote.SSRC())},
	}); err != nil {
		r.Logf("request keyframe for %s: %v", remote.ID(), err)
	}
}
