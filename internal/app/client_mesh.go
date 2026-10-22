package app

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// 客户端接入联机网络（m-ui-client/1）。
//
// 用户设备（如 Clash Caesar）用面板管理员账号在浏览器里登录一次，得到签发面板签名的设备证书。
// 之后设备每个请求都带上证书并用自己的 Ed25519 私钥签名；任何一个信任签发面板的面板
// （即 Multi-control 里的 Peer）都能直接验证，不需要事先同步设备登记——设备连上网络里任意
// 一个面板就能工作。面板之间只同步吊销名单；证书 30 天过期并由设备自动续签，漏同步的吊销
// 最多持续到证书过期。
//
// 设备只拿到只读权限：查看网络里的面板和在线状态、拉取某个 Username 在某个面板（或融合）
// 的 Clash 订阅。所有管理操作都不对设备开放。

const (
	clientSpec           = "m-ui-client/1"
	clientWireBase       = "/_m-ui/client/v1/"
	clientDeviceWirePath = "/_m-ui/peer/device/v1"
	clientDevicesFile    = "client-devices.json"
	clientCertTTL        = 30 * 24 * time.Hour
	clientClockSkew      = 5 * time.Minute
	clientCodeTTL        = 2 * time.Minute
	clientWireLimit      = 64 << 10
	clientMaxDevices     = 512
	clientGossipInterval = 10 * time.Minute
	clientFileMagic      = "MUIN"

	clientDomainNetwork     = "m-ui-client-network-v1"
	clientDomainCert        = "m-ui-device-cert-v1"
	clientDomainRequest     = "m-ui-device-request-v1"
	clientDomainConfig      = "m-ui-client-config-v1"
	clientDomainRevocations = "m-ui-device-revocations-v1"
)

var (
	clientDeviceIDPattern  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	clientNoncePattern     = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)
	clientChallengePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	clientVerifierPattern  = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
	clientRedirectPattern  = regexp.MustCompile(`^http://127\.0\.0\.1:([0-9]{1,5})/callback$`)
	clientStatePattern     = regexp.MustCompile(`^[A-Za-z0-9._~-]{8,128}$`)
	clientIDPattern        = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

// ---- 签名信封 ----

// 信封："<payload 的标准 base64>.<签名的标准 base64>"，签名对象是 domain + "\x00" + payload 原始字节。
// 验签的是收到的字节本身，验过之后才解析 JSON，两端都不需要规范化 JSON。
func clientSignEnvelope(key ed25519.PrivateKey, domain string, payload []byte) string {
	sig := ed25519.Sign(key, append([]byte(domain+"\x00"), payload...))
	return base64.StdEncoding.EncodeToString(payload) + "." + base64.StdEncoding.EncodeToString(sig)
}

// clientSplitEnvelope 只解码不验签，用于先读出签名者再找公钥。
func clientSplitEnvelope(envelope string) (payload, sig []byte, err error) {
	parts := strings.Split(envelope, ".")
	if len(parts) != 2 {
		return nil, nil, fmt.Errorf("签名信封格式无效")
	}
	payload, err = base64.StdEncoding.DecodeString(parts[0])
	if err != nil || base64.StdEncoding.EncodeToString(payload) != parts[0] {
		return nil, nil, fmt.Errorf("签名信封格式无效")
	}
	sig, err = base64.StdEncoding.DecodeString(parts[1])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, nil, fmt.Errorf("签名信封格式无效")
	}
	return payload, sig, nil
}

func clientVerifyEnvelope(public ed25519.PublicKey, domain string, payload, sig []byte) bool {
	return len(public) == ed25519.PublicKeySize && ed25519.Verify(public, append([]byte(domain+"\x00"), payload...), sig)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func randomURLToken(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ---- 数据结构 ----

type clientDeviceCert struct {
	Spec      string `json:"spec"`
	DeviceID  string `json:"deviceId"`
	PublicKey string `json:"publicKey"`
	Name      string `json:"name"`
	Issuer    string `json:"issuer"`
	IssuedAt  int64  `json:"issuedAt"`
	ExpiresAt int64  `json:"expiresAt"`
}

type clientNodeRow struct {
	PeerNode
	Online   bool  `json:"online"`
	LastSeen int64 `json:"lastSeen,omitempty"`
}

type clientNetworkDoc struct {
	Spec      string          `json:"spec"`
	Signer    PeerNode        `json:"signer"`
	Nodes     []clientNodeRow `json:"nodes"`
	LoginURL  string          `json:"loginUrl,omitempty"`
	Usernames []string        `json:"usernames"`
	IssuedAt  int64           `json:"issuedAt"`
}

type clientNetworkFile struct {
	Magic   string `json:"magic"`
	V       int    `json:"v"`
	Spec    string `json:"spec"`
	Network string `json:"network"`
}

type clientDeviceRecord struct {
	Name      string `json:"name"`
	PublicKey string `json:"publicKey"`
	Issuer    string `json:"issuer"`
	FirstSeen int64  `json:"firstSeen"`
	LastSeen  int64  `json:"lastSeen"`
}

type clientDevicesDisk struct {
	Devices map[string]clientDeviceRecord `json:"devices"`
	Revoked map[string]int64              `json:"revoked"`
}

type clientPendingCode struct {
	Challenge   string
	RedirectURI string
	ClientID    string
	DeviceName  string
	Expires     time.Time
}

type clientRevocationDoc struct {
	Revoked   map[string]int64 `json:"revoked"`
	Timestamp int64            `json:"timestamp"`
	Nonce     string           `json:"nonce"`
}

type clientRevocationMessage struct {
	Sender   PeerNode `json:"sender"`
	Envelope string   `json:"envelope"`
}

type ClientMesh struct {
	mu        sync.Mutex
	path      string
	disk      clientDevicesDisk
	codes     map[string]clientPendingCode
	replays   map[string]time.Time
	persisted time.Time
	peers     *PeerNetwork
}

func newClientMesh(dataDir string, peers *PeerNetwork) (*ClientMesh, error) {
	c := &ClientMesh{
		path:    filepath.Join(dataDir, clientDevicesFile),
		disk:    clientDevicesDisk{Devices: map[string]clientDeviceRecord{}, Revoked: map[string]int64{}},
		codes:   map[string]clientPendingCode{},
		replays: map[string]time.Time{},
		peers:   peers,
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c, nil
		}
		return nil, err
	}
	var disk clientDevicesDisk
	if err := json.Unmarshal(data, &disk); err != nil {
		return nil, fmt.Errorf("客户端设备记录无效: %w", err)
	}
	if err := validateClientDevicesDisk(&disk); err != nil {
		return nil, err
	}
	c.disk = disk
	return c, nil
}

func validateClientDevicesDisk(disk *clientDevicesDisk) error {
	if disk.Devices == nil {
		disk.Devices = map[string]clientDeviceRecord{}
	}
	if disk.Revoked == nil {
		disk.Revoked = map[string]int64{}
	}
	if len(disk.Devices) > clientMaxDevices*2 || len(disk.Revoked) > clientMaxDevices*8 {
		return fmt.Errorf("客户端设备记录过多")
	}
	for id := range disk.Devices {
		if !clientDeviceIDPattern.MatchString(id) {
			return fmt.Errorf("客户端设备记录无效")
		}
	}
	for id := range disk.Revoked {
		if !clientDeviceIDPattern.MatchString(id) {
			return fmt.Errorf("客户端设备记录无效")
		}
	}
	return nil
}

func (c *ClientMesh) persistLocked() error {
	data, err := json.MarshalIndent(c.disk, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(c.path), ".client-devices-*.tmp")
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
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	c.persisted = time.Now()
	return os.Rename(file.Name(), c.path)
}

func (c *ClientMesh) exportDisk() clientDevicesDisk {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := clientDevicesDisk{Devices: map[string]clientDeviceRecord{}, Revoked: map[string]int64{}}
	for id, record := range c.disk.Devices {
		out.Devices[id] = record
	}
	for id, at := range c.disk.Revoked {
		out.Revoked[id] = at
	}
	return out
}

// restoreDisk 合并备份里的设备记录与吊销名单：吊销只增不减，避免恢复旧备份把已吊销的设备放回来。
func (c *ClientMesh) restoreDisk(disk clientDevicesDisk) error {
	if err := validateClientDevicesDisk(&disk); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, record := range disk.Devices {
		if _, exists := c.disk.Devices[id]; !exists {
			c.disk.Devices[id] = record
		}
	}
	for id, at := range disk.Revoked {
		if old, exists := c.disk.Revoked[id]; !exists || at < old {
			c.disk.Revoked[id] = at
		}
	}
	return c.persistLocked()
}

// ---- 面板身份 ----

func (p *PeerNetwork) privateKey() ed25519.PrivateKey {
	p.mu.Lock()
	defer p.mu.Unlock()
	key, _ := base64.StdEncoding.DecodeString(p.disk.PrivateKey)
	return ed25519.PrivateKey(key)
}

func (p *PeerNetwork) selfNode() PeerNode {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.disk.Self
}

// trustedNode 返回本面板或某个受信任（未断开）的 Peer 的身份描述。
func (p *PeerNetwork) trustedNode(id string) (PeerNode, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id == p.disk.Self.ID {
		return p.disk.Self, true
	}
	if p.disk.Blocked[id] {
		return PeerNode{}, false
	}
	node, ok := p.disk.Peers[id]
	return node, ok
}

func nodePublicKey(node PeerNode) ed25519.PublicKey {
	key, err := base64.StdEncoding.DecodeString(node.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil
	}
	return ed25519.PublicKey(key)
}

// ---- 设备证书与请求校验 ----

func (c *ClientMesh) issueCert(deviceID, publicKey, name string) (string, clientDeviceCert, error) {
	self := c.peers.selfNode()
	now := time.Now()
	cert := clientDeviceCert{Spec: clientSpec, DeviceID: deviceID, PublicKey: publicKey, Name: name, Issuer: self.ID, IssuedAt: now.UnixMilli(), ExpiresAt: now.Add(clientCertTTL).UnixMilli()}
	payload, err := json.Marshal(cert)
	if err != nil {
		return "", cert, err
	}
	return clientSignEnvelope(c.peers.privateKey(), clientDomainCert, payload), cert, nil
}

func (c *ClientMesh) verifyCert(envelope string) (clientDeviceCert, error) {
	var cert clientDeviceCert
	payload, sig, err := clientSplitEnvelope(envelope)
	if err != nil {
		return cert, err
	}
	if err := json.Unmarshal(payload, &cert); err != nil || cert.Spec != clientSpec {
		return cert, fmt.Errorf("设备证书无效")
	}
	issuer, ok := c.peers.trustedNode(cert.Issuer)
	if !ok {
		return cert, fmt.Errorf("设备证书的签发面板不在本面板的联机网络中")
	}
	if !clientVerifyEnvelope(nodePublicKey(issuer), clientDomainCert, payload, sig) {
		return cert, fmt.Errorf("设备证书签名无效")
	}
	if !clientDeviceIDPattern.MatchString(cert.DeviceID) {
		return cert, fmt.Errorf("设备证书无效")
	}
	if key, err := base64.StdEncoding.DecodeString(cert.PublicKey); err != nil || len(key) != ed25519.PublicKeySize {
		return cert, fmt.Errorf("设备证书无效")
	}
	if time.Now().UnixMilli() > cert.ExpiresAt {
		return cert, fmt.Errorf("设备证书已过期，请重新登录")
	}
	c.mu.Lock()
	_, revoked := c.disk.Revoked[cert.DeviceID]
	c.mu.Unlock()
	if revoked {
		return cert, errClientRevoked
	}
	return cert, nil
}

var errClientRevoked = errors.New("device revoked")

func clientRequestBytes(method, requestURI, timestamp, nonce string, body []byte) []byte {
	return []byte(strings.Join([]string{method, requestURI, timestamp, nonce, sha256Hex(body)}, "\x00"))
}

// authenticate 校验设备请求：证书由受信任面板签发且未过期、未吊销；请求由证书里的设备公钥签名；
// 时间在允许偏差内；nonce 没有用过。返回证书和本次请求的 nonce（响应签名要绑定它）。
func (c *ClientMesh) authenticate(r *http.Request, body []byte) (clientDeviceCert, string, error) {
	cert, err := c.verifyCert(r.Header.Get("X-MUI-Cert"))
	if err != nil {
		return cert, "", err
	}
	timestamp, nonce := r.Header.Get("X-MUI-Time"), r.Header.Get("X-MUI-Nonce")
	if !clientNoncePattern.MatchString(nonce) {
		return cert, "", fmt.Errorf("请求格式无效")
	}
	var ms int64
	if _, err := fmt.Sscan(timestamp, &ms); err != nil {
		return cert, "", fmt.Errorf("请求格式无效")
	}
	if delta := time.Since(time.UnixMilli(ms)); delta > clientClockSkew || delta < -clientClockSkew {
		return cert, "", fmt.Errorf("请求时间无效，请校准设备时间")
	}
	sig, err := base64.StdEncoding.DecodeString(r.Header.Get("X-MUI-Sig"))
	key, _ := base64.StdEncoding.DecodeString(cert.PublicKey)
	signed := append([]byte(clientDomainRequest+"\x00"), clientRequestBytes(r.Method, r.URL.RequestURI(), timestamp, nonce, body)...)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(key), signed, sig) {
		return cert, "", fmt.Errorf("请求签名无效")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, expires := range c.replays {
		if now.After(expires) {
			delete(c.replays, k)
		}
	}
	replayKey := cert.DeviceID + ":" + nonce
	if _, used := c.replays[replayKey]; used {
		return cert, "", fmt.Errorf("请求已被使用")
	}
	if len(c.replays) >= 8192 {
		return cert, "", fmt.Errorf("请求过多")
	}
	c.replays[replayKey] = now.Add(2 * clientClockSkew)
	c.touchLocked(cert)
	return cert, nonce, nil
}

// touchLocked 记录设备最近一次访问本面板，持久化做节流。
func (c *ClientMesh) touchLocked(cert clientDeviceCert) {
	now := time.Now().UnixMilli()
	record, exists := c.disk.Devices[cert.DeviceID]
	if !exists {
		if len(c.disk.Devices) >= clientMaxDevices {
			return
		}
		record = clientDeviceRecord{Name: cert.Name, PublicKey: cert.PublicKey, Issuer: cert.Issuer, FirstSeen: now}
	}
	record.Name, record.PublicKey, record.LastSeen = cert.Name, cert.PublicKey, now
	c.disk.Devices[cert.DeviceID] = record
	if !exists || time.Since(c.persisted) > 10*time.Minute {
		if err := c.persistLocked(); err != nil {
			log.Printf("Client devices persist failed: %v", err)
		}
	}
}

// ---- 网络文档 ----

func (a *App) clientUsernames() []string {
	a.manager.mu.Lock()
	local := cloneStringMap(a.manager.state.SubscriptionTokens)
	a.manager.mu.Unlock()
	var sources map[string]crossSubscriptionCacheSource
	if a.cross != nil {
		sources = a.cross.snapshot()
	}
	names := []string{}
	for name := range aggregateCrossTokens(local, sources) {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (a *App) clientLoginURL() string {
	self := a.peers.selfNode()
	if self.Endpoint == "" {
		return ""
	}
	return self.Endpoint + a.manager.panelPath() + "client/authorize"
}

func (a *App) clientNetworkEnvelope() (string, error) {
	view := a.peers.view()
	doc := clientNetworkDoc{Spec: clientSpec, Signer: view.Self, LoginURL: a.clientLoginURL(), Usernames: a.clientUsernames(), IssuedAt: time.Now().UnixMilli()}
	doc.Nodes = append(doc.Nodes, clientNodeRow{PeerNode: view.Self, Online: true, LastSeen: doc.IssuedAt})
	for _, row := range view.Peers {
		item := clientNodeRow{PeerNode: row.PeerNode, Online: row.Online}
		if !row.LastSeen.IsZero() {
			item.LastSeen = row.LastSeen.UnixMilli()
		}
		doc.Nodes = append(doc.Nodes, item)
	}
	payload, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return clientSignEnvelope(a.peers.privateKey(), clientDomainNetwork, payload), nil
}

// ---- 设备端点 ----

func (a *App) serveClientWire(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.peers == nil || a.clients == nil {
		http.NotFound(w, r)
		return
	}
	action := strings.TrimPrefix(r.URL.Path, clientWireBase)
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, clientWireLimit))
	if err != nil {
		clientError(w, http.StatusBadRequest, "bad_request", "请求过大或无法读取")
		return
	}
	if action == "enroll" && r.Method == http.MethodPost {
		a.clientEnroll(w, body)
		return
	}
	cert, nonce, err := a.clients.authenticate(r, body)
	if errors.Is(err, errClientRevoked) {
		clientError(w, http.StatusForbidden, "revoked", "设备已被吊销，请重新登录")
		return
	}
	if err != nil {
		clientError(w, http.StatusUnauthorized, "unauthorized", a.tr(err.Error()))
		return
	}
	switch {
	case action == "network" && r.Method == http.MethodGet:
		envelope, err := a.clientNetworkEnvelope()
		if err != nil {
			clientError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		writeClientJSON(w, map[string]string{"network": envelope})
	case action == "renew" && r.Method == http.MethodPost:
		envelope, _, err := a.clients.issueCert(cert.DeviceID, cert.PublicKey, cert.Name)
		if err != nil {
			clientError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		writeClientJSON(w, map[string]string{"cert": envelope})
	case action == "config" && r.Method == http.MethodGet:
		a.clientConfig(w, r, nonce)
	default:
		clientError(w, http.StatusNotFound, "not_found", "接口不存在")
	}
}

func writeClientJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}

func clientError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": message})
}

func (a *App) clientEnroll(w http.ResponseWriter, body []byte) {
	var input struct {
		Code         string `json:"code"`
		CodeVerifier string `json:"codeVerifier"`
		RedirectURI  string `json:"redirectUri"`
		ClientID     string `json:"clientId"`
		DeviceID     string `json:"deviceId"`
		PublicKey    string `json:"publicKey"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		clientError(w, http.StatusBadRequest, "bad_request", "请求格式无效")
		return
	}
	key, err := base64.StdEncoding.DecodeString(input.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize || !clientDeviceIDPattern.MatchString(input.DeviceID) || !clientVerifierPattern.MatchString(input.CodeVerifier) {
		clientError(w, http.StatusBadRequest, "bad_request", "请求格式无效")
		return
	}
	c := a.clients
	c.mu.Lock()
	pending, ok := c.codes[input.Code]
	delete(c.codes, input.Code)
	_, revoked := c.disk.Revoked[input.DeviceID]
	c.mu.Unlock()
	challenge := sha256.Sum256([]byte(input.CodeVerifier))
	if !ok || time.Now().After(pending.Expires) ||
		!hmac.Equal([]byte(base64.RawURLEncoding.EncodeToString(challenge[:])), []byte(pending.Challenge)) ||
		pending.RedirectURI != input.RedirectURI || pending.ClientID != input.ClientID {
		clientError(w, http.StatusBadRequest, "invalid_grant", "登录已过期或无效，请重新登录")
		return
	}
	if revoked {
		clientError(w, http.StatusForbidden, "revoked", "该设备 ID 已被吊销，请重新登录")
		return
	}
	certEnvelope, cert, err := c.issueCert(input.DeviceID, input.PublicKey, pending.DeviceName)
	if err != nil {
		clientError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	c.mu.Lock()
	c.touchLocked(cert)
	c.mu.Unlock()
	network, err := a.clientNetworkEnvelope()
	if err != nil {
		clientError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeClientJSON(w, map[string]string{"cert": certEnvelope, "network": network})
}

// clientSnapshot 组装某个 Username 的订阅：source 为本面板 ID 时用本机入站；为 "merged" 时与
// /isub 相同，合并本机和全部缓存的 Peer；为其它 Peer 的 ID 时只用该 Peer 的缓存（设备连不上
// 那个面板时由这里代为生成）。
func (a *App) clientSnapshot(username, source string) (subscriptionSnapshot, bool) {
	self := a.peers.selfNode()
	a.manager.mu.Lock()
	settings := a.manager.state.Settings
	local := cloneInboundList(a.manager.state.Inbounds)
	a.manager.mu.Unlock()
	snapshot := subscriptionSnapshot{Settings: settings, Username: username}
	selfServer := crossSubscriptionHost(self.Endpoint)
	add := func(inbounds []Inbound, server string) {
		for _, inbound := range inbounds {
			for _, client := range inbound.Clients {
				if subscriptionUsername(client) == username {
					snapshot.Clients = append(snapshot.Clients, subscriptionClient{Inbound: inbound, Client: client, Server: server})
				}
			}
		}
	}
	if source == self.ID || source == "merged" {
		add(local, selfServer)
	}
	if source != self.ID && a.cross != nil {
		for _, cached := range orderedCrossSources(a.cross.snapshot()) {
			if source != "merged" && cached.Node.ID != source {
				continue
			}
			if server := crossSubscriptionHost(cached.Node.Endpoint); server != "" {
				add(cached.Inbounds, server)
			}
		}
	}
	return snapshot, len(snapshot.Clients) > 0
}

func (a *App) clientConfig(w http.ResponseWriter, r *http.Request, nonce string) {
	username := strings.TrimSpace(r.URL.Query().Get("username"))
	source := r.URL.Query().Get("source")
	if username == "" || (source != "merged" && !peerIDPattern.MatchString(source)) {
		clientError(w, http.StatusBadRequest, "bad_request", "请求格式无效")
		return
	}
	snapshot, ok := a.clientSnapshot(username, source)
	if !ok {
		clientError(w, http.StatusNotFound, "not_found", "该面板上没有这个 Username 的可用节点")
		return
	}
	if a.subTemplates != nil {
		snapshot.Template = a.subTemplates.active()
	}
	data, err := buildMihomoSubscription(snapshot, crossSubscriptionHost(a.peers.selfNode().Endpoint))
	if err != nil {
		clientError(w, http.StatusNotFound, "not_found", a.tr(err.Error()))
		return
	}
	// 响应签名绑定请求 nonce：面板走 http 时也能防篡改，旧响应也无法冒充新请求的结果。
	sig := ed25519.Sign(a.peers.privateKey(), []byte(clientDomainConfig+"\x00"+nonce+"\x00"+sha256Hex(data)))
	setSubscriptionHeaders(w, snapshot)
	w.Header().Set("X-MUI-Signer", a.peers.selfNode().ID)
	w.Header().Set("X-MUI-Signature", base64.StdEncoding.EncodeToString(sig))
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	_, _ = w.Write(data)
}

// ---- 浏览器授权页 ----

var clientAuthorizeTemplate = template.Must(template.New("authorize").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>m-ui</title>
<style>
body{font-family:system-ui,-apple-system,"Segoe UI",sans-serif;background:#0f1220;color:#e8eaf2;display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0}
.card{width:min(380px,92vw);background:rgba(255,255,255,.06);border:1px solid rgba(255,255,255,.12);border-radius:18px;padding:28px;box-shadow:0 20px 60px rgba(0,0,0,.35)}
h1{font-size:18px;margin:0 0 6px}p{color:#aab0c4;font-size:14px;line-height:1.5;margin:0 0 18px}
input{width:100%;box-sizing:border-box;margin:0 0 12px;padding:11px 12px;border-radius:10px;border:1px solid rgba(255,255,255,.18);background:rgba(0,0,0,.25);color:inherit;font-size:14px}
.row{display:flex;gap:10px}button{flex:1;padding:11px;border:0;border-radius:10px;font-size:14px;cursor:pointer}
.ok{background:#5b7cfa;color:#fff}.no{background:rgba(255,255,255,.1);color:inherit}.err{color:#ff8a8a;font-size:13px;margin:0 0 12px}
b{color:#fff}
</style></head><body><form class="card" method="post">
<h1>{{.Title}}</h1><p>{{.Text}}</p>
{{if .Error}}<div class="err">{{.Error}}</div>{{end}}
{{if not .LoggedIn}}<input name="username" autocomplete="username" placeholder="{{.UserLabel}}" required>
<input name="password" type="password" autocomplete="current-password" placeholder="{{.PassLabel}}" required>{{end}}
<input type="hidden" name="csrf" value="{{.CSRF}}">
<div class="row"><button class="no" name="decision" value="deny">{{.Deny}}</button><button class="ok" name="decision" value="allow">{{.Allow}}</button></div>
</form></body></html>`))

type clientAuthorizeRequest struct {
	ClientID    string
	RedirectURI string
	Challenge   string
	State       string
	DeviceName  string
}

func parseClientAuthorize(values url.Values) (clientAuthorizeRequest, error) {
	req := clientAuthorizeRequest{
		ClientID:    values.Get("client_id"),
		RedirectURI: values.Get("redirect_uri"),
		Challenge:   values.Get("code_challenge"),
		State:       values.Get("state"),
		DeviceName:  strings.TrimSpace(values.Get("device_name")),
	}
	match := clientRedirectPattern.FindStringSubmatch(req.RedirectURI)
	if values.Get("response_type") != "code" || values.Get("code_challenge_method") != "S256" || !clientIDPattern.MatchString(req.ClientID) ||
		match == nil || !clientChallengePattern.MatchString(req.Challenge) || !clientStatePattern.MatchString(req.State) {
		return req, fmt.Errorf("授权请求无效")
	}
	if req.DeviceName == "" {
		req.DeviceName = req.ClientID
	}
	if len([]rune(req.DeviceName)) > 64 || strings.ContainsAny(req.DeviceName, "\r\n") {
		return req, fmt.Errorf("授权请求无效")
	}
	return req, nil
}

// clientAuthorizeCSRF binds the consent form to the browser's own session token.
func (a *App) clientAuthorizeCSRF(r *http.Request, req clientAuthorizeRequest) string {
	mac := hmac.New(sha256.New, []byte(sessionToken(r)))
	_, _ = mac.Write([]byte("client-authorize\x00" + req.Challenge + "\x00" + req.RedirectURI + "\x00" + req.State))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *App) handleClientAuthorize(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	if a.peers == nil || a.clients == nil {
		http.NotFound(w, r)
		return
	}
	req, err := parseClientAuthorize(r.URL.Query())
	if err != nil {
		http.Error(w, a.tr(err.Error()), http.StatusBadRequest)
		return
	}
	loggedIn := a.isLoggedIn(r)
	render := func(status int, message string) {
		data := map[string]any{
			"Title":     a.tr("授权设备接入联机网络"),
			"Text":      fmt.Sprintf(a.tr("设备「%s」请求接入本面板所在的联机网络。授权后它可以查看网络里的面板并拉取订阅，但不能做任何管理操作。"), req.DeviceName),
			"LoggedIn":  loggedIn,
			"UserLabel": a.tr("管理员账号"),
			"PassLabel": a.tr("密码"),
			"Allow":     a.tr("授权"),
			"Deny":      a.tr("拒绝"),
			"Error":     message,
			"CSRF":      "",
		}
		if loggedIn {
			data["CSRF"] = a.clientAuthorizeCSRF(r, req)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_ = clientAuthorizeTemplate.Execute(w, data)
	}
	switch r.Method {
	case http.MethodGet:
		render(http.StatusOK, "")
		return
	case http.MethodPost:
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		render(http.StatusBadRequest, a.tr("请求格式无效"))
		return
	}
	redirect, _ := url.Parse(req.RedirectURI)
	query := redirect.Query()
	query.Set("state", req.State)
	if r.PostForm.Get("decision") != "allow" {
		query.Set("error", "access_denied")
		redirect.RawQuery = query.Encode()
		http.Redirect(w, r, redirect.String(), http.StatusSeeOther)
		return
	}
	ip := remoteIP(r)
	if loggedIn {
		if !hmac.Equal([]byte(r.PostForm.Get("csrf")), []byte(a.clientAuthorizeCSRF(r, req))) {
			render(http.StatusForbidden, a.tr("请求已失效，请刷新页面重试"))
			return
		}
	} else {
		if a.logins.blocked(ip) {
			render(http.StatusTooManyRequests, a.tr(loginFailMessage))
			return
		}
		a.manager.mu.Lock()
		valid := r.PostForm.Get("username") == a.manager.state.Settings.Username && verifyPassword(a.manager.state.Settings.Password, r.PostForm.Get("password"))
		a.manager.mu.Unlock()
		if !valid {
			a.logins.fail(ip)
			render(http.StatusUnauthorized, a.tr("账号或密码错误"))
			return
		}
		a.logins.succeed(ip)
	}
	code, err := randomURLToken(32)
	if err != nil {
		render(http.StatusInternalServerError, err.Error())
		return
	}
	c := a.clients
	c.mu.Lock()
	for k, pending := range c.codes {
		if time.Now().After(pending.Expires) {
			delete(c.codes, k)
		}
	}
	if len(c.codes) >= 64 {
		c.mu.Unlock()
		render(http.StatusTooManyRequests, a.tr("请求过多"))
		return
	}
	c.codes[code] = clientPendingCode{Challenge: req.Challenge, RedirectURI: req.RedirectURI, ClientID: req.ClientID, DeviceName: req.DeviceName, Expires: time.Now().Add(clientCodeTTL)}
	c.mu.Unlock()
	query.Set("code", code)
	redirect.RawQuery = query.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusSeeOther)
}

// ---- 吊销名单在面板之间同步 ----

func (c *ClientMesh) revoke(id string) error {
	if !clientDeviceIDPattern.MatchString(id) {
		return fmt.Errorf("设备 ID 无效")
	}
	c.mu.Lock()
	if _, exists := c.disk.Revoked[id]; !exists {
		c.disk.Revoked[id] = time.Now().UnixMilli()
	}
	err := c.persistLocked()
	c.mu.Unlock()
	if err == nil {
		go c.gossip()
	}
	return err
}

func (c *ClientMesh) revocationMessage() (clientRevocationMessage, bool) {
	c.mu.Lock()
	revoked := map[string]int64{}
	for id, at := range c.disk.Revoked {
		revoked[id] = at
	}
	c.mu.Unlock()
	if len(revoked) == 0 {
		return clientRevocationMessage{}, false
	}
	doc := clientRevocationDoc{Revoked: revoked, Timestamp: time.Now().UnixMilli()}
	if err := randomToken(&doc.Nonce); err != nil {
		return clientRevocationMessage{}, false
	}
	payload, _ := json.Marshal(doc)
	return clientRevocationMessage{Sender: c.peers.selfNode(), Envelope: clientSignEnvelope(c.peers.privateKey(), clientDomainRevocations, payload)}, true
}

// gossip 把本面板的吊销名单推给所有 Peer。名单只增不减，所以各面板反复互推最终一致。
func (c *ClientMesh) gossip() {
	message, ok := c.revocationMessage()
	if !ok {
		return
	}
	data, _ := json.Marshal(message)
	view := c.peers.view()
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, row := range view.Peers {
		if row.Endpoint == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(endpoint string) {
			defer wg.Done()
			defer func() { <-sem }()
			req, err := http.NewRequest(http.MethodPost, endpoint+clientDeviceWirePath, bytes.NewReader(data))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			if response, err := c.peers.client.Do(req); err == nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
				response.Body.Close()
			}
		}(row.Endpoint)
	}
	wg.Wait()
}

func (c *ClientMesh) runGossip(stop <-chan struct{}) {
	ticker := time.NewTicker(clientGossipInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			c.gossip()
		}
	}
}

func (c *ClientMesh) serveRevocations(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var message clientRevocationMessage
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, clientWireLimit*4))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&message); err != nil {
		http.Error(w, "invalid message", http.StatusBadRequest)
		return
	}
	sender, ok := c.peers.trustedNode(message.Sender.ID)
	if !ok || sender.ID == c.peers.selfNode().ID || sender.PublicKey != message.Sender.PublicKey {
		http.Error(w, "peer not trusted", http.StatusForbidden)
		return
	}
	payload, sig, err := clientSplitEnvelope(message.Envelope)
	if err != nil || !clientVerifyEnvelope(nodePublicKey(sender), clientDomainRevocations, payload, sig) {
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}
	var doc clientRevocationDoc
	if err := json.Unmarshal(payload, &doc); err != nil || len(doc.Revoked) > clientMaxDevices*8 {
		http.Error(w, "invalid message", http.StatusBadRequest)
		return
	}
	if delta := time.Since(time.UnixMilli(doc.Timestamp)); delta > clientClockSkew || delta < -clientClockSkew {
		http.Error(w, "invalid timestamp", http.StatusForbidden)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	changed := false
	for id, at := range doc.Revoked {
		if !clientDeviceIDPattern.MatchString(id) {
			continue
		}
		if old, exists := c.disk.Revoked[id]; !exists || at < old {
			c.disk.Revoked[id] = at
			changed = true
		}
	}
	if changed {
		if err := c.persistLocked(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- 管理接口 ----

type clientDeviceView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Issuer    string `json:"issuer"`
	FirstSeen int64  `json:"firstSeen"`
	LastSeen  int64  `json:"lastSeen"`
	Revoked   bool   `json:"revoked"`
}

func (c *ClientMesh) devicesView() []clientDeviceView {
	c.mu.Lock()
	defer c.mu.Unlock()
	list := []clientDeviceView{}
	for id, record := range c.disk.Devices {
		_, revoked := c.disk.Revoked[id]
		list = append(list, clientDeviceView{ID: id, Name: record.Name, Issuer: record.Issuer, FirstSeen: record.FirstSeen, LastSeen: record.LastSeen, Revoked: revoked})
	}
	slices.SortFunc(list, func(a, b clientDeviceView) int {
		if a.LastSeen != b.LastSeen {
			if a.LastSeen > b.LastSeen {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	return list
}

// handleClientMesh 是面板管理页用的接口：
//
//	GET  /api/client-mesh/devices          设备列表
//	POST /api/client-mesh/devices/revoke   {id} 吊销设备（同步到所有 Peer）
//	GET  /api/client-mesh/file             下载客户端插件文件（.muinet）
func (a *App) handleClientMesh(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.peers == nil || a.clients == nil {
		writeJSON(w, http.StatusServiceUnavailable, apiResponse{Message: a.tr("Multi-control 未初始化，请检查面板日志")})
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/client-mesh")
	switch {
	case action == "/devices" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: a.clients.devicesView()})
	case action == "/devices/revoke" && r.Method == http.MethodPost:
		var input struct {
			ID string `json:"id"`
		}
		if err := decodeJSON(r, &input); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{Message: "invalid request"})
			return
		}
		if err := a.clients.revoke(input.ID); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: a.clients.devicesView()})
	case action == "/file" && r.Method == http.MethodGet:
		if a.peers.selfNode().Endpoint == "" {
			writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr("请先设置本机联机地址")})
			return
		}
		envelope, err := a.clientNetworkEnvelope()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, apiResponse{Message: err.Error()})
			return
		}
		data, _ := json.MarshalIndent(clientNetworkFile{Magic: clientFileMagic, V: 1, Spec: clientSpec, Network: envelope}, "", "  ")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="m-ui-network.muinet"`)
		_, _ = w.Write(data)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
	}
}
