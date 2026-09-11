package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func crossSubscriptionFixture(t *testing.T) (*App, *meshTestPanel, *App, *meshTestPanel) {
	t.Helper()
	a, pa := newSyncPanel(t, "Panel A")
	b, pb := newSyncPanel(t, "Panel B")
	connectMeshPanels(t, pa, pb)
	var err error
	a.cross, err = newCrossSubscriptionManager(pa.dir)
	if err != nil {
		t.Fatal(err)
	}
	b.cross, err = newCrossSubscriptionManager(pb.dir)
	if err != nil {
		t.Fatal(err)
	}
	a.manager.state.Settings.CrossPanelSubscriptionPath = "/isub4ad5kaf479afnbj2"
	b.manager.state.Settings.CrossPanelSubscriptionPath = "/isub5bd6kaf479afnbj3"
	a.manager.state.SubscriptionTokens = map[string]string{"alice": "abc123def4567890"}
	b.manager.state.SubscriptionTokens = map[string]string{"alice": "abc123def4567890"}
	if err = a.manager.saveInbound(syncTestInbound("Local Zulu", 23011)); err != nil {
		t.Fatal(err)
	}
	remote := syncTestInbound("Remote Alpha", 23012)
	remote.Clients = remote.Clients[:1]
	if err = b.manager.saveInbound(remote); err != nil {
		t.Fatal(err)
	}
	return a, pa, b, pb
}

func TestCrossSubscriptionPullCacheAndPublicURLs(t *testing.T) {
	a, pa, b, pb := crossSubscriptionFixture(t)
	node := b.peers.view().Self
	inbounds, tokens, err := a.sendInboundPull(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	if len(inbounds) != 1 || inbounds[0].Name != "Remote Alpha" || len(inbounds[0].Clients) != 1 {
		t.Fatalf("unexpected pull: %+v", inbounds)
	}
	if err = a.cross.update(node, inbounds, tokens, nil); err != nil {
		t.Fatal(err)
	}
	if got := len(a.cross.snapshot()); got != 1 {
		t.Fatalf("cache sources = %d", got)
	}
	loaded, err := newCrossSubscriptionManager(pa.dir)
	if err != nil {
		t.Fatal(err)
	}
	if source := loaded.snapshot()[node.ID]; len(source.Inbounds) != 1 || source.Inbounds[0].Name != "Remote Alpha" {
		t.Fatal("cache did not persist")
	}
	a.cross = loaded

	request := httptest.NewRequest(http.MethodGet, "/isub4ad5kaf479afnbj2/abc123def4567890", nil)
	request.Host = "127.0.0.1"
	request.Header.Set("Accept", "text/html")
	response := httptest.NewRecorder()
	a.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("page response %d %s", response.Code, response.Header().Get("Content-Type"))
	}
	html := response.Body.String()
	localLink := "vless://00000000-0000-0000-0000-000000000001@127.0.0.1:23011"
	remoteLink := "vless://00000000-0000-0000-0000-000000000001@127.0.0.1:23012"
	if !strings.Contains(html, localLink) || !strings.Contains(html, remoteLink) {
		t.Fatalf("aggregated page missing local/remote node: %s", html[:min(len(html), 500)])
	}
	if !strings.Contains(html, "/isub4ad5kaf479afnbj2/abc123def4567890/clash") {
		t.Fatal("page did not advertise cross Clash URL")
	}
	if strings.Contains(html, "/isub4ad5kaf479afnbj2/abc123def4567890/clash/clash") {
		t.Fatal("cross Clash URL duplicated suffix")
	}
	if strings.Index(html, "Local Zulu") > strings.Index(html, "Remote Alpha") {
		t.Fatal("cross nodes are not ordered by inbound name")
	}

	request = httptest.NewRequest(http.MethodGet, "/isub4ad5kaf479afnbj2/abc123def4567890/clash", nil)
	request.Host = "127.0.0.1"
	response = httptest.NewRecorder()
	a.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "yaml") {
		t.Fatalf("Clash response %d %s", response.Code, response.Header().Get("Content-Type"))
	}
	if !strings.Contains(response.Body.String(), "server: \"127.0.0.1\"") || !strings.Contains(response.Body.String(), "port: 23012") {
		t.Fatalf("remote node missing from Clash YAML: %s", response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/isub4ad5kaf479afnbj2/abc123def4567890", nil)
	request.Host = "127.0.0.1"
	response = httptest.NewRecorder()
	a.routes().ServeHTTP(response, request)
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(response.Body.String()))
	if err != nil || !bytes.Contains(decoded, []byte(remoteLink)) {
		t.Fatalf("plain cross subscription missing remote link")
	}

	if err = a.cross.clear(); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/isub4ad5kaf479afnbj2/abc123def4567890/clash", nil)
	request.Host = "127.0.0.1"
	response = httptest.NewRecorder()
	a.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "23012") {
		t.Fatal("clearing cache left remote node")
	}
	if _, err = os.Stat(filepath.Join(pa.dir, crossSubscriptionCacheFile)); err != nil {
		t.Fatal(err)
	}
	_ = pb
}

func TestCrossSubscriptionRemoteOnlyUsernameUsesCachedToken(t *testing.T) {
	a, pa, b, _ := crossSubscriptionFixture(t)
	a.manager.mu.Lock()
	delete(a.manager.state.SubscriptionTokens, "alice")
	a.manager.mu.Unlock()
	remoteToken := "remote1234567890"
	remoteOnly := syncTestInbound("Remote Only", 23014)
	remoteOnly.Clients[0].Username = "remote-user"
	remoteOnly.Clients[0].Name = "Remote User"
	if err := a.cross.update(b.peers.view().Self, []Inbound{remoteOnly}, map[string]string{"remote-user": remoteToken}, nil); err != nil {
		t.Fatal(err)
	}
	view := a.crossSubscriptionView(httptest.NewRequest(http.MethodGet, "/", nil))
	found := false
	for _, user := range view.Users {
		if user.Username == "remote-user" && strings.HasSuffix(user.URL, "/"+remoteToken) {
			found = true
		}
	}
	if !found {
		t.Fatalf("remote-only user URL missing: %+v", view.Users)
	}
	request := httptest.NewRequest(http.MethodGet, "/isub4ad5kaf479afnbj2/"+remoteToken, nil)
	request.Host = "127.0.0.1"
	request.Header.Set("Accept", "text/html")
	response := httptest.NewRecorder()
	a.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Remote Only") {
		t.Fatalf("remote-only page unavailable: %d", response.Code)
	}
	_ = pa
}

func TestCrossSubscriptionPullRedactsServerSecretsAndUsesSubscriptionPort(t *testing.T) {
	a, pa, b, _ := crossSubscriptionFixture(t)
	a.manager.mu.Lock()
	a.manager.state.Settings.SubscriptionPort = 24443
	a.manager.state.Settings.PanelDomain = "panel.example.com"
	input := syncTestInbound("Secret-bearing", 23015)
	input.Certificate = "-----BEGIN CERTIFICATE-----\nprivate-certificate\n-----END CERTIFICATE-----"
	input.PrivateKey = "-----BEGIN PRIVATE KEY-----\nprivate-key\n-----END PRIVATE KEY-----"
	input.ClientAuthCert = "-----BEGIN CERTIFICATE-----\nclient-ca\n-----END CERTIFICATE-----"
	input.Reality.PrivateKey = "server-reality-private-key"
	input.Decryption = "server-decryption-key"
	input.ECHKey = "server-ech-key"
	a.manager.state.Inbounds = []Inbound{input}
	a.manager.mu.Unlock()
	redacted, tokens, err := a.crossPanelPullInbounds()
	if err != nil {
		t.Fatal(err)
	}
	if len(redacted) != 1 || len(tokens) != 2 {
		t.Fatalf("unexpected redacted pull: %+v %v", redacted, tokens)
	}
	data, _ := json.Marshal(redacted[0])
	for _, secret := range []string{"private-certificate", "private-key", "client-ca", "server-reality-private-key", "server-decryption-key", "server-ech-key"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("server secret leaked in pull: %s", secret)
		}
	}
	view := a.crossSubscriptionView(httptest.NewRequest(http.MethodGet, "/", nil))
	if len(view.Users) < 1 || !strings.Contains(view.Users[0].URL, ":24443/") {
		t.Fatalf("dedicated subscription port missing: %+v", view.Users)
	}
	_ = pa
	_ = b
}

func TestCrossSubscriptionPullJobAndCacheFailureRetention(t *testing.T) {
	a, pa, b, _ := crossSubscriptionFixture(t)
	if err := a.cross.update(b.peers.view().Self, []Inbound{syncTestInbound("Cached Remote", 23013)}, map[string]string{"alice": "abc123def4567890"}, nil); err != nil {
		t.Fatal(err)
	}
	input := httptest.NewRequest(http.MethodPost, "/hidden-panel/api/cross-subscriptions", nil)
	input.AddCookie(&http.Cookie{Name: "mui_session", Value: "test-session"})
	response := httptest.NewRecorder()
	a.routes().ServeHTTP(response, input)
	if response.Code != http.StatusAccepted {
		t.Fatalf("start pull status %d: %s", response.Code, response.Body.String())
	}
	var started apiResponse
	if err := json.Unmarshal(response.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	viewBytes, _ := json.Marshal(started.Data)
	var view crossSubscriptionView
	if err := json.Unmarshal(viewBytes, &view); err != nil {
		t.Fatal(err)
	}
	if view.ActiveJob == "" {
		t.Fatal("pull did not expose active job")
	}
	jobID := view.ActiveJob
	deadline := time.Now().Add(10 * time.Second)
	for {
		request := httptest.NewRequest(http.MethodGet, "/hidden-panel/api/cross-subscriptions?job="+jobID, nil)
		request.AddCookie(&http.Cookie{Name: "mui_session", Value: "test-session"})
		response = httptest.NewRecorder()
		a.routes().ServeHTTP(response, request)
		var envelope struct {
			Data crossSubscriptionJob `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Data.Done {
			if envelope.Data.Targets[0].Status != "success" {
				t.Fatalf("pull target failed: %+v", envelope.Data.Targets)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pull job did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if source := a.cross.snapshot()[b.peers.view().Self.ID]; len(source.Inbounds) != 1 || source.Inbounds[0].Name != "Remote Alpha" {
		t.Fatal("successful pull did not replace stale cache")
	}
	if err := a.cross.update(b.peers.view().Self, nil, nil, io.ErrUnexpectedEOF); err != nil {
		t.Fatal(err)
	}
	if source := a.cross.snapshot()[b.peers.view().Self.ID]; len(source.Inbounds) != 1 || source.LastError == "" {
		t.Fatal("failed pull discarded retained cache")
	}
	_ = pa
}
