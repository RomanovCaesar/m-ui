package app

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const inboundSyncWirePath = "/_m-ui/peer/inbound/v1"
const inboundSyncWireLimit = 6 << 20

// Separate from membership gossip: credentials are encrypted with fresh X25519
// session keys, even on HTTP panels. Both ephemeral keys and every ciphertext
// are authenticated by the already-paired Ed25519 node identities.
type inboundSyncWire struct {
	Version    int      `json:"version"`
	Kind       string   `json:"kind"`
	Sender     PeerNode `json:"sender"`
	Recipient  string   `json:"recipient"`
	Timestamp  int64    `json:"timestamp"`
	Nonce      string   `json:"nonce"`
	ReplyTo    string   `json:"replyTo,omitempty"`
	Session    string   `json:"session,omitempty"`
	Key        string   `json:"key,omitempty"`
	Ciphertext string   `json:"ciphertext,omitempty"`
	Signature  string   `json:"signature"`
}
type inboundSyncSession struct {
	Sender  string
	Kind    string
	Key     []byte
	Expires time.Time
}

func syncWireBytes(message inboundSyncWire) []byte {
	message.Signature = ""
	data, _ := json.Marshal(message)
	return append([]byte("m-ui-inbound-sync-v1\n"), data...)
}
func (p *PeerNetwork) signSyncWire(message *inboundSyncWire) error {
	message.Version = 1
	message.Timestamp = time.Now().Unix()
	if err := randomToken(&message.Nonce); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	message.Sender = p.disk.Self
	key, _ := base64.StdEncoding.DecodeString(p.disk.PrivateKey)
	message.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, syncWireBytes(*message)))
	return nil
}
func (p *PeerNetwork) verifySyncWire(message inboundSyncWire, kind, reply, expectedID string, request bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if message.Version != 1 || message.Kind != kind || message.ReplyTo != reply || message.Recipient != p.disk.Self.ID || !peerNoncePattern.MatchString(message.Nonce) {
		return fmt.Errorf("同步协议格式无效")
	}
	if delta := time.Now().Unix() - message.Timestamp; delta > 120 || delta < -120 {
		return fmt.Errorf("请校准两台服务器时间")
	}
	node, ok := p.disk.Peers[message.Sender.ID]
	if !ok || p.disk.Blocked[node.ID] || (expectedID != "" && node.ID != expectedID) || node.PublicKey != message.Sender.PublicKey {
		return fmt.Errorf("对等节点未受信任或已断开")
	}
	if err := verifyPeerNode(message.Sender); err != nil {
		return err
	}
	key, _ := base64.StdEncoding.DecodeString(node.PublicKey)
	signature, err := base64.StdEncoding.DecodeString(message.Signature)
	if err != nil || !ed25519.Verify(key, syncWireBytes(message), signature) {
		return fmt.Errorf("同步消息签名无效")
	}
	if request {
		for key, expires := range p.replays {
			if time.Now().After(expires) {
				delete(p.replays, key)
			}
		}
		id := "inbound:" + node.ID + ":" + message.Nonce
		if _, ok := p.replays[id]; ok {
			return fmt.Errorf("重复的同步消息")
		}
		if len(p.replays) >= 4096 {
			return fmt.Errorf("同步请求过多")
		}
		p.replays[id] = time.Now().Add(4 * time.Minute)
	}
	return nil
}
func syncSessionKey(private *ecdh.PrivateKey, remote, from, to string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(remote)
	if err != nil {
		return nil, err
	}
	public, err := ecdh.X25519().NewPublicKey(data)
	if err != nil {
		return nil, err
	}
	secret, err := private.ECDH(public)
	if err != nil {
		return nil, err
	}
	// Domain-separate this random shared secret from all other panel protocols.
	h := sha256.New()
	h.Write([]byte("m-ui-inbound-sync-key-v1\n" + from + "\n" + to + "\n"))
	h.Write(secret)
	return h.Sum(nil), nil
}
func syncAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func encryptSync(key, data []byte, context string) (string, error) {
	gcm, err := syncAEAD(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, data, []byte(context))), nil
}
func decryptSync(key []byte, encoded, context string) ([]byte, error) {
	gcm, err := syncAEAD(key)
	if err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) < gcm.NonceSize() {
		return nil, fmt.Errorf("同步密文无效")
	}
	return gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], []byte(context))
}

func (a *App) serveInboundSyncPeer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.peers == nil || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(15 * time.Second))
	_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, inboundSyncWireLimit))
	decoder.DisallowUnknownFields()
	var message inboundSyncWire
	if err := decoder.Decode(&message); err != nil {
		http.Error(w, "invalid message", 400)
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid trailing data", 400)
		return
	}
	if message.Kind != "hello" && message.Kind != "apply" && message.Kind != "pull-hello" && message.Kind != "pull" {
		http.Error(w, "unsupported operation", 400)
		return
	}
	if err := a.peers.verifySyncWire(message, message.Kind, "", "", true); err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	reply := inboundSyncWire{Kind: message.Kind + "-reply", Recipient: message.Sender.ID, ReplyTo: message.Nonce}
	if message.Kind == "hello" || message.Kind == "pull-hello" {
		if message.Ciphertext != "" || message.Session != "" {
			http.Error(w, "invalid hello", 400)
			return
		}
		private, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			http.Error(w, "key generation failed", 500)
			return
		}
		key, err := syncSessionKey(private, message.Key, message.Sender.ID, message.Recipient)
		if err != nil {
			http.Error(w, "invalid session key", 400)
			return
		}
		reply.Key = base64.StdEncoding.EncodeToString(private.PublicKey().Bytes())
		if err = a.peers.signSyncWire(&reply); err != nil {
			http.Error(w, "signing failed", 500)
			return
		}
		a.inboundSync.mu.Lock()
		if a.inboundSync.sessions == nil {
			a.inboundSync.sessions = map[string]inboundSyncSession{}
		}
		for id, s := range a.inboundSync.sessions {
			if time.Now().After(s.Expires) {
				delete(a.inboundSync.sessions, id)
			}
		}
		if len(a.inboundSync.sessions) >= 256 {
			a.inboundSync.mu.Unlock()
			http.Error(w, "too many sessions", 429)
			return
		}
		sessionKind := "apply"
		if message.Kind == "pull-hello" {
			sessionKind = "pull"
		}
		a.inboundSync.sessions[reply.Nonce] = inboundSyncSession{Sender: message.Sender.ID, Kind: sessionKind, Key: key, Expires: time.Now().Add(time.Minute)}
		a.inboundSync.mu.Unlock()
	} else {
		a.inboundSync.mu.Lock()
		session, ok := a.inboundSync.sessions[message.Session]
		if ok && session.Sender == message.Sender.ID {
			delete(a.inboundSync.sessions, message.Session)
		}
		a.inboundSync.mu.Unlock()
		expectedKind := "apply"
		if message.Kind == "pull" {
			expectedKind = "pull"
		}
		if !ok || session.Sender != message.Sender.ID || session.Kind != expectedKind || time.Now().After(session.Expires) {
			http.Error(w, "session expired or already used", 409)
			return
		}
		data, err := decryptSync(session.Key, message.Ciphertext, "request:"+message.Session)
		if err != nil || len(data) > maxSyncPayload {
			http.Error(w, "invalid encrypted payload", 400)
			return
		}
		var resultData []byte
		if message.Kind == "apply" {
			var items []Inbound
			if json.Unmarshal(data, &items) != nil || len(items) == 0 || len(items) > maxSyncInbounds {
				http.Error(w, "invalid inbound list", 400)
				return
			}
			count := 0
			seen := map[string]bool{}
			for _, in := range items {
				count += len(in.Clients)
				if seen[in.ID] {
					http.Error(w, "duplicate inbound", 400)
					return
				}
				seen[in.ID] = true
			}
			if count > maxSyncClients {
				http.Error(w, "too many clients", 400)
				return
			}
			// Serialize revocation with the actual mutation, not just the handshake.
			a.peers.mu.Lock()
			_, trusted := a.peers.disk.Peers[message.Sender.ID]
			if !trusted || a.peers.disk.Blocked[message.Sender.ID] {
				a.peers.mu.Unlock()
				http.Error(w, "peer disconnected", 403)
				return
			}
			results := a.manager.receiveSyncedInbounds(message.Sender.ID, items)
			a.peers.mu.Unlock()
			resultData, _ = json.Marshal(results)
		} else {
			inbounds, tokens, err := a.crossPanelPullInbounds()
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			resultData, _ = json.Marshal(crossPanelPullPayload{Inbounds: inbounds, Tokens: tokens})
		}
		reply.Session = message.Session
		reply.Ciphertext, err = encryptSync(session.Key, resultData, "response:"+message.Session)
		if err != nil {
			http.Error(w, "result encryption failed", 500)
			return
		}
		if err = a.peers.signSyncWire(&reply); err != nil {
			http.Error(w, "signing failed", 500)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reply)
}

func (a *App) syncWireExchange(ctx context.Context, node PeerNode, message inboundSyncWire) (inboundSyncWire, error) {
	var reply inboundSyncWire
	a.peers.mu.Lock()
	known, trusted := a.peers.disk.Peers[node.ID]
	trusted = trusted && !a.peers.disk.Blocked[node.ID] && known.PublicKey == node.PublicKey
	a.peers.mu.Unlock()
	if !trusted {
		return reply, fmt.Errorf("目标面板已断开，同步已停止")
	}
	if err := a.peers.signSyncWire(&message); err != nil {
		return reply, err
	}
	data, _ := json.Marshal(message)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, node.Endpoint+inboundSyncWirePath, bytes.NewReader(data))
	if err != nil {
		return reply, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Retain TLS validation, no proxies, and no redirects from the mesh client.
	client := *a.peers.client
	client.Timeout = 25 * time.Second
	response, err := client.Do(req)
	if err != nil {
		return reply, fmt.Errorf("连接中断或超时；结果可能未返回，请检查目标后重试（重复同步会合并）")
	}
	defer response.Body.Close()
	if response.StatusCode == 404 {
		return reply, fmt.Errorf("目标面板版本不支持 Sync Inbound，请先升级目标面板")
	}
	if response.StatusCode != 200 {
		return reply, fmt.Errorf("目标拒绝同步 (HTTP %d)，请检查联机状态和版本", response.StatusCode)
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, inboundSyncWireLimit+1))
	if err != nil || len(data) > inboundSyncWireLimit || json.Unmarshal(data, &reply) != nil {
		return reply, fmt.Errorf("目标同步响应无效，请检查目标面板结果")
	}
	if err = a.peers.verifySyncWire(reply, message.Kind+"-reply", message.Nonce, node.ID, false); err != nil {
		return reply, err
	}
	return reply, nil
}
func (a *App) sendInboundSync(ctx context.Context, node PeerNode, items []Inbound) ([]inboundSyncResult, error) {
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	hello, err := a.syncWireExchange(ctx, node, inboundSyncWire{Kind: "hello", Recipient: node.ID, Key: base64.StdEncoding.EncodeToString(private.PublicKey().Bytes())})
	if err != nil {
		return nil, err
	}
	selfID := a.peers.view().Self.ID
	key, err := syncSessionKey(private, hello.Key, selfID, node.ID)
	if err != nil {
		return nil, fmt.Errorf("目标会话密钥无效")
	}
	data, _ := json.Marshal(items)
	encoded, err := encryptSync(key, data, "request:"+hello.Nonce)
	if err != nil {
		return nil, err
	}
	reply, err := a.syncWireExchange(ctx, node, inboundSyncWire{Kind: "apply", Recipient: node.ID, Session: hello.Nonce, Ciphertext: encoded})
	if err != nil {
		return nil, err
	}
	if reply.Session != hello.Nonce {
		return nil, fmt.Errorf("同步结果与任务不符")
	}
	data, err = decryptSync(key, reply.Ciphertext, "response:"+hello.Nonce)
	if err != nil {
		return nil, fmt.Errorf("同步结果解密失败")
	}
	var results []inboundSyncResult
	if json.Unmarshal(data, &results) != nil || len(results) != len(items) {
		return nil, fmt.Errorf("同步结果数量不符，请检查目标面板")
	}
	for i, result := range results {
		if result.ID != items[i].ID || (result.Status != "created" && result.Status != "updated" && result.Status != "failed") {
			return nil, fmt.Errorf("同步结果与入站不符")
		}
	}
	return results, nil
}

func (a *App) sendInboundPull(ctx context.Context, node PeerNode) ([]Inbound, map[string]string, error) {
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	hello, err := a.syncWireExchange(ctx, node, inboundSyncWire{Kind: "pull-hello", Recipient: node.ID, Key: base64.StdEncoding.EncodeToString(private.PublicKey().Bytes())})
	if err != nil {
		return nil, nil, err
	}
	selfID := a.peers.view().Self.ID
	key, err := syncSessionKey(private, hello.Key, selfID, node.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("目标会话密钥无效")
	}
	encoded, err := encryptSync(key, []byte("{}"), "request:"+hello.Nonce)
	if err != nil {
		return nil, nil, err
	}
	reply, err := a.syncWireExchange(ctx, node, inboundSyncWire{Kind: "pull", Recipient: node.ID, Session: hello.Nonce, Ciphertext: encoded})
	if err != nil {
		return nil, nil, err
	}
	if reply.Session != hello.Nonce {
		return nil, nil, fmt.Errorf("拉取结果与任务不符")
	}
	data, err := decryptSync(key, reply.Ciphertext, "response:"+hello.Nonce)
	if err != nil {
		return nil, nil, fmt.Errorf("拉取结果解密失败")
	}
	var payload crossPanelPullPayload
	if json.Unmarshal(data, &payload) != nil || len(payload.Inbounds) > maxSyncInbounds {
		return nil, nil, fmt.Errorf("目标返回的入站信息无效")
	}
	clients := 0
	for _, inbound := range payload.Inbounds {
		clients += len(inbound.Clients)
	}
	if clients > maxSyncClients {
		return nil, nil, fmt.Errorf("目标返回的 Client 数量超过限制")
	}
	for name, token := range payload.Tokens {
		if strings.TrimSpace(name) == "" || !validSubscriptionToken(token) {
			delete(payload.Tokens, name)
		}
	}
	return payload.Inbounds, payload.Tokens, nil
}
