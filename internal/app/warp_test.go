package app

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

func warpFixture() WarpDevice {
	var device WarpDevice
	_ = json.Unmarshal([]byte(`{"id":"00000000-0000-0000-0000-000000000001","token":"test-access-token","name":"m-ui","model":"m-ui","enabled":true,"account":{"license":"aaaaaaaa-bbbbbbbb-cccccccc","account_type":"free","role":"child","premium_data":0,"quota":0,"usage":0},"config":{"client_id":"AQID","interface":{"addresses":{"v4":"172.16.0.2","v6":"2606:4700:110:8abc::2"}},"peers":[{"public_key":"AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=","endpoint":{"host":"engage.cloudflareclient.com:2408"}}]}}`), &device)
	return device
}

func warpFixtureAccount() *WarpAccount {
	device := warpFixture()
	return &WarpAccount{AccessToken: device.Token, DeviceID: device.ID, LicenseKey: device.Account.License, PrivateKey: base64.StdEncoding.EncodeToString(make([]byte, 32)), Device: device}
}

func TestWarpAccountLifecycleAndConcurrentCreate(t *testing.T) {
	var creates int
	var publicKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/reg":
			creates++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			publicKey, _ = body["key"].(string)
			if body["tos"] == "" || body["model"] != "m-ui" || body["private-key"] != nil || r.Header.Get("CF-Client-Version") == "" {
				t.Error("bad registration request")
			}
			_ = json.NewEncoder(w).Encode(warpFixture())
		case r.Method == http.MethodGet:
			if r.Header.Get("Authorization") != "Bearer test-access-token" {
				t.Error("token was not attached")
			}
			device := warpFixture()
			device.Account.PremiumData = 4096
			_ = json.NewEncoder(w).Encode(device)
		case r.Method == http.MethodPut:
			if r.URL.Path != "/reg/00000000-0000-0000-0000-000000000001/account" {
				t.Error("bad license path")
			}
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(WarpAccountInfo{License: body["license"], AccountType: "unlimited"})
		default:
			t.Error("unexpected external action")
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	m := &CoreManager{dataDir: t.TempDir(), state: defaultState(), warpClient: &warpClient{baseURL: server.URL, httpClient: server.Client()}}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.createWarpAccount(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if creates != 1 {
		t.Fatalf("created %d remote devices", creates)
	}
	account := m.warpSnapshot()
	secret, err := decodeWarpKey(account.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdh.X25519().NewPrivateKey(secret)
	if err != nil {
		t.Fatal(err)
	}
	if base64.StdEncoding.EncodeToString(key.PublicKey().Bytes()) != publicKey {
		t.Fatal("public and private keys do not match")
	}
	if len(m.state.Outbounds) != 0 {
		t.Fatal("Create must not add an outbound before Add Outbound")
	}
	refreshed, err := m.refreshWarpAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Device.Account.PremiumData != 4096 || account.Device.Account.PremiumData != 0 {
		t.Fatal("refresh mutated published account")
	}
	updated, err := m.updateWarpLicense(context.Background(), "dddddddd-eeeeeeee-ffffffff")
	if err != nil {
		t.Fatal(err)
	}
	if updated.LicenseKey != "dddddddd-eeeeeeee-ffffffff" || updated.Device.Account.AccountType != "unlimited" {
		t.Fatal("license update missing")
	}
	reloaded := &CoreManager{dataDir: m.dataDir}
	if err := reloaded.load(); err != nil {
		t.Fatal(err)
	}
	if reloaded.state.WARP.PrivateKey != account.PrivateKey || reloaded.state.WARP.LicenseKey != updated.LicenseKey {
		t.Fatal("credentials not persisted")
	}
	if err := m.deleteWarpAccount(); err != nil {
		t.Fatal(err)
	}
	if m.warpSnapshot() != nil {
		t.Fatal("local Delete did not clear account")
	}
}

func TestWarpFailureDoesNotPersistOrLeakCredentials(t *testing.T) {
	for _, body := range []string{`{"success":false,"errors":[{"message":"test-access-token"}]}`, `{}`, `null`, `not json`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		m := &CoreManager{dataDir: t.TempDir(), state: defaultState(), warpClient: &warpClient{baseURL: server.URL, httpClient: server.Client()}}
		if _, err := m.createWarpAccount(context.Background()); err == nil {
			t.Fatalf("accepted malformed response %s", body)
		} else if strings.Contains(err.Error(), "test-access-token") {
			t.Fatal("upstream credentials leaked in error")
		}
		if m.warpSnapshot() != nil {
			t.Fatal("failed registration persisted")
		}
		server.Close()
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
	defer server.Close()
	m := &CoreManager{dataDir: t.TempDir(), state: State{WARP: warpFixtureAccount()}, warpClient: &warpClient{baseURL: server.URL, httpClient: server.Client()}}
	before := m.warpSnapshot()
	if _, err := m.updateWarpLicense(context.Background(), "dddddddd-eeeeeeee-ffffffff"); err == nil {
		t.Fatal("accepted rate limit")
	}
	if _, err := m.refreshWarpAccount(context.Background()); err == nil {
		t.Fatal("accepted rate limit")
	}
	if m.warpSnapshot() != before {
		t.Fatal("failed remote request replaced local credentials")
	}
	if _, err := m.updateWarpLicense(context.Background(), "bad-license"); err == nil {
		t.Fatal("accepted invalid license")
	}
}

func TestWarpWireGuardConfigAndRouting(t *testing.T) {
	account := warpFixtureAccount()
	item, err := warpOutbound(account)
	if err != nil {
		t.Fatal(err)
	}
	item.ID = "warp-id"
	items, err := normalizeMihomoOutbounds([]MihomoOutbound{item})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].WarpDeviceID != account.DeviceID {
		t.Fatal("normalization lost WARP identity")
	}
	data, err := mihomoOutboundYAML(items)
	if err != nil {
		t.Fatal(err)
	}
	var node map[string]any
	if err = yaml.Unmarshal(data, &node); err != nil {
		t.Fatal(err)
	}
	if node["type"] != "wireguard" || node["ip"] != "172.16.0.2" || node["port"] != 2408 || node["mtu"] != 1420 || len(node["reserved"].([]any)) != 3 {
		t.Fatalf("incorrect WireGuard YAML: %s", data)
	}
	if node["reserved"].([]any)[2] != 3 {
		t.Fatal("client_id bytes were not decoded")
	}
	b := defaultMihomoBasics()
	b.WarpDomains = []string{"example.com", "geosite:google"}
	b.IPv4Domains = []string{"example.net"}
	b.BlockDomains = []string{"blocked.example.com"}
	if _, _, _, err = compileMihomoBasics(b, nil, nil); err == nil {
		t.Fatal("WARP rules without WARP outbound were accepted")
	}
	_, _, rules, err := compileMihomoBasics(b, items, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rules[0] != "DOMAIN-SUFFIX,blocked.example.com,REJECT" || rules[1] != "DOMAIN-SUFFIX,example.net,MUI-IPV4" || rules[2] != "DOMAIN-SUFFIX,example.com,warp" {
		t.Fatalf("unexpected rule order: %v", rules)
	}
	renamed := strings.Replace(string(data), `name: "warp"`, `name: "my-warp"`, 1)
	parsed, _, err := convertOutboundInput(mihomoYAMLParseRequest{YAML: renamed, Context: items, EditingID: item.ID})
	if err != nil {
		t.Fatal(err)
	}
	if parsed[0].WarpDeviceID != account.DeviceID || findWarpOutbound(parsed).Name != "my-warp" {
		t.Fatal("rename lost WARP identity")
	}
	_, _, rules, err = compileMihomoBasics(b, parsed, nil)
	if err != nil || rules[2] != "DOMAIN-SUFFIX,example.com,my-warp" {
		t.Fatal("WARP routing did not follow renamed outbound")
	}
	invalid := *account
	invalid.Device.Config.ClientID = "bad"
	if _, err = warpOutbound(&invalid); err == nil {
		t.Fatal("accepted invalid reserved bytes")
	}
	core := os.Getenv("MUI_TEST_CORE")
	if core == "" {
		return
	}
	state := defaultState()
	state.Inbounds = nil
	state.Outbounds = items
	b.WarpDomains = []string{"example.com"}
	state.MihomoBasics = &b
	m := &CoreManager{dataDir: t.TempDir(), state: state}
	config, err := m.renderConfig()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(m.dataDir, "config.yaml")
	if err = os.WriteFile(file, config, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(core, "-d", m.dataDir, "-t", "-f", file).CombinedOutput(); err != nil {
		t.Fatalf("Mihomo rejected WARP: %v\n%s", err, output)
	}
}

func TestWarpEndpointsRequireAuthAndHideAccountFromPolling(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir(), state: defaultState()}
	m.state.WARP = warpFixtureAccount()
	app := &App{manager: m, session: "test-session"}
	for _, test := range []struct{ method, path string }{{"GET", "/api/mihomo/warp"}, {"POST", "/api/mihomo/warp/create"}, {"POST", "/api/mihomo/warp/refresh"}, {"POST", "/api/mihomo/warp/license"}, {"DELETE", "/api/mihomo/warp"}} {
		rec := httptest.NewRecorder()
		app.routes().ServeHTTP(rec, httptest.NewRequest(test.method, test.path, nil))
		if rec.Code != 401 {
			t.Fatalf("unauthenticated %s: %d", test.path, rec.Code)
		}
	}
	for _, path := range []string{"/api/state", "/api/mihomo/warp"} {
		req := httptest.NewRequest("GET", path, nil)
		req.AddCookie(&http.Cookie{Name: "mui_session", Value: "test-session"})
		rec := httptest.NewRecorder()
		app.routes().ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		contains := strings.Contains(rec.Body.String(), "test-access-token")
		if path == "/api/state" && contains {
			t.Fatal("WARP account leaked through general polling")
		}
		if path == "/api/mihomo/warp" && (!contains || rec.Header().Get("Cache-Control") != "no-store") {
			t.Fatal("account view missing or cacheable")
		}
	}
}

// Explicit opt-in fixture for browser tests; never contacts Cloudflare.
func TestWarpBrowserFixture(t *testing.T) {
	address := os.Getenv("MUI_WARP_BROWSER_ADDR")
	if address == "" {
		t.Skip("browser fixture disabled")
	}
	dataDir := os.Getenv("MUI_WARP_BROWSER_DATA")
	if dataDir == "" {
		t.Fatal("MUI_WARP_BROWSER_DATA is required")
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	cloudflare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "PUT" {
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "rejected") {
				_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"rejected"}]}`))
				return
			}
			_ = json.NewEncoder(w).Encode(WarpAccountInfo{License: "dddddddd-eeeeeeee-ffffffff", AccountType: "unlimited"})
			return
		}
		_ = json.NewEncoder(w).Encode(warpFixture())
	}))
	defer cloudflare.Close()
	m := &CoreManager{dataDir: dataDir, state: defaultState(), warpClient: &warpClient{baseURL: cloudflare.URL, httpClient: cloudflare.Client()}}
	m.state.Inbounds = nil
	m.state.Settings.PanelListen = address
	app := &App{manager: m, session: "fixture-session"}
	t.Logf("WARP browser fixture: http://%s", address)
	if err := http.ListenAndServe(address, app.routes()); err != nil {
		t.Fatal(err)
	}
}
