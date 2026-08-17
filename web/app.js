// 极简信令客户端：原生 WebSocket + RTCPeerConnection。
// 约定：服务端始终发起 offer，客户端只回答。

const $ = (id) => document.getElementById(id);

const state = {
  ws: null,
  role: null, // 'publisher' | 'viewer' | null
  room: '',
  pc: null,
  localStream: null,
  remoteStream: null,
  stun: [],
};

async function init() {
  $('publish').addEventListener('click', startPublish);
  $('watch').addEventListener('click', startWatch);
  $('leave').addEventListener('click', leave);

  try {
    const res = await fetch('/api/config');
    const cfg = await res.json();
    state.stun = cfg.stun || [];
  } catch (err) {
    console.warn('获取 STUN 配置失败', err);
  }

  connectWS();
}

function connectWS() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  state.ws = new WebSocket(`${proto}://${location.host}/ws`);

  state.ws.onmessage = (ev) => {
    let msg;
    try {
      msg = JSON.parse(ev.data);
    } catch {
      return;
    }
    handleMessage(msg);
  };

  state.ws.onclose = () => {
    setStatus('信令连接已断开');
    cleanupPeer();
  };
}

function send(obj) {
  if (state.ws && state.ws.readyState === WebSocket.OPEN) {
    state.ws.send(JSON.stringify(obj));
  }
}

async function handleMessage(msg) {
  switch (msg.type) {
    case 'joined':
      setStatus(
        `已加入房间 ${msg.room}，身份：${msg.role === 'publisher' ? '主播' : '观众'}`,
      );
      $('leave').disabled = false;
      break;

    case 'offer':
      await handleOffer(msg.sdp);
      break;

    case 'ice':
      if (state.pc) {
        state.pc.addIceCandidate(msg.candidate).catch(console.warn);
      }
      break;

    case 'publisher-left':
      setStatus('主播已离开，等待下一个主播…');
      $('remote').srcObject = null;
      break;

    case 'error':
      setStatus('错误：' + msg.message);
      break;
  }
}

async function startPublish() {
  if (state.role) return;
  const room = requireRoom();
  if (!room) return;

  if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
    setStatus('当前页面不是 HTTPS/localhost，浏览器禁用了摄像头/麦克风 API');
    return;
  }

  try {
    state.localStream = await navigator.mediaDevices.getUserMedia({
      video: { width: { ideal: 1280 }, height: { ideal: 720 } },
      audio: true,
    });
  } catch (err) {
    setStatus('无法访问摄像头/麦克风：' + err.message);
    return;
  }

  $('preview').srcObject = state.localStream;
  state.role = 'publisher';
  state.room = room;
  send({ type: 'join', room, role: 'publisher' });
}

async function startWatch() {
  if (state.role) return;
  const room = requireRoom();
  if (!room) return;

  state.role = 'viewer';
  state.room = room;
  send({ type: 'join', room, role: 'viewer' });
}

function requireRoom() {
  const room = $('room').value.trim();
  if (!room) {
    setStatus('请先输入房间号');
    return null;
  }
  return room;
}

function makePeer() {
  cleanupPeer();
  state.remoteStream = new MediaStream();

  state.pc = new RTCPeerConnection({
    iceServers: state.stun.map((urls) => ({ urls })),
  });

  state.pc.onicecandidate = (ev) => {
    if (ev.candidate) send({ type: 'ice', candidate: ev.candidate.toJSON() });
  };

  state.pc.onconnectionstatechange = () => {
    const st = state.pc.connectionState;
    setStatus(`WebRTC 连接状态：${st}`);
  };

  state.pc.ontrack = (ev) => {
    // 服务端转发来的轨道（观众角色）
    state.remoteStream.addTrack(ev.track);
    $('remote').srcObject = state.remoteStream;
    ev.track.onended = () => {
      state.remoteStream.removeTrack(ev.track);
    };
  };

  return state.pc;
}

async function handleOffer(sdp) {
  const pc = makePeer();

  if (state.role === 'publisher' && state.localStream) {
    // 主播：把本地轨道挂到服务端预置的 recvonly transceiver 上
    for (const track of state.localStream.getTracks()) {
      pc.addTrack(track, state.localStream);
    }
  }

  await pc.setRemoteDescription(sdp);
  const answer = await pc.createAnswer();
  await pc.setLocalDescription(answer);
  send({ type: 'answer', sdp: pc.localDescription });
}

function cleanupPeer() {
  if (state.pc) {
    state.pc.ontrack = null;
    state.pc.onicecandidate = null;
    state.pc.close();
    state.pc = null;
  }
  if (state.localStream) {
    for (const t of state.localStream.getTracks()) t.stop();
    state.localStream = null;
  }
  state.remoteStream = null;
  $('preview').srcObject = null;
  $('remote').srcObject = null;
}

function leave() {
  send({ type: 'leave' });
  cleanupPeer();
  state.role = null;
  state.room = '';
  $('leave').disabled = true;
  setStatus('已离开房间');
}

function setStatus(text) {
  $('status').textContent = text;
}

init();
