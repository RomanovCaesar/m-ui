package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const peerWirePath = "/_m-ui/peer/v1"
const maxMeshPeers = 128
const peerHeartbeat = 15 * time.Second
const peerWireLimit = 256 << 10

// Only self-signed public descriptors travel through the mesh. Pairing tokens
// and private keys never leave the local control plane.
type PeerNode struct {
	ID        string `json:"id"`
	PublicKey string `json:"publicKey"`
	Name      string `json:"name"`
	Endpoint  string `json:"endpoint"`
	Revision  uint64 `json:"revision"`
	Signature string `json:"signature"`
}

type peerDisk struct {
	PrivateKey string              `json:"privateKey"`
	Token      string              `json:"pairingToken,omitempty"`
	Self       PeerNode            `json:"self"`
	Peers      map[string]PeerNode `json:"peers"`
	Blocked    map[string]bool     `json:"blocked,omitempty"`
}

type peerMessage struct {
	Version   int        `json:"version"`
	Kind      string     `json:"kind"`
	Sender    PeerNode   `json:"sender"`
	Peers     []PeerNode `json:"peers,omitempty"`
	Timestamp int64      `json:"timestamp"`
	Nonce     string     `json:"nonce"`
	ReplyTo   string     `json:"replyTo,omitempty"`
	Signature string     `json:"signature"`
	MAC       string     `json:"mac,omitempty"`
}

type peerStatus struct {
	LastSeen    time.Time `json:"lastSeen"`
	LastAttempt time.Time `json:"lastAttempt"`
	Error       string    `json:"error,omitempty"`
	Failures    int       `json:"-"`
	NextAttempt time.Time `json:"-"`
}

type peerRow struct {
	PeerNode
	peerStatus
	Online bool `json:"online"`
}

type peerView struct {
	Self         PeerNode  `json:"self"`
	TokenEnabled bool      `json:"tokenEnabled"`
	Peers        []peerRow `json:"peers"`
	Blocked      []string  `json:"blocked"`
	MaxPeers     int       `json:"maxPeers"`
}

type PeerNetwork struct {
	mu         sync.Mutex
	syncMu     sync.Mutex
	disk       peerDisk
	path       string
	status     map[string]peerStatus
	replays    map[string]time.Time
	pairWindow time.Time
	pairCount  int
	client     *http.Client
	wake       chan struct{}
}

func newPeerNetwork(dataDir string) (*PeerNetwork, error) {
	p := &PeerNetwork{path: filepath.Join(dataDir, "multi-control.json"), status: map[string]peerStatus{}, replays: map[string]time.Time{}, wake: make(chan struct{}, 1)}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxIdleConns = 32
	transport.MaxIdleConnsPerHost = 2
	transport.ResponseHeaderTimeout = 6 * time.Second
	p.client = &http.Client{Transport: transport, Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	data, err := os.ReadFile(p.path)
	if err == nil {
		if err = json.Unmarshal(data, &p.disk); err != nil {
			return nil, fmt.Errorf("Multi-control 状态无效: %w", err)
		}
		if err = p.validateDisk(); err != nil {
			return nil, err
		}
		return p, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	name, _ := os.Hostname()
	if name == "" {
		name = "m-ui"
	}
	if len(name) > 128 {
		name = name[:128]
	}
	p.disk = peerDisk{PrivateKey: base64.StdEncoding.EncodeToString(private), Self: PeerNode{ID: peerID(pub), PublicKey: base64.StdEncoding.EncodeToString(pub), Name: name, Revision: 1}, Peers: map[string]PeerNode{}, Blocked: map[string]bool{}}
	p.signSelf(&p.disk)
	if err = p.persist(p.disk); err != nil {
		return nil, err
	}
	return p, nil
}

func peerID(public ed25519.PublicKey) string {
	sum := sha256.Sum256(public)
	return hex.EncodeToString(sum[:16])
}
func peerNodeBytes(node PeerNode) []byte {
	node.Signature = ""
	data, _ := json.Marshal(node)
	return append([]byte("m-ui-peer-node-v1\n"), data...)
}
func (p *PeerNetwork) signSelf(disk *peerDisk) {
	key, _ := base64.StdEncoding.DecodeString(disk.PrivateKey)
	disk.Self.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(key), peerNodeBytes(disk.Self)))
}

func canonicalPeerEndpoint(endpoint string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u == nil || !containsString([]string{"http", "https"}, u.Scheme) || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("联机地址必须是 http(s)://主机:面板端口，不含路径")
	}
	host := strings.ToLower(u.Hostname())
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || host == "" {
		return "", fmt.Errorf("联机地址缺少主机或有效端口")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.Zone() != "" {
			return "", fmt.Errorf("联机地址不能使用通配、组播或链路本地地址")
		}
		host = ip.String()
	} else if err := validatePanelDomain(host); err != nil {
		return "", fmt.Errorf("联机主机名无效")
	}
	return u.Scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func verifyPeerNode(node PeerNode) error {
	key, err := base64.StdEncoding.DecodeString(node.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize || peerID(key) != node.ID || node.Revision == 0 || len(node.Name) > 128 || strings.ContainsAny(node.Name, "\r\n") {
		return fmt.Errorf("节点身份无效")
	}
	if node.Endpoint != "" {
		endpoint, err := canonicalPeerEndpoint(node.Endpoint)
		if err != nil || endpoint != node.Endpoint {
			return fmt.Errorf("节点联机地址无效")
		}
	}
	sig, err := base64.StdEncoding.DecodeString(node.Signature)
	if err != nil || !ed25519.Verify(key, peerNodeBytes(node), sig) {
		return fmt.Errorf("节点身份签名无效")
	}
	return nil
}

func (p *PeerNetwork) validateDisk() error {
	key, err := base64.StdEncoding.DecodeString(p.disk.PrivateKey)
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return fmt.Errorf("Multi-control 私钥无效")
	}
	if !bytes.Equal(ed25519.NewKeyFromSeed(key[:32]), key) || p.disk.Self.PublicKey != base64.StdEncoding.EncodeToString(key[32:]) {
		return fmt.Errorf("Multi-control 身份与私钥不符")
	}
	if err := verifyPeerNode(p.disk.Self); err != nil {
		return err
	}
	if p.disk.Token != "" && !validPairToken(p.disk.Token) {
		return fmt.Errorf("Multi-control 配对 token 无效")
	}
	if len(p.disk.Peers) > maxMeshPeers {
		return fmt.Errorf("Multi-control 节点数量超过限制")
	}
	for id, blocked := range p.disk.Blocked {
		if !blocked || !peerIDPattern.MatchString(id) || id == p.disk.Self.ID {
			return fmt.Errorf("Multi-control 断开记录无效")
		}
		if _, exists := p.disk.Peers[id]; exists {
			return fmt.Errorf("Multi-control 节点同时处于连接和断开状态")
		}
	}
	for id, node := range p.disk.Peers {
		if id != node.ID || id == p.disk.Self.ID {
			return fmt.Errorf("Multi-control 节点记录无效")
		}
		if err := verifyPeerNode(node); err != nil {
			return err
		}
	}
	if p.disk.Peers == nil {
		p.disk.Peers = map[string]PeerNode{}
	}
	if p.disk.Blocked == nil {
		p.disk.Blocked = map[string]bool{}
	}
	return nil
}

func (p *PeerNetwork) persist(disk peerDisk) error {
	data, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(p.path), ".multi-control-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), p.path)
}

func clonePeerDisk(disk peerDisk) peerDisk {
	copy := disk
	copy.Peers = make(map[string]PeerNode, len(disk.Peers))
	copy.Blocked = make(map[string]bool, len(disk.Blocked))
	for id, node := range disk.Peers {
		copy.Peers[id] = node
	}
	for id, value := range disk.Blocked {
		copy.Blocked[id] = value
	}
	return copy
}

func (p *PeerNetwork) configure(name, endpoint string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 || strings.ContainsAny(name, "\r\n") {
		return fmt.Errorf("节点名称需为 1-128 字符")
	}
	endpoint, err := canonicalPeerEndpoint(endpoint)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.disk.Self.Name == name && p.disk.Self.Endpoint == endpoint {
		return nil
	}
	next := clonePeerDisk(p.disk)
	next.Self.Name = name
	next.Self.Endpoint = endpoint
	next.Self.Revision++
	p.signSelf(&next)
	if err = p.persist(next); err != nil {
		return err
	}
	p.disk = next
	p.signal()
	return nil
}

func validPairToken(value string) bool {
	return peerTokenPattern.MatchString(value) && strings.ContainsAny(value, "abcdefghijklmnopqrstuvwxyz") && strings.ContainsAny(value, "0123456789")
}

var peerTokenPattern = regexp.MustCompile(`^[a-z0-9]{16,32}$`)
var peerIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var peerNoncePattern = regexp.MustCompile(`^[0-9a-f]{36}$`)

func newPairToken() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	for {
		result := make([]byte, 0, 16)
		for len(result) < 16 {
			var b [1]byte
			if _, err := rand.Read(b[:]); err != nil {
				return "", err
			}
			if b[0] < 252 {
				result = append(result, alphabet[int(b[0])%36])
			}
		}
		if validPairToken(string(result)) {
			return string(result), nil
		}
	}
}

func (p *PeerNetwork) setToken(token string) error {
	token = strings.TrimSpace(token)
	if !validPairToken(token) {
		return fmt.Errorf("配对 token 必须为 16 至 32 位小写字母和数字混合")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.disk.Self.Endpoint == "" {
		return fmt.Errorf("请先设置本机联机地址")
	}
	next := clonePeerDisk(p.disk)
	next.Token = token
	if err := p.persist(next); err != nil {
		return err
	}
	p.disk = next
	return nil
}

func (p *PeerNetwork) rotateToken() (string, error) {
	token, err := newPairToken()
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.disk.Self.Endpoint == "" {
		return "", fmt.Errorf("请先设置本机联机地址")
	}
	next := clonePeerDisk(p.disk)
	next.Token = token
	if err = p.persist(next); err != nil {
		return "", err
	}
	p.disk = next
	return token, nil
}
func (p *PeerNetwork) disableToken() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	next := clonePeerDisk(p.disk)
	next.Token = ""
	if err := p.persist(next); err != nil {
		return err
	}
	p.disk = next
	return nil
}
func (p *PeerNetwork) view() peerView {
	p.mu.Lock()
	defer p.mu.Unlock()
	view := peerView{Self: p.disk.Self, TokenEnabled: p.disk.Token != "", Peers: []peerRow{}, Blocked: []string{}, MaxPeers: maxMeshPeers}
	for id, node := range p.disk.Peers {
		status := p.status[id]
		view.Peers = append(view.Peers, peerRow{node, status, !status.LastSeen.IsZero() && time.Since(status.LastSeen) < 45*time.Second})
	}
	slices.SortFunc(view.Peers, func(a, b peerRow) int { return strings.Compare(a.ID, b.ID) })
	for id := range p.disk.Blocked {
		view.Blocked = append(view.Blocked, id)
	}
	slices.Sort(view.Blocked)
	return view
}

func peerMessageBytes(message peerMessage) []byte {
	message.Signature = ""
	message.MAC = ""
	data, _ := json.Marshal(message)
	return append([]byte("m-ui-peer-message-v1\n"), data...)
}
func peerMAC(message peerMessage, token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write(peerMessageBytes(message))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func (p *PeerNetwork) messageLocked(kind, reply, token string, includePeers bool) peerMessage {
	message := peerMessage{Version: 1, Kind: kind, Sender: p.disk.Self, Timestamp: time.Now().Unix(), ReplyTo: reply}
	_ = randomToken(&message.Nonce)
	if includePeers {
		for _, node := range p.disk.Peers {
			message.Peers = append(message.Peers, node)
		}
		slices.SortFunc(message.Peers, func(a, b PeerNode) int { return strings.Compare(a.ID, b.ID) })
	}
	key, _ := base64.StdEncoding.DecodeString(p.disk.PrivateKey)
	message.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(key), peerMessageBytes(message)))
	if token != "" {
		message.MAC = peerMAC(message, token)
	}
	return message
}

func verifyPeerMessage(message peerMessage, kind, reply string) error {
	if message.Version != 1 || message.Kind != kind || message.ReplyTo != reply || !peerNoncePattern.MatchString(message.Nonce) || len(message.Peers) > maxMeshPeers {
		return fmt.Errorf("联机协议格式无效")
	}
	if delta := time.Now().Unix() - message.Timestamp; delta > 120 || delta < -120 {
		return fmt.Errorf("联机消息时间无效，请校准服务器时间")
	}
	if err := verifyPeerNode(message.Sender); err != nil {
		return err
	}
	if message.Sender.Endpoint == "" {
		return fmt.Errorf("对方未公布联机地址")
	}
	key, _ := base64.StdEncoding.DecodeString(message.Sender.PublicKey)
	sig, err := base64.StdEncoding.DecodeString(message.Signature)
	if err != nil || !ed25519.Verify(key, peerMessageBytes(message), sig) {
		return fmt.Errorf("联机消息签名无效")
	}
	for _, node := range message.Peers {
		if err := verifyPeerNode(node); err != nil {
			return err
		}
	}
	return nil
}

func (p *PeerNetwork) acceptLocked(message peerMessage, explicit bool) error {
	if message.Sender.ID == p.disk.Self.ID {
		return fmt.Errorf("不能连接本面板或具有相同身份的副本")
	}
	if p.disk.Blocked[message.Sender.ID] && !explicit {
		return fmt.Errorf("该节点已在本面板断开")
	}
	next := clonePeerDisk(p.disk)
	changed := false
	if explicit && next.Blocked[message.Sender.ID] {
		delete(next.Blocked, message.Sender.ID)
		changed = true
	}
	nodes := append([]PeerNode{message.Sender}, message.Peers...)
	for _, node := range nodes {
		if node.ID == next.Self.ID || next.Blocked[node.ID] {
			continue
		}
		old, exists := next.Peers[node.ID]
		if exists && node.Revision <= old.Revision {
			continue
		}
		if !exists && len(next.Peers) >= maxMeshPeers {
			if node.ID == message.Sender.ID {
				return fmt.Errorf("已达到 %d 个对等节点上限", maxMeshPeers)
			}
			continue
		}
		next.Peers[node.ID] = node
		changed = true
	}
	if changed {
		if err := p.persist(next); err != nil {
			return err
		}
		p.disk = next
		p.signal()
	}
	status := p.status[message.Sender.ID]
	status.LastSeen = time.Now()
	status.Error = ""
	status.Failures = 0
	status.NextAttempt = time.Time{}
	p.status[message.Sender.ID] = status
	return nil
}

func (p *PeerNetwork) servePeer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(6 * time.Second))
	_ = controller.SetWriteDeadline(time.Now().Add(8 * time.Second))
	var message peerMessage
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, peerWireLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&message); err != nil {
		http.Error(w, "invalid message", 400)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		http.Error(w, "invalid trailing data", 400)
		return
	}
	if message.Kind != "pair" && message.Kind != "sync" {
		http.Error(w, "invalid operation", 400)
		return
	}
	p.mu.Lock()
	locked := true
	defer func() {
		if locked {
			p.mu.Unlock()
		}
	}()
	if message.Kind == "pair" {
		if time.Since(p.pairWindow) > time.Minute {
			p.pairWindow = time.Now()
			p.pairCount = 0
		}
		p.pairCount++
		if p.pairCount > 30 {
			http.Error(w, "pairing rate limit", 429)
			return
		}
		if p.disk.Token == "" || !hmac.Equal([]byte(message.MAC), []byte(peerMAC(message, p.disk.Token))) {
			http.Error(w, "invalid pairing token", 403)
			return
		}
	} else {
		if _, ok := p.disk.Peers[message.Sender.ID]; !ok || p.disk.Blocked[message.Sender.ID] {
			http.Error(w, "peer not trusted", 403)
			return
		}
	}
	if err := verifyPeerMessage(message, message.Kind, ""); err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	// Prevent a captured, signed old request from repeatedly refreshing liveness.
	for key, expires := range p.replays {
		if time.Now().After(expires) {
			delete(p.replays, key)
		}
	}
	key := message.Sender.ID + ":" + message.Nonce
	if _, exists := p.replays[key]; exists {
		http.Error(w, "replayed message", 409)
		return
	}
	if len(p.replays) >= 4096 {
		http.Error(w, "too many messages", 429)
		return
	}
	p.replays[key] = time.Now().Add(4 * time.Minute)
	if err := p.acceptLocked(message, false); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	token := ""
	if message.Kind == "pair" {
		token = p.disk.Token
	}
	reply := p.messageLocked(message.Kind+"-reply", message.Nonce, token, true)
	p.mu.Unlock()
	locked = false
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reply)
}

func (p *PeerNetwork) exchange(ctx context.Context, endpoint, kind, token, expectedID string) error {
	p.mu.Lock()
	message := p.messageLocked(kind, "", token, kind == "sync")
	p.mu.Unlock()
	data, _ := json.Marshal(message)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+peerWirePath, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("无法连接 %s：%w", endpoint, err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("联机失败 (HTTP %d)，请检查 token、对等信任和目标面板端口", response.StatusCode)
	}
	var reply peerMessage
	responseData, readErr := io.ReadAll(io.LimitReader(response.Body, peerWireLimit+1))
	if readErr != nil || len(responseData) > peerWireLimit {
		return fmt.Errorf("联机响应过大或无法读取")
	}
	if err = json.Unmarshal(responseData, &reply); err != nil {
		return fmt.Errorf("目标返回了无效的联机响应")
	}
	if err = verifyPeerMessage(reply, kind+"-reply", message.Nonce); err != nil {
		return err
	}
	if kind == "pair" && !hmac.Equal([]byte(reply.MAC), []byte(peerMAC(reply, token))) {
		return fmt.Errorf("目标未能证明持有配对 token")
	}
	if expectedID != "" && reply.Sender.ID != expectedID {
		return fmt.Errorf("目标节点身份发生变化，请重新配对")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// A user may disconnect this peer while its request is in flight.
	if kind == "sync" {
		if _, exists := p.disk.Peers[expectedID]; !exists {
			return fmt.Errorf("连接已断开")
		}
	}
	return p.acceptLocked(reply, kind == "pair")
}

func (p *PeerNetwork) connect(ctx context.Context, host string, port int, scheme, token string) error {
	p.mu.Lock()
	endpointReady := p.disk.Self.Endpoint != ""
	p.mu.Unlock()
	if !endpointReady {
		return fmt.Errorf("请先设置本机联机地址")
	}
	if !validPairToken(token) {
		return fmt.Errorf("Token 必须是 16 至 32 位小写字母和数字混合")
	}
	if scheme != "auto" && scheme != "http" && scheme != "https" {
		return fmt.Errorf("协议必须为 auto/http/https")
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	protocols := []string{scheme}
	if scheme == "auto" {
		protocols = []string{"https", "http"}
	}
	var last error
	for _, protocol := range protocols {
		endpoint, err := canonicalPeerEndpoint(protocol + "://" + net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			return err
		}
		if err = p.exchange(ctx, endpoint, "pair", token, ""); err == nil {
			return nil
		} else {
			last = err
		}
		if ctx.Err() != nil {
			break
		}
	}
	return last
}

func (p *PeerNetwork) disconnect(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.disk.Peers[id]; !exists {
		return fmt.Errorf("节点不存在")
	}
	next := clonePeerDisk(p.disk)
	delete(next.Peers, id)
	next.Blocked[id] = true
	if err := p.persist(next); err != nil {
		return err
	}
	p.disk = next
	delete(p.status, id)
	return nil
}

func (p *PeerNetwork) unblock(id string) error {
	if !peerIDPattern.MatchString(id) {
		return fmt.Errorf("节点 ID 无效")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	next := clonePeerDisk(p.disk)
	delete(next.Blocked, id)
	if err := p.persist(next); err != nil {
		return err
	}
	p.disk = next
	p.signal()
	return nil
}

func (p *PeerNetwork) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}
func (p *PeerNetwork) run(ctx context.Context) {
	ticker := time.NewTicker(peerHeartbeat)
	defer ticker.Stop()
	defer p.client.CloseIdleConnections()
	p.signal()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.syncPeers(ctx, false)
		case <-p.wake:
			// Membership/address changes must reach existing neighbors immediately,
			// even if their previous heartbeat set a future NextAttempt.
			p.syncPeers(ctx, true)
		}
	}
}

func (p *PeerNetwork) syncPeers(ctx context.Context, force bool) {
	if !p.syncMu.TryLock() {
		return
	}
	defer p.syncMu.Unlock()
	p.mu.Lock()
	nodes := []PeerNode{}
	for id, node := range p.disk.Peers {
		if force || time.Now().After(p.status[id].NextAttempt) {
			nodes = append(nodes, node)
		}
	}
	p.mu.Unlock()
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, node := range nodes {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(node PeerNode) {
			defer wg.Done()
			defer func() { <-sem }()
			err := fmt.Errorf("对方尚未公布联机地址")
			if node.Endpoint != "" {
				err = p.exchange(ctx, node.Endpoint, "sync", "", node.ID)
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			if _, exists := p.disk.Peers[node.ID]; !exists {
				return
			}
			status := p.status[node.ID]
			status.LastAttempt = time.Now()
			if err != nil {
				status.Error = err.Error()
				status.Failures++
				delay := peerHeartbeat * time.Duration(1<<min(status.Failures-1, 4))
				status.NextAttempt = time.Now().Add(delay)
			} else {
				status.NextAttempt = time.Now().Add(peerHeartbeat)
			}
			p.status[node.ID] = status
		}(node)
	}
	wg.Wait()
}

func (a *App) withPeerNetwork(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == inboundSyncWirePath {
			a.serveInboundSyncPeer(w, r)
			return
		}
		if r.URL.Path == peerWirePath {
			if a.peers == nil {
				http.NotFound(w, r)
			} else {
				a.peers.servePeer(w, r)
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) handleMultiControl(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.peers == nil {
		writeJSON(w, 503, apiResponse{Message: a.tr("Multi-control 未初始化，请检查面板日志")})
		return
	}
	p := a.peers
	action := strings.TrimPrefix(r.URL.Path, "/api/multi-control")
	var input struct {
		Name     string `json:"name"`
		Endpoint string `json:"endpoint"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Scheme   string `json:"scheme"`
		Token    string `json:"token"`
		ID       string `json:"id"`
	}
	var err error
	switch {
	case action == "" && r.Method == http.MethodGet:
		writeJSON(w, 200, apiResponse{OK: true, Data: a.trPeerView(p.view())})
		return
	case action == "/token" && r.Method == http.MethodGet:
		p.mu.Lock()
		token := p.disk.Token
		p.mu.Unlock()
		writeJSON(w, 200, apiResponse{OK: true, Data: map[string]string{"token": token}})
		return
	case action == "/token" && (r.Method == http.MethodPost || r.Method == http.MethodPut):
		var body struct {
			Token string `json:"token"`
		}
		_ = decodeJSON(r, &body)
		body.Token = strings.TrimSpace(body.Token)
		if body.Token != "" {
			err = p.setToken(body.Token)
			if err == nil {
				writeJSON(w, 200, apiResponse{OK: true, Data: map[string]string{"token": body.Token}})
				return
			}
		} else if r.Method == http.MethodPut {
			err = fmt.Errorf("配对 token 必须为 16 至 32 位小写字母和数字混合")
		} else {
			var token string
			token, err = p.rotateToken()
			if err == nil {
				writeJSON(w, 200, apiResponse{OK: true, Data: map[string]string{"token": token}})
				return
			}
		}
	case action == "/token" && r.Method == http.MethodDelete:
		err = p.disableToken()
	case action == "/settings" && r.Method == http.MethodPut:
		if err = decodeJSON(r, &input); err == nil {
			err = p.configure(input.Name, input.Endpoint)
		}
	case action == "/connect" && r.Method == http.MethodPost:
		if err = decodeJSON(r, &input); err == nil {
			err = p.connect(r.Context(), input.Host, input.Port, input.Scheme, input.Token)
		}
	case action == "/disconnect" && r.Method == http.MethodPost:
		if err = decodeJSON(r, &input); err == nil {
			err = p.disconnect(input.ID)
		}
	case action == "/unblock" && r.Method == http.MethodPost:
		if err = decodeJSON(r, &input); err == nil {
			err = p.unblock(input.ID)
		}
	case action == "/sync" && r.Method == http.MethodPost:
		p.signal()
	default:
		writeJSON(w, 405, apiResponse{Message: "method not allowed"})
		return
	}
	if err != nil {
		writeJSON(w, 400, apiResponse{Message: a.tr(err.Error())})
		return
	}
	writeJSON(w, 200, apiResponse{OK: true, Data: a.trPeerView(p.view())})
}
