package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type meshTestPanel struct {
	network *PeerNetwork
	server  *httptest.Server
	dir     string
}

func newMeshTestPanel(t *testing.T, name string) *meshTestPanel {
	t.Helper()
	dir := t.TempDir()
	p, err := newPeerNetwork(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{peers: p, manager: &CoreManager{dataDir: dir, state: defaultState()}, session: "test-session"}
	app.manager.state.Settings.PanelPath = "/hidden-panel/"
	server := httptest.NewServer(app.routes())
	t.Cleanup(server.Close)
	if err = p.configure(name, server.URL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.client.CloseIdleConnections)
	return &meshTestPanel{p, server, dir}
}
func connectMeshPanels(t *testing.T, from, to *meshTestPanel) {
	t.Helper()
	token, err := to.network.rotateToken()
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(to.server.URL)
	port, _ := strconv.Atoi(u.Port())
	if err = from.network.connect(context.Background(), u.Hostname(), port, "http", token); err != nil {
		t.Fatal(err)
	}
}
func convergeMesh(panels ...*meshTestPanel) {
	for i := 0; i < 3; i++ {
		for _, panel := range panels {
			panel.network.syncPeers(context.Background(), true)
		}
	}
}

func TestMeshDiscoverySurvivesOfflineSeedAndRestart(t *testing.T) {
	a, b, c := newMeshTestPanel(t, "A"), newMeshTestPanel(t, "B"), newMeshTestPanel(t, "C")
	connectMeshPanels(t, a, b)
	connectMeshPanels(t, c, b)
	convergeMesh(a, b, c)
	for _, panel := range []*meshTestPanel{a, b, c} {
		if view := panel.network.view(); len(view.Peers) != 2 {
			t.Fatalf("%s did not discover full mesh: %#v", view.Self.Name, view.Peers)
		}
		for _, peer := range panel.network.view().Peers {
			if !peer.Online {
				t.Fatalf("%s cannot directly contact %s", panel.network.view().Self.Name, peer.Name)
			}
		}
	}
	b.server.Close()
	a.network.mu.Lock()
	status := a.network.status[b.network.disk.Self.ID]
	status.LastSeen = time.Now().Add(-time.Minute)
	a.network.status[b.network.disk.Self.ID] = status
	a.network.mu.Unlock()
	convergeMesh(a, c)
	for _, peer := range a.network.view().Peers {
		if peer.Name == "C" && !peer.Online {
			t.Fatal("C depends on B")
		}
		if peer.Name == "B" && peer.Online {
			t.Fatal("offline seed stayed online")
		}
	}
	oldID := c.network.view().Self.ID
	c.server.Close()
	reloaded, err := newPeerNetwork(c.dir)
	if err != nil {
		t.Fatal(err)
	}
	c.network = reloaded
	t.Cleanup(reloaded.client.CloseIdleConnections)
	if reloaded.view().Self.ID != oldID || len(reloaded.view().Peers) != 2 {
		t.Fatal("restart lost identity or membership")
	}
	app := &App{peers: reloaded, manager: &CoreManager{dataDir: c.dir, state: defaultState()}}
	c.server = httptest.NewServer(app.routes())
	t.Cleanup(c.server.Close)
	if err = reloaded.configure("C", c.server.URL); err != nil {
		t.Fatal(err)
	}
	convergeMesh(c, a)
	for _, peer := range a.network.view().Peers {
		if peer.ID == oldID && (!peer.Online || peer.Endpoint != c.server.URL) {
			t.Fatal("peer endpoint did not update after restart")
		}
	}
}

func TestMeshIndependentNetworksCanMerge(t *testing.T) {
	a, b, c, d := newMeshTestPanel(t, "A"), newMeshTestPanel(t, "B"), newMeshTestPanel(t, "C"), newMeshTestPanel(t, "D")
	connectMeshPanels(t, a, b)
	connectMeshPanels(t, c, d)
	connectMeshPanels(t, b, c)
	convergeMesh(a, b, c, d)
	for _, panel := range []*meshTestPanel{a, b, c, d} {
		if len(panel.network.view().Peers) != 3 {
			t.Fatalf("%s did not merge existing networks", panel.network.view().Self.Name)
		}
	}
}

func TestMeshBackgroundDiscovery(t *testing.T) {
	a, b, c := newMeshTestPanel(t, "A"), newMeshTestPanel(t, "B"), newMeshTestPanel(t, "C")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel()
	for _, panel := range []*meshTestPanel{a, b, c} {
		wg.Add(1)
		go func(p *PeerNetwork) { defer wg.Done(); p.run(ctx) }(panel.network)
	}
	connectMeshPanels(t, a, b)
	connectMeshPanels(t, c, b)
	deadline := time.After(20 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready := true
		for _, panel := range []*meshTestPanel{a, b, c} {
			view := panel.network.view()
			if len(view.Peers) != 2 {
				ready = false
			}
			for _, peer := range view.Peers {
				if !peer.Online {
					ready = false
				}
			}
		}
		if ready {
			return
		}
		select {
		case <-deadline:
			t.Fatal("background gossip did not converge")
		case <-ticker.C:
		}
	}
}

func peerTestRequest(t *testing.T, p *PeerNetwork, endpoint string, message peerMessage) int {
	t.Helper()
	data, _ := json.Marshal(message)
	response, err := p.client.Post(endpoint+peerWirePath, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	return response.StatusCode
}

func TestMeshTokenRotationAuthenticationAndReplay(t *testing.T) {
	a, b := newMeshTestPanel(t, "A"), newMeshTestPanel(t, "B")
	token, err := b.network.rotateToken()
	if err != nil {
		t.Fatal(err)
	}
	if !validPairToken(token) {
		t.Fatal("token must be exactly 16 mixed lowercase letters and digits")
	}
	a.network.mu.Lock()
	message := a.network.messageLocked("pair", "", "wrong-token", false)
	a.network.mu.Unlock()
	if code := peerTestRequest(t, a.network, b.server.URL, message); code != 403 {
		t.Fatalf("wrong token accepted: %d", code)
	}
	if len(b.network.view().Peers) != 0 {
		t.Fatal("failed pairing changed membership")
	}
	a.network.mu.Lock()
	message = a.network.messageLocked("pair", "", token, false)
	a.network.mu.Unlock()
	if code := peerTestRequest(t, a.network, b.server.URL, message); code != 200 {
		t.Fatalf("valid pairing rejected: %d", code)
	}
	if code := peerTestRequest(t, a.network, b.server.URL, message); code != 409 {
		t.Fatalf("replay accepted: %d", code)
	}
	if _, err = b.network.rotateToken(); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(b.server.URL)
	port, _ := strconv.Atoi(u.Port())
	if err = a.network.connect(context.Background(), u.Hostname(), port, "http", token); err == nil {
		t.Fatal("rotated token accepted")
	}
	connectMeshPanels(t, a, b)
	if err = b.network.disableToken(); err != nil {
		t.Fatal(err)
	}
	if err = a.network.exchange(context.Background(), b.server.URL, "sync", "", b.network.view().Self.ID); err != nil {
		t.Fatalf("disabling pairing broke an established link: %v", err)
	}
	a.network.mu.Lock()
	message = a.network.messageLocked("sync", "", "", true)
	a.network.mu.Unlock()
	message.Sender.Name = "tampered"
	if code := peerTestRequest(t, a.network, b.server.URL, message); code != 403 {
		t.Fatal("tampered message accepted")
	}
	stranger := newMeshTestPanel(t, "untrusted")
	stranger.network.mu.Lock()
	unknown := stranger.network.messageLocked("sync", "", "", true)
	stranger.network.mu.Unlock()
	if code := peerTestRequest(t, stranger.network, b.server.URL, unknown); code != 403 {
		t.Fatal("untrusted sync accepted")
	}
}

func TestMeshCustomTokenValidationAndPairing(t *testing.T) {
	p := newMeshTestPanel(t, "Generator")
	genToken, err := p.network.rotateToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(genToken) != 16 {
		t.Fatalf("generated token length = %d, want 16", len(genToken))
	}
	if !validPairToken(genToken) {
		t.Fatalf("generated token %q is not valid", genToken)
	}

	validCases := []string{
		"a1b2c3d4e5f6g7h8",
		"1234567890abcdef1234567890abcdef",
		"abcdefghijklmnopqrstuvwxyz012345",
		"token1234567890abcdef",
	}
	for _, tc := range validCases {
		if !validPairToken(tc) {
			t.Errorf("expected token %q to be valid", tc)
		}
	}

	invalidCases := []string{
		"a1b2c3d4e5",
		"1234567890abcdef1234567890abcdef1",
		"abcdefghijklmnop",
		"1234567890123456",
		"Abc123def456ghi7",
		"abc_123-def#456!",
	}
	for _, tc := range invalidCases {
		if validPairToken(tc) {
			t.Errorf("expected token %q to be invalid", tc)
		}
	}

	a, b := newMeshTestPanel(t, "A"), newMeshTestPanel(t, "B")
	custom32Token := "1234567890abcdef1234567890abcdef"
	if err := b.network.setToken(custom32Token); err != nil {
		t.Fatalf("failed to set 32-char token: %v", err)
	}
	if b.network.disk.Token != custom32Token {
		t.Fatalf("b token mismatch: got %q, want %q", b.network.disk.Token, custom32Token)
	}

	if err := b.network.setToken("invalid"); err == nil {
		t.Fatal("expected error setting invalid token")
	}

	u, _ := url.Parse(b.server.URL)
	port, _ := strconv.Atoi(u.Port())
	if err := a.network.connect(context.Background(), u.Hostname(), port, "http", custom32Token); err != nil {
		t.Fatalf("failed to connect with 32-char token: %v", err)
	}
	if len(a.network.view().Peers) != 1 || len(b.network.view().Peers) != 1 {
		t.Fatal("peer connection with 32-char token failed")
	}
}

func TestMeshLocalDisconnectDoesNotResurrectFromGossip(t *testing.T) {
	a, b, c := newMeshTestPanel(t, "A"), newMeshTestPanel(t, "B"), newMeshTestPanel(t, "C")
	connectMeshPanels(t, a, b)
	connectMeshPanels(t, c, b)
	convergeMesh(a, b, c)
	cID := c.network.view().Self.ID
	if err := a.network.disconnect(cID); err != nil {
		t.Fatal(err)
	}
	convergeMesh(b, c, a)
	for _, peer := range a.network.view().Peers {
		if peer.ID == cID {
			t.Fatal("blocked peer was resurrected by gossip")
		}
	}
	if len(b.network.view().Peers) != 2 {
		t.Fatal("local disconnect mutated unrelated nodes")
	}
	if err := a.network.unblock(cID); err != nil {
		t.Fatal(err)
	}
	convergeMesh(a, b, c)
	if len(a.network.view().Peers) != 2 {
		t.Fatal("unblocked peer not rediscovered")
	}
}

func TestMeshPublicAndAdminRoutesAreSeparated(t *testing.T) {
	a, b := newMeshTestPanel(t, "A"), newMeshTestPanel(t, "B")
	connectMeshPanels(t, a, b) // public handshake works without knowing /hidden-panel/.
	for _, path := range []string{"/hidden-panel/api/multi-control", "/hidden-panel/api/multi-control/token"} {
		response, err := a.network.client.Get(a.server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatalf("admin route %s returned %d", path, response.StatusCode)
		}
	}
	app := &App{peers: a.network, manager: &CoreManager{dataDir: a.dir, state: defaultState()}}
	response := httptest.NewRecorder()
	app.subscriptionRoutes().ServeHTTP(response, httptest.NewRequest("POST", peerWirePath, strings.NewReader("{}")))
	if response.Code != 404 {
		t.Fatal("subscription service exposed mesh protocol")
	}
	view, _ := json.Marshal(a.network.view())
	if bytes.Contains(view, []byte(a.network.disk.PrivateKey)) || bytes.Contains(view, []byte("pairingToken")) {
		t.Fatal("status exposed mesh credentials")
	}
	data, err := os.ReadFile(filepath.Join(a.dir, "multi-control.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(b.network.disk.Token)) {
		t.Fatal("stored a remote pairing token")
	}

	// Test POST /api/multi-control/token with custom token
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/multi-control/token", strings.NewReader(`{"token":"usercustomtoken123"}`))
	app.handleMultiControl(recorder, req)
	if recorder.Code != 200 {
		t.Fatalf("custom token save returned %d: %s", recorder.Code, recorder.Body.String())
	}
	if a.network.disk.Token != "usercustomtoken123" {
		t.Fatalf("custom token not saved: got %q", a.network.disk.Token)
	}

	// Test POST /api/multi-control/token with invalid token
	recorder = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/multi-control/token", strings.NewReader(`{"token":"short123"}`))
	app.handleMultiControl(recorder, req)
	if recorder.Code != 400 {
		t.Fatalf("invalid token save expected 400, got %d", recorder.Code)
	}

	// Test POST /api/multi-control/token without token generates 16-character token
	recorder = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/multi-control/token", strings.NewReader(`{}`))
	app.handleMultiControl(recorder, req)
	if recorder.Code != 200 {
		t.Fatalf("generate token returned %d", recorder.Code)
	}
	if len(a.network.disk.Token) != 16 {
		t.Fatalf("generated token length = %d, want 16", len(a.network.disk.Token))
	}

	if err := a.network.connect(context.Background(), "127.0.0.1", 80, "ftp", "abcdefghijkl1234"); err == nil {
		t.Fatal("invalid protocol accepted")
	}
	for _, address := range []string{"http://0.0.0.0:2053", "http://169.254.169.254:80", "http://user:pass@example.com:80", "http://example.com:2053/path"} {
		if _, err := canonicalPeerEndpoint(address); err == nil {
			t.Fatalf("invalid endpoint %s accepted", address)
		}
	}
}

// Run only when explicitly requested for isolated local UI testing.
func TestMultiControlBrowserFixture(t *testing.T) {
	address := os.Getenv("MUI_PEER_BROWSER_ADDR")
	if address == "" {
		t.Skip("browser fixture disabled")
	}
	dir, err := os.MkdirTemp("", "m-ui-mesh-ui-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if configured := os.Getenv("MUI_PEER_BROWSER_DATA"); configured != "" {
		dir = configured
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	p, err := newPeerNetwork(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.configure(valueOr(os.Getenv("MUI_PEER_BROWSER_NAME"), "Panel "+address), "http://"+address); err != nil {
		t.Fatal(err)
	}
	m := &CoreManager{dataDir: dir, state: defaultState()}
	m.state.Settings.PanelListen = address
	m.state.Inbounds = nil
	m.state.Settings.PanelPath = "/testpath/"
	app := &App{manager: m, peers: p, session: "test-session"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.run(ctx)
	t.Logf("Mesh test panel http://%s", address)
	if err = http.ListenAndServe(address, app.routes()); err != nil {
		t.Fatal(err)
	}
}
