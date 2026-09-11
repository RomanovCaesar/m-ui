package app

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func newSyncPanel(t *testing.T, name string) (*App, *meshTestPanel) {
	t.Helper()
	dir := t.TempDir()
	p, err := newPeerNetwork(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{peers: p, session: "test-session", manager: &CoreManager{dataDir: dir, state: defaultState()}}
	app.manager.state.Inbounds = nil
	app.manager.state.Settings.PanelPath = "/hidden-panel/"
	s := httptest.NewServer(app.routes())
	t.Cleanup(s.Close)
	t.Cleanup(p.client.CloseIdleConnections)
	if err = p.configure(name, s.URL); err != nil {
		t.Fatal(err)
	}
	return app, &meshTestPanel{p, s, dir}
}
func syncTestInbound(id string, port int) Inbound {
	return Inbound{ID: id, Name: id, Type: "vless", Listen: "0.0.0.0", Port: port, Enabled: true, UDP: true, Decryption: "none", Clients: []Client{
		{ID: "alice-id", Name: "Alice email", Username: "alice", UUID: "00000000-0000-0000-0000-000000000001", Enabled: true},
		{ID: "bob-id", Name: "Bob email", Username: "bob", UUID: "00000000-0000-0000-0000-000000000002", Enabled: true},
	}}
}
func syncSelection(in Inbound, ids ...string) inboundSyncSelection {
	return inboundSyncSelection{InboundID: in.ID, Revision: inboundSyncRevision(in), ClientIDs: ids}
}
func syncAPICall(t *testing.T, app *App, method, url string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(method, url, bytes.NewReader(data))
	req.AddCookie(&http.Cookie{Name: "mui_session", Value: "test-session"})
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, req)
	return w
}

func TestInboundSyncSelectedClientsMergePersistenceAndCounters(t *testing.T) {
	a, pa := newSyncPanel(t, "A")
	b, pb := newSyncPanel(t, "B")
	connectMeshPanels(t, pa, pb)
	in := syncTestInbound("VLESS", 23001)
	in.Traffic.Up = 500
	in.Clients[0].Traffic.Up = 99
	in.Clients[0].LastOnline = "source-time"
	selected, err := selectSyncInbounds([]Inbound{in}, []inboundSyncSelection{syncSelection(in, "alice-id")})
	if err != nil {
		t.Fatal(err)
	}
	results, err := a.sendInboundSync(context.Background(), b.peers.view().Self, selected)
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatalf("first sync: %v %+v", err, results)
	}
	b.manager.mu.Lock()
	target := b.manager.state.Inbounds[0]
	if len(target.Clients) != 1 || target.Clients[0].Username != "alice" || target.Traffic.Up != 0 || target.Clients[0].Traffic.Up != 0 || target.Clients[0].LastOnline != "" {
		t.Fatal("selection or counters leaked")
	}
	targetID := target.ID
	clientID := target.Clients[0].ID
	createdAt := target.Clients[0].CreatedAt
	b.manager.state.Inbounds[0].Traffic.Up = 150
	b.manager.state.Inbounds[0].Clients[0].Traffic.Up = 75
	b.manager.state.Inbounds[0].Clients[0].LastOnline = "target-time"
	token := b.manager.state.SubscriptionTokens["alice"]
	b.manager.mu.Unlock()
	selected, _ = selectSyncInbounds([]Inbound{in}, []inboundSyncSelection{syncSelection(in, "bob-id")})
	if _, err = a.sendInboundSync(context.Background(), b.peers.view().Self, selected); err != nil {
		t.Fatal(err)
	}
	in.Clients[0].Notes = "updated metadata"
	selected, _ = selectSyncInbounds([]Inbound{in}, []inboundSyncSelection{syncSelection(in, "alice-id")})
	results, err = a.sendInboundSync(context.Background(), b.peers.view().Self, selected)
	if err != nil || results[0].Status != "updated" {
		t.Fatalf("repeat: %v %+v", err, results)
	}
	b.manager.mu.Lock()
	state := cloneSyncState(b.manager.state)
	b.manager.mu.Unlock()
	target = state.Inbounds[0]
	if target.Clients[0].CreatedAt != createdAt {
		t.Fatal("target client creation time changed")
	}
	if len(state.Inbounds) != 1 || len(target.Clients) != 2 || target.ID != targetID || target.Clients[0].ID != clientID || target.Traffic.Up != 150 || target.Clients[0].Traffic.Up != 75 || target.Clients[0].LastOnline != "target-time" || target.Clients[0].Notes != "updated metadata" {
		t.Fatal("repeat did not preserve target identity/counters/unselected client")
	}
	if token == "" || state.SubscriptionTokens["alice"] != token {
		t.Fatal("target subscription token changed")
	}
	loaded := &CoreManager{dataDir: pb.dir}
	if err = loaded.load(); err != nil {
		t.Fatal(err)
	}
	if len(loaded.state.Inbounds) != 1 || loaded.state.Inbounds[0].SyncOrigin.NodeID != a.peers.view().Self.ID {
		t.Fatal("origin not persisted")
	}
	yaml, err := os.ReadFile(filepath.Join(pb.dir, "config.yaml"))
	if err != nil || !bytes.Contains(yaml, []byte("alice")) || !bytes.Contains(yaml, []byte("bob")) {
		t.Fatal("target YAML not written")
	}
}

func TestInboundSyncConflictsDoNotOverwriteLocalInbounds(t *testing.T) {
	a, _ := newSyncPanel(t, "A")
	local := syncTestInbound("Local", 23001)
	if err := a.manager.saveInbound(local); err != nil {
		t.Fatal(err)
	}
	conflict := syncTestInbound("Remote conflict", 23001)
	good := syncTestInbound("Remote good", 23002)
	results := a.manager.receiveSyncedInbounds(strings.Repeat("a", 32), []Inbound{conflict, good})
	if results[0].Status != "failed" || !strings.Contains(results[0].Message, "端口") || results[1].Status != "created" {
		t.Fatalf("unexpected results: %+v", results)
	}
	if a.manager.state.Inbounds[0].ID != local.ID || len(a.manager.state.Inbounds) != 2 {
		t.Fatal("local inbound overwritten")
	}
	reserved := syncTestInbound("reserved", 2053)
	results = a.manager.receiveSyncedInbounds(strings.Repeat("a", 32), []Inbound{reserved})
	if results[0].Status != "failed" {
		t.Fatal("panel port overwritten")
	}
	// Failure to persist never publishes the candidate state.
	a.manager.dataDir = filepath.Join(t.TempDir(), "missing")
	results = a.manager.receiveSyncedInbounds(strings.Repeat("a", 32), []Inbound{syncTestInbound("unwritable", 23003)})
	if results[0].Status != "failed" || len(a.manager.state.Inbounds) != 2 {
		t.Fatal("failed save mutated state")
	}
	// A remote node cannot claim somebody else's origin and update their data.
	forged := good
	forged.SyncOrigin = &InboundSyncOrigin{NodeID: strings.Repeat("a", 32), InboundID: good.ID}
	results = a.manager.receiveSyncedInbounds(strings.Repeat("b", 32), []Inbound{forged})
	if results[0].Status != "failed" {
		t.Fatal("forged origin accepted")
	}
}

func TestInboundSyncRejectsStaleMissingAndSingleClientSelections(t *testing.T) {
	in := syncTestInbound("test", 23001)
	for _, choice := range []inboundSyncSelection{syncSelection(in, "missing"), syncSelection(in, "alice-id", "alice-id"), {InboundID: in.ID, Revision: "stale", ClientIDs: []string{"alice-id"}}} {
		if _, err := selectSyncInbounds([]Inbound{in}, []inboundSyncSelection{choice}); err == nil {
			t.Fatal("invalid selection accepted")
		}
	}
	a, _ := newSyncPanel(t, "A")
	ss := Inbound{ID: "ss", Name: "SS", Type: "shadowsocks", Listen: "0.0.0.0", Port: 23002, Cipher: "aes-128-gcm", Enabled: true, Clients: []Client{{ID: "one", Name: "one", Username: "alice", Password: "secret-password", Enabled: true}}}
	results := a.manager.receiveSyncedInbounds(strings.Repeat("b", 32), []Inbound{ss})
	if results[0].Status != "created" {
		t.Fatalf("SS not synchronized: %+v", results)
	}
	ss.Clients[0].ID = "two"
	ss.Clients[0].Username = "bob"
	results = a.manager.receiveSyncedInbounds(strings.Repeat("b", 32), []Inbound{ss})
	if results[0].Status != "failed" || len(a.manager.state.Inbounds[0].Clients) != 1 {
		t.Fatal("single-client inbound silently truncated")
	}
	in.TLS = true
	in.Certificate = "/etc/secret.pem"
	in.PrivateKey = "/etc/key.pem"
	if err := validateSyncCertificates(in); err == nil {
		t.Fatal("target file path accepted")
	}
}

func TestInboundSyncCertificateFilesBecomePortablePEM(t *testing.T) {
	a, pa := newSyncPanel(t, "A")
	b, pb := newSyncPanel(t, "B")
	connectMeshPanels(t, pa, pb)
	tlsServer := httptest.NewTLSServer(http.NotFoundHandler())
	defer tlsServer.Close()
	pair := tlsServer.TLS.Certificates[0]
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pair.Certificate[0]})
	keyData, err := x509.MarshalPKCS8PrivateKey(pair.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	key := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyData})
	in := syncTestInbound("TLS portable", 23005)
	in.TLS = true
	in.CertificateMode = "file"
	in.Certificate = filepath.Join(pa.dir, "cert.pem")
	in.PrivateKey = filepath.Join(pa.dir, "key.pem")
	in.ClientAuthCert = in.Certificate
	if err = os.WriteFile(in.Certificate, cert, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(in.PrivateKey, key, 0600); err != nil {
		t.Fatal(err)
	}
	if err = portableSyncCertificates(&in); err != nil {
		t.Fatal(err)
	}
	if in.CertificateMode != "content" || in.Certificate != string(cert) || in.PrivateKey != string(key) {
		t.Fatal("certificate files not materialized")
	}
	results, err := a.sendInboundSync(context.Background(), b.peers.view().Self, []Inbound{in})
	if err != nil || results[0].Status != "created" {
		t.Fatalf("TLS transfer: %v %+v", err, results)
	}
	stored := b.manager.state.Inbounds[0]
	if stored.Certificate != string(cert) || stored.PrivateKey != string(key) || stored.ClientAuthCert != string(cert) {
		t.Fatal("certificate contents not preserved")
	}
}

func TestInboundSyncPersistenceFailureRollsBackGeneratedYAML(t *testing.T) {
	a, pa := newSyncPanel(t, "target")
	if err := a.manager.saveInbound(syncTestInbound("existing", 23001)); err != nil {
		t.Fatal(err)
	}
	if err := a.manager.writeConfig(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(pa.dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(pa.dir, "state.json.tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	results := a.manager.receiveSyncedInbounds(strings.Repeat("a", 32), []Inbound{syncTestInbound("new", 23002)})
	if results[0].Status != "failed" || len(a.manager.state.Inbounds) != 1 {
		t.Fatal("failed persistence published state")
	}
	after, err := os.ReadFile(filepath.Join(pa.dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("old YAML not restored")
	}
}

func TestInboundSyncEncryptedAuthenticatedAndReplayProtected(t *testing.T) {
	a, pa := newSyncPanel(t, "A")
	b, pb := newSyncPanel(t, "B")
	connectMeshPanels(t, pa, pb)
	var mu sync.Mutex
	var captured []byte
	var responseBody []byte
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		rr := httptest.NewRecorder()
		b.routes().ServeHTTP(rr, r)
		var message inboundSyncWire
		_ = json.Unmarshal(body, &message)
		if message.Kind == "apply" {
			mu.Lock()
			captured = slices.Clone(body)
			responseBody = slices.Clone(rr.Body.Bytes())
			mu.Unlock()
		}
		for k, values := range rr.Header() {
			w.Header()[k] = values
		}
		w.WriteHeader(rr.Code)
		w.Write(rr.Body.Bytes())
	}))
	defer proxy.Close()
	node := b.peers.view().Self
	node.Endpoint = proxy.URL
	in := syncTestInbound("private inbound name", 23001)
	in.Clients[0].Password = "DO-NOT-SEND-IN-PLAINTEXT"
	if _, err := a.sendInboundSync(context.Background(), node, []Inbound{in}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	body := slices.Clone(captured)
	reply := slices.Clone(responseBody)
	mu.Unlock()
	for _, secret := range []string{in.Name, in.Clients[0].UUID, in.Clients[0].Password} {
		if bytes.Contains(body, []byte(secret)) || bytes.Contains(reply, []byte(secret)) {
			t.Fatal("plaintext credentials/config exposed")
		}
	}
	request := httptest.NewRequest("POST", inboundSyncWirePath, bytes.NewReader(body))
	w := httptest.NewRecorder()
	b.routes().ServeHTTP(w, request)
	if w.Code != 403 {
		t.Fatalf("replay status: %d", w.Code)
	}
	var message inboundSyncWire
	json.Unmarshal(body, &message)
	message.Ciphertext += "tampered"
	if err := randomToken(&message.Nonce); err != nil {
		t.Fatal(err)
	}
	tampered, _ := json.Marshal(message)
	w = httptest.NewRecorder()
	b.routes().ServeHTTP(w, httptest.NewRequest("POST", inboundSyncWirePath, bytes.NewReader(tampered)))
	if w.Code != 403 {
		t.Fatal("tampered signature accepted")
	}
	stranger, _ := newSyncPanel(t, "stranger")
	if _, err := stranger.sendInboundSync(context.Background(), b.peers.view().Self, []Inbound{in}); err == nil {
		t.Fatal("unknown node accepted")
	}
	// Disconnect after hello but before apply also revokes mutation rights.
	private, _ := ecdh.X25519().GenerateKey(rand.Reader)
	hello, err := a.syncWireExchange(context.Background(), b.peers.view().Self, inboundSyncWire{Kind: "hello", Recipient: node.ID, Key: base64.StdEncoding.EncodeToString(private.PublicKey().Bytes())})
	if err != nil {
		t.Fatal(err)
	}
	key, _ := syncSessionKey(private, hello.Key, a.peers.view().Self.ID, node.ID)
	payload, _ := json.Marshal([]Inbound{in})
	encoded, _ := encryptSync(key, payload, "request:"+hello.Nonce)
	if err = b.peers.disconnect(a.peers.view().Self.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.syncWireExchange(context.Background(), node, inboundSyncWire{Kind: "apply", Recipient: node.ID, Session: hello.Nonce, Ciphertext: encoded}); err == nil {
		t.Fatal("revoked node can mutate")
	}
	w = httptest.NewRecorder()
	b.subscriptionRoutes().ServeHTTP(w, httptest.NewRequest("POST", inboundSyncWirePath, strings.NewReader("{}")))
	if w.Code != 404 {
		t.Fatal("subscription port exposed control route")
	}
}

func TestInboundSyncJobMultipleTargetsAndIdempotentSubmission(t *testing.T) {
	a, pa := newSyncPanel(t, "A")
	b, pb := newSyncPanel(t, "B")
	c, pc := newSyncPanel(t, "C")
	connectMeshPanels(t, pa, pb)
	connectMeshPanels(t, pa, pc)
	in := syncTestInbound("source", 23001)
	if err := a.manager.saveInbound(in); err != nil {
		t.Fatal(err)
	}
	conflict := syncTestInbound("local target", 23001)
	if err := c.manager.saveInbound(conflict); err != nil {
		t.Fatal(err)
	}
	input := inboundSyncRequest{PeerIDs: []string{pb.network.view().Self.ID, pc.network.view().Self.ID}, Selections: []inboundSyncSelection{syncSelection(a.manager.state.Inbounds[0], "alice-id")}}
	if err := randomToken(&input.JobID); err != nil {
		t.Fatal(err)
	}
	w := syncAPICall(t, a, "POST", "/hidden-panel/api/inbound-sync", input)
	if w.Code != 202 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	w = syncAPICall(t, a, "POST", "/hidden-panel/api/inbound-sync", input)
	if w.Code != 200 {
		t.Fatal("duplicate job did not return existing task")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		w = syncAPICall(t, a, "GET", "/hidden-panel/api/inbound-sync?job="+input.JobID, nil)
		var response struct {
			Data inboundSyncJob `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		job := response.Data
		if job.Done {
			if job.Completed != 2 || job.Targets[0].Status != "success" || job.Targets[1].Status != "failed" {
				t.Fatalf("results %+v", job)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.manager.mu.Lock()
	count := len(b.manager.state.Inbounds)
	b.manager.mu.Unlock()
	if count != 1 {
		t.Fatal("duplicate transfer")
	}
	w = httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("POST", "/hidden-panel/api/inbound-sync", strings.NewReader("{}")))
	if w.Code != 401 {
		t.Fatal("admin API unprotected")
	}
	w = syncAPICall(t, a, "GET", "/hidden-panel/api/inbound-sync", nil)
	if bytes.Contains(w.Body.Bytes(), []byte(in.Clients[0].UUID)) {
		t.Fatal("options expose credentials")
	}
}

// Three disposable panels with known matrix cells and a deliberate target-port
// conflict, for the interactive browser verification of the complete workflow.
func TestInboundSyncBrowserFixture(t *testing.T) {
	if os.Getenv("MUI_SYNC_BROWSER") != "1" {
		t.Skip("browser fixture disabled")
	}
	apps := []*App{}
	panels := []*meshTestPanel{}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	for i, name := range []string{"Panel A", "Panel B", "Panel C"} {
		dir := t.TempDir()
		p, err := newPeerNetwork(dir)
		if err != nil {
			t.Fatal(err)
		}
		address := fmt.Sprintf("127.0.0.1:%d", 21560+i)
		if err = p.configure(name, "http://"+address); err != nil {
			t.Fatal(err)
		}
		m := &CoreManager{dataDir: dir, state: defaultState()}
		m.state.Settings.PanelListen = address
		m.state.Settings.PanelPath = "/testpath/"
		m.state.Inbounds = nil
		app := &App{manager: m, peers: p, session: "test-session"}
		app.cross, err = newCrossSubscriptionManager(dir)
		if err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		server := &http.Server{Handler: app.routes()}
		go server.Serve(listener)
		t.Cleanup(func() { server.Close() })
		go p.run(ctx)
		apps = append(apps, app)
		panels = append(panels, &meshTestPanel{p, &httptest.Server{URL: "http://" + address}, dir})
	}
	connectMeshPanels(t, panels[0], panels[1])
	connectMeshPanels(t, panels[0], panels[2])
	convergeMesh(panels...)
	first := syncTestInbound("VLESS Main", 23001)
	second := syncTestInbound("VLESS Backup", 23002)
	second.Clients = second.Clients[:1]
	ss := Inbound{ID: "ss", Name: "SS Single", Type: "shadowsocks", Listen: "0.0.0.0", Port: 23003, Cipher: "aes-128-gcm", Enabled: true, Clients: []Client{{ID: "ss-bob", Name: "Bob SS", Username: "bob", Password: "browser-fixture-secret", Enabled: true}}}
	snell := Inbound{ID: "snell", Name: "Snell Single", Type: "snell", Listen: "0.0.0.0", Port: 23004, Enabled: true, Clients: []Client{{ID: "snell-carol", Name: "Carol Snell", Username: "carol", Password: "browser-fixture-psk", Enabled: true}}}
	for _, in := range []Inbound{first, second, ss, snell} {
		if err := apps[0].manager.saveInbound(in); err != nil {
			t.Fatal(err)
		}
	}
	if err := apps[2].manager.saveInbound(syncTestInbound("Local conflicting listener", 23001)); err != nil {
		t.Fatal(err)
	}
	remoteB := syncTestInbound("Remote B Alpha", 23101)
	remoteB.Clients = remoteB.Clients[:1]
	if err := apps[1].manager.saveInbound(remoteB); err != nil {
		t.Fatal(err)
	}
	remoteC := syncTestInbound("Remote C Beta", 23102)
	remoteC.Clients[0].Username, remoteC.Clients[0].Name = "remote-only", "Remote Only"
	remoteC.Clients = remoteC.Clients[:1]
	if err := apps[2].manager.saveInbound(remoteC); err != nil {
		t.Fatal(err)
	}
	t.Log("Sync Inbound browser fixture ready: http://127.0.0.1:21560/testpath/ (admin/admin); targets 21561 and 21562")
	<-ctx.Done()
}
