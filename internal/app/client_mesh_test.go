package app

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// clientTestDevice 模拟 Clash Caesar 一侧：设备密钥、证书、按协议签名请求。
type clientTestDevice struct {
	id   string
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
	cert string
}

func newClientTestDevice(t *testing.T) *clientTestDevice {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	id := fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	return &clientTestDevice{id: id, pub: pub, priv: priv}
}

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// authorize 走浏览器授权页（账号密码方式），返回一次性 code。
func clientTestAuthorize(t *testing.T, panel *meshTestPanel, verifier string, form url.Values) (*http.Response, url.Values) {
	t.Helper()
	challenge := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {"clash-caesar"},
		"redirect_uri":          {"http://127.0.0.1:43210/callback"},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
		"state":                 {"state-123456"},
		"device_name":           {"Test PC"},
	}
	response, err := noRedirect.PostForm(panel.server.URL+"/hidden-panel/client/authorize?"+query.Encode(), form)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	location, _ := url.Parse(response.Header.Get("Location"))
	if location == nil {
		return response, url.Values{}
	}
	return response, location.Query()
}

func clientTestEnroll(t *testing.T, panel *meshTestPanel, device *clientTestDevice) map[string]string {
	t.Helper()
	verifier := strings.Repeat("v", 50)
	response, query := clientTestAuthorize(t, panel, verifier, url.Values{"username": {"admin"}, "password": {"admin"}, "decision": {"allow"}})
	if response.StatusCode != http.StatusSeeOther || query.Get("code") == "" || query.Get("state") != "state-123456" {
		t.Fatalf("authorize failed: %d %v", response.StatusCode, query)
	}
	body, _ := json.Marshal(map[string]string{
		"code": query.Get("code"), "codeVerifier": verifier, "redirectUri": "http://127.0.0.1:43210/callback",
		"clientId": "clash-caesar", "deviceId": device.id, "publicKey": base64.StdEncoding.EncodeToString(device.pub),
	})
	enroll, err := http.Post(panel.server.URL+clientWireBase+"enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer enroll.Body.Close()
	var result map[string]string
	if err := json.NewDecoder(enroll.Body).Decode(&result); err != nil || enroll.StatusCode != 200 {
		t.Fatalf("enroll failed: %d %v", enroll.StatusCode, result)
	}
	device.cert = result["cert"]
	return result
}

func (d *clientTestDevice) request(t *testing.T, method, endpoint, path, nonce string) *http.Response {
	t.Helper()
	timestamp := fmt.Sprint(time.Now().UnixMilli())
	sig := ed25519.Sign(d.priv, append([]byte(clientDomainRequest+"\x00"), clientRequestBytes(method, path, timestamp, nonce, nil)...))
	req, _ := http.NewRequest(method, endpoint+path, nil)
	req.Header.Set("X-MUI-Cert", d.cert)
	req.Header.Set("X-MUI-Time", timestamp)
	req.Header.Set("X-MUI-Nonce", nonce)
	req.Header.Set("X-MUI-Sig", base64.StdEncoding.EncodeToString(sig))
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func clientTestOpen(t *testing.T, envelope, domain string, signer PeerNode) []byte {
	t.Helper()
	payload, sig, err := clientSplitEnvelope(envelope)
	if err != nil || !clientVerifyEnvelope(nodePublicKey(signer), domain, payload, sig) {
		t.Fatalf("envelope not signed by %s: %v", signer.Name, err)
	}
	return payload
}

func clientMeshFixture(t *testing.T) (*App, *meshTestPanel, *App, *meshTestPanel) {
	t.Helper()
	a, pa, b, pb := crossSubscriptionFixture(t)
	var err error
	if a.clients, err = newClientMesh(pa.dir, a.peers); err != nil {
		t.Fatal(err)
	}
	if b.clients, err = newClientMesh(pb.dir, b.peers); err != nil {
		t.Fatal(err)
	}
	// A 缓存了 B 的入站，用于融合订阅和"代 B 生成"
	node := b.peers.view().Self
	inbounds, tokens, err := a.sendInboundPull(t.Context(), node)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.cross.update(node, inbounds, tokens, nil); err != nil {
		t.Fatal(err)
	}
	return a, pa, b, pb
}

func TestClientMeshEnrollNetworkAndSubscriptions(t *testing.T) {
	a, pa, b, pb := clientMeshFixture(t)
	selfA, selfB := a.peers.view().Self, b.peers.view().Self
	device := newClientTestDevice(t)
	result := clientTestEnroll(t, pa, device)

	var cert clientDeviceCert
	_ = json.Unmarshal(clientTestOpen(t, result["cert"], clientDomainCert, selfA), &cert)
	if cert.DeviceID != device.id || cert.Issuer != selfA.ID || cert.Name != "Test PC" || cert.ExpiresAt <= time.Now().UnixMilli() {
		t.Fatalf("unexpected cert: %+v", cert)
	}
	var network clientNetworkDoc
	_ = json.Unmarshal(clientTestOpen(t, result["network"], clientDomainNetwork, selfA), &network)
	if len(network.Nodes) != 2 || network.LoginURL != selfA.Endpoint+"/hidden-panel/client/authorize" || !slices.Contains(network.Usernames, "alice") || !slices.Contains(network.Usernames, "bob") {
		t.Fatalf("unexpected network: %+v", network)
	}

	fetch := func(panel *meshTestPanel, signer PeerNode, source, nonce string) (int, string) {
		path := clientWireBase + "config?" + url.Values{"username": {"alice"}, "source": {source}}.Encode()
		response := device.request(t, http.MethodGet, panel.server.URL, path, nonce)
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		if response.StatusCode == 200 {
			sig, _ := base64.StdEncoding.DecodeString(response.Header.Get("X-MUI-Signature"))
			signed := []byte(clientDomainConfig + "\x00" + nonce + "\x00" + sha256Hex(body))
			if response.Header.Get("X-MUI-Signer") != signer.ID || !ed25519.Verify(nodePublicKey(signer), signed, sig) {
				t.Fatalf("config response not signed by %s", signer.Name)
			}
			if !strings.Contains(response.Header.Get("Subscription-Userinfo"), "upload=") {
				t.Fatalf("missing Subscription-Userinfo header")
			}
		}
		return response.StatusCode, string(body)
	}

	if status, body := fetch(pa, selfA, selfA.ID, "nonce-local-0001"); status != 200 || !strings.Contains(body, "Local Zulu") || strings.Contains(body, "Remote Alpha") {
		t.Fatalf("local subscription: %d %s", status, body)
	}
	// 证书由 A 签发，B 不需要任何同步就能直接认
	if status, body := fetch(pb, selfB, selfB.ID, "nonce-remote-0001"); status != 200 || !strings.Contains(body, "Remote Alpha") {
		t.Fatalf("subscription from B with A's certificate: %d %s", status, body)
	}
	// 设备连不上 B 时，A 用缓存代 B 生成
	if status, body := fetch(pa, selfA, selfB.ID, "nonce-relay-00001"); status != 200 || !strings.Contains(body, "Remote Alpha") || strings.Contains(body, "Local Zulu") {
		t.Fatalf("relayed subscription: %d %s", status, body)
	}
	if status, body := fetch(pa, selfA, "merged", "nonce-merged-0001"); status != 200 || !strings.Contains(body, "Remote Alpha") || !strings.Contains(body, "Local Zulu") {
		t.Fatalf("merged subscription: %d %s", status, body)
	}

	// 重放同一个 nonce 被拒绝
	if status, _ := fetch(pa, selfA, selfA.ID, "nonce-local-0001"); status != http.StatusUnauthorized {
		t.Fatalf("replayed request accepted: %d", status)
	}
	// 篡改过的证书被拒绝
	tampered := *device
	tampered.cert = strings.Replace(device.cert, device.cert[:8], "AAAAAAAA", 1)
	if response := tampered.request(t, http.MethodGet, pa.server.URL, clientWireBase+"network", "nonce-tamper-001"); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tampered cert accepted: %d", response.StatusCode)
	}
	// 另一台设备的私钥签不出这张证书的请求
	impostor := newClientTestDevice(t)
	impostor.cert = device.cert
	if response := impostor.request(t, http.MethodGet, pa.server.URL, clientWireBase+"network", "nonce-imposter-1"); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("request signed by another key accepted: %d", response.StatusCode)
	}

	// 网络状态可以从任何一个面板取，并由该面板签名
	response := device.request(t, http.MethodGet, pb.server.URL, clientWireBase+"network", "nonce-network-01")
	var reply map[string]string
	_ = json.NewDecoder(response.Body).Decode(&reply)
	response.Body.Close()
	_ = json.Unmarshal(clientTestOpen(t, reply["network"], clientDomainNetwork, selfB), &network)
	if network.Signer.ID != selfB.ID || len(network.Nodes) != 2 {
		t.Fatalf("unexpected network from B: %+v", network)
	}

	// 续签由当前面板签发
	response = device.request(t, http.MethodPost, pb.server.URL, clientWireBase+"renew", "nonce-renew-0001")
	_ = json.NewDecoder(response.Body).Decode(&reply)
	response.Body.Close()
	_ = json.Unmarshal(clientTestOpen(t, reply["cert"], clientDomainCert, selfB), &cert)
	if cert.Issuer != selfB.ID || cert.DeviceID != device.id {
		t.Fatalf("unexpected renewed cert: %+v", cert)
	}

	// 两个面板都记下了这台设备
	if devices := a.clients.devicesView(); len(devices) != 1 || devices[0].ID != device.id || devices[0].Name != "Test PC" {
		t.Fatalf("unexpected device list on A: %+v", devices)
	}

	// 在 A 吊销，同步到 B 后 B 也拒绝
	if err := a.clients.revoke(device.id); err != nil {
		t.Fatal(err)
	}
	a.clients.gossip()
	response = device.request(t, http.MethodGet, pb.server.URL, clientWireBase+"network", "nonce-revoked-01")
	_ = json.NewDecoder(response.Body).Decode(&reply)
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden || reply["error"] != "revoked" {
		t.Fatalf("revoked device accepted by B: %d %v", response.StatusCode, reply)
	}
}

func TestClientMeshRejectsCertificatesFromOutsideTheNetwork(t *testing.T) {
	_, pa, _, _ := clientMeshFixture(t)
	outsider, po := newSyncPanel(t, "Outsider")
	var err error
	if outsider.clients, err = newClientMesh(po.dir, outsider.peers); err != nil {
		t.Fatal(err)
	}
	device := newClientTestDevice(t)
	clientTestEnroll(t, pa, device)
	response := device.request(t, http.MethodGet, po.server.URL, clientWireBase+"network", "nonce-outsider-1")
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("panel outside the network accepted the certificate: %d", response.StatusCode)
	}
}

func TestClientAuthorizeValidationAndLoginLimit(t *testing.T) {
	_, pa, _, _ := clientMeshFixture(t)
	verifier := strings.Repeat("w", 50)

	// 拒绝时带 error 回到回环地址
	response, query := clientTestAuthorize(t, pa, verifier, url.Values{"decision": {"deny"}, "username": {"admin"}, "password": {"admin"}})
	if response.StatusCode != http.StatusSeeOther || query.Get("error") != "access_denied" || query.Get("code") != "" {
		t.Fatalf("deny: %d %v", response.StatusCode, query)
	}

	// 只接受 127.0.0.1 的回调地址
	bad := url.Values{"response_type": {"code"}, "client_id": {"clash-caesar"}, "redirect_uri": {"http://evil.example/callback"},
		"code_challenge": {strings.Repeat("a", 43)}, "code_challenge_method": {"S256"}, "state": {"state-123456"}}
	get, err := http.Get(pa.server.URL + "/hidden-panel/client/authorize?" + bad.Encode())
	if err != nil {
		t.Fatal(err)
	}
	get.Body.Close()
	if get.StatusCode != http.StatusBadRequest {
		t.Fatalf("foreign redirect accepted: %d", get.StatusCode)
	}

	// 授权页在隐藏面板路径下，根路径上不存在
	root, err := http.Get(pa.server.URL + "/client/authorize")
	if err != nil {
		t.Fatal(err)
	}
	root.Body.Close()
	if root.StatusCode != http.StatusNotFound {
		t.Fatalf("authorize page reachable outside the panel path: %d", root.StatusCode)
	}

	// 连续输错密码后暂时锁定，正确密码也不行
	for i := 0; i < clientLoginMaxFails; i++ {
		if response, _ := clientTestAuthorize(t, pa, verifier, url.Values{"decision": {"allow"}, "username": {"admin"}, "password": {"wrong"}}); response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("wrong password not rejected: %d", response.StatusCode)
		}
	}
	if response, _ := clientTestAuthorize(t, pa, verifier, url.Values{"decision": {"allow"}, "username": {"admin"}, "password": {"admin"}}); response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("login not rate limited: %d", response.StatusCode)
	}
}

func TestClientEnrollCodeIsSingleUseAndBoundToVerifier(t *testing.T) {
	_, pa, _, _ := clientMeshFixture(t)
	device := newClientTestDevice(t)
	verifier := strings.Repeat("x", 50)
	_, query := clientTestAuthorize(t, pa, verifier, url.Values{"username": {"admin"}, "password": {"admin"}, "decision": {"allow"}})
	enroll := func(v string) int {
		body, _ := json.Marshal(map[string]string{
			"code": query.Get("code"), "codeVerifier": v, "redirectUri": "http://127.0.0.1:43210/callback",
			"clientId": "clash-caesar", "deviceId": device.id, "publicKey": base64.StdEncoding.EncodeToString(device.pub),
		})
		response, err := http.Post(pa.server.URL+clientWireBase+"enroll", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	if status := enroll(strings.Repeat("y", 50)); status != http.StatusBadRequest {
		t.Fatalf("wrong verifier accepted: %d", status)
	}
	// 用错一次之后 code 已作废，正确的 verifier 也不行
	if status := enroll(verifier); status != http.StatusBadRequest {
		t.Fatalf("code reused: %d", status)
	}
}

func TestClientMeshAdminAPIAndNetworkFile(t *testing.T) {
	a, pa, _, _ := clientMeshFixture(t)
	device := newClientTestDevice(t)
	clientTestEnroll(t, pa, device)

	file := syncAPICall(t, a, http.MethodGet, "/hidden-panel/api/client-mesh/file", nil)
	var network clientNetworkFile
	if err := json.Unmarshal(file.Body.Bytes(), &network); err != nil || network.Magic != clientFileMagic || network.Spec != clientSpec {
		t.Fatalf("unexpected network file: %s", file.Body.String())
	}
	if strings.Contains(file.Body.String(), "privateKey") || strings.Contains(file.Body.String(), "pairingToken") {
		t.Fatalf("network file leaks secrets")
	}
	clientTestOpen(t, network.Network, clientDomainNetwork, a.peers.view().Self)

	revoke := syncAPICall(t, a, http.MethodPost, "/hidden-panel/api/client-mesh/devices/revoke", map[string]string{"id": device.id})
	var reply struct {
		Data []clientDeviceView `json:"data"`
	}
	_ = json.Unmarshal(revoke.Body.Bytes(), &reply)
	if revoke.Code != 200 || len(reply.Data) != 1 || !reply.Data[0].Revoked {
		t.Fatalf("revoke via API: %d %s", revoke.Code, revoke.Body.String())
	}

	// 未登录不能用管理接口
	unauthenticated, _ := http.Get(pa.server.URL + "/hidden-panel/api/client-mesh/devices")
	unauthenticated.Body.Close()
	if unauthenticated.StatusCode == 200 {
		t.Fatalf("admin API reachable without login")
	}

	// 备份恢复只增不减吊销名单
	disk := a.clients.exportDisk()
	fresh, err := newClientMesh(t.TempDir(), a.peers)
	if err != nil {
		t.Fatal(err)
	}
	if err = fresh.restoreDisk(disk); err != nil {
		t.Fatal(err)
	}
	if _, revoked := fresh.exportDisk().Revoked[device.id]; !revoked {
		t.Fatalf("revocation lost in backup round trip")
	}
}
