package app

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPBKDF2SHA256KnownVector(t *testing.T) {
	got := hex.EncodeToString(pbkdf2SHA256([]byte("password"), []byte("salt"), 1, 32))
	want := "120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b"
	if got != want {
		t.Fatalf("pbkdf2 mismatch: got %s want %s", got, want)
	}
}

func TestPasswordRoundTrip(t *testing.T) {
	encoded, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded, "correct horse") {
		t.Fatal("encoded password contains plaintext")
	}
	if !verifyPassword(encoded, "correct horse battery staple") {
		t.Fatal("correct password rejected")
	}
	if verifyPassword(encoded, "wrong") {
		t.Fatal("wrong password accepted")
	}
}

func TestRenderConfigUsesManagedListenersOnly(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir(), state: State{
		Settings: Settings{APIAddress: defaultCoreAPI, APISecret: "secret", MixedPort: defaultCorePort, AllowLAN: true, Mode: "rule", LogLevel: "info"},
		Inbounds: []Inbound{{ID: "one", Name: "test-mixed", Type: "mixed", Listen: "127.0.0.1", Port: 19080, Enabled: true, UDP: true}},
	}}
	data, err := m.renderConfig()
	if err != nil {
		t.Fatal(err)
	}
	config := string(data)
	if strings.Contains(config, "mixed-port:") {
		t.Fatal("mixed-port duplicates the managed listener")
	}
	if !strings.Contains(config, "listeners:\n  - name: \"test-mixed\"\n    type: \"mixed\"\n    listen: \"127.0.0.1\"") {
		t.Fatalf("unexpected YAML listeners:\n%s", config)
	}
	if strings.HasPrefix(strings.TrimSpace(config), "{") {
		t.Fatalf("configuration is JSON, not YAML:\n%s", config)
	}
}

func TestMihomoOutboundsAndRoutingRenderToNativeConfig(t *testing.T) {
	state := defaultState()
	state.Inbounds = nil
	state.Outbounds = []MihomoOutbound{
		{ID: "proxy-1", Kind: "proxy", Name: "edge-ss", Type: "ss", Server: "edge.example.com", Port: 443, Password: "secret", Cipher: "aes-256-gcm", UDP: true, IPVersion: "ipv4-prefer"},
		{ID: "group-1", Kind: "group", Name: "Proxy", Type: "url-test", Proxies: []string{"edge-ss", "DIRECT"}, TestURL: defaultMihomoTestURL, Interval: 300, Tolerance: 50},
	}
	state.RoutingRules = []MihomoRoutingRule{
		{ID: "rule-1", Type: "DOMAIN-SUFFIX", Payload: "example.com", Target: "Proxy"},
		{ID: "rule-2", Type: "IP-CIDR", Payload: "10.0.0.0/8", Target: "DIRECT", NoResolve: true},
		{ID: "rule-3", Type: "MATCH", Target: "DIRECT"},
	}
	m := &CoreManager{dataDir: t.TempDir(), state: state}
	data, err := m.renderConfig()
	if err != nil {
		t.Fatal(err)
	}
	config := string(data)
	for _, expected := range []string{
		"proxies:\n  - name: \"edge-ss\"\n    type: \"ss\"\n    server: \"edge.example.com\"\n    port: 443",
		"proxy-groups:\n  - name: \"Proxy\"\n    type: \"url-test\"",
		"proxies:\n      - \"edge-ss\"\n      - \"DIRECT\"",
		"tolerance: 50",
		"- \"DOMAIN-SUFFIX,example.com,Proxy\"",
		"- \"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve\"",
		"- \"MATCH,DIRECT\"",
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("managed Mihomo config is missing %q:\n%s", expected, config)
		}
	}
}

func TestMihomoSettingsValidationRejectsBrokenReferences(t *testing.T) {
	validProxy := MihomoOutbound{Kind: "proxy", Name: "node", Type: "ss", Server: "example.com", Port: 443, Password: "secret", Cipher: "aes-256-gcm"}
	for name, outbounds := range map[string][]MihomoOutbound{
		"duplicate names": {validProxy, validProxy},
		"missing member":  {validProxy, {Kind: "group", Name: "group", Type: "select", Proxies: []string{"missing"}}},
		"group cycle": {
			validProxy,
			{Kind: "group", Name: "group-a", Type: "select", Proxies: []string{"group-b"}},
			{Kind: "group", Name: "group-b", Type: "select", Proxies: []string{"group-a"}},
		},
	} {
		if _, err := normalizeMihomoOutbounds(outbounds); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}

	outbounds, err := normalizeMihomoOutbounds([]MihomoOutbound{validProxy})
	if err != nil {
		t.Fatal(err)
	}
	for name, rules := range map[string][]MihomoRoutingRule{
		"missing target": {{Type: "DOMAIN", Payload: "example.com", Target: "missing"}},
		"match not last": {{Type: "MATCH", Target: "DIRECT"}, {Type: "DOMAIN", Payload: "example.com", Target: "node"}},
		"bad cidr":       {{Type: "IP-CIDR", Payload: "10.0.0.0", Target: "DIRECT"}},
		"bad no resolve": {{Type: "DOMAIN", Payload: "example.com", Target: "DIRECT", NoResolve: true}},
	} {
		if _, err := normalizeMihomoRoutingRules(rules, outbounds); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}

func TestMihomoRulesAlwaysHaveFinalMatch(t *testing.T) {
	rules, err := normalizeMihomoRoutingRules([]MihomoRoutingRule{{Type: "DOMAIN", Payload: "example.com", Target: "DIRECT"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 || rules[1].Type != "MATCH" || rules[1].Target != "DIRECT" {
		t.Fatalf("rules = %#v, want an appended MATCH,DIRECT fallback", rules)
	}
}

func TestMihomoSettingsPageWiring(t *testing.T) {
	index, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	content := string(index)
	for _, needle := range []string{
		`data-view="mihomo"`, `id="view-mihomo"`, `Mihomo Settings`, `id="mihomo-settings-root"`,
		`id="mihomo-add-outbound"`, `id="mihomo-add-rule"`, `id="mihomo-warp"`, `id="mihomo-save"`,
		`mihomo-settings.css`, `mihomo-settings.js`,
	} {
		if !strings.Contains(content, needle) {
			t.Fatalf("web/index.html is missing Mihomo settings wiring %q", needle)
		}
	}
	if strings.Contains(content, `data-view="config"`) || strings.Contains(content, `id="view-config"`) || strings.Contains(content, "Mihomo Configs") {
		t.Fatal("the old Mihomo Config page must not remain in the sidebar or view list")
	}
	for file, needles := range map[string][]string{
		"web/mihomo-settings.js":  {"function openOutboundModal", "function openRuleModal", "Routing Rule", "function openWarpModal", "/api/mihomo/settings"},
		"web/mihomo-settings.css": {".mihomo-tabs", ".mihomo-table", ".mihomo-modal"},
	} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, needle := range needles {
			if !strings.Contains(string(data), needle) {
				t.Fatalf("%s is missing %q", file, needle)
			}
		}
	}
}

func TestDuplicateInboundPortRejected(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir(), state: State{
		Settings: Settings{Username: "admin", Password: "hash"},
		Inbounds: []Inbound{{ID: "one", Name: "first", Type: "mixed", Listen: "0.0.0.0", Port: 12080, Enabled: true}},
	}}
	err := m.saveInbound(Inbound{ID: "two", Name: "second", Type: "socks", Listen: "0.0.0.0", Port: 12080, Enabled: true})
	if err == nil {
		t.Fatal("expected duplicate port error")
	}
}

func TestInboundPortConflictRules(t *testing.T) {
	newManager := func(existing Inbound) *CoreManager {
		return &CoreManager{dataDir: t.TempDir(), state: State{
			Settings: Settings{Username: "admin", Password: "hash"},
			Inbounds: []Inbound{existing},
		}}
	}
	disabled := Inbound{ID: "one", Name: "first", Type: "mixed", Listen: "0.0.0.0", Port: 12080}
	if err := newManager(disabled).saveInbound(Inbound{ID: "two", Name: "second", Type: "socks", Listen: "0.0.0.0", Port: 12080, Enabled: true}); err == nil {
		t.Fatal("a disabled inbound still reserves its port")
	}
	wildcard := Inbound{ID: "one", Name: "first", Type: "mixed", Listen: "0.0.0.0", Port: 12080, Enabled: true}
	if err := newManager(wildcard).saveInbound(Inbound{ID: "two", Name: "second", Type: "socks", Listen: "127.0.0.1", Port: 12080, Enabled: true}); err == nil {
		t.Fatal("0.0.0.0 must collide with a specific address on the same port")
	}
	specific := Inbound{ID: "one", Name: "first", Type: "mixed", Listen: "127.0.0.1", Port: 12080, Enabled: true}
	if err := newManager(specific).saveInbound(Inbound{ID: "two", Name: "second", Type: "socks", Listen: "127.0.0.1", Port: 12081, Enabled: true}); err != nil {
		t.Fatalf("distinct ports must be accepted: %v", err)
	}
	if err := newManager(specific).saveInbound(Inbound{ID: "two", Name: "second", Type: "socks", Listen: "192.168.1.5", Port: 12080, Enabled: true}); err != nil {
		t.Fatalf("distinct interfaces may share a port: %v", err)
	}
	if err := newManager(specific).saveInbound(Inbound{ID: "one", Name: "renamed", Type: "mixed", Listen: "127.0.0.1", Port: 12080, Enabled: true}); err != nil {
		t.Fatalf("an inbound must not conflict with itself: %v", err)
	}
}

func TestApprovedDownloadHosts(t *testing.T) {
	allowed := []string{"github.com", "release-assets.githubusercontent.com", "fastly.jsdelivr.net", "cdn.jsdelivr.net"}
	for _, host := range allowed {
		if !isApprovedDownloadHost(host) {
			t.Fatalf("expected %s to be allowed", host)
		}
	}
	for _, host := range []string{"example.com", "github.com.example.com", "jsdelivr.net.example.org"} {
		if isApprovedDownloadHost(host) {
			t.Fatalf("expected %s to be rejected", host)
		}
	}
}

func TestShortCoreVersion(t *testing.T) {
	input := "Mihomo Meta v1.19.30 windows amd64 with go1.26.6 Sun Aug 16 10:03:02 UTC 2026"
	if got := shortCoreVersion(input); got != "v1.19.30" {
		t.Fatalf("unexpected short version: %q", got)
	}
}

func TestTrafficSnapshotAccumulatesDeltas(t *testing.T) {
	m := &CoreManager{
		dataDir:         t.TempDir(),
		connectionStats: map[string]connectionSample{},
		state: State{Inbounds: []Inbound{{
			ID: "inbound-1", Name: "listener-one", Clients: []Client{{ID: "client-1", Name: "alice", Username: "alice", Enabled: true}},
		}}},
	}
	snapshot := func(upload, download float64) map[string]any {
		return map[string]any{"uploadTotal": upload, "downloadTotal": download, "connections": []any{map[string]any{
			"id": "connection-1", "upload": upload, "download": download,
			"metadata": map[string]any{"inboundName": "listener-one", "inboundUser": "alice"},
		}}}
	}
	m.applyTrafficSnapshot(snapshot(100, 200))
	m.applyTrafficSnapshot(snapshot(150, 260))
	if got := m.state.Traffic.Up; got != 150 {
		t.Fatalf("unexpected global upload: %d", got)
	}
	if got := m.state.Traffic.Down; got != 260 {
		t.Fatalf("unexpected global download: %d", got)
	}
	if got := m.state.Inbounds[0].Traffic.Up; got != 150 {
		t.Fatalf("unexpected inbound upload: %d", got)
	}
	if got := m.state.Inbounds[0].Clients[0].Traffic.Down; got != 260 {
		t.Fatalf("unexpected client download: %d", got)
	}
}

func TestRenderConfigIncludesEnabledClients(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir(), state: State{
		Settings: Settings{APIAddress: defaultCoreAPI, APISecret: "secret", AllowLAN: true, Mode: "rule", LogLevel: "info"},
		Inbounds: []Inbound{{ID: "one", Name: "mixed-auth", Type: "mixed", Listen: "127.0.0.1", Port: 19081, Enabled: true, UDP: true, Clients: []Client{
			{ID: "client-one", Name: "alice", Username: "alice", Password: "secret-one", Enabled: true},
			{ID: "client-two", Name: "bob", Username: "bob", Password: "secret-two", Enabled: false},
		}}},
	}}
	data, err := m.renderConfig()
	if err != nil {
		t.Fatal(err)
	}
	config := string(data)
	if !strings.Contains(config, `username: "alice"`) || !strings.Contains(config, `password: "secret-one"`) {
		t.Fatalf("enabled client missing from config:\n%s", config)
	}
	if strings.Contains(config, `username: "bob"`) {
		t.Fatalf("disabled client included in config:\n%s", config)
	}
}

func TestAdvancedInboundFieldsRenderToYAML(t *testing.T) {
	inbound := Inbound{
		ID: "vless-one", Name: "vless-reality", Type: "vless", Listen: "0.0.0.0", Port: 2443, Enabled: true,
		Network: "grpc", GRPCServiceName: "m-ui-grpc", Decryption: "none",
		Clients: []Client{{ID: "client-one", Name: "alice", Username: "alice", UUID: "00000000-0000-0000-0000-000000000001", Flow: "xtls-rprx-vision", Enabled: true}},
		Reality: RealitySettings{Enabled: true, Dest: "example.com:443", PrivateKey: "private-key", ServerNames: "example.com,www.example.com", ShortIDs: "1234,5678"},
		Mux:     MuxSettings{Padding: true, BrutalEnabled: true, Up: "100 Mbps", Down: "200 Mbps"},
	}
	config, err := inboundConfig(inbound)
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalYAML(config)
	if err != nil {
		t.Fatal(err)
	}
	yaml := string(data)
	for _, expected := range []string{"grpc-service-name: \"m-ui-grpc\"", "reality-config:", "server-names:", "flow: \"xtls-rprx-vision\"", "mux-option:"} {
		if !strings.Contains(yaml, expected) {
			t.Fatalf("missing %q in YAML:\n%s", expected, yaml)
		}
	}
}

func TestShadowsocksRejectsMultipleClients(t *testing.T) {
	err := validateInbound(Inbound{Type: "shadowsocks", Cipher: "2022-blake3-aes-256-gcm", Clients: []Client{{Name: "one", Password: "client-one", Enabled: true}, {Name: "two", Password: "client-two", Enabled: true}}})
	if err == nil {
		t.Fatal("SS listener should reject multiple clients")
	}
}

func TestShadowsocksUsesClientPasswordFor2022(t *testing.T) {
	inbound := Inbound{Type: "shadowsocks", Cipher: "2022-blake3-aes-256-gcm", Password: "legacy-inbound-password", Clients: []Client{{Name: "one", Password: "client-one", Enabled: true}}}
	config, err := inboundConfig(inbound)
	if err != nil {
		t.Fatal(err)
	}
	if got := config["password"]; got != "client-one" {
		t.Fatalf("SS 2022 password = %v, want client password", got)
	}
}

func TestSubscriptionPathsAndTokens(t *testing.T) {
	settings := Settings{PanelPath: "/panel/", SubscriptionPath: "/sub", ClashPath: "/clash"}
	if got := subscriptionPublicPath(settings, "abc123def4567890"); got != "/sub/abc123def4567890" {
		t.Fatalf("subscription path = %q", got)
	}
	if got := subscriptionClashPublicPath(settings, "abc123def4567890"); got != "/sub/abc123def4567890/clash" {
		t.Fatalf("clash path = %q", got)
	}
	if got := subscriptionAssetPublicPath(settings); got != "/sub/_assets/qrious2.min.js" {
		t.Fatalf("subscription asset path = %q", got)
	}
	if got := subscriptionAssetPublicPath(Settings{PanelPath: "/panel/"}); got != "/_subscription-assets/qrious2.min.js" {
		t.Fatalf("root subscription asset path = %q", got)
	}
	token, err := randomSubscriptionToken()
	if err != nil || len(token) != 16 || !validSubscriptionToken(token) {
		t.Fatalf("generated subscription token = %q, err = %v", token, err)
	}
	generatedPath, err := randomSubscriptionPath()
	if err != nil || !validGeneratedSubscriptionPath(generatedPath) {
		t.Fatalf("generated subscription path = %q, err = %v", generatedPath, err)
	}
	if _, _, ok := matchSubscriptionPath(settings, "/panel/sub/abc123def4567890/clash"); ok {
		t.Fatal("panel path should be stripped before matching public subscription paths")
	}
	if got, format, ok := matchSubscriptionPath(settings, "/sub/abc123def4567890/clash"); !ok || format != subscriptionFormatClash || got != "abc123def4567890" {
		t.Fatalf("matched clash path = %q, format=%v, ok=%v", got, format, ok)
	}
	if err := validateSubscriptionPaths("/static", "/clash"); err == nil {
		t.Fatal("reserved panel paths must not be accepted as subscription paths")
	}
}

func TestSubscriptionServicePortAddressAndValidation(t *testing.T) {
	settings := defaultState().Settings
	serialized, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), `"subscriptionPort"`) {
		t.Fatal("the default subscription port must remain unset")
	}
	if got := subscriptionListenAddress(settings); got != settings.PanelListen {
		t.Fatalf("unset subscription port address = %q, want panel address %q", got, settings.PanelListen)
	}
	settings.PanelListen = "0.0.0.0:2053"
	settings.SubscriptionPort = 2096
	if got := subscriptionListenAddress(settings); got != "0.0.0.0:2096" {
		t.Fatalf("dedicated subscription address = %q", got)
	}
	if err := validateSubscriptionServicePort(settings, nil); err != nil {
		t.Fatalf("valid dedicated subscription port rejected: %v", err)
	}
	settings.PanelListen = "[::1]:2053"
	if got := subscriptionListenAddress(settings); got != "[::1]:2096" {
		t.Fatalf("IPv6 dedicated subscription address = %q", got)
	}

	for name, mutate := range map[string]func(*Settings){
		"negative":       func(s *Settings) { s.SubscriptionPort = -1 },
		"above maximum":  func(s *Settings) { s.SubscriptionPort = 65536 },
		"panel conflict": func(s *Settings) { s.SubscriptionPort = 2053 },
		"api conflict": func(s *Settings) {
			s.PanelListen = "0.0.0.0:2053"
			s.APIAddress = "127.0.0.1:9093"
			s.SubscriptionPort = 9093
		},
	} {
		candidate := defaultState().Settings
		mutate(&candidate)
		if err := validateSubscriptionServicePort(candidate, nil); err == nil {
			t.Fatalf("%s subscription port must be rejected", name)
		}
	}

	settings = defaultState().Settings
	settings.PanelListen = "0.0.0.0:2053"
	settings.SubscriptionPort = 2443
	inbounds := []Inbound{{Name: "occupied", Listen: "127.0.0.1", Port: 2443}}
	if err := validateSubscriptionServicePort(settings, inbounds); err == nil {
		t.Fatal("subscription port overlapping an inbound must be rejected")
	}
}

func TestFirstLoadGeneratesAndPersistsSubscriptionPath(t *testing.T) {
	dataDir := t.TempDir()
	manager := &CoreManager{dataDir: dataDir}
	if err := manager.load(); err != nil {
		t.Fatalf("first load: %v", err)
	}
	generated := manager.state.Settings.SubscriptionPath
	if !validGeneratedSubscriptionPath(generated) {
		t.Fatalf("first load subscription path = %q", generated)
	}

	reloaded := &CoreManager{dataDir: dataDir}
	if err := reloaded.load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.state.Settings.SubscriptionPath; got != generated {
		t.Fatalf("reload subscription path = %q, want persisted %q", got, generated)
	}
}

func TestLegacyEmptySubscriptionPathIsMigrated(t *testing.T) {
	dataDir := t.TempDir()
	state := defaultState()
	state.Settings.SubscriptionPath = ""
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal legacy state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "state.json"), data, 0600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	manager := &CoreManager{dataDir: dataDir}
	if err := manager.load(); err != nil {
		t.Fatalf("load legacy state: %v", err)
	}
	if !validGeneratedSubscriptionPath(manager.state.Settings.SubscriptionPath) {
		t.Fatalf("migrated subscription path = %q", manager.state.Settings.SubscriptionPath)
	}

	reloaded := &CoreManager{dataDir: dataDir}
	if err := reloaded.load(); err != nil {
		t.Fatalf("reload migrated state: %v", err)
	}
	if got := reloaded.state.Settings.SubscriptionPath; got != manager.state.Settings.SubscriptionPath {
		t.Fatalf("migrated path was not persisted: got %q, want %q", got, manager.state.Settings.SubscriptionPath)
	}
}

func TestNewSubscriptionPathEndpoint(t *testing.T) {
	app := &App{}
	request := httptest.NewRequest(http.MethodPost, "/api/tools/subscription-path", nil)
	response := httptest.NewRecorder()
	app.handleNewSubscriptionPath(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected response: %d, Cache-Control=%q", response.Code, response.Header().Get("Cache-Control"))
	}
	var body struct {
		OK   bool `json:"ok"`
		Data struct {
			Path string `json:"path"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.OK || !validGeneratedSubscriptionPath(body.Data.Path) {
		t.Fatalf("unexpected generated path response: %s", response.Body.String())
	}
}

func TestSubscriptionAggregatesSameUsernameAcrossInbounds(t *testing.T) {
	manager := &CoreManager{state: State{SubscriptionTokens: map[string]string{}}}
	clients := []Client{{Name: "alice", Username: "alice", Password: "one", Enabled: true}, {Name: "alice-2", Username: "alice", Password: "two", Enabled: true}}
	if err := manager.ensureSubscriptionTokensLocked(clients); err != nil {
		t.Fatal(err)
	}
	if len(manager.state.SubscriptionTokens) != 1 || !validSubscriptionToken(manager.state.SubscriptionTokens["alice"]) {
		t.Fatalf("unexpected token map: %#v", manager.state.SubscriptionTokens)
	}
	snapshot := subscriptionSnapshot{Settings: Settings{ClashPath: "/clash"}, Username: "alice", Token: manager.state.SubscriptionTokens["alice"], Clients: []subscriptionClient{{Inbound: Inbound{Name: "one", Type: "shadowsocks", Port: 1001, Enabled: true, Cipher: "aes-256-gcm"}, Client: clients[0]}, {Inbound: Inbound{Name: "two", Type: "shadowsocks", Port: 1002, Enabled: true, Cipher: "aes-256-gcm"}, Client: clients[1]}}}
	if got := subscriptionLinks(snapshot, "example.com"); len(got) != 2 {
		t.Fatalf("subscription node count = %d, want 2", len(got))
	}
	data, err := buildMihomoSubscription(snapshot, "example.com")
	if err != nil || !strings.Contains(string(data), "one-alice") || !strings.Contains(string(data), "two-alice-2") {
		t.Fatalf("unexpected Mihomo subscription: err=%v\n%s", err, data)
	}
}

func TestSubscriptionClientSuffixFormats(t *testing.T) {
	const token = "abc123def4567890"
	settings := Settings{SubscriptionPath: "/sub", CrossPanelSubscriptionPath: "/isub4ad5kaf479afnbj2", ClashPath: "/clash"}
	// Every conversion switched on so the table below exercises suffix parsing
	// only; the default on/off matrix is covered by the toggles test.
	allOn := make(map[string]bool, len(subscriptionClientKeys))
	for _, key := range subscriptionClientKeys {
		allOn[key] = true
	}
	settings.SubscriptionClients = allOn
	cases := []struct {
		suffix string
		want   subscriptionFormat
	}{
		{"", subscriptionFormatPage},
		{"/clash", subscriptionFormatClash},
		{"/sing-box", subscriptionFormatSingbox},
		{"/v2box", subscriptionFormatBase64},
		{"/v2rayng", subscriptionFormatBase64},
		{"/v2raytun", subscriptionFormatBase64},
		{"/npvtunnel", subscriptionFormatBase64},
		{"/happ", subscriptionFormatBase64},
		{"/shadowrocket", subscriptionFormatBase64},
		{"/streisand", subscriptionFormatBase64},
		{"/surge", subscriptionFormatSurge},
		{"/surgemac", subscriptionFormatSurgeMac},
		{"/surfboard", subscriptionFormatSurfboard},
		{"/loon", subscriptionFormatLoon},
		{"/qx", subscriptionFormatQX},
		{"/stash", subscriptionFormatStash},
		{"/egern", subscriptionFormatEgern},
	}
	for _, tc := range cases {
		if got, format, ok := matchSubscriptionPath(settings, "/sub/"+token+tc.suffix); !ok || got != token || format != tc.want {
			t.Fatalf("local suffix %q => token=%q format=%v ok=%v", tc.suffix, got, format, ok)
		}
		if got, format, ok := matchCrossPanelSubscriptionPath(settings, "/isub4ad5kaf479afnbj2/"+token+tc.suffix); !ok || got != token || format != tc.want {
			t.Fatalf("cross suffix %q => token=%q format=%v ok=%v", tc.suffix, got, format, ok)
		}
	}
	// An unknown client suffix leaves the token invalid, so the request 404s
	// rather than silently falling back to another format.
	if _, _, ok := matchSubscriptionPath(settings, "/sub/"+token+"/unknownclient"); ok {
		t.Fatal("an unknown client suffix must not match a subscription")
	}
}

func TestSingboxSubscriptionProducer(t *testing.T) {
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	clients := []subscriptionClient{
		{Inbound: Inbound{Name: "ss", Type: "shadowsocks", Port: 8388, Enabled: true, Cipher: "aes-256-gcm"}, Client: Client{Name: "u", Password: "sp", Enabled: true}},
		{Inbound: Inbound{Name: "vl", Type: "vless", Port: 443, Enabled: true, Reality: RealitySettings{Enabled: true, PublicKey: "PBK", ServerNames: "reality.example", ShortIDs: "ab", Fingerprint: "chrome"}}, Client: Client{Name: "u", UUID: "uuid-1", Flow: "xtls-rprx-vision", Enabled: true}},
		{Inbound: Inbound{Name: "vm", Type: "vmess", Port: 80, Enabled: true, WSPath: "/ws", Host: "h.com", TLS: true, TLSServerName: "h.com"}, Client: Client{Name: "u", UUID: "uuid-2", Enabled: true}},
		{Inbound: Inbound{Name: "tj", Type: "trojan", Port: 443, Enabled: true, GRPCServiceName: "gsvc", TLS: true, TLSServerName: "t.com"}, Client: Client{Name: "u", Password: "tp", Enabled: true}},
		{Inbound: Inbound{Name: "hy", Type: "hysteria2", Port: 8443, Enabled: true, TLSServerName: "hy.com", Hysteria: HysteriaSettings{Obfs: "salamander", ObfsPassword: "o"}}, Client: Client{Name: "u", Password: "hp", Enabled: true}},
		{Inbound: Inbound{Name: "tu", Type: "tuic", Port: 8444, Enabled: true, TLSServerName: "tu.com", TUIC: TUICSettings{CongestionController: "bbr"}}, Client: Client{Name: "u", UUID: "uuid-3", Password: "tup", Enabled: true}},
		{Inbound: Inbound{Name: "at", Type: "anytls", Port: 8445, Enabled: true, TLSServerName: "at.com"}, Client: Client{Name: "u", Password: "ap", Enabled: true}},
		{Inbound: Inbound{Name: "sk", Type: "socks", Port: 1080, Enabled: true}, Client: Client{Name: "u", Username: "su", Password: "skp", Enabled: true}},
		{Inbound: Inbound{Name: "mi", Type: "mieru", Port: 9000, Enabled: true, Mieru: MieruSettings{Transport: "TCP"}}, Client: Client{Name: "u", Username: "mu", Password: "mp", Enabled: true}},
		{Inbound: Inbound{Name: "xh", Type: "vless", Port: 8080, Enabled: true, XHTTP: XHTTPSettings{Enabled: true}}, Client: Client{Name: "u", UUID: "uuid-x", Enabled: true}},
	}
	data, err := buildSingboxSubscription(subscriptionSnapshot{Username: "u", Token: "abc123def4567890", Clients: clients}, "srv.example")
	if err != nil {
		t.Fatalf("buildSingboxSubscription: %v", err)
	}
	var config struct {
		Outbounds []map[string]any `json:"outbounds"`
		Route     map[string]any   `json:"route"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("sing-box output is not valid JSON: %v\n%s", err, data)
	}
	byTag := map[string]map[string]any{}
	for _, out := range config.Outbounds {
		tag, _ := out["tag"].(string)
		byTag[tag] = out
	}
	// 8 supported protocols + selector + direct; mieru and XHTTP are dropped.
	if len(config.Outbounds) != 10 {
		t.Fatalf("outbound count = %d, want 10: %v", len(config.Outbounds), byTag)
	}
	if _, ok := byTag["mi-u"]; ok {
		t.Fatal("mieru has no sing-box outbound and must be skipped")
	}
	if _, ok := byTag["xh-u"]; ok {
		t.Fatal("XHTTP transport is unsupported by sing-box and must be skipped")
	}
	if ss := byTag["ss-u"]; ss["type"] != "shadowsocks" || ss["method"] != "aes-256-gcm" || ss["password"] != "sp" || ss["server"] != "srv.example" {
		t.Fatalf("unexpected shadowsocks outbound: %#v", ss)
	}
	vl := byTag["vl-u"]
	vlTLS := obj(vl["tls"])
	vlReality := obj(vlTLS["reality"])
	if vl["type"] != "vless" || vl["uuid"] != "uuid-1" || vl["flow"] != "xtls-rprx-vision" || vlTLS["server_name"] != "reality.example" || vlReality["public_key"] != "PBK" {
		t.Fatalf("unexpected vless outbound: %#v", vl)
	}
	vm := byTag["vm-u"]
	vmTransport := obj(vm["transport"])
	if vm["type"] != "vmess" || vm["security"] != "auto" || vmTransport["type"] != "ws" || vmTransport["path"] != "/ws" || obj(vmTransport["headers"])["Host"] != "h.com" || !obj(vm["tls"])["enabled"].(bool) {
		t.Fatalf("unexpected vmess outbound: %#v", vm)
	}
	tj := byTag["tj-u"]
	if tj["type"] != "trojan" || tj["password"] != "tp" || obj(tj["transport"])["service_name"] != "gsvc" || obj(tj["tls"])["server_name"] != "t.com" {
		t.Fatalf("unexpected trojan outbound: %#v", tj)
	}
	hy := byTag["hy-u"]
	if hy["type"] != "hysteria2" || obj(hy["obfs"])["type"] != "salamander" || obj(hy["obfs"])["password"] != "o" || !obj(hy["tls"])["enabled"].(bool) {
		t.Fatalf("unexpected hysteria2 outbound: %#v", hy)
	}
	tu := byTag["tu-u"]
	if tu["type"] != "tuic" || tu["uuid"] != "uuid-3" || tu["password"] != "tup" || tu["congestion_control"] != "bbr" || tu["udp_relay_mode"] != "native" {
		t.Fatalf("unexpected tuic outbound: %#v", tu)
	}
	if at := byTag["at-u"]; at["type"] != "anytls" || at["password"] != "ap" || !obj(at["tls"])["enabled"].(bool) {
		t.Fatalf("unexpected anytls outbound: %#v", byTag["at-u"])
	}
	if sk := byTag["sk-u"]; sk["type"] != "socks" || sk["version"] != "5" || sk["username"] != "su" || sk["password"] != "skp" {
		t.Fatalf("unexpected socks outbound: %#v", byTag["sk-u"])
	}
	proxy := byTag["PROXY"]
	if proxy["type"] != "selector" || config.Route["final"] != "PROXY" {
		t.Fatalf("selector/route wiring is wrong: proxy=%#v route=%#v", proxy, config.Route)
	}
	tags, _ := proxy["outbounds"].([]any)
	if len(tags) == 0 || tags[len(tags)-1] != "direct" {
		t.Fatalf("selector must list every node and end with direct: %#v", tags)
	}
}

func TestClientSubscriptionProducers(t *testing.T) {
	clients := []subscriptionClient{
		{Inbound: Inbound{Name: "ss", Type: "shadowsocks", Port: 8388, Enabled: true, Cipher: "aes-256-gcm", UDP: true, Host: "obfs.com", SimpleObfs: SimpleObfsSettings{Enabled: true, Mode: "http"}}, Client: Client{Name: "u", Password: "sp", Enabled: true}},
		{Inbound: Inbound{Name: "vlr", Type: "vless", Port: 443, Enabled: true, Reality: RealitySettings{Enabled: true, PublicKey: "PBK", ServerNames: "reality.example", ShortIDs: "ab"}}, Client: Client{Name: "u", UUID: "uuid-1", Enabled: true}},
		{Inbound: Inbound{Name: "vlw", Type: "vless", Port: 443, Enabled: true, WSPath: "/ws", TLS: true, TLSServerName: "vl.com"}, Client: Client{Name: "u", UUID: "uuid-2", Enabled: true}},
		{Inbound: Inbound{Name: "vm", Type: "vmess", Port: 80, Enabled: true, WSPath: "/ws", Host: "h.com", TLS: true, TLSServerName: "h.com"}, Client: Client{Name: "u", UUID: "uuid-3", Enabled: true}},
		{Inbound: Inbound{Name: "vg", Type: "vmess", Port: 443, Enabled: true, GRPCServiceName: "gsvc", TLS: true, TLSServerName: "g.com"}, Client: Client{Name: "u", UUID: "uuid-4", Enabled: true}},
		{Inbound: Inbound{Name: "tj", Type: "trojan", Port: 443, Enabled: true, WSPath: "/tws", TLS: true, TLSServerName: "t.com", AllowInsecure: true}, Client: Client{Name: "u", Password: "tp", Enabled: true}},
		{Inbound: Inbound{Name: "tg", Type: "trojan", Port: 443, Enabled: true, GRPCServiceName: "tgsvc", TLS: true, TLSServerName: "tg.com"}, Client: Client{Name: "u", Password: "tgp", Enabled: true}},
		{Inbound: Inbound{Name: "hy", Type: "hysteria2", Port: 8443, Enabled: true, TLSServerName: "hy.com", Hysteria: HysteriaSettings{Obfs: "salamander", ObfsPassword: "o", Up: "100 Mbps", Down: "200 Mbps"}}, Client: Client{Name: "u", Password: "hp", Enabled: true}},
		{Inbound: Inbound{Name: "tu", Type: "tuic", Port: 8444, Enabled: true, TLSServerName: "tu.com"}, Client: Client{Name: "u", UUID: "uuid-5", Password: "tup", Enabled: true}},
		{Inbound: Inbound{Name: "at", Type: "anytls", Port: 8445, Enabled: true, TLSServerName: "at.com"}, Client: Client{Name: "u", Password: "ap", Enabled: true}},
		{Inbound: Inbound{Name: "sk", Type: "socks", Port: 1080, Enabled: true}, Client: Client{Name: "u", Username: "su", Password: "skp", Enabled: true}},
		{Inbound: Inbound{Name: "ht", Type: "http", Port: 8080, Enabled: true}, Client: Client{Name: "u", Username: "hu", Password: "hpw", Enabled: true}},
		{Inbound: Inbound{Name: "mi", Type: "mieru", Port: 9000, Enabled: true, Mieru: MieruSettings{Transport: "TCP"}}, Client: Client{Name: "u", Username: "mu", Password: "mp", Enabled: true}},
	}
	snapshot := subscriptionSnapshot{Username: "u", Token: "abc123def4567890", Clients: clients}
	body := func(format subscriptionFormat) string {
		t.Helper()
		data, contentType, err := buildClientSubscription(snapshot, "srv.example", format)
		if err != nil {
			t.Fatalf("format %v: %v", format, err)
		}
		if format == subscriptionFormatStash || format == subscriptionFormatEgern {
			if !strings.Contains(contentType, "application/yaml") {
				t.Fatalf("format %v content type = %q", format, contentType)
			}
		} else if !strings.Contains(contentType, "text/plain") {
			t.Fatalf("format %v content type = %q", format, contentType)
		}
		return string(data)
	}
	lines := func(text string) []string { return strings.Split(strings.TrimSuffix(text, "\n"), "\n") }
	hasLine := func(text, prefix string) bool {
		for _, line := range lines(text) {
			if strings.HasPrefix(line, prefix) {
				return true
			}
		}
		return false
	}

	// Surge: reality / grpc / vless / mieru nodes vanish, ws+obfs+tuic+hy survive.
	surge := body(subscriptionFormatSurge)
	if !hasLine(surge, "ss-u=ss,srv.example,8388,encrypt-method=aes-256-gcm,password=\"sp\",obfs=http,obfs-host=obfs.com,udp-relay=true") {
		t.Fatalf("unexpected surge ss line:\n%s", surge)
	}
	if !hasLine(surge, "vm-u=vmess,srv.example,80,username=uuid-3,ws=true,ws-path=/ws,ws-headers=\"Host:\"h.com\"\",vmess-aead=true,tls=true,sni=\"h.com\",udp-relay=true") {
		t.Fatalf("unexpected surge vmess ws line:\n%s", surge)
	}
	if !hasLine(surge, "tu-u=tuic-v5,srv.example,8444,uuid=uuid-5,password=\"tup\",sni=\"tu.com\"") {
		t.Fatalf("unexpected surge tuic line:\n%s", surge)
	}
	if !strings.Contains(surge, "hy-u=hysteria2,srv.example,8443,password=\"hp\",salamander-password=\"o\",sni=\"hy.com\",download-bandwidth=200") {
		t.Fatalf("unexpected surge hysteria2 line:\n%s", surge)
	}
	for _, gone := range []string{"vlr-u=", "vlw-u=", "vg-u=", "tg-u=", "mi-u="} {
		if strings.Contains(surge, gone) {
			t.Fatalf("surge must skip %q:\n%s", gone, surge)
		}
	}

	// Surfboard: same skips, trojan password unquoted, ws-headers unquoted whole.
	surfboard := body(subscriptionFormatSurfboard)
	if !strings.Contains(surfboard, "tj-u=trojan,srv.example,443,password=tp,ws=true,ws-path=/tws") {
		t.Fatalf("unexpected surfboard trojan line:\n%s", surfboard)
	}
	if strings.Contains(surfboard, "vlr-u=") || strings.Contains(surfboard, "vg-u=") {
		t.Fatalf("surfboard must skip reality/grpc nodes:\n%s", surfboard)
	}

	// SurgeMac: unsupported nodes become external lines with an embedded config.
	surgeMac := body(subscriptionFormatSurgeMac)
	if !hasLine(surgeMac, "ss-u=ss,") {
		t.Fatalf("surge mac keeps native surge lines:\n%s", surgeMac)
	}
	externalLine := ""
	for _, line := range lines(surgeMac) {
		if strings.HasPrefix(line, "vlr-u=external,") {
			externalLine = line
		}
	}
	if externalLine == "" {
		t.Fatalf("surge mac must emit an external line for vless+reality:\n%s", surgeMac)
	}
	if !strings.Contains(externalLine, `exec="/usr/local/bin/mihomo",local-port=65535,args="-config",args="`) {
		t.Fatalf("unexpected external line shape: %s", externalLine)
	}
	encoded := externalLine[strings.LastIndex(externalLine, `args="`)+len(`args="`):]
	encoded = encoded[:strings.Index(encoded, `"`)]
	embedded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("external args are not base64: %v", err)
	}
	var embeddedConfig struct {
		MixedPort   int              `json:"mixed-port"`
		Mode        string           `json:"mode"`
		Proxies     []map[string]any `json:"proxies"`
		ProxyGroups []map[string]any `json:"proxy-groups"`
	}
	if err := json.Unmarshal(embedded, &embeddedConfig); err != nil {
		t.Fatalf("embedded config is not JSON: %v\n%s", err, embedded)
	}
	if embeddedConfig.MixedPort != 65535 || embeddedConfig.Mode != "global" || len(embeddedConfig.Proxies) != 1 || embeddedConfig.Proxies[0]["name"] != "proxy" || embeddedConfig.Proxies[0]["type"] != "vless" {
		t.Fatalf("unexpected embedded mihomo config: %#v", embeddedConfig)
	}

	// Loon: reality survives, tuic/grpc/mieru do not, ss uses positional cipher.
	loon := body(subscriptionFormatLoon)
	if !hasLine(loon, "ss-u=shadowsocks,srv.example,8388,aes-256-gcm,\"sp\",obfs-name=http,obfs-host=obfs.com,udp=true") {
		t.Fatalf("unexpected loon ss line:\n%s", loon)
	}
	if !strings.Contains(loon, "vlr-u=vless,srv.example,443,\"uuid-1\",transport=tcp,over-tls=true,sni=reality.example,public-key=\"PBK\",short-id=ab,udp=true") {
		t.Fatalf("unexpected loon vless reality line:\n%s", loon)
	}
	for _, gone := range []string{"tu-u=", "tg-u=", "mi-u="} {
		if strings.Contains(loon, gone) {
			t.Fatalf("loon must skip %q:\n%s", gone, loon)
		}
	}

	// QX: tag suffix, inverted tls-verification, reality appended after the tag.
	qx := body(subscriptionFormatQX)
	if !hasLine(qx, "shadowsocks=srv.example:8388,method=aes-256-gcm,password=sp,obfs=http,obfs-host=obfs.com,udp-relay=true,tag=ss-u") {
		t.Fatalf("unexpected qx ss line:\n%s", qx)
	}
	if !strings.Contains(qx, "tj-u") || !strings.Contains(qx, ",obfs=wss,obfs-uri=/tws,tls-verification=false,tls-host=t.com,udp-relay=true,tag=tj-u") {
		t.Fatalf("unexpected qx trojan line:\n%s", qx)
	}
	if !strings.Contains(qx, "vless=srv.example:443,method=none,password=uuid-1,obfs=over-tls,tls-host=reality.example,udp-relay=true,tag=vlr-u,reality-base64-pubkey=PBK,reality-hex-shortid=ab") {
		t.Fatalf("unexpected qx vless reality line:\n%s", qx)
	}
	for _, gone := range []string{"hy-u", "tu-u=", "mi-u="} {
		if strings.Contains(qx, gone) {
			t.Fatalf("qx must skip %q:\n%s", gone, qx)
		}
	}

	// Stash: clash YAML with stash rewrites; mieru kept, vless+reality+ws dropped.
	stash := body(subscriptionFormatStash)
	for _, needle := range []string{"proxies:", "type: \"mieru\"", "auth: \"hp\"", "down-speed: \"200\"", "version: 5", "servername: \"vl.com\""} {
		if !strings.Contains(stash, needle) {
			t.Fatalf("stash output missing %q:\n%s", needle, stash)
		}
	}
	if strings.Contains(stash, "password: \"hp\"") {
		t.Fatalf("stash hysteria2 must rename password to auth:\n%s", stash)
	}

	// Egern: nested per-type maps, snake_case fields, mieru/grpc dropped.
	egern := body(subscriptionFormatEgern)
	for _, needle := range []string{"shadowsocks:", "user_id: \"uuid-1\"", "public_key: \"PBK\"", "udp_relay: true", "alpn:", "- \"h3\"", "obfs_password: \"o\""} {
		if !strings.Contains(egern, needle) {
			t.Fatalf("egern output missing %q:\n%s", needle, egern)
		}
	}
	for _, gone := range []string{"mieru:", "tg-u"} {
		if strings.Contains(egern, gone) {
			t.Fatalf("egern must skip %q:\n%s", gone, egern)
		}
	}
}

func TestSubscriptionClientConversionToggles(t *testing.T) {
	const token = "abc123def4567890"
	base := Settings{SubscriptionPath: "/sub", CrossPanelSubscriptionPath: "/isub4ad5kaf479afnbj2", ClashPath: "/clash"}

	// Shipped defaults: the popular clients are on, the long tail is off.
	for _, key := range []string{"clash", "sing-box", "v2box", "v2rayng", "shadowrocket", "surge", "surgemac", "surfboard", "loon", "qx", "stash"} {
		if !subscriptionClientEnabled(base, key) {
			t.Fatalf("client %q must be enabled by default", key)
		}
	}
	for _, key := range []string{"v2raytun", "npvtunnel", "happ", "streisand", "egern"} {
		if subscriptionClientEnabled(base, key) {
			t.Fatalf("client %q must be disabled by default", key)
		}
	}

	// Disabled conversions behave like an unknown suffix: no match, so 404.
	if _, _, ok := matchSubscriptionPath(base, "/sub/"+token+"/egern"); ok {
		t.Fatal("egern is off by default and must not match")
	}
	enabled := Settings{SubscriptionPath: "/sub", ClashPath: "/clash", SubscriptionClients: map[string]bool{"egern": true}}
	if _, format, ok := matchSubscriptionPath(enabled, "/sub/"+token+"/egern"); !ok || format != subscriptionFormatEgern {
		t.Fatalf("enabled egern must match, ok=%v format=%v", ok, format)
	}
	off := Settings{SubscriptionPath: "/sub", ClashPath: "/clash", SubscriptionClients: map[string]bool{"surge": false, "clash": false}}
	if _, _, ok := matchSubscriptionPath(off, "/sub/"+token+"/surge"); ok {
		t.Fatal("explicitly disabled surge must not match")
	}
	if _, _, ok := matchSubscriptionPath(off, "/sub/"+token+"/clash"); ok {
		t.Fatal("explicitly disabled clash must not match")
	}
	if got, format, ok := matchSubscriptionPath(off, "/sub/"+token); !ok || got != token || format != subscriptionFormatPage {
		t.Fatalf("the bare subscription page carries no toggle: got=%q format=%v ok=%v", got, format, ok)
	}

	// Normalizing expands to every known client and drops unknown keys.
	dirty := Settings{SubscriptionClients: map[string]bool{"egern": true, "junk": true}}
	if !normalizeSubscriptionClientToggles(&dirty) {
		t.Fatal("normalizing a partial map must report a change")
	}
	if len(dirty.SubscriptionClients) != len(subscriptionClientKeys) || !dirty.SubscriptionClients["egern"] || dirty.SubscriptionClients["streisand"] || dirty.SubscriptionClients["junk"] {
		t.Fatalf("unexpected normalized toggles: %#v", dirty.SubscriptionClients)
	}
	if normalizeSubscriptionClientToggles(&dirty) {
		t.Fatal("normalizing an already normalized map must be a no-op")
	}

	// HTTP level: default state 404s egern, an enabled state serves it, and a
	// disabled clash hides the Clash QR card on the public page.
	manager := &CoreManager{state: State{
		Settings:           Settings{PanelPath: "/panel/", SubscriptionPath: "/sub", ClashPath: "/clash"},
		SubscriptionTokens: map[string]string{"alice": token},
		Inbounds:           []Inbound{{Name: "ss", Type: "shadowsocks", Listen: "0.0.0.0", Port: 8388, Enabled: true, Cipher: "aes-256-gcm", UDP: true, Clients: []Client{{Name: "alice", Username: "alice", Password: "secret", Enabled: true}}}},
	}}
	app := &App{manager: manager, session: "admin-session"}
	handler := app.routes()

	egernResponse := httptest.NewRecorder()
	handler.ServeHTTP(egernResponse, httptest.NewRequest(http.MethodGet, "/sub/"+token+"/egern", nil))
	if egernResponse.Code != http.StatusNotFound {
		t.Fatalf("egern must 404 while disabled, got %d", egernResponse.Code)
	}
	manager.state.Settings.SubscriptionClients = map[string]bool{"egern": true}
	egernResponse = httptest.NewRecorder()
	handler.ServeHTTP(egernResponse, httptest.NewRequest(http.MethodGet, "/sub/"+token+"/egern", nil))
	if egernResponse.Code != http.StatusOK || !strings.Contains(egernResponse.Body.String(), "shadowsocks:") {
		t.Fatalf("enabled egern must serve YAML, got %d:\n%s", egernResponse.Code, egernResponse.Body.String())
	}

	pageRequest := httptest.NewRequest(http.MethodGet, "/sub/"+token, nil)
	pageRequest.Header.Set("Accept", "text/html")
	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK || !strings.Contains(pageResponse.Body.String(), `id="clash-qr"`) || !strings.Contains(pageResponse.Body.String(), `&#34;streisand&#34;:false`) || !strings.Contains(pageResponse.Body.String(), `&#34;egern&#34;:true`) {
		t.Fatalf("page must show the clash card and publish toggles while clash is on:\n%s", pageResponse.Body.String())
	}
	manager.state.Settings.SubscriptionClients = map[string]bool{"clash": false, "egern": true}
	pageResponse = httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK || strings.Contains(pageResponse.Body.String(), `id="clash-qr"`) {
		t.Fatalf("disabled clash must hide the Clash QR card:\n%s", pageResponse.Body.String())
	}

	// Settings page and subscription page wiring for the toggle block.
	index, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"Client Subscription Conversion", "buildSubscriptionClientsPayload(form)", "SUBSCRIPTION_CLIENT_DEFAULT_ON", "data-subscription-client="} {
		if !strings.Contains(string(index), needle) {
			t.Fatalf("settings page is missing %q", needle)
		}
	}
	page, err := os.ReadFile("web/subscription.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"data-toggles=", "clientToggles[client.suffix.slice(1)]===false", "{{if .ClashEnabled}}"} {
		if !strings.Contains(string(page), needle) {
			t.Fatalf("subscription page is missing %q", needle)
		}
	}
}

func TestPublicSubscriptionResponses(t *testing.T) {
	const token = "abc123def4567890"
	manager := &CoreManager{state: State{
		Settings:           Settings{PanelPath: "/panel/", SubscriptionPath: "/sub", ClashPath: "/clash"},
		SubscriptionTokens: map[string]string{"alice": token},
		Inbounds:           []Inbound{{Name: "ss", Type: "shadowsocks", Listen: "0.0.0.0", Port: 8388, Enabled: true, Cipher: "aes-256-gcm", UDP: true, Clients: []Client{{Name: "alice", Username: "alice", Password: "secret", Enabled: true}}}},
	}}
	app := &App{manager: manager, session: "admin-session"}
	handler := app.routes()

	htmlRequest := httptest.NewRequest(http.MethodGet, "/sub/"+token, nil)
	htmlRequest.Header.Set("Accept", "text/html")
	htmlResponse := httptest.NewRecorder()
	handler.ServeHTTP(htmlResponse, htmlRequest)
	htmlBody := htmlResponse.Body.String()
	if htmlResponse.Code != http.StatusOK || !strings.Contains(htmlBody, "Subscription info") || !strings.Contains(htmlBody, "/sub/"+token+"/clash") {
		t.Fatalf("unexpected subscription page %d:\n%s", htmlResponse.Code, htmlResponse.Body.String())
	}
	if strings.Contains(htmlBody, "/panel/") || !strings.Contains(htmlBody, "/sub/_assets/qrious2.min.js") {
		t.Fatalf("public subscription page leaked the panel path or omitted its public asset URL:\n%s", htmlBody)
	}

	assetRequest := httptest.NewRequest(http.MethodGet, "/sub/_assets/qrious2.min.js", nil)
	assetResponse := httptest.NewRecorder()
	handler.ServeHTTP(assetResponse, assetRequest)
	if assetResponse.Code != http.StatusOK || !strings.Contains(assetResponse.Header().Get("Content-Type"), "application/javascript") || !strings.Contains(assetResponse.Body.String(), "QRious") {
		t.Fatalf("unexpected subscription asset response %d (%s)", assetResponse.Code, assetResponse.Header().Get("Content-Type"))
	}

	rawRequest := httptest.NewRequest(http.MethodGet, "/sub/"+token, nil)
	rawResponse := httptest.NewRecorder()
	handler.ServeHTTP(rawResponse, rawRequest)
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rawResponse.Body.String()))
	if err != nil || rawResponse.Code != http.StatusOK || !strings.Contains(string(decoded), "ss://") {
		t.Fatalf("unexpected raw subscription %d: err=%v body=%q", rawResponse.Code, err, rawResponse.Body.String())
	}

	clashRequest := httptest.NewRequest(http.MethodGet, "/sub/"+token+"/clash", nil)
	clashResponse := httptest.NewRecorder()
	handler.ServeHTTP(clashResponse, clashRequest)
	if clashResponse.Code != http.StatusOK || !strings.Contains(clashResponse.Header().Get("Content-Type"), "application/yaml") || !strings.Contains(clashResponse.Body.String(), "proxy-groups:") {
		t.Fatalf("unexpected Mihomo subscription %d:\n%s", clashResponse.Code, clashResponse.Body.String())
	}

	singboxRequest := httptest.NewRequest(http.MethodGet, "/sub/"+token+"/sing-box", nil)
	singboxResponse := httptest.NewRecorder()
	handler.ServeHTTP(singboxResponse, singboxRequest)
	if singboxResponse.Code != http.StatusOK || !strings.Contains(singboxResponse.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("unexpected sing-box subscription %d (%s)", singboxResponse.Code, singboxResponse.Header().Get("Content-Type"))
	}
	var singboxConfig struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(singboxResponse.Body.Bytes(), &singboxConfig); err != nil {
		t.Fatalf("sing-box body is not valid JSON: %v\n%s", err, singboxResponse.Body.String())
	}
	if len(singboxConfig.Outbounds) != 3 || singboxConfig.Outbounds[0]["type"] != "shadowsocks" {
		t.Fatalf("unexpected sing-box outbounds: %#v", singboxConfig.Outbounds)
	}

	clientRequest := httptest.NewRequest(http.MethodGet, "/sub/"+token+"/v2box", nil)
	clientRequest.Header.Set("Accept", "text/html")
	clientResponse := httptest.NewRecorder()
	handler.ServeHTTP(clientResponse, clientRequest)
	clientDecoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(clientResponse.Body.String()))
	if err != nil || clientResponse.Code != http.StatusOK || !strings.Contains(string(clientDecoded), "ss://") {
		t.Fatalf("client suffix must return the base64 list even to a browser %d: err=%v body=%q", clientResponse.Code, err, clientResponse.Body.String())
	}

	surgeRequest := httptest.NewRequest(http.MethodGet, "/sub/"+token+"/surge", nil)
	surgeResponse := httptest.NewRecorder()
	handler.ServeHTTP(surgeResponse, surgeRequest)
	if surgeResponse.Code != http.StatusOK || !strings.Contains(surgeResponse.Header().Get("Content-Type"), "text/plain") || !strings.Contains(surgeResponse.Body.String(), "=ss,") {
		t.Fatalf("unexpected surge subscription %d (%s):\n%s", surgeResponse.Code, surgeResponse.Header().Get("Content-Type"), surgeResponse.Body.String())
	}

	stashRequest := httptest.NewRequest(http.MethodGet, "/sub/"+token+"/stash", nil)
	stashResponse := httptest.NewRecorder()
	handler.ServeHTTP(stashResponse, stashRequest)
	if stashResponse.Code != http.StatusOK || !strings.Contains(stashResponse.Header().Get("Content-Type"), "application/yaml") || !strings.Contains(stashResponse.Body.String(), "proxies:") {
		t.Fatalf("unexpected stash subscription %d (%s):\n%s", stashResponse.Code, stashResponse.Header().Get("Content-Type"), stashResponse.Body.String())
	}

	panelOnly := app.panelRoutes(false)
	panelSubscriptionResponse := httptest.NewRecorder()
	panelOnly.ServeHTTP(panelSubscriptionResponse, httptest.NewRequest(http.MethodGet, "/sub/"+token, nil))
	if panelSubscriptionResponse.Code != http.StatusNotFound {
		t.Fatalf("dedicated mode must remove subscriptions from the panel port, got %d", panelSubscriptionResponse.Code)
	}

	subscriptionOnly := app.subscriptionRoutes()
	panelResponse := httptest.NewRecorder()
	subscriptionOnly.ServeHTTP(panelResponse, httptest.NewRequest(http.MethodGet, "/panel/", nil))
	if panelResponse.Code != http.StatusNotFound {
		t.Fatalf("subscription service must not expose the panel, got %d", panelResponse.Code)
	}
	dedicatedResponse := httptest.NewRecorder()
	subscriptionOnly.ServeHTTP(dedicatedResponse, httptest.NewRequest(http.MethodGet, "/sub/"+token, nil))
	if dedicatedResponse.Code != http.StatusOK {
		t.Fatalf("subscription service did not serve a subscription, got %d", dedicatedResponse.Code)
	}
}

func TestSubscriptionPageSettingsMenuWiring(t *testing.T) {
	page, err := os.ReadFile("web/subscription.html")
	if err != nil {
		t.Fatal(err)
	}
	content := string(page)
	for _, needle := range []string{
		`id="settings-button"`, `id="settings-menu"`, `id="dark-mode"`, `id="ultra-dark"`, `id="language-select"`,
		`data-theme="dark"`, `data-ultra-dark="true"`, `html[data-theme="dark"]`, `html[data-theme="dark"][data-ultra-dark="true"]`,
		"function toggleSettings()", "function closeSettings(", "function applyTheme(", "function applyUltraDark(", "function applyLanguage(",
		"mui-theme", "mui-is-ultra-dark", "mui-language", "Panel Settings", "Ultra Dark", "面板设置", "超暗色",
		`id="android-btn"`, `id="ios-btn"`, `id="mac-btn"`, `class="client-menu"`, "function closeClientMenus(", "'/sing-box'", "'/v2box'", "'/shadowrocket'", "'/streisand'",
		"'/surge'", "'/surgemac'", "'/surfboard'", "'/loon'", "'/qx'", "'/stash'", "'/egern'",
	} {
		if !strings.Contains(content, needle) {
			t.Fatalf("subscription settings menu is missing %q", needle)
		}
	}
}

func TestSaveInboundNormalizesShadowsocksToSingleClient(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir(), state: State{Settings: Settings{Username: "admin", Password: "hash"}}}
	err := m.saveInbound(Inbound{
		ID:       "ss",
		Name:     "single-ss",
		Type:     "shadowsocks",
		Listen:   "0.0.0.0",
		Port:     19081,
		Enabled:  true,
		Cipher:   "2022-blake3-aes-256-gcm",
		Password: "legacy-inbound-password",
		Clients: []Client{
			{Name: "one", Password: "client-one", Enabled: true},
			{Name: "two", Password: "client-two", Enabled: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.state.Inbounds) != 1 {
		t.Fatalf("saved inbounds = %d, want 1", len(m.state.Inbounds))
	}
	saved := m.state.Inbounds[0]
	if saved.Password != "" || saved.Username != "" || saved.UUID != "" {
		t.Fatalf("legacy inbound credentials were retained: %#v", saved)
	}
	if len(saved.Clients) != 1 || saved.Clients[0].Password != "client-one" {
		t.Fatalf("saved SS clients = %#v, want one client with client-one", saved.Clients)
	}
	config, err := inboundConfig(saved)
	if err != nil {
		t.Fatal(err)
	}
	if got := config["password"]; got != "client-one" {
		t.Fatalf("rendered SS password = %v, want client-one", got)
	}
}

func TestSimpleAuthCredentialsRenderToUsers(t *testing.T) {
	inbound := Inbound{Type: "mixed", SimpleAuthEnabled: true, Clients: []Client{{Name: "alice", Username: "alice", Password: "secret", Enabled: true}}}
	config, err := inboundConfig(inbound)
	if err != nil {
		t.Fatal(err)
	}
	users, ok := config["users"].([]map[string]any)
	if !ok || len(users) != 1 || users[0]["username"] != "alice" || users[0]["password"] != "secret" {
		t.Fatalf("unexpected mixed users: %#v", config["users"])
	}
}

func TestAnyTLSAcceptsSupportedSecurityModes(t *testing.T) {
	client := Client{Name: "alice", Username: "alice", Password: "secret", Enabled: true}
	for name, inbound := range map[string]Inbound{
		"certificate": {Type: "anytls", Certificate: "cert.pem", PrivateKey: "key.pem", Clients: []Client{client}},
		"shadow tls":  {Type: "anytls", ShadowTLS: ShadowTLSSettings{Enabled: true, Version: 3, Password: "secret", Dest: "example.com:443"}, Clients: []Client{client}},
		"restls":      {Type: "anytls", RestTLS: RestTLSSettings{Enabled: true, Dest: "example.com:443", Password: "secret"}, Clients: []Client{client}},
		"insecure":    {Type: "anytls", AllowInsecure: true, Clients: []Client{client}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateInbound(inbound); err != nil {
				t.Fatalf("supported security mode rejected: %v", err)
			}
		})
	}
}

func TestTrojanRejectsMissingSecurity(t *testing.T) {
	err := validateInbound(Inbound{Type: "trojan", Clients: []Client{{Name: "alice", Password: "secret", Enabled: true}}})
	if err == nil {
		t.Fatal("expected unsecured Trojan listener to be rejected")
	}
}

func TestTrojanMihomoSecurityOptionsRender(t *testing.T) {
	client := Client{Name: "alice", Password: "trojan-password", Enabled: true}
	for name, inbound := range map[string]Inbound{
		"jls":       {Type: "trojan", JLS: JLSSettings{Enabled: true, Username: "jls", Password: "jls-password", Dest: "example.com:443"}, Clients: []Client{client}},
		"trojan ss": {Type: "trojan", TrojanSS: TrojanSSSettings{Enabled: true, Method: "aes-128-gcm", Password: "ss-password"}, Clients: []Client{client}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateInbound(inbound); err != nil {
				t.Fatalf("supported Trojan security rejected: %v", err)
			}
			config, err := inboundConfig(inbound)
			if err != nil {
				t.Fatal(err)
			}
			data, err := marshalYAML(config)
			if err != nil {
				t.Fatal(err)
			}
			yaml := string(data)
			if name == "jls" && !strings.Contains(yaml, "jls-config:") {
				t.Fatalf("JLS config missing:\n%s", yaml)
			}
			if name == "trojan ss" && !strings.Contains(yaml, "ss-option:") {
				t.Fatalf("Trojan SS config missing:\n%s", yaml)
			}
		})
	}
}

func TestRealityFallbackLimitsRender(t *testing.T) {
	inbound := Inbound{
		Name: "reality-limits", Type: "trojan", Listen: "0.0.0.0", Port: 443, Enabled: true,
		Clients: []Client{{Name: "alice", Password: "secret", Enabled: true}},
		Reality: RealitySettings{Enabled: true, Dest: "example.com:443", PrivateKey: "private", ServerNames: "example.com", LimitFallbackUploadAfterBytes: 100, LimitFallbackUploadBytesPerSec: 200, LimitFallbackUploadBurstBytesPerSec: 300},
	}
	config, err := inboundConfig(inbound)
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalYAML(config)
	if err != nil {
		t.Fatal(err)
	}
	yaml := string(data)
	for _, expected := range []string{"limit-fallback-upload:", "after-bytes: 100", "bytes-per-sec: 200", "burst-bytes-per-sec: 300"} {
		if !strings.Contains(yaml, expected) {
			t.Fatalf("missing %q:\n%s", expected, yaml)
		}
	}
}

func TestTLSInboundRequiresCertificateAndPrivateKey(t *testing.T) {
	err := validateInbound(Inbound{Type: "vless", TLS: true})
	if err == nil {
		t.Fatal("expected TLS listener without certificate to be rejected")
	}
	if err := validateInbound(Inbound{Type: "vless", TLS: true, Certificate: "cert.pem", PrivateKey: "key.pem"}); err != nil {
		t.Fatalf("valid TLS listener rejected: %v", err)
	}
}

func TestTLSCertificateContentIsPreservedInMihomoYAML(t *testing.T) {
	inbound := Inbound{
		Name: "tls-content", Type: "vless", Listen: "0.0.0.0", Port: 2443, Enabled: true, TLS: true,
		CertificateMode: "content", Certificate: "CERTIFICATE PEM", PrivateKey: "PRIVATE KEY PEM",
		Clients: []Client{{Name: "alice", UUID: "00000000-0000-0000-0000-000000000001", Enabled: true}},
	}
	config, err := inboundConfig(inbound)
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalYAML(config)
	if err != nil {
		t.Fatal(err)
	}
	yaml := string(data)
	if !strings.Contains(yaml, "certificate: \"CERTIFICATE PEM\"") || !strings.Contains(yaml, "private-key: \"PRIVATE KEY PEM\"") {
		t.Fatalf("certificate content missing from YAML:\n%s", yaml)
	}
}

func TestDisabledTLSDoesNotRenderStaleCertificateSettings(t *testing.T) {
	inbound := Inbound{
		Name: "plain", Type: "vless", Listen: "0.0.0.0", Port: 2443, Enabled: true,
		TLS: false, Certificate: "cert.pem", PrivateKey: "key.pem", ClientAuthType: "request", ECHKey: "ech-key",
		Clients: []Client{{Name: "alice", UUID: "00000000-0000-0000-0000-000000000001", Enabled: true}},
	}
	config, err := inboundConfig(inbound)
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalYAML(config)
	if err != nil {
		t.Fatal(err)
	}
	yaml := string(data)
	for _, field := range []string{"certificate:", "private-key:", "client-auth-type:", "ech-key:"} {
		if strings.Contains(yaml, field) {
			t.Fatalf("stale TLS field %q rendered for non-TLS inbound:\n%s", field, yaml)
		}
	}
}

func TestPanelTLSStatusRequiresBothValidFiles(t *testing.T) {
	configured, valid := panelTLSStatus(Settings{})
	if configured || valid {
		t.Fatal("empty panel TLS settings should be unconfigured")
	}
	configured, valid = panelTLSStatus(Settings{PanelCertFile: "cert.pem", PanelKeyFile: "key.pem"})
	if !configured || valid {
		t.Fatal("missing panel TLS files should be configured but invalid")
	}
}

func TestX25519KeyPairDerivesMatchingPublicKey(t *testing.T) {
	pair, err := generateX25519KeyPair()
	if err != nil {
		t.Fatal(err)
	}
	derived, err := deriveX25519PublicKey(pair.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if pair.PublicKey != derived {
		t.Fatalf("derived public key mismatch: got %q want %q", derived, pair.PublicKey)
	}
	privateBytes, err := base64.RawURLEncoding.DecodeString(pair.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(privateBytes) != 32 {
		t.Fatalf("private key size = %d, want 32", len(privateBytes))
	}
	if _, err := ecdh.X25519().NewPrivateKey(privateBytes); err != nil {
		t.Fatalf("generated private key is not X25519 compatible: %v", err)
	}
}

func TestVLESSEncryptionGenerationUsesMihomoSyntax(t *testing.T) {
	for _, auth := range []string{"x25519", "mlkem768"} {
		t.Run(auth, func(t *testing.T) {
			pair, err := generateVLESSEncryption(auth)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(pair.Decryption, "mlkem768x25519plus.native.600s.") {
				t.Fatalf("unexpected decryption: %q", pair.Decryption)
			}
			if !strings.HasPrefix(pair.Encryption, "mlkem768x25519plus.native.0rtt.") {
				t.Fatalf("unexpected encryption: %q", pair.Encryption)
			}
			serverKey := strings.TrimPrefix(pair.Decryption, "mlkem768x25519plus.native.600s.")
			clientKey := strings.TrimPrefix(pair.Encryption, "mlkem768x25519plus.native.0rtt.")
			serverBytes, err := base64.RawURLEncoding.DecodeString(serverKey)
			if err != nil {
				t.Fatal(err)
			}
			clientBytes, err := base64.RawURLEncoding.DecodeString(clientKey)
			if err != nil {
				t.Fatal(err)
			}
			if auth == "x25519" && (len(serverBytes) != 32 || len(clientBytes) != 32) {
				t.Fatalf("X25519 key sizes = %d/%d, want 32/32", len(serverBytes), len(clientBytes))
			}
			if auth == "mlkem768" && (len(serverBytes) != 64 || len(clientBytes) != 1184) {
				t.Fatalf("ML-KEM key sizes = %d/%d, want 64/1184", len(serverBytes), len(clientBytes))
			}
		})
	}
}

func TestNormalizeInboundSecretsRestoresClientMetadata(t *testing.T) {
	enc, err := generateVLESSEncryption("mlkem768")
	if err != nil {
		t.Fatal(err)
	}
	reality, err := generateX25519KeyPair()
	if err != nil {
		t.Fatal(err)
	}
	inbound := Inbound{
		Type:       "vless",
		Decryption: enc.Decryption,
		Reality:    RealitySettings{Enabled: true, PrivateKey: reality.PrivateKey},
	}
	if err := normalizeInboundSecrets(&inbound); err != nil {
		t.Fatal(err)
	}
	if inbound.VLESSAuth != "mlkem768" {
		t.Fatalf("authentication = %q, want mlkem768", inbound.VLESSAuth)
	}
	if inbound.Encryption != enc.Encryption {
		t.Fatalf("restored encryption mismatch")
	}
	if inbound.Reality.PublicKey != reality.PublicKey {
		t.Fatalf("restored Reality public key mismatch")
	}
}

func TestRenderConfigPreservesVLESSClientMetadataAsComments(t *testing.T) {
	pair, err := generateX25519KeyPair()
	if err != nil {
		t.Fatal(err)
	}
	m := &CoreManager{dataDir: t.TempDir(), state: State{
		Settings: Settings{APIAddress: defaultCoreAPI, APISecret: "secret", AllowLAN: true, Mode: "rule", LogLevel: "info"},
		Inbounds: []Inbound{{
			ID: "one", Name: "vless-reality", Type: "vless", Listen: "127.0.0.1", Port: 2443, Enabled: true,
			Decryption: "mlkem768x25519plus.native.600s.example", Encryption: "mlkem768x25519plus.native.0rtt.example",
			Clients: []Client{{ID: "client", Name: "alice", UUID: "00000000-0000-0000-0000-000000000001", Enabled: true}},
			Reality: RealitySettings{Enabled: true, Dest: "example.com:443", PrivateKey: pair.PrivateKey, PublicKey: pair.PublicKey, ShortIDs: "1234", ServerNames: "example.com"},
		}},
	}}
	data, err := m.renderConfig()
	if err != nil {
		t.Fatal(err)
	}
	config := string(data)
	if !strings.Contains(config, `# reality-public-key "vless-reality": "`+pair.PublicKey+`"`) {
		t.Fatalf("public key metadata missing:\n%s", config)
	}
	if !strings.Contains(config, `# vless-client-encryption "vless-reality": "mlkem768x25519plus.native.0rtt.example"`) {
		t.Fatalf("client encryption metadata missing:\n%s", config)
	}
}

func TestYAMLUsesReadableListenerAndRealityOrder(t *testing.T) {
	data, err := marshalYAML(map[string]any{
		"listeners": []any{map[string]any{
			"reality-config": map[string]any{"proxy": "", "short-id": []string{"a1b2"}, "private-key": "private", "server-names": []string{"example.com"}, "dest": "example.com:443"},
			"decryption":     "none", "port": 443, "listen": "0.0.0.0", "type": "vless", "name": "vision",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	config := string(data)
	listener := strings.Index(config, "  - name: \"vision\"")
	if listener < 0 {
		t.Fatalf("listener missing:\n%s", config)
	}
	for _, pair := range []struct{ first, second string }{
		{"name: \"vision\"", "type: \"vless\""},
		{"type: \"vless\"", "listen: \"0.0.0.0\""},
		{"listen: \"0.0.0.0\"", "port: 443"},
		{"dest: \"example.com:443\"", "server-names:"},
		{"server-names:", "private-key: \"private\""},
		{"private-key: \"private\"", "short-id:"},
	} {
		if strings.Index(config, pair.first) >= strings.Index(config, pair.second) {
			t.Fatalf("expected %q before %q:\n%s", pair.first, pair.second, config)
		}
	}
}

func TestXHTTPOptionsRenderToMihomoListenerConfig(t *testing.T) {
	inbound := Inbound{
		Name: "vless-xhttp", Type: "vless", Listen: "0.0.0.0", Port: 443, Enabled: true,
		Clients: []Client{{Name: "alice", UUID: "00000000-0000-0000-0000-000000000001", Enabled: true}},
		XHTTP: XHTTPSettings{
			Enabled: true, Path: "/xhttp", Host: "example.com", Mode: "stream-up", XPaddingBytes: "100-1000", XPaddingObfsMode: true,
			XPaddingKey: "x_padding", XPaddingHeader: "Referer", XPaddingPlacement: "queryInHeader", XPaddingMethod: "repeat-x",
			UplinkHTTPMethod: "POST", SessionPlacement: "query", SessionKey: "sid", SeqPlacement: "header", SeqKey: "seq",
			UplinkDataPlacement: "body", UplinkDataKey: "data", UplinkChunkSize: "1024", NoSSEHeader: true,
			ScStreamUpServerSecs: "20-80", ScMaxBufferedPosts: "30", ScMaxEachPostBytes: "1000000",
		},
	}
	config, err := inboundConfig(inbound)
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalYAML(config)
	if err != nil {
		t.Fatal(err)
	}
	yaml := string(data)
	for _, expected := range []string{"xhttp-config:", "x-padding-bytes: \"100-1000\"", "x-padding-obfs-mode: true", "uplink-http-method: \"POST\"", "session-placement: \"query\"", "seq-placement: \"header\"", "no-sse-header: true", "sc-stream-up-server-secs: \"20-80\""} {
		if !strings.Contains(yaml, expected) {
			t.Fatalf("missing %q in YAML:\n%s", expected, yaml)
		}
	}
}

func TestXHTTPRejectsUnsupportedOptions(t *testing.T) {
	base := Inbound{Type: "vless", XHTTP: XHTTPSettings{Enabled: true}}
	for field, inbound := range map[string]Inbound{
		"mode":      {Type: base.Type, XHTTP: XHTTPSettings{Enabled: true, Mode: "websocket"}},
		"placement": {Type: base.Type, XHTTP: XHTTPSettings{Enabled: true, XPaddingPlacement: "path"}},
		"method":    {Type: base.Type, XHTTP: XHTTPSettings{Enabled: true, XPaddingMethod: "random"}},
	} {
		t.Run(field, func(t *testing.T) {
			if err := validateInbound(inbound); err == nil {
				t.Fatal("expected unsupported XHTTP option to be rejected")
			}
		})
	}
}

func TestSnellListenerRendersSinglePSK(t *testing.T) {
	inbound := Inbound{
		Name: "snell-a", Type: "snell", Listen: "0.0.0.0", Port: 18443, Enabled: true, UDP: true,
		Password: "legacy-inbound-password",
		Snell:    SnellSettings{Version: 4, ObfsMode: "HTTP", ObfsHost: " bing.com "},
		Clients:  []Client{{Name: "one", Password: "psk-secret", Enabled: true}},
	}
	config, err := inboundConfig(inbound)
	if err != nil {
		t.Fatal(err)
	}
	if got := config["psk"]; got != "psk-secret" {
		t.Fatalf("snell psk = %v, want psk-secret", got)
	}
	for _, unsupported := range []string{"password", "users", "cipher"} {
		if _, ok := config[unsupported]; ok {
			t.Fatalf("snell listener must not carry %q: %#v", unsupported, config)
		}
	}
	if got := config["version"]; got != 4 {
		t.Fatalf("snell version = %v, want 4", got)
	}
	if got := config["udp"]; got != true {
		t.Fatalf("snell udp = %v, want true", got)
	}
	obfs, ok := config["obfs-opts"].(map[string]any)
	if !ok {
		t.Fatalf("snell obfs-opts missing: %#v", config)
	}
	if obfs["mode"] != "http" || obfs["host"] != "bing.com" {
		t.Fatalf("snell obfs-opts = %#v, want lowercased mode and trimmed host", obfs)
	}
}

func TestSnellAlwaysStatesListenerVersion(t *testing.T) {
	// Mihomo's snell listener defaults to v4 while its client defaults to v1, so
	// m-ui always writes the version even when the operator left it unset.
	if got := snellVersion(Inbound{Type: "snell"}); got != 4 {
		t.Fatalf("snellVersion default = %d, want 4", got)
	}
	if got := snellVersion(Inbound{Type: "snell", Snell: SnellSettings{Version: 2}}); got != 2 {
		t.Fatalf("snellVersion explicit = %d, want 2", got)
	}
	config, err := inboundConfig(Inbound{Name: "snell-default", Type: "snell", Listen: "0.0.0.0", Port: 18444, Clients: []Client{{Name: "one", Password: "psk-secret", Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := config["version"]; got != 4 {
		t.Fatalf("rendered snell version = %v, want 4", got)
	}
	for _, omitted := range []string{"udp", "obfs-opts"} {
		if _, ok := config[omitted]; ok {
			t.Fatalf("snell listener without that feature must omit %q: %#v", omitted, config)
		}
	}
}

func TestSnellAcceptsSupportedSecurityModes(t *testing.T) {
	client := Client{Name: "one", Password: "psk-secret", Enabled: true}
	for name, inbound := range map[string]Inbound{
		"plain":     {Type: "snell", Clients: []Client{client}},
		"obfs http": {Type: "snell", Snell: SnellSettings{ObfsMode: "http"}, Clients: []Client{client}},
		"obfs tls":  {Type: "snell", Snell: SnellSettings{ObfsMode: "tls"}, Clients: []Client{client}},
		"udp v3":    {Type: "snell", UDP: true, Snell: SnellSettings{Version: 3}, Clients: []Client{client}},
		"shadowtls": {Type: "snell", ShadowTLS: ShadowTLSSettings{Enabled: true, Version: 3, Password: "st"}, Clients: []Client{client}},
		"restls":    {Type: "snell", RestTLS: RestTLSSettings{Enabled: true, Dest: "example.com:443"}, Clients: []Client{client}},
		"jls":       {Type: "snell", JLS: JLSSettings{Enabled: true, Username: "u", Password: "p", Dest: "example.com:443"}, Clients: []Client{client}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateInbound(inbound); err != nil {
				t.Fatalf("snell inbound should be accepted: %v", err)
			}
		})
	}
}

func TestSnellRejectsUnsupportedCombinations(t *testing.T) {
	client := Client{Name: "one", Password: "psk-secret", Enabled: true}
	for name, expect := range map[string]struct {
		inbound Inbound
		message string
	}{
		"no client":         {Inbound{Type: "snell"}, "仅支持一个客户端"},
		"two clients":       {Inbound{Type: "snell", Clients: []Client{client, {Name: "two", Password: "other", Enabled: true}}}, "仅支持一个客户端"},
		"disabled client":   {Inbound{Type: "snell", Clients: []Client{{Name: "one", Password: "psk-secret"}}}, "启用且填写了 PSK"},
		"empty psk":         {Inbound{Type: "snell", Clients: []Client{{Name: "one", Enabled: true}}}, "启用且填写了 PSK"},
		"version too high":  {Inbound{Type: "snell", Snell: SnellSettings{Version: 6}, Clients: []Client{client}}, "版本必须在 1-5 之间"},
		"version negative":  {Inbound{Type: "snell", Snell: SnellSettings{Version: -1}, Clients: []Client{client}}, "版本必须在 1-5 之间"},
		"unknown obfs":      {Inbound{Type: "snell", Snell: SnellSettings{ObfsMode: "salamander"}, Clients: []Client{client}}, "Obfs Mode 不受 Mihomo 支持"},
		"udp below v3":      {Inbound{Type: "snell", UDP: true, Snell: SnellSettings{Version: 2}, Clients: []Client{client}}, "不支持 UDP"},
		"two wrappers":      {Inbound{Type: "snell", ShadowTLS: ShadowTLSSettings{Enabled: true}, RestTLS: RestTLSSettings{Enabled: true}, Clients: []Client{client}}, "只能启用一种"},
		"obfs with wrapper": {Inbound{Type: "snell", Snell: SnellSettings{ObfsMode: "tls"}, ShadowTLS: ShadowTLSSettings{Enabled: true}, Clients: []Client{client}}, "Obfs 与 ShadowTLS"},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateInbound(expect.inbound)
			if err == nil {
				t.Fatal("expected snell inbound to be rejected")
			}
			if !strings.Contains(err.Error(), expect.message) {
				t.Fatalf("error = %q, want it to mention %q", err.Error(), expect.message)
			}
		})
	}
}

func TestSaveInboundNormalizesSnellToSinglePSKClient(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir(), state: State{Settings: Settings{Username: "admin", Password: "hash"}}}
	err := m.saveInbound(Inbound{
		ID: "snell", Name: "single-snell", Type: "snell", Listen: "0.0.0.0", Port: 18445, Enabled: true,
		Password: "legacy-inbound-psk",
		Snell:    SnellSettings{Version: 4},
		Clients: []Client{
			{Name: "one", Password: "psk-one", Enabled: true},
			{Name: "two", Password: "psk-two", Enabled: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.state.Inbounds) != 1 {
		t.Fatalf("saved inbounds = %d, want 1", len(m.state.Inbounds))
	}
	saved := m.state.Inbounds[0]
	if saved.Password != "" || saved.Username != "" || saved.UUID != "" {
		t.Fatalf("legacy listener credentials were retained: %#v", saved)
	}
	if len(saved.Clients) != 1 || saved.Clients[0].Password != "psk-one" {
		t.Fatalf("saved snell clients = %#v, want one client holding psk-one", saved.Clients)
	}
	config, err := inboundConfig(saved)
	if err != nil {
		t.Fatal(err)
	}
	if got := config["psk"]; got != "psk-one" {
		t.Fatalf("rendered snell psk = %v, want psk-one", got)
	}
}

func TestSaveInboundAdoptsLegacySnellListenerPSK(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir(), state: State{Settings: Settings{Username: "admin", Password: "hash"}}}
	if err := m.saveInbound(Inbound{
		ID: "snell", Name: "legacy-snell", Type: "snell", Listen: "0.0.0.0", Port: 18446, Enabled: true,
		Password: "legacy-inbound-psk", Clients: []Client{},
	}); err != nil {
		t.Fatal(err)
	}
	saved := m.state.Inbounds[0]
	if len(saved.Clients) != 1 || saved.Clients[0].Password != "legacy-inbound-psk" || !saved.Clients[0].Enabled {
		t.Fatalf("legacy snell PSK was not moved into the sole client: %#v", saved.Clients)
	}
}

func TestYAMLOrdersSnellListenerKeys(t *testing.T) {
	config, err := inboundConfig(Inbound{
		Name: "snell-a", Type: "snell", Listen: "0.0.0.0", Port: 18443, Enabled: true, UDP: true,
		Snell:   SnellSettings{Version: 4, ObfsMode: "http", ObfsHost: "bing.com"},
		Clients: []Client{{Name: "one", Password: "psk-secret", Enabled: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalYAML(map[string]any{"listeners": []any{config}})
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(data)
	if !strings.Contains(rendered, "  - name: \"snell-a\"") {
		t.Fatalf("snell listener missing:\n%s", rendered)
	}
	for _, pair := range []struct{ first, second string }{
		{"name: \"snell-a\"", "type: \"snell\""},
		{"type: \"snell\"", "listen: \"0.0.0.0\""},
		{"listen: \"0.0.0.0\"", "port: 18443"},
		{"port: 18443", "psk: \"psk-secret\""},
		{"psk: \"psk-secret\"", "version: 4"},
		{"version: 4", "udp: true"},
		{"udp: true", "obfs-opts:"},
	} {
		if strings.Index(rendered, pair.first) >= strings.Index(rendered, pair.second) {
			t.Fatalf("expected %q before %q:\n%s", pair.first, pair.second, rendered)
		}
	}
}

func hysteria2Inbound(settings HysteriaSettings) Inbound {
	return Inbound{
		Name: "hy2-a", Type: "hysteria2", Listen: "0.0.0.0", Port: 18470, Enabled: true,
		Certificate: "/etc/ssl/hy2.crt", PrivateKey: "/etc/ssl/hy2.key",
		Clients:  []Client{{Name: "one", Username: "one", Password: "hy2-secret", Enabled: true}},
		Hysteria: settings,
	}
}

func TestHysteria2ListenerRendersEveryMihomoOption(t *testing.T) {
	config, err := inboundConfig(hysteria2Inbound(HysteriaSettings{
		Obfs: " Gecko ", ObfsPassword: "obfs-secret", ObfsMinPacketSize: 512, ObfsMaxPacketSize: 1200,
		Up: "200 Mbps", Down: "500 Mbps", IgnoreClientBandwidth: true,
		Masquerade: " https://example.com ", ALPN: "h3, hysteria", UdpMTU: 1197,
		CWND: 32, BBRProfile: " Aggressive ",
		InitialStreamReceiveWindow: 8388608, MaxStreamReceiveWindow: 16777216,
		InitialConnectionReceiveWindow: 20971520, MaxConnectionReceiveWindow: 41943040,
	}))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"obfs": "gecko", "obfs-password": "obfs-secret", "obfs-min-packet-size": 512, "obfs-max-packet-size": 1200,
		"up": "200 Mbps", "down": "500 Mbps", "ignore-client-bandwidth": true,
		"masquerade": "https://example.com", "udp-mtu": 1197, "cwnd": 32, "bbr-profile": "aggressive",
		"initial-stream-receive-window": uint64(8388608), "max-stream-receive-window": uint64(16777216),
		"initial-connection-receive-window": uint64(20971520), "max-connection-receive-window": uint64(41943040),
		"certificate": "/etc/ssl/hy2.crt", "private-key": "/etc/ssl/hy2.key",
	} {
		if got := config[key]; got != want {
			t.Fatalf("hysteria2 %s = %#v, want %#v", key, got, want)
		}
	}
	if alpn, ok := config["alpn"].([]string); !ok || len(alpn) != 2 || alpn[0] != "h3" || alpn[1] != "hysteria" {
		t.Fatalf("hysteria2 alpn = %#v, want [h3 hysteria]", config["alpn"])
	}
	if users, ok := config["users"].(map[string]any); !ok || users["one"] != "hy2-secret" {
		t.Fatalf("hysteria2 users = %#v, want one -> hy2-secret", config["users"])
	}
	// Mihomo hardcodes TLS 1.3, BBR congestion and the global UDP timeout, and its
	// hysteria2 listener never reads max-idle-time, so none of those may be emitted.
	for _, unsupported := range []string{"max-idle-time", "congestion", "udp", "obfs-opts", "password"} {
		if _, ok := config[unsupported]; ok {
			t.Fatalf("hysteria2 listener must not carry %q: %#v", unsupported, config)
		}
	}
}

func TestHysteria2OmitsUnsetOptions(t *testing.T) {
	config, err := inboundConfig(hysteria2Inbound(HysteriaSettings{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, omitted := range []string{
		"obfs", "obfs-password", "obfs-min-packet-size", "obfs-max-packet-size", "up", "down",
		"ignore-client-bandwidth", "masquerade", "alpn", "udp-mtu", "cwnd", "bbr-profile",
		"initial-stream-receive-window", "max-stream-receive-window",
		"initial-connection-receive-window", "max-connection-receive-window",
	} {
		if _, ok := config[omitted]; ok {
			t.Fatalf("unset hysteria2 option %q must fall through to the Mihomo default: %#v", omitted, config)
		}
	}
}

func TestHysteria2AcceptsSupportedConfigurations(t *testing.T) {
	for name, settings := range map[string]HysteriaSettings{
		"bare":              {},
		"salamander":        {Obfs: "salamander", ObfsPassword: "obfs-secret"},
		"gecko":             {Obfs: "gecko", ObfsPassword: "obfs-secret"},
		"gecko sizes":       {Obfs: "GECKO", ObfsPassword: "obfs-secret", ObfsMinPacketSize: 512, ObfsMaxPacketSize: 1200},
		"gecko equal sizes": {Obfs: "gecko", ObfsPassword: "obfs-secret", ObfsMinPacketSize: 700, ObfsMaxPacketSize: 700},
		"gecko min only":    {Obfs: "gecko", ObfsPassword: "obfs-secret", ObfsMinPacketSize: 512},
		"masquerade file":   {Masquerade: "file:///var/www"},
		"masquerade http":   {Masquerade: "http://example.com"},
		"masquerade https":  {Masquerade: " https://example.com/path "},
		"bandwidth":         {Up: "100 Mbps", Down: "100 Mbps"},
		"ignore bandwidth":  {IgnoreClientBandwidth: true},
		"bbr conservative":  {BBRProfile: "conservative"},
		"bbr standard":      {BBRProfile: "Standard"},
		"bbr aggressive":    {BBRProfile: "aggressive"},
		"cwnd":              {CWND: 32},
		"windows":           {InitialStreamReceiveWindow: 8388608, MaxStreamReceiveWindow: 8388608, InitialConnectionReceiveWindow: 20971520, MaxConnectionReceiveWindow: 20971520},
		"max window only":   {MaxStreamReceiveWindow: 8388608, MaxConnectionReceiveWindow: 20971520},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateInbound(hysteria2Inbound(settings)); err != nil {
				t.Fatalf("hysteria2 inbound should be accepted: %v", err)
			}
		})
	}
}

func TestHysteria2RejectsUnsupportedCombinations(t *testing.T) {
	for name, expect := range map[string]struct {
		settings HysteriaSettings
		message  string
	}{
		"unknown obfs":         {HysteriaSettings{Obfs: "chacha20", ObfsPassword: "p"}, "Obfs 不受 Mihomo 支持"},
		"obfs without secret":  {HysteriaSettings{Obfs: "salamander"}, "必须配置混淆密码"},
		"gecko without pass":   {HysteriaSettings{Obfs: "gecko", ObfsMinPacketSize: 512}, "必须配置混淆密码"},
		"sizes without gecko":  {HysteriaSettings{Obfs: "salamander", ObfsPassword: "p", ObfsMaxPacketSize: 1200}, "仅 gecko 混淆支持"},
		"sizes without obfs":   {HysteriaSettings{ObfsMinPacketSize: 512}, "仅 gecko 混淆支持"},
		"negative min size":    {HysteriaSettings{Obfs: "gecko", ObfsPassword: "p", ObfsMinPacketSize: -1}, "不能为负数"},
		"min above max":        {HysteriaSettings{Obfs: "gecko", ObfsPassword: "p", ObfsMinPacketSize: 1300, ObfsMaxPacketSize: 1200}, "最小报文长度不能大于最大报文长度"},
		"masquerade scheme":    {HysteriaSettings{Masquerade: "string://example.com"}, "仅支持 file://"},
		"masquerade bare":      {HysteriaSettings{Masquerade: "example.com"}, "仅支持 file://"},
		"masquerade file dir":  {HysteriaSettings{Masquerade: "file://"}, "必须带绝对目录"},
		"masquerade file host": {HysteriaSettings{Masquerade: "file://var/www"}, "需要三个斜杠的绝对目录"},
		"masquerade no host":   {HysteriaSettings{Masquerade: "https://"}, "必须带主机名"},
		"bbr profile":          {HysteriaSettings{BBRProfile: "bbr"}, "BBR Profile 无效"},
		"negative cwnd":        {HysteriaSettings{CWND: -1}, "Init CWND 不能为负数"},
		"negative mtu":         {HysteriaSettings{UdpMTU: -1}, "UDP MTU 不能为负数"},
		"stream window order":  {HysteriaSettings{InitialStreamReceiveWindow: 16777216, MaxStreamReceiveWindow: 8388608}, "Init Stream Window 不能大于"},
		"conn window order":    {HysteriaSettings{InitialConnectionReceiveWindow: 41943040, MaxConnectionReceiveWindow: 20971520}, "Init Conn Window 不能大于"},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateInbound(hysteria2Inbound(expect.settings))
			if err == nil {
				t.Fatal("expected hysteria2 inbound to be rejected")
			}
			if !strings.Contains(err.Error(), expect.message) {
				t.Fatalf("error = %q, want it to mention %q", err.Error(), expect.message)
			}
		})
	}
}

func TestHysteria2RequiresCertificate(t *testing.T) {
	inbound := hysteria2Inbound(HysteriaSettings{})
	inbound.PrivateKey = ""
	err := validateInbound(inbound)
	if err == nil || !strings.Contains(err.Error(), "必须配置证书和私钥") {
		t.Fatalf("error = %v, want the certificate requirement", err)
	}
}
func TestYAMLOrdersHysteria2ListenerKeys(t *testing.T) {
	config, err := inboundConfig(hysteria2Inbound(HysteriaSettings{
		Obfs: "gecko", ObfsPassword: "obfs-secret", ObfsMinPacketSize: 512, ObfsMaxPacketSize: 1200,
		Up: "200 Mbps", Down: "500 Mbps", IgnoreClientBandwidth: true,
		Masquerade: "https://example.com", ALPN: "h3", UdpMTU: 1197,
		CWND: 32, BBRProfile: "aggressive",
		InitialStreamReceiveWindow: 8388608, MaxStreamReceiveWindow: 16777216,
		InitialConnectionReceiveWindow: 20971520, MaxConnectionReceiveWindow: 41943040,
	}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalYAML(map[string]any{"listeners": []any{config}})
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(data)
	if !strings.Contains(rendered, "  - name: \"hy2-a\"") {
		t.Fatalf("hysteria2 listener missing:\n%s", rendered)
	}
	for _, pair := range []struct{ first, second string }{
		{"name: \"hy2-a\"", "type: \"hysteria2\""},
		{"type: \"hysteria2\"", "listen: \"0.0.0.0\""},
		{"listen: \"0.0.0.0\"", "port: 18470"},
		{"port: 18470", "users:"},
		{"users:", "certificate: \"/etc/ssl/hy2.crt\""},
		{"certificate: \"/etc/ssl/hy2.crt\"", "private-key: \"/etc/ssl/hy2.key\""},
		{"private-key: \"/etc/ssl/hy2.key\"", "up: \"200 Mbps\""},
		{"up: \"200 Mbps\"", "down: \"500 Mbps\""},
		{"down: \"500 Mbps\"", "ignore-client-bandwidth: true"},
		{"ignore-client-bandwidth: true", "obfs: \"gecko\""},
		{"obfs: \"gecko\"", "obfs-password: \"obfs-secret\""},
		{"obfs-password: \"obfs-secret\"", "obfs-min-packet-size: 512"},
		{"obfs-min-packet-size: 512", "obfs-max-packet-size: 1200"},
		{"obfs-max-packet-size: 1200", "masquerade: \"https://example.com\""},
		{"masquerade: \"https://example.com\"", "alpn:"},
		{"alpn:", "udp-mtu: 1197"},
		{"udp-mtu: 1197", "cwnd: 32"},
		{"cwnd: 32", "bbr-profile: \"aggressive\""},
		{"bbr-profile: \"aggressive\"", "initial-stream-receive-window: 8388608"},
		{"initial-stream-receive-window: 8388608", "max-stream-receive-window: 16777216"},
		{"max-stream-receive-window: 16777216", "initial-connection-receive-window: 20971520"},
		{"initial-connection-receive-window: 20971520", "max-connection-receive-window: 41943040"},
	} {
		if strings.Index(rendered, pair.first) >= strings.Index(rendered, pair.second) {
			t.Fatalf("expected %q before %q:\n%s", pair.first, pair.second, rendered)
		}
	}
}

func TestAllowInsecureOnlyRendersForListenersThatDeclareIt(t *testing.T) {
	client := Client{Name: "alice", Username: "alice", Password: "secret", Enabled: true}
	hysteria := hysteria2Inbound(HysteriaSettings{})
	hysteria.AllowInsecure = true
	for _, testCase := range []struct {
		inbound Inbound
		want    bool
	}{
		{Inbound{Type: "anytls", AllowInsecure: true, Clients: []Client{client}}, true},
		{Inbound{Type: "trojan", AllowInsecure: true, Clients: []Client{client}}, true},
		{Inbound{Type: "vless", AllowInsecure: true, Clients: []Client{client}}, true},
		{hysteria, false},
		{Inbound{Type: "tuic", AllowInsecure: true, Certificate: "c.crt", PrivateKey: "c.key", Clients: []Client{client}}, false},
		{Inbound{Type: "anytls", Certificate: "c.crt", PrivateKey: "c.key", Clients: []Client{client}}, false},
	} {
		config, err := inboundConfig(testCase.inbound)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := config["allow-insecure"]; ok != testCase.want {
			t.Fatalf("%s allow-insecure present = %v, want %v", testCase.inbound.Type, ok, testCase.want)
		}
	}
}

func TestTLSServerNameStaysOutOfListenerConfig(t *testing.T) {
	inbound := hysteria2Inbound(HysteriaSettings{})
	inbound.TLSServerName = "example.com"
	config, err := inboundConfig(inbound)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"sni", "server-name", "servername", "tls-server-name"} {
		if _, ok := config[key]; ok {
			t.Fatalf("hysteria2 listener must not carry %s: %#v", key, config)
		}
	}
}

func TestTLSServerNameRejectsNonHostnames(t *testing.T) {
	for _, name := range []string{"https://example.com", "example.com:443", "user@example.com", "a b", "example.com/path", "example.com?x=1", "example.com#tag", "a&b"} {
		inbound := hysteria2Inbound(HysteriaSettings{})
		inbound.TLSServerName = name
		if err := validateInbound(inbound); err == nil {
			t.Fatalf("expected %q to be rejected as SNI", name)
		}
	}
	inbound := hysteria2Inbound(HysteriaSettings{})
	inbound.TLSServerName = " example.com "
	if err := validateInbound(inbound); err != nil {
		t.Fatalf("plain hostname rejected: %v", err)
	}
}

func tuicInbound(settings TUICSettings) Inbound {
	return Inbound{
		Name: "tuic-a", Type: "tuic", Listen: "0.0.0.0", Port: 18471, Enabled: true,
		Certificate: "/etc/ssl/tuic.crt", PrivateKey: "/etc/ssl/tuic.key",
		Clients: []Client{{Name: "one", Username: "one", UUID: "8f1d3a52-6c4e-4f0b-9e2a-1b7c5d9e0f34", Password: "tuic-secret", Enabled: true}},
		TUIC:    settings,
	}
}

func TestTUICListenerRendersEveryMihomoOption(t *testing.T) {
	config, err := inboundConfig(tuicInbound(TUICSettings{
		CongestionController: " BBR ", MaxIdleTime: 20000, AuthenticationTimeout: 2000,
		ALPN: "h3, tuic", MaxUDPRelayPacketSize: 1300, CWND: 48, BBRProfile: " Aggressive ",
	}))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"congestion-controller": "bbr", "max-idle-time": 20000, "authentication-timeout": 2000,
		"max-udp-relay-packet-size": 1300, "cwnd": 48, "bbr-profile": "aggressive",
		"certificate": "/etc/ssl/tuic.crt", "private-key": "/etc/ssl/tuic.key",
	} {
		if got := config[key]; got != want {
			t.Fatalf("tuic %s = %#v, want %#v", key, got, want)
		}
	}
	if alpn, ok := config["alpn"].([]string); !ok || len(alpn) != 2 || alpn[0] != "h3" || alpn[1] != "tuic" {
		t.Fatalf("tuic alpn = %#v, want [h3 tuic]", config["alpn"])
	}
	// Mihomo keys the tuic user map by UUID and authenticates with the password.
	if users, ok := config["users"].(map[string]any); !ok || users["8f1d3a52-6c4e-4f0b-9e2a-1b7c5d9e0f34"] != "tuic-secret" {
		t.Fatalf("tuic users = %#v, want the UUID mapped to tuic-secret", config["users"])
	}
	// token would downgrade the listener to TUIC v4, max-datagram-frame-size is
	// absent from TuicOption, and the QUIC receive windows are hardcoded, so none
	// of these may leak into the listener config.
	for _, unsupported := range []string{"token", "max-datagram-frame-size", "initial-stream-receive-window", "max-connection-receive-window", "allow-insecure", "up", "down", "password", "udp"} {
		if _, ok := config[unsupported]; ok {
			t.Fatalf("tuic listener must not carry %q: %#v", unsupported, config)
		}
	}
}

func TestTUICOmitsUnsetOptions(t *testing.T) {
	config, err := inboundConfig(tuicInbound(TUICSettings{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, omitted := range []string{"congestion-controller", "max-idle-time", "authentication-timeout", "alpn", "max-udp-relay-packet-size", "cwnd", "bbr-profile"} {
		if _, ok := config[omitted]; ok {
			t.Fatalf("unset tuic option %q must fall through to the Mihomo default: %#v", omitted, config)
		}
	}
}

func TestTUICAcceptsSupportedConfigurations(t *testing.T) {
	for name, settings := range map[string]TUICSettings{
		"bare":              {},
		"defaults":          {MaxIdleTime: 15000, AuthenticationTimeout: 1000, ALPN: "h3"},
		"cubic":             {CongestionController: "cubic"},
		"new reno":          {CongestionController: "new_reno"},
		"bbr":               {CongestionController: "bbr"},
		"bbr meta v1":       {CongestionController: "bbr_meta_v1"},
		"bbr meta v2":       {CongestionController: "bbr_meta_v2"},
		"bbr profile":       {CongestionController: "BBR", BBRProfile: "conservative"},
		"bbr v2 profile":    {CongestionController: "bbr_meta_v2", BBRProfile: "aggressive"},
		"bbr cwnd":          {CongestionController: "bbr", CWND: 64},
		"bbr meta v1 cwnd":  {CongestionController: "bbr_meta_v1", CWND: 16},
		"relay packet size": {MaxUDPRelayPacketSize: 1200},
	} {
		if err := validateInbound(tuicInbound(settings)); err != nil {
			t.Fatalf("%s rejected: %v", name, err)
		}
	}
}

func TestTUICRejectsUnsupportedCombinations(t *testing.T) {
	for name, testCase := range map[string]struct {
		settings TUICSettings
		want     string
	}{
		"unknown controller":  {TUICSettings{CongestionController: "reno"}, "Congestion Controller"},
		"hysteria controller": {TUICSettings{CongestionController: "brutal"}, "Congestion Controller"},
		"unknown profile":     {TUICSettings{CongestionController: "bbr", BBRProfile: "turbo"}, "BBR Profile 无效"},
		"profile without bbr": {TUICSettings{CongestionController: "cubic", BBRProfile: "standard"}, "BBR Profile 仅"},
		"profile on bbr v1":   {TUICSettings{CongestionController: "bbr_meta_v1", BBRProfile: "standard"}, "BBR Profile 仅"},
		"profile without cc":  {TUICSettings{BBRProfile: "standard"}, "BBR Profile 仅"},
		"cwnd on cubic":       {TUICSettings{CongestionController: "cubic", CWND: 32}, "Init CWND 仅"},
		"negative cwnd":       {TUICSettings{CongestionController: "bbr", CWND: -1}, "Init CWND 不能为负数"},
		"negative idle":       {TUICSettings{MaxIdleTime: -1}, "Max Idle Time"},
		"negative auth":       {TUICSettings{AuthenticationTimeout: -1}, "Authentication Timeout"},
		"negative relay":      {TUICSettings{MaxUDPRelayPacketSize: -1}, "Max UDP Relay Packet Size"},
	} {
		err := validateInbound(tuicInbound(testCase.settings))
		if err == nil || !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("%s error = %v, want %q", name, err, testCase.want)
		}
	}
}

func TestTUICRequiresCertificateAndClientUUID(t *testing.T) {
	inbound := tuicInbound(TUICSettings{})
	inbound.Certificate = ""
	if err := validateInbound(inbound); err == nil || !strings.Contains(err.Error(), "必须配置证书和私钥") {
		t.Fatalf("error = %v, want the certificate requirement", err)
	}
	inbound = tuicInbound(TUICSettings{})
	inbound.Clients[0].UUID = ""
	if err := validateInbound(inbound); err == nil {
		t.Fatal("tuic clients without a UUID must be rejected")
	}
}

func TestYAMLOrdersTUICListenerKeys(t *testing.T) {
	config, err := inboundConfig(tuicInbound(TUICSettings{
		CongestionController: "bbr", MaxIdleTime: 20000, AuthenticationTimeout: 2000,
		ALPN: "h3", MaxUDPRelayPacketSize: 1300, CWND: 48, BBRProfile: "aggressive",
	}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalYAML(map[string]any{"listeners": []any{config}})
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(data)
	if !strings.Contains(rendered, "  - name: \"tuic-a\"") {
		t.Fatalf("tuic listener missing:\n%s", rendered)
	}
	for _, pair := range []struct{ first, second string }{
		{"name: \"tuic-a\"", "type: \"tuic\""},
		{"type: \"tuic\"", "listen: \"0.0.0.0\""},
		{"listen: \"0.0.0.0\"", "port: 18471"},
		{"port: 18471", "users:"},
		{"users:", "certificate: \"/etc/ssl/tuic.crt\""},
		{"certificate: \"/etc/ssl/tuic.crt\"", "private-key: \"/etc/ssl/tuic.key\""},
		{"private-key: \"/etc/ssl/tuic.key\"", "alpn:"},
		{"alpn:", "congestion-controller: \"bbr\""},
		{"congestion-controller: \"bbr\"", "cwnd: 48"},
		{"cwnd: 48", "bbr-profile: \"aggressive\""},
		{"bbr-profile: \"aggressive\"", "max-idle-time: 20000"},
		{"max-idle-time: 20000", "authentication-timeout: 2000"},
		{"authentication-timeout: 2000", "max-udp-relay-packet-size: 1300"},
	} {
		if strings.Index(rendered, pair.first) >= strings.Index(rendered, pair.second) {
			t.Fatalf("expected %q before %q:\n%s", pair.first, pair.second, rendered)
		}
	}
}

func anytlsInbound(settings AnyTLSSettings) Inbound {
	return Inbound{
		Name: "anytls-a", Type: "anytls", Listen: "0.0.0.0", Port: 18472, Enabled: true, TLS: true,
		Certificate: "/etc/ssl/anytls.crt", PrivateKey: "/etc/ssl/anytls.key",
		Clients: []Client{{Name: "one", Username: "one", Password: "anytls-secret", Enabled: true}},
		AnyTLS:  settings,
	}
}

func TestAnyTLSRendersPaddingScheme(t *testing.T) {
	config, err := inboundConfig(anytlsInbound(AnyTLSSettings{PaddingScheme: "\r\nstop=2\r\n0=30-30\r\n1=100-400\r\n"}))
	if err != nil {
		t.Fatal(err)
	}
	// Mihomo splits the scheme on "\n" and keeps every other byte, so a CRLF paste
	// has to be normalised before it reaches the listener config.
	if got := config["padding-scheme"]; got != "stop=2\n0=30-30\n1=100-400" {
		t.Fatalf("padding-scheme = %#v", got)
	}
	if users, ok := config["users"].(map[string]any); !ok || users["one"] != "anytls-secret" {
		t.Fatalf("anytls users = %#v, want one -> anytls-secret", config["users"])
	}
	data, err := marshalYAML(map[string]any{"listeners": []any{config}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `padding-scheme: "stop=2\n0=30-30\n1=100-400"`) {
		t.Fatalf("multi-line padding scheme must survive as an escaped scalar:\n%s", data)
	}
	empty, err := inboundConfig(anytlsInbound(AnyTLSSettings{PaddingScheme: " \n \n"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := empty["padding-scheme"]; ok {
		t.Fatalf("blank padding scheme must fall through to the Mihomo default: %#v", empty)
	}
}

func TestAnyTLSPaddingSchemeMatchesMihomoParser(t *testing.T) {
	for name, scheme := range map[string]string{
		"empty":        "",
		"default":      anytlsDefaultPaddingScheme,
		"stop only":    "stop=8",
		"crlf":         "stop=8\r\n0=30-30",
		"blank lines":  "\nstop=8\n\n0=30-30\n",
		"extra keys":   "stop=8\n0=30-30\nnonsense",
		"zero stop":    "stop=0",
		"unused range": "stop=1\n9=500-1000",
	} {
		if err := validateInbound(anytlsInbound(AnyTLSSettings{PaddingScheme: scheme})); err != nil {
			t.Fatalf("%s rejected: %v", name, err)
		}
	}
	for name, testCase := range map[string]struct {
		scheme string
		want   string
	}{
		"no pairs":         {"nonsense", "key=value"},
		"missing stop":     {"0=30-30\n1=100-400", "必须包含 stop=N"},
		"spaced key":       {"stop = 8\n0=30-30", "必须包含 stop=N"},
		"uppercase stop":   {"STOP=8\n0=30-30", "必须包含 stop=N"},
		"non integer":      {"stop=eight", "stop 必须是整数"},
		"spaced value":     {"stop= 8", "stop 必须是整数"},
		"decimal stop":     {"stop=8.5", "stop 必须是整数"},
		"trailing comma":   {"stop=8,", "stop 必须是整数"},
		"empty stop value": {"stop=", "stop 必须是整数"},
	} {
		err := validateInbound(anytlsInbound(AnyTLSSettings{PaddingScheme: testCase.scheme}))
		if err == nil || !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("%s error = %v, want %q", name, err, testCase.want)
		}
	}
}

func TestPanelOffersMihomoDefaultPaddingScheme(t *testing.T) {
	page, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	escaped := strings.ReplaceAll(anytlsDefaultPaddingScheme, "\n", `\n`)
	if !strings.Contains(string(page), escaped) {
		t.Fatalf("web/index.html must offer Mihomo's own default padding scheme:\n%s", escaped)
	}
}

func TestMuxOptionOnlyRendersForListenersThatDeclareIt(t *testing.T) {
	client := Client{Name: "alice", Username: "alice", Password: "secret", UUID: "8f1d3a52-6c4e-4f0b-9e2a-1b7c5d9e0f34", Enabled: true}
	mux := MuxSettings{Padding: true, BrutalEnabled: true, Up: "100 Mbps", Down: "100 Mbps"}
	for _, testCase := range []struct {
		typeName string
		want     bool
	}{
		{"hysteria2", true}, {"shadowsocks", true}, {"trojan", true},
		{"tuic", true}, {"vless", true}, {"vmess", true}, {"sudoku", true}, {"shadowquic", true},
		{"anytls", false}, {"snell", false}, {"mixed", false}, {"socks", false}, {"http", false}, {"mieru", false},
		{"trusttunnel", false}, {"hysteria2-realm", false},
	} {
		inbound := Inbound{
			Name: "mux-" + testCase.typeName, Type: testCase.typeName, Listen: "0.0.0.0", Port: 18473,
			Certificate: "/etc/ssl/c.crt", PrivateKey: "/etc/ssl/c.key",
			Clients: []Client{client}, Mux: mux,
		}
		config, err := inboundConfig(inbound)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := config["mux-option"]; ok != testCase.want {
			t.Fatalf("%s mux-option present = %v, want %v", testCase.typeName, ok, testCase.want)
		}
	}
}

func TestJLSFollowsTheListenersThatDeclareIt(t *testing.T) {
	jls := JLSSettings{Enabled: true, Dest: "example.com:443", Username: "jls-user", Password: "jls-pass"}
	for _, testCase := range []struct {
		typeName string
		allowed  bool
	}{
		{"anytls", true}, {"shadowsocks", true}, {"snell", true}, {"trojan", true}, {"vless", true}, {"vmess", true},
		{"hysteria2", false}, {"tuic", false}, {"mixed", false}, {"socks", false}, {"http", false},
	} {
		inbound := Inbound{
			Name: "jls-" + testCase.typeName, Type: testCase.typeName, Listen: "0.0.0.0", Port: 18474,
			Certificate: "/etc/ssl/c.crt", PrivateKey: "/etc/ssl/c.key",
			Clients: []Client{{Name: "alice", Username: "alice", Password: "secret", UUID: "8f1d3a52-6c4e-4f0b-9e2a-1b7c5d9e0f34", Enabled: true}},
			JLS:     jls,
		}
		if testCase.typeName == "shadowsocks" {
			inbound.Cipher = "aes-256-gcm"
		}
		err := validateInbound(inbound)
		if testCase.allowed && err != nil {
			t.Fatalf("%s must accept JLS: %v", testCase.typeName, err)
		}
		if !testCase.allowed && (err == nil || !strings.Contains(err.Error(), "JLS 不支持")) {
			t.Fatalf("%s JLS error = %v, want the unsupported message", testCase.typeName, err)
		}
		if !testCase.allowed {
			continue
		}
		config, err := inboundConfig(inbound)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := config["jls-config"]; !ok {
			t.Fatalf("%s must render jls-config: %#v", testCase.typeName, config)
		}
	}
}

func mieruInbound(settings MieruSettings) Inbound {
	return Inbound{
		Name: "mieru-a", Type: "mieru", Listen: "0.0.0.0", Port: 18475, Enabled: true, UDP: true,
		Clients: []Client{{Name: "one", Username: "one", Password: "mieru-secret", Enabled: true}},
		Mieru:   settings,
	}
}

func TestMieruListenerRendersEveryMihomoOption(t *testing.T) {
	config, err := inboundConfig(mieruInbound(MieruSettings{
		Transport: " udp ", TrafficPattern: " YWJjZA== ", UserHintIsMandatory: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		// Mihomo compares the transport verbatim against "TCP"/"UDP".
		"transport": "UDP", "traffic-pattern": "YWJjZA==", "user-hint-is-mandatory": true,
	} {
		if got := config[key]; got != want {
			t.Fatalf("mieru %s = %#v, want %#v", key, got, want)
		}
	}
	if users, ok := config["users"].(map[string]any); !ok || users["one"] != "mieru-secret" {
		t.Fatalf("mieru users = %#v, want one mapped to mieru-secret", config["users"])
	}
	// MieruOption declares no TLS, no mux and no udp switch, and Mihomo silently
	// drops keys it does not know, so none of the shared plumbing may leak in.
	for _, unsupported := range []string{"certificate", "private-key", "allow-insecure", "mux-option", "udp", "password", "uuid", "reality-config", "jls-config"} {
		if _, ok := config[unsupported]; ok {
			t.Fatalf("mieru listener must not carry %q: %#v", unsupported, config)
		}
	}
}
func TestMieruOmitsUnsetOptions(t *testing.T) {
	config, err := inboundConfig(mieruInbound(MieruSettings{Transport: "TCP"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, omitted := range []string{"traffic-pattern", "user-hint-is-mandatory"} {
		if _, ok := config[omitted]; ok {
			t.Fatalf("unset mieru option %q must fall through to the Mihomo default: %#v", omitted, config)
		}
	}
}

func TestMieruAcceptsSupportedConfigurations(t *testing.T) {
	for name, settings := range map[string]MieruSettings{
		"tcp":            {Transport: "TCP"},
		"udp":            {Transport: "UDP"},
		"lower case":     {Transport: "tcp"},
		"padded":         {Transport: " UDP "},
		"user hint":      {Transport: "TCP", UserHintIsMandatory: true},
		"traffic":        {Transport: "TCP", TrafficPattern: "YWJjZA=="},
		"padded traffic": {Transport: "TCP", TrafficPattern: " YWJjZA== "},
	} {
		if err := validateInbound(mieruInbound(settings)); err != nil {
			t.Fatalf("%s rejected: %v", name, err)
		}
	}
}

func TestMieruRejectsUnsupportedCombinations(t *testing.T) {
	for name, testCase := range map[string]struct {
		mutate func(*Inbound)
		want   string
	}{
		"no transport":    {func(i *Inbound) { i.Mieru.Transport = "" }, "必须选择 Transport"},
		"unknown":         {func(i *Inbound) { i.Mieru.Transport = "quic" }, "只支持 TCP 或 UDP"},
		"bad traffic":     {func(i *Inbound) { i.Mieru.TrafficPattern = "not base64!" }, "Traffic Pattern"},
		"no client":       {func(i *Inbound) { i.Clients = nil }, "至少需要一个启用的客户端"},
		"disabled client": {func(i *Inbound) { i.Clients[0].Enabled = false }, "至少需要一个启用的客户端"},
		"nameless client": {func(i *Inbound) { i.Clients[0].Username, i.Clients[0].Name = "", "" }, "必须填写用户名"},
		"tls":             {func(i *Inbound) { i.TLS = true }, "不使用 TLS"},
		"certificate":     {func(i *Inbound) { i.Certificate = "/etc/ssl/c.crt" }, "不使用 TLS"},
		"allow insecure":  {func(i *Inbound) { i.AllowInsecure = true }, "没有 allow-insecure"},
		"reality":         {func(i *Inbound) { i.Reality.Enabled = true }, "不支持 Reality"},
		"jls":             {func(i *Inbound) { i.JLS.Enabled = true }, "不支持 Reality"},
		"shadow tls":      {func(i *Inbound) { i.ShadowTLS.Enabled = true }, "不支持 Reality"},
	} {
		inbound := mieruInbound(MieruSettings{Transport: "TCP"})
		testCase.mutate(&inbound)
		err := validateInbound(inbound)
		if err == nil || !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("%s error = %v, want %q", name, err, testCase.want)
		}
	}
}
func TestYAMLOrdersMieruListenerKeys(t *testing.T) {
	config, err := inboundConfig(mieruInbound(MieruSettings{
		Transport: "UDP", TrafficPattern: "YWJjZA==", UserHintIsMandatory: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalYAML(map[string]any{"listeners": []any{config}})
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(data)
	for _, pair := range []struct{ first, second string }{
		{"name: \"mieru-a\"", "type: \"mieru\""},
		{"type: \"mieru\"", "listen: \"0.0.0.0\""},
		{"listen: \"0.0.0.0\"", "port: 18475"},
		{"port: 18475", "transport: \"UDP\""},
		{"transport: \"UDP\"", "users:"},
		{"users:", "traffic-pattern: \"YWJjZA==\""},
		{"traffic-pattern: \"YWJjZA==\"", "user-hint-is-mandatory: true"},
	} {
		if strings.Index(rendered, pair.first) >= strings.Index(rendered, pair.second) {
			t.Fatalf("expected %q before %q:\n%s", pair.first, pair.second, rendered)
		}
	}
}

func sudokuInbound(settings SudokuSettings) Inbound {
	return Inbound{
		Name: "sudoku-a", Type: "sudoku", Listen: "0.0.0.0", Port: 18476, Enabled: true, UDP: true,
		Clients: []Client{{Name: "one", Username: "one", Password: "sudoku-key", Enabled: true}},
		Sudoku:  settings,
	}
}
func TestSudokuListenerRendersEveryMihomoOption(t *testing.T) {
	config, err := inboundConfig(sudokuInbound(SudokuSettings{
		AEADMethod: " AES-128-GCM ", TableType: " Prefer_ASCII ", PaddingEnabled: true, PaddingMin: 5, PaddingMax: 40,
		HandshakeTimeout: 12, EnablePureDownlink: true, CustomTables: []string{"XP VVXPVV", "vvxxppvv"},
		HTTPMaskMode: " Stream ", PathRoot: "/mask/", Fallback: "127.0.0.1:8080",
	}))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"key": "sudoku-key", "aead-method": "aes-128-gcm", "table-type": "prefer_ascii",
		"padding-min": 5, "padding-max": 40, "handshake-timeout": 12,
		"enable-pure-downlink": true, "fallback": "127.0.0.1:8080",
	} {
		if got := config[key]; got != want {
			t.Fatalf("sudoku %s = %#v, want %#v", key, got, want)
		}
	}
	// Several patterns go to custom-tables; the scalar key must stay away because
	// Mihomo lets the list win and a stale scalar would only mislead the reader.
	tables, ok := config["custom-tables"].([]string)
	if !ok || len(tables) != 2 || tables[0] != "xpvvxpvv" || tables[1] != "vvxxppvv" {
		t.Fatalf("sudoku custom-tables = %#v, want the two normalized patterns", config["custom-tables"])
	}
	if _, ok := config["custom-table"]; ok {
		t.Fatalf("sudoku must not write both custom-table and custom-tables: %#v", config)
	}
	mask, ok := config["httpmask"].(map[string]any)
	if !ok || mask["mode"] != "stream" || mask["path-root"] != "mask" {
		t.Fatalf("sudoku httpmask = %#v, want mode stream and path-root mask", config["httpmask"])
	}
	if _, ok := mask["disable"]; ok {
		t.Fatalf("httpmask.disable must stay out while masking is on: %#v", mask)
	}
	// SudokuOption has no TLS layer and no udp switch, and the PSK lives in `key`,
	// so neither the listener credentials nor a users map may appear.
	for _, unsupported := range []string{"certificate", "private-key", "allow-insecure", "udp", "users", "password", "psk", "disable-http-mask", "http-mask-mode", "path-root"} {
		if _, ok := config[unsupported]; ok {
			t.Fatalf("sudoku listener must not carry %q: %#v", unsupported, config)
		}
	}
}
func TestSudokuOmitsUnsetOptionsButStatesNegotiatedDefaults(t *testing.T) {
	config, err := inboundConfig(sudokuInbound(SudokuSettings{}))
	if err != nil {
		t.Fatal(err)
	}
	// aead-method, table-type and enable-pure-downlink have to agree on both ends,
	// and the client default is not guaranteed to track the listener's, so they are
	// always written out — the same reason Snell always states its version.
	for key, want := range map[string]any{
		"aead-method": "chacha20-poly1305", "table-type": "prefer_entropy", "enable-pure-downlink": false,
	} {
		if got := config[key]; got != want {
			t.Fatalf("sudoku %s = %#v, want %#v", key, got, want)
		}
	}
	for _, omitted := range []string{"padding-min", "padding-max", "handshake-timeout", "custom-table", "custom-tables", "httpmask", "fallback"} {
		if _, ok := config[omitted]; ok {
			t.Fatalf("unset sudoku option %q must fall through to the Mihomo default: %#v", omitted, config)
		}
	}
}

func TestSudokuPaddingIsOnlyWrittenWhenEnabled(t *testing.T) {
	// 0 is a legal padding rate, so the panel keeps an explicit switch instead of
	// treating 0 as "unset" — otherwise "no padding" would be unreachable.
	off, err := inboundConfig(sudokuInbound(SudokuSettings{PaddingMin: 7, PaddingMax: 9}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := off["padding-min"]; ok {
		t.Fatalf("padding must stay unset while the switch is off: %#v", off)
	}
	zero, err := inboundConfig(sudokuInbound(SudokuSettings{PaddingEnabled: true}))
	if err != nil {
		t.Fatal(err)
	}
	if zero["padding-min"] != 0 || zero["padding-max"] != 0 {
		t.Fatalf("an explicit zero padding rate must reach Mihomo: %#v", zero)
	}
}

func TestSudokuSingleCustomTableUsesTheScalarKey(t *testing.T) {
	config, err := inboundConfig(sudokuInbound(SudokuSettings{CustomTables: []string{" ", "xxppvvvv"}}))
	if err != nil {
		t.Fatal(err)
	}
	if config["custom-table"] != "xxppvvvv" {
		t.Fatalf("sudoku custom-table = %#v, want xxppvvvv", config["custom-table"])
	}
	if _, ok := config["custom-tables"]; ok {
		t.Fatalf("a single pattern must not be written as a list: %#v", config)
	}
}
func TestSudokuDisabledHTTPMaskRendersTheNestedObject(t *testing.T) {
	// The nested object's `disable` overrides the flat disable-http-mask key inside
	// Mihomo, so the panel only ever writes the object.
	config, err := inboundConfig(sudokuInbound(SudokuSettings{DisableHTTPMask: true}))
	if err != nil {
		t.Fatal(err)
	}
	mask, ok := config["httpmask"].(map[string]any)
	if !ok || mask["disable"] != true {
		t.Fatalf("sudoku httpmask = %#v, want disable: true", config["httpmask"])
	}
	if _, ok := mask["mode"]; ok {
		t.Fatalf("a disabled HTTP mask must not carry a mode: %#v", mask)
	}
	data, err := marshalYAML(map[string]any{"listeners": []any{config}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "httpmask:\n      disable: true") {
		t.Fatalf("httpmask must nest under the listener:\n%s", data)
	}
}

func TestSaveInboundNormalizesSudokuToSingleKeyClient(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir(), state: State{Settings: Settings{Username: "admin", Password: "hash"}}}
	err := m.saveInbound(Inbound{
		ID: "sudoku", Name: "single-sudoku", Type: "sudoku", Listen: "0.0.0.0", Port: 18477, Enabled: true,
		Password: "legacy-inbound-key",
		Clients: []Client{
			{Name: "one", Password: "key-one", Enabled: true},
			{Name: "two", Password: "key-two", Enabled: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	saved := m.state.Inbounds[0]
	if saved.Password != "" || saved.Username != "" || saved.UUID != "" {
		t.Fatalf("legacy listener credentials were retained: %#v", saved)
	}
	if len(saved.Clients) != 1 || saved.Clients[0].Password != "key-one" {
		t.Fatalf("saved sudoku clients = %#v, want one client holding key-one", saved.Clients)
	}
	config, err := inboundConfig(saved)
	if err != nil {
		t.Fatal(err)
	}
	if got := config["key"]; got != "key-one" {
		t.Fatalf("rendered sudoku key = %v, want key-one", got)
	}
}
func TestSudokuAcceptsSupportedConfigurations(t *testing.T) {
	for name, settings := range map[string]SudokuSettings{
		"bare":            {},
		"kernel defaults": {AEADMethod: "chacha20-poly1305", TableType: "prefer_entropy", PaddingEnabled: true, PaddingMin: 10, PaddingMax: 30, HandshakeTimeout: 5, EnablePureDownlink: true},
		"no aead":         {AEADMethod: "none"},
		"aes":             {AEADMethod: "AES-128-GCM"},
		"ascii table":     {TableType: "prefer_ascii"},
		"split tables":    {TableType: "up_ascii_down_entropy"},
		"legacy alias":    {TableType: "entropy"},
		"zero padding":    {PaddingEnabled: true},
		"equal padding":   {PaddingEnabled: true, PaddingMin: 20, PaddingMax: 20},
		"max padding":     {PaddingEnabled: true, PaddingMin: 0, PaddingMax: 100},
		"ignored padding": {PaddingMin: 90, PaddingMax: 10},
		"mask modes":      {HTTPMaskMode: "ws", PathRoot: "cdn-edge_1"},
		"mask disabled":   {DisableHTTPMask: true},
		"custom table":    {CustomTables: []string{"XPVV XPVV"}},
		"custom tables":   {CustomTables: []string{"xpvvxpvv", "vvxxppvv", ""}},
		"fallback":        {Fallback: "127.0.0.1:8080"},
		"fallback v6":     {Fallback: "[::1]:8080"},
	} {
		if err := validateInbound(sudokuInbound(settings)); err != nil {
			t.Fatalf("%s rejected: %v", name, err)
		}
	}
}
func TestSudokuRejectsUnsupportedCombinations(t *testing.T) {
	for name, testCase := range map[string]struct {
		mutate func(*Inbound)
		want   string
	}{
		"unknown aead":     {func(i *Inbound) { i.Sudoku.AEADMethod = "aes-256-gcm" }, "AEAD Method"},
		"unknown table":    {func(i *Inbound) { i.Sudoku.TableType = "random" }, "Table Type"},
		"padding too big":  {func(i *Inbound) { i.Sudoku.PaddingEnabled, i.Sudoku.PaddingMax = true, 101 }, "0-100"},
		"negative padding": {func(i *Inbound) { i.Sudoku.PaddingEnabled, i.Sudoku.PaddingMin = true, -1 }, "0-100"},
		"inverted padding": {func(i *Inbound) { i.Sudoku.PaddingEnabled, i.Sudoku.PaddingMin, i.Sudoku.PaddingMax = true, 30, 10 }, "不能小于"},
		"negative timeout": {func(i *Inbound) { i.Sudoku.HandshakeTimeout = -1 }, "Handshake Timeout"},
		"unknown mask":     {func(i *Inbound) { i.Sudoku.HTTPMaskMode = "strict" }, "HTTP Mask Mode"},
		"nested path":      {func(i *Inbound) { i.Sudoku.PathRoot = "a/b" }, "只能是一段路径"},
		"slash only":       {func(i *Inbound) { i.Sudoku.PathRoot = "//" }, "不能只有斜杠"},
		"bad path char":    {func(i *Inbound) { i.Sudoku.PathRoot = "cdn.edge" }, "字母、数字"},
		"short table":      {func(i *Inbound) { i.Sudoku.CustomTables = []string{"xpvv"} }, "8 个符号"},
		"bad table symbol": {func(i *Inbound) { i.Sudoku.CustomTables = []string{"xpvvxpva"} }, "x / p / v"},
		"bad table shape":  {func(i *Inbound) { i.Sudoku.CustomTables = []string{"xxxppvvv"} }, "2 个 x、2 个 p、4 个 v"},
		"bad fallback":     {func(i *Inbound) { i.Sudoku.Fallback = "127.0.0.1" }, "host:port"},
		"named port":       {func(i *Inbound) { i.Sudoku.Fallback = "127.0.0.1:http" }, "端口必须是数字"},
		"two clients":      {func(i *Inbound) { i.Clients = append(i.Clients, i.Clients[0]) }, "仅支持一个客户端"},
		"no client":        {func(i *Inbound) { i.Clients = nil }, "仅支持一个客户端"},
		"keyless client":   {func(i *Inbound) { i.Clients[0].Password = "" }, "填写了 Key"},
		"disabled client":  {func(i *Inbound) { i.Clients[0].Enabled = false }, "填写了 Key"},
		"tls":              {func(i *Inbound) { i.TLS = true }, "不使用 TLS"},
		"allow insecure":   {func(i *Inbound) { i.AllowInsecure = true }, "没有 allow-insecure"},
		"jls":              {func(i *Inbound) { i.JLS.Enabled = true }, "不支持 Reality"},
	} {
		inbound := sudokuInbound(SudokuSettings{})
		testCase.mutate(&inbound)
		err := validateInbound(inbound)
		if err == nil || !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("%s error = %v, want %q", name, err, testCase.want)
		}
	}
}
func TestYAMLOrdersSudokuListenerKeys(t *testing.T) {
	config, err := inboundConfig(sudokuInbound(SudokuSettings{
		AEADMethod: "aes-128-gcm", TableType: "prefer_ascii", PaddingEnabled: true, PaddingMin: 5, PaddingMax: 40,
		HandshakeTimeout: 12, EnablePureDownlink: true, CustomTables: []string{"xpvvxpvv"},
		HTTPMaskMode: "stream", PathRoot: "mask", Fallback: "127.0.0.1:8080",
	}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalYAML(map[string]any{"listeners": []any{config}})
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(data)
	for _, pair := range []struct{ first, second string }{
		{"name: \"sudoku-a\"", "type: \"sudoku\""},
		{"type: \"sudoku\"", "listen: \"0.0.0.0\""},
		{"listen: \"0.0.0.0\"", "port: 18476"},
		{"port: 18476", "key: \"sudoku-key\""},
		{"key: \"sudoku-key\"", "aead-method: \"aes-128-gcm\""},
		{"aead-method: \"aes-128-gcm\"", "table-type: \"prefer_ascii\""},
		{"table-type: \"prefer_ascii\"", "custom-table: \"xpvvxpvv\""},
		{"custom-table: \"xpvvxpvv\"", "padding-min: 5"},
		{"padding-min: 5", "padding-max: 40"},
		{"padding-max: 40", "handshake-timeout: 12"},
		{"handshake-timeout: 12", "enable-pure-downlink: true"},
		{"enable-pure-downlink: true", "httpmask:"},
		{"httpmask:", "mode: \"stream\""},
		{"mode: \"stream\"", "path-root: \"mask\""},
		{"path-root: \"mask\"", "fallback: \"127.0.0.1:8080\""},
	} {
		if strings.Index(rendered, pair.first) >= strings.Index(rendered, pair.second) {
			t.Fatalf("expected %q before %q:\n%s", pair.first, pair.second, rendered)
		}
	}
}

// TestPanelOffersEveryKernelChoiceForNewProtocols pins the drawer's option lists
// to the enums the kernel actually accepts, because both protocols validate them
// late — sudoku even re-validates per connection — so a typo in the page would
// only surface as a broken client.
func TestPanelOffersEveryKernelChoiceForNewProtocols(t *testing.T) {
	page, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	choices := append([]string{}, mieruTransports...)
	choices = append(choices, sudokuAEADMethods...)
	choices = append(choices, sudokuHTTPMaskModes...)
	// The panel only offers the four canonical table types; "ascii"/"entropy" stay
	// accepted on input because Mihomo still canonicalizes those legacy spellings.
	choices = append(choices, "prefer_entropy", "prefer_ascii", "up_ascii_down_entropy", "up_entropy_down_ascii")
	for _, choice := range choices {
		if !strings.Contains(string(page), `<option value="`+choice+`">`) {
			t.Fatalf("web/index.html must offer the %q option", choice)
		}
	}
}

// sing-mux refuses to build a service with Brutal enabled unless the host is
// Linux, and Mihomo turns that refusal into "Listener <name> listen err: TCP
// Brutal is only supported on Linux" — the listener never binds. The panel
// therefore drops the switch on such hosts instead of writing a config that
// cannot run.
func TestBrutalMuxIsDroppedOnHostsWithoutTCPBrutal(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		hostSupports bool
		wantEnabled  bool
		wantRate     string
	}{
		{"linux keeps it", true, true, "100 Mbps"},
		{"other kernels drop it", false, false, ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			restore := brutalHostSupported
			brutalHostSupported = testCase.hostSupports
			defer func() { brutalHostSupported = restore }()
			m := &CoreManager{dataDir: t.TempDir(), state: State{Settings: Settings{Username: "admin", Password: "hash"}}}
			err := m.saveInbound(Inbound{
				ID: "brutal", Name: "brutal-vmess", Type: "vmess", Listen: "0.0.0.0", Port: 18478, Enabled: true,
				Clients: []Client{{Name: "one", UUID: "b0f5f0f6-1c2d-4a9b-8f4e-2f5a6b7c8d9e", Enabled: true}},
				Mux:     MuxSettings{Padding: true, BrutalEnabled: true, Up: "100 Mbps", Down: "100 Mbps"},
			})
			if err != nil {
				t.Fatal(err)
			}
			saved := m.state.Inbounds[0]
			if saved.Mux.BrutalEnabled != testCase.wantEnabled || saved.Mux.Up != testCase.wantRate || saved.Mux.Down != testCase.wantRate {
				t.Fatalf("saved mux = %#v, want enabled=%v rate=%q", saved.Mux, testCase.wantEnabled, testCase.wantRate)
			}
			if !saved.Mux.Padding {
				t.Fatal("mux padding must survive: it is not part of the Brutal option")
			}
			config, err := inboundConfig(saved)
			if err != nil {
				t.Fatal(err)
			}
			mux, ok := config["mux-option"].(map[string]any)
			if !ok {
				t.Fatalf("mux-option = %#v, want a map (padding is still on)", config["mux-option"])
			}
			brutal, ok := mux["brutal"].(map[string]any)
			if !ok {
				t.Fatalf("mux-option.brutal = %#v, want a map", mux["brutal"])
			}
			if brutal["enabled"] != testCase.wantEnabled {
				t.Fatalf("rendered brutal.enabled = %v, want %v", brutal["enabled"], testCase.wantEnabled)
			}
		})
	}
}

// A state.json written before this rule existed - or copied over from a Linux
// box - can still ask for Brutal, so the load-time migration fixes it once and
// persists the result instead of letting the listener fail on every start.
func TestMigrationClearsBrutalMuxOnHostsWithoutTCPBrutal(t *testing.T) {
	brutalInbound := func() Inbound {
		return Inbound{
			ID: "legacy", Name: "legacy-vmess", Type: "vmess", Listen: "0.0.0.0", Port: 18479, Enabled: true,
			CreatedAt: "2026-01-01T00:00:00Z",
			Clients:   []Client{{ID: "c1", Name: "one", UUID: "b0f5f0f6-1c2d-4a9b-8f4e-2f5a6b7c8d9e", Enabled: true, CreatedAt: "2026-01-01T00:00:00Z"}},
			Mux:       MuxSettings{BrutalEnabled: true, Up: "50 Mbps", Down: "50 Mbps"},
		}
	}
	restore := brutalHostSupported
	defer func() { brutalHostSupported = restore }()

	brutalHostSupported = false
	m := &CoreManager{dataDir: t.TempDir(), state: State{Inbounds: []Inbound{brutalInbound()}}}
	if !m.migrateStateLocked() {
		t.Fatal("migration must report a change so the corrected state is written back")
	}
	if got := m.state.Inbounds[0].Mux; got.BrutalEnabled || got.Up != "" || got.Down != "" {
		t.Fatalf("migrated mux = %#v, want the Brutal group cleared", got)
	}

	brutalHostSupported = true
	onLinux := &CoreManager{dataDir: t.TempDir(), state: State{Inbounds: []Inbound{brutalInbound()}}}
	// A state that already went through one migration carries the expanded
	// subscription toggle map, so the toggles normalization stays a no-op here
	// and the assertion isolates the Brutal rule.
	normalizeSubscriptionClientToggles(&onLinux.state.Settings)
	if onLinux.migrateStateLocked() {
		t.Fatal("migration must leave a supported Brutal configuration alone")
	}
	if got := onLinux.state.Inbounds[0].Mux; !got.BrutalEnabled || got.Up != "50 Mbps" {
		t.Fatalf("migrated mux on Linux = %#v, want it untouched", got)
	}
}

// The panel learns the host kernel from /api/state's platform field, so the page
// has to carry both the guard and the kernel's own wording for the tooltip. The
// wording itself now lives in the i18n dictionary, one entry per language.
func TestPanelExplainsWhyBrutalMuxIsUnavailable(t *testing.T) {
	page, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		"muiT('drawer.brutalLinuxOnly')",
		"function brutalMuxSupported()",
		"app.state?.platform",
		"syncBrutalUI();",
	} {
		if !strings.Contains(string(page), needle) {
			t.Fatalf("web/index.html must contain %q", needle)
		}
	}
	dictionary, err := os.ReadFile("web/i18n.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		"'drawer.brutalLinuxOnly': 'TCP Brutal 仅支持 Linux'",
		"'drawer.brutalLinuxOnly': 'TCP Brutal is only supported on Linux'",
	} {
		if !strings.Contains(string(dictionary), needle) {
			t.Fatalf("web/i18n.js must contain %q", needle)
		}
	}
}

func kcpTunInbound(settings KcpTunSettings) Inbound {
	return Inbound{
		Name: "ss-kcp", Type: "shadowsocks", Listen: "0.0.0.0", Port: 18490, Enabled: true, UDP: true,
		Cipher:  "2022-blake3-aes-256-gcm",
		Clients: []Client{{Name: "one", Username: "one", Password: "ss-psk", Enabled: true}},
		KcpTun:  settings,
	}
}

func kcpTunBlock(t *testing.T, inbound Inbound) map[string]any {
	t.Helper()
	config, err := inboundConfig(inbound)
	if err != nil {
		t.Fatal(err)
	}
	block, ok := config["kcp-tun"].(map[string]any)
	if !ok {
		t.Fatalf("shadowsocks listener has no kcp-tun block: %#v", config)
	}
	return block
}

func TestKcpTunListenerRendersEveryMihomoOption(t *testing.T) {
	block := kcpTunBlock(t, kcpTunInbound(KcpTunSettings{
		Enabled: true, Key: " tunnel-key ", Crypt: " Salsa20 ", Mode: " Manual ",
		MTU: 1300, SndWnd: 256, RcvWnd: 1024, DataShard: 20, ParityShard: 6, DSCP: 46, RateLimit: 100,
		NoComp: true, AckNodelay: true, NoDelay: 1, Interval: 20, Resend: 2, NoCongestion: 1,
		SockBuf: 8388608, SmuxVer: 2, SmuxBuf: 8388608, StreamBuf: 4194304, FrameSize: 16384, KeepAlive: 15,
	}))
	for key, want := range map[string]any{
		"enable": true, "key": "tunnel-key", "crypt": "salsa20", "mode": "manual",
		"mtu": 1300, "sndwnd": 256, "rcvwnd": 1024, "datashard": 20, "parityshard": 6, "dscp": 46,
		"ratelimit": 100, "nocomp": true, "acknodelay": true, "nodelay": 1, "interval": 20,
		"resend": 2, "nc": 1, "sockbuf": 8388608, "smuxver": 2, "smuxbuf": 8388608,
		"streambuf": 4194304, "framesize": 16384, "keepalive": 15,
	} {
		if got := block[key]; got != want {
			t.Fatalf("kcp-tun %s = %#v, want %#v", key, got, want)
		}
	}
	if len(block) != 23 {
		t.Fatalf("kcp-tun block has %d keys, want the 23 the kernel reads: %#v", len(block), block)
	}
}

// Key, crypt and mode are always written out because the kernel's silent
// fallbacks ("it's a secrect", aes, fast) have to be repeated verbatim on every
// client; the mode-driven four stay out unless the mode is manual, which is the
// only one FillDefaults does not overwrite.
func TestKcpTunOmitsUnsetOptionsButStatesTheSharedSecret(t *testing.T) {
	block := kcpTunBlock(t, kcpTunInbound(KcpTunSettings{Enabled: true, Key: "tunnel-key"}))
	for key, want := range map[string]any{"enable": true, "key": "tunnel-key", "crypt": "aes", "mode": "fast"} {
		if got := block[key]; got != want {
			t.Fatalf("kcp-tun %s = %#v, want %#v", key, got, want)
		}
	}
	if len(block) != 4 {
		t.Fatalf("kcp-tun block = %#v, want only enable/key/crypt/mode", block)
	}
	tuned := kcpTunBlock(t, kcpTunInbound(KcpTunSettings{
		Enabled: true, Key: "tunnel-key", Mode: "fast3", NoDelay: 1, Interval: 20, Resend: 2, NoCongestion: 1,
	}))
	for _, overwritten := range []string{"nodelay", "interval", "resend", "nc"} {
		if _, ok := tuned[overwritten]; ok {
			t.Fatalf("mode fast3 overwrites %q in FillDefaults, so it must stay out: %#v", overwritten, tuned)
		}
	}
	manual := kcpTunBlock(t, kcpTunInbound(KcpTunSettings{Enabled: true, Key: "tunnel-key", Mode: "manual"}))
	for _, pinned := range []string{"nodelay", "interval", "resend", "nc"} {
		if got, ok := manual[pinned]; !ok || got != 0 {
			t.Fatalf("manual mode must pin %q even at zero: %#v", pinned, manual)
		}
	}
}

func TestKcpTunOnlyRendersForShadowsocks(t *testing.T) {
	inbound := kcpTunInbound(KcpTunSettings{Enabled: true, Key: "tunnel-key"})
	inbound.Type = "snell"
	inbound.Snell = SnellSettings{Version: 4}
	config, err := inboundConfig(inbound)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := config["kcp-tun"]; ok {
		t.Fatalf("only the shadowsocks listener declares kcp-tun: %#v", config)
	}
	if err := validateKcpTun(inbound); err == nil {
		t.Fatal("validateInbound must reject kcp-tun outside shadowsocks")
	}
}

func TestKcpTunAcceptsSupportedConfigurations(t *testing.T) {
	for _, settings := range []KcpTunSettings{
		{Enabled: true, Key: "tunnel-key"},
		{Enabled: true, Key: "tunnel-key", Crypt: "aes-128-gcm", Mode: "normal"},
		{Enabled: true, Key: "tunnel-key", Crypt: "null", Mode: "manual", NoDelay: 1, Interval: 10, Resend: 2, NoCongestion: 1},
		{Enabled: true, Key: "tunnel-key", MTU: 50, SmuxVer: 2, FrameSize: 65535, DataShard: 253, ParityShard: 3},
		{Enabled: true, Key: "tunnel-key", SmuxBuf: 2097152, StreamBuf: 2097152},
	} {
		if err := validateInbound(kcpTunInbound(settings)); err != nil {
			t.Fatalf("validateInbound(%#v) = %v, want nil", settings, err)
		}
	}
}

func TestKcpTunRejectsUnsupportedCombinations(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		mutate  func(*Inbound)
		wantErr string
	}{
		{"missing key", func(i *Inbound) { i.KcpTun.Key = " " }, "Key"},
		{"unknown crypt", func(i *Inbound) { i.KcpTun.Crypt = "chacha20" }, "Crypt"},
		{"unknown mode", func(i *Inbound) { i.KcpTun.Mode = "turbo" }, "Mode"},
		{"simple-obfs", func(i *Inbound) { i.SimpleObfs = SimpleObfsSettings{Enabled: true, Mode: "http"} }, "simple-obfs"},
		{"shadow-tls", func(i *Inbound) { i.ShadowTLS.Enabled = true }, "Shadow-TLS"},
		{"jls", func(i *Inbound) { i.JLS.Enabled = true }, "Shadow-TLS"},
		{"negative window", func(i *Inbound) { i.KcpTun.SndWnd = -1 }, "Send Window"},
		{"tiny mtu", func(i *Inbound) { i.KcpTun.MTU = 49 }, "MTU"},
		{"huge mtu", func(i *Inbound) { i.KcpTun.MTU = 1501 }, "MTU"},
		{"smux version", func(i *Inbound) { i.KcpTun.SmuxVer = 3 }, "smux 版本"},
		{"frame size", func(i *Inbound) { i.KcpTun.FrameSize = 65536 }, "Frame Size"},
		{"stream buffer", func(i *Inbound) { i.KcpTun.SmuxBuf = 1048576 }, "Stream Buffer"},
		{"shard total", func(i *Inbound) { i.KcpTun.DataShard, i.KcpTun.ParityShard = 200, 200 }, "Shard"},
	} {
		inbound := kcpTunInbound(KcpTunSettings{Enabled: true, Key: "tunnel-key"})
		testCase.mutate(&inbound)
		err := validateInbound(inbound)
		if err == nil {
			t.Fatalf("%s: validateInbound = nil, want an error", testCase.name)
		}
		if !strings.Contains(err.Error(), testCase.wantErr) {
			t.Fatalf("%s: validateInbound = %v, want it to mention %q", testCase.name, err, testCase.wantErr)
		}
	}
}

func TestYAMLOrdersKcpTunListenerKeys(t *testing.T) {
	config, err := inboundConfig(kcpTunInbound(KcpTunSettings{
		Enabled: true, Key: "tunnel-key", Crypt: "salsa20", Mode: "manual",
		MTU: 1300, SndWnd: 256, RcvWnd: 1024, DataShard: 20, ParityShard: 6, DSCP: 46, RateLimit: 100,
		NoComp: true, AckNodelay: true, NoDelay: 1, Interval: 20, Resend: 2, NoCongestion: 1,
		SockBuf: 8388608, SmuxVer: 2, SmuxBuf: 8388608, StreamBuf: 4194304, FrameSize: 16384, KeepAlive: 15,
	}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalYAML(map[string]any{"listeners": []any{config}})
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(data)
	for _, pair := range []struct{ first, second string }{
		{"cipher: \"2022-blake3-aes-256-gcm\"", "kcp-tun:"},
		{"kcp-tun:", "enable: true"},
		{"enable: true", "key: \"tunnel-key\""},
		{"key: \"tunnel-key\"", "crypt: \"salsa20\""},
		{"crypt: \"salsa20\"", "mode: \"manual\""},
		{"mode: \"manual\"", "mtu: 1300"},
		{"mtu: 1300", "sndwnd: 256"},
		{"sndwnd: 256", "rcvwnd: 1024"},
		{"rcvwnd: 1024", "datashard: 20"},
		{"datashard: 20", "parityshard: 6"},
		{"parityshard: 6", "dscp: 46"},
		{"dscp: 46", "ratelimit: 100"},
		{"ratelimit: 100", "nocomp: true"},
		{"nocomp: true", "acknodelay: true"},
		{"acknodelay: true", "nodelay: 1"},
		{"nodelay: 1", "interval: 20"},
		{"interval: 20", "resend: 2"},
		{"resend: 2", "nc: 1"},
		{"nc: 1", "sockbuf: 8388608"},
		{"sockbuf: 8388608", "smuxver: 2"},
		{"smuxver: 2", "smuxbuf: 8388608"},
		{"smuxbuf: 8388608", "streambuf: 4194304"},
		{"streambuf: 4194304", "framesize: 16384"},
		{"framesize: 16384", "keepalive: 15"},
	} {
		if strings.Index(rendered, pair.first) >= strings.Index(rendered, pair.second) {
			t.Fatalf("expected %q before %q:\n%s", pair.first, pair.second, rendered)
		}
	}
}

// The Transmission section is only meaningful for the four listeners that really
// take a stream transport; every other protocol used to open an empty block. The
// panel therefore drives both the option list and the section itself off one
// table, which this test pins together with the kcp-tun interlocks.
func TestPanelOnlyShowsTransmissionForListenersWithATransport(t *testing.T) {
	page, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`function transportsForType(type){if(type==='vmess')return['tcp','ws','grpc','mkcp','mekya'];if(type==='vless')return['tcp','ws','grpc','xhttp'];if(type==='trojan')return['tcp','ws','grpc'];if(type==='shadowsocks')return['tcp','kcptun'];return[];}`,
		`transports=transportsForType(type),stream=transports.length>0`,
		`showFields('.inbound-transport',stream);`,
		`showFields('.transport-kcptun',type==='shadowsocks'&&network==='kcptun');`,
		`function syncKcpTunUI()`,
		"syncKcpTunUI();",
		`if(type==='shadowsocks'&&network==='kcptun')return['none'];`,
		`inbound.type==='shadowsocks'&&inbound.kcpTun?.enabled`,
	} {
		if !strings.Contains(string(page), needle) {
			t.Fatalf("web/index.html must contain %q", needle)
		}
	}
}

// kcptun resolves an unknown cipher to aes and an unknown mode to "pass the four
// tuning knobs through", both without a word in the log, so the drawer has to
// offer exactly the kernel's spellings.
func TestPanelOffersEveryKcpTunChoice(t *testing.T) {
	page, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(page)
	if !strings.Contains(rendered, `const kcpTunCryptModes=['`+strings.Join(kcpTunCryptModes, `','`)+`'];`) {
		t.Fatalf("web/index.html must list the kernel's crypt modes in the same order as the backend: %v", kcpTunCryptModes)
	}
	for _, choice := range kcpTunModes {
		if !strings.Contains(rendered, `<option value="`+choice+`">`+choice+`</option>`) {
			t.Fatalf("web/index.html must offer the %q kcp-tun mode", choice)
		}
	}
}

func hysteria2RealmInbound(settings Hysteria2RealmSettings) Inbound {
	return Inbound{
		Name: "realm-a", Type: "hysteria2-realm", Listen: "0.0.0.0", Port: 18492, Enabled: true,
		Hysteria2Realm: settings,
	}
}

func TestHysteria2RealmListenerRendersEveryMihomoOption(t *testing.T) {
	config, err := inboundConfig(hysteria2RealmInbound(Hysteria2RealmSettings{
		Token: " realm-token ", MaxRealms: 128, MaxRealmsPerIP: 2,
		TrustedProxyHeader: " X-Forwarded-For ", RealmNamePattern: ` ^[a-z]{3,8}$ `,
	}))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"token": "realm-token", "max-realms": 128, "max-realms-per-ip": 2,
		"trusted-proxy-header": "X-Forwarded-For", "realm-name-pattern": `^[a-z]{3,8}$`,
	} {
		if got := config[key]; got != want {
			t.Fatalf("hysteria2-realm %s = %#v, want %#v", key, got, want)
		}
	}
	// The rendezvous API authenticates with a bearer token, drops the tunnel it is
	// handed and wraps nothing, so there is no user table and no transport key. Its
	// `alpn` option is declared but never copied into tlsConfig.NextProtos, so the
	// panel leaves it out rather than writing a key Mihomo ignores.
	for _, unsupported := range []string{"users", "password", "psk", "key", "uuid", "udp", "alpn", "mux-option", "allow-insecure", "network", "certificate", "private-key"} {
		if _, ok := config[unsupported]; ok {
			t.Fatalf("hysteria2-realm listener must not carry %q: %#v", unsupported, config)
		}
	}
}

// listener/parse.go starts from DefaultHysteria2RealmServerOption(), so every key
// the panel omits falls back to 65536 realms, 4 per IP and Mihomo's own name
// pattern. All three are therefore always written: 0 means "unlimited" and an
// empty pattern compiles to a regexp that matches every realm name, which is what
// the cleared field in the drawer promises.
func TestHysteria2RealmAlwaysStatesItsLimitsAndPattern(t *testing.T) {
	config, err := inboundConfig(hysteria2RealmInbound(Hysteria2RealmSettings{Token: "realm-token"}))
	if err != nil {
		t.Fatal(err)
	}
	if config["max-realms"] != 0 || config["max-realms-per-ip"] != 0 {
		t.Fatalf("unlimited realms must reach Mihomo as explicit zeros: %#v", config)
	}
	pattern, ok := config["realm-name-pattern"].(string)
	if !ok || pattern != "" {
		t.Fatalf("realm-name-pattern = %#v, want an explicit empty string", config["realm-name-pattern"])
	}
	if _, ok := config["trusted-proxy-header"]; ok {
		t.Fatalf("an unset proxy header must fall through to the Mihomo default: %#v", config)
	}
}

func TestHysteria2RealmAcceptsSupportedConfigurations(t *testing.T) {
	for name, mutate := range map[string]func(*Inbound){
		"minimal":        func(i *Inbound) {},
		"unlimited":      func(i *Inbound) { i.Hysteria2Realm.MaxRealms, i.Hysteria2Realm.MaxRealmsPerIP = 0, 0 },
		"kernel pattern": func(i *Inbound) { i.Hysteria2Realm.RealmNamePattern = hysteria2RealmDefaultNamePattern },
		"kernel defaults": func(i *Inbound) {
			i.Hysteria2Realm.MaxRealms = hysteria2RealmDefaultMaxRealms
			i.Hysteria2Realm.MaxRealmsPerIP = hysteria2RealmDefaultMaxRealmsPerIP
		},
		"proxy header": func(i *Inbound) { i.Hysteria2Realm.TrustedProxyHeader = "CF-Connecting-IP" },
		"behind tls": func(i *Inbound) {
			i.TLS, i.Certificate, i.PrivateKey = true, "/etc/ssl/realm.crt", "/etc/ssl/realm.key"
		},
	} {
		inbound := hysteria2RealmInbound(Hysteria2RealmSettings{Token: "realm-token"})
		mutate(&inbound)
		if err := validateInbound(inbound); err != nil {
			t.Fatalf("%s must be accepted: %v", name, err)
		}
	}
}

func TestHysteria2RealmRejectsUnsupportedCombinations(t *testing.T) {
	for name, testCase := range map[string]struct {
		mutate func(*Inbound)
		want   string
	}{
		"no token":        {func(i *Inbound) { i.Hysteria2Realm.Token = "   " }, "必须配置 Token"},
		"negative limit":  {func(i *Inbound) { i.Hysteria2Realm.MaxRealms = -1 }, "不能为负数"},
		"negative per ip": {func(i *Inbound) { i.Hysteria2Realm.MaxRealmsPerIP = -1 }, "不能为负数"},
		"broken pattern":  {func(i *Inbound) { i.Hysteria2Realm.RealmNamePattern = "^[a-z" }, "不是有效的正则"},
		"header value":    {func(i *Inbound) { i.Hysteria2Realm.TrustedProxyHeader = "X-Real-IP: 10.0.0.1" }, "HTTP 头名称"},
		"client":          {func(i *Inbound) { i.Clients = []Client{{Name: "one", Password: "p", Enabled: true}} }, "不需要客户端"},
		"mux":             {func(i *Inbound) { i.Mux.Padding = true }, "没有 mux-option"},
		"allow insecure":  {func(i *Inbound) { i.AllowInsecure = true }, "没有 allow-insecure"},
		"jls":             {func(i *Inbound) { i.JLS.Enabled = true }, "不支持 Reality"},
		"reality":         {func(i *Inbound) { i.Reality.Enabled = true }, "不支持 Reality"},
		"tls without key": {func(i *Inbound) { i.TLS = true }, "需要同时配置证书和私钥"},
	} {
		inbound := hysteria2RealmInbound(Hysteria2RealmSettings{Token: "realm-token"})
		testCase.mutate(&inbound)
		err := validateInbound(inbound)
		if err == nil || !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("%s error = %v, want %q", name, err, testCase.want)
		}
	}
}

func TestYAMLOrdersHysteria2RealmListenerKeys(t *testing.T) {
	config, err := inboundConfig(hysteria2RealmInbound(Hysteria2RealmSettings{
		Token: "realm-token", MaxRealms: 128, MaxRealmsPerIP: 2,
		TrustedProxyHeader: "X-Forwarded-For", RealmNamePattern: `^[a-z]{3,8}$`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalYAML(map[string]any{"listeners": []any{config}})
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(data)
	for _, pair := range []struct{ first, second string }{
		{"name: \"realm-a\"", "type: \"hysteria2-realm\""},
		{"type: \"hysteria2-realm\"", "listen: \"0.0.0.0\""},
		{"listen: \"0.0.0.0\"", "port: 18492"},
		{"port: 18492", "token: \"realm-token\""},
		{"token: \"realm-token\"", "max-realms: 128"},
		{"max-realms: 128", "max-realms-per-ip: 2"},
		{"max-realms-per-ip: 2", "trusted-proxy-header: \"X-Forwarded-For\""},
		{"trusted-proxy-header: \"X-Forwarded-For\"", "realm-name-pattern: \"^[a-z]{3,8}$\""},
	} {
		if strings.Index(rendered, pair.first) >= strings.Index(rendered, pair.second) {
			t.Fatalf("expected %q before %q:\n%s", pair.first, pair.second, rendered)
		}
	}
}

func shadowQuicInbound(settings ShadowQuicSettings) Inbound {
	if strings.TrimSpace(settings.JLSAddr) == "" {
		settings.JLSAddr = "example.com:443"
	}
	return Inbound{
		Name: "sq-a", Type: "shadowquic", Listen: "0.0.0.0", Port: 18493, Enabled: true,
		Clients:    []Client{{Name: "one", Username: "one", Password: "sq-secret", Enabled: true}},
		ShadowQuic: settings,
	}
}

func TestShadowQuicListenerRendersEveryMihomoOption(t *testing.T) {
	config, err := inboundConfig(shadowQuicInbound(ShadowQuicSettings{
		JLSAddr: " example.com:443 ", JLSSNI: " cdn.example.com ", JLSProxy: " DIRECT ", JLSRateLimit: 1048576,
		ALPN: "h3, hq", QUICVersions: " V1 , v2 ", ZeroRTT: true,
		CongestionController: " BBR ", CWND: 48, BBRProfile: " Aggressive ",
		Up: "200 Mbps", Down: "500 Mbps",
		MaxIdleTime: 30000, MaxDatagramFrameSize: 1400, RecvWindowConn: 1048576, RecvWindow: 2097152,
		DisableMTUDiscovery: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"zero-rtt": true, "congestion-controller": "bbr", "cwnd": 48, "bbr-profile": "aggressive",
		"up": "200 Mbps", "down": "500 Mbps", "max-idle-time": 30000, "max-datagram-frame-size": 1400,
		"recv-window-conn": 1048576, "recv-window": 2097152, "disable-mtu-discovery": true,
	} {
		if got := config[key]; got != want {
			t.Fatalf("shadowquic %s = %#v, want %#v", key, got, want)
		}
	}
	upstream, ok := config["jls-upstream"].(map[string]any)
	if !ok || upstream["addr"] != "example.com:443" || upstream["sni"] != "cdn.example.com" || upstream["proxy"] != "DIRECT" || upstream["rate-limit"] != uint64(1048576) {
		t.Fatalf("shadowquic jls-upstream = %#v, want the whole mirrored upstream", config["jls-upstream"])
	}
	if alpn, ok := config["alpn"].([]string); !ok || len(alpn) != 2 || alpn[0] != "h3" || alpn[1] != "hq" {
		t.Fatalf("shadowquic alpn = %#v, want [h3 hq]", config["alpn"])
	}
	// ParseQUICVersion only knows the lowercase spellings.
	if versions, ok := config["quic-versions"].([]string); !ok || len(versions) != 2 || versions[0] != "v1" || versions[1] != "v2" {
		t.Fatalf("shadowquic quic-versions = %#v, want [v1 v2]", config["quic-versions"])
	}
	users, ok := config["users"].([]map[string]any)
	if !ok || len(users) != 1 || users[0]["username"] != "one" || users[0]["password"] != "sq-secret" {
		t.Fatalf("shadowquic users = %#v, want the username/password list", config["users"])
	}
	// JLS replaces the certificate: shadowquic/server.go self-signs a throwaway
	// P-256 pair, so none of the TLS keys exist on this listener.
	for _, unsupported := range []string{"certificate", "private-key", "client-auth-type", "ech-key", "allow-insecure", "udp", "network", "token", "key", "psk"} {
		if _, ok := config[unsupported]; ok {
			t.Fatalf("shadowquic listener must not carry %q: %#v", unsupported, config)
		}
	}
}

// listener/parse.go pre-fills ZeroRTT with true, so the panel always states it —
// the same reason Snell always writes its version. Everything else falls through
// to Mihomo's own defaults (30s idle, h3, 1400-byte datagrams, bbr).
func TestShadowQuicOmitsUnsetOptionsButStatesZeroRTT(t *testing.T) {
	config, err := inboundConfig(shadowQuicInbound(ShadowQuicSettings{}))
	if err != nil {
		t.Fatal(err)
	}
	if config["zero-rtt"] != false {
		t.Fatalf("zero-rtt = %#v, want an explicit false", config["zero-rtt"])
	}
	upstream, ok := config["jls-upstream"].(map[string]any)
	if !ok || upstream["addr"] != "example.com:443" {
		t.Fatalf("jls-upstream = %#v, want just the mandatory addr", config["jls-upstream"])
	}
	for _, omitted := range []string{"sni", "proxy", "rate-limit"} {
		if _, ok := upstream[omitted]; ok {
			t.Fatalf("unset jls-upstream option %q must stay out: %#v", omitted, upstream)
		}
	}
	for _, omitted := range []string{
		"alpn", "quic-versions", "congestion-controller", "cwnd", "bbr-profile", "up", "down",
		"ignore-client-bandwidth", "max-idle-time", "max-datagram-frame-size", "recv-window-conn",
		"recv-window", "disable-mtu-discovery",
	} {
		if _, ok := config[omitted]; ok {
			t.Fatalf("unset shadowquic option %q must fall through to the Mihomo default: %#v", omitted, config)
		}
	}
}

func TestShadowQuicIgnoreClientBandwidthReplacesTheDownRate(t *testing.T) {
	config, err := inboundConfig(shadowQuicInbound(ShadowQuicSettings{IgnoreClientBandwidth: true, Up: "200 Mbps"}))
	if err != nil {
		t.Fatal(err)
	}
	if config["ignore-client-bandwidth"] != true || config["up"] != "200 Mbps" {
		t.Fatalf("shadowquic bandwidth keys = %#v", config)
	}
	if _, ok := config["down"]; ok {
		t.Fatalf("ignoring the client's rate must not leave a receive rate behind: %#v", config)
	}
}

func TestShadowQuicAcceptsSupportedConfigurations(t *testing.T) {
	for name, settings := range map[string]ShadowQuicSettings{
		"minimal":          {JLSAddr: "example.com:443"},
		"ipv6 upstream":    {JLSAddr: "[2001:db8::1]:443"},
		"both versions":    {JLSAddr: "example.com:443", QUICVersions: "v1,v2"},
		"cubic":            {JLSAddr: "example.com:443", CongestionController: "cubic"},
		"bbr with profile": {JLSAddr: "example.com:443", CongestionController: "bbr", CWND: 32, BBRProfile: "conservative"},
		"meta v2":          {JLSAddr: "example.com:443", CongestionController: "bbr_meta_v2", BBRProfile: "aggressive"},
		"meta v1 cwnd":     {JLSAddr: "example.com:443", CongestionController: "bbr_meta_v1", CWND: 64},
		"brutal rates":     {JLSAddr: "example.com:443", Up: "200 Mbps", Down: "500 Mbps"},
		"ignore client":    {JLSAddr: "example.com:443", Up: "200 Mbps", IgnoreClientBandwidth: true},
		"quic windows":     {JLSAddr: "example.com:443", MaxIdleTime: 30000, RecvWindowConn: 1048576, RecvWindow: 2097152, DisableMTUDiscovery: true},
	} {
		if err := validateInbound(shadowQuicInbound(settings)); err != nil {
			t.Fatalf("%s must be accepted: %v", name, err)
		}
	}
}

func TestShadowQuicRejectsUnsupportedCombinations(t *testing.T) {
	for name, testCase := range map[string]struct {
		mutate func(*Inbound)
		want   string
	}{
		"no upstream":      {func(i *Inbound) { i.ShadowQuic.JLSAddr = "  " }, "必须配置 JLS Upstream"},
		"upstream no port": {func(i *Inbound) { i.ShadowQuic.JLSAddr = "example.com" }, "host:port"},
		"upstream no host": {func(i *Inbound) { i.ShadowQuic.JLSAddr = ":443" }, "host:port"},
		"named port":       {func(i *Inbound) { i.ShadowQuic.JLSAddr = "example.com:https" }, "端口无效"},
		"unknown version":  {func(i *Inbound) { i.ShadowQuic.QUICVersions = "v1,v3" }, "QUIC 版本不受支持"},
		"unknown control":  {func(i *Inbound) { i.ShadowQuic.CongestionController = "brutal" }, "Congestion Controller"},
		"profile on cubic": {func(i *Inbound) { i.ShadowQuic.CongestionController, i.ShadowQuic.BBRProfile = "cubic", "standard" }, "BBR Profile 仅"},
		"cwnd on new reno": {func(i *Inbound) { i.ShadowQuic.CongestionController, i.ShadowQuic.CWND = "new_reno", 32 }, "Init CWND 仅"},
		"bad profile":      {func(i *Inbound) { i.ShadowQuic.CongestionController, i.ShadowQuic.BBRProfile = "bbr", "turbo" }, "BBR Profile 无效"},
		"negative window":  {func(i *Inbound) { i.ShadowQuic.RecvWindow = -1 }, "不能为负数"},
		"ignore plus down": {func(i *Inbound) { i.ShadowQuic.IgnoreClientBandwidth, i.ShadowQuic.Down = true, "500 Mbps" }, "不能再设置 Down"},
		"no client":        {func(i *Inbound) { i.Clients = nil }, "至少需要一个启用的客户端"},
		"disabled client":  {func(i *Inbound) { i.Clients[0].Enabled = false }, "至少需要一个启用的客户端"},
		"nameless client":  {func(i *Inbound) { i.Clients[0].Name, i.Clients[0].Username = "", "" }, "必须有用户名"},
		"certificate":      {func(i *Inbound) { i.Certificate, i.PrivateKey = "/etc/ssl/c.crt", "/etc/ssl/c.key" }, "不需要证书"},
		"tls":              {func(i *Inbound) { i.TLS = true }, "不需要证书"},
		"allow insecure":   {func(i *Inbound) { i.AllowInsecure = true }, "没有 allow-insecure"},
		"jls wrapper":      {func(i *Inbound) { i.JLS.Enabled = true }, "不支持 Reality"},
	} {
		inbound := shadowQuicInbound(ShadowQuicSettings{JLSAddr: "example.com:443"})
		testCase.mutate(&inbound)
		err := validateInbound(inbound)
		if err == nil || !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("%s error = %v, want %q", name, err, testCase.want)
		}
	}
}

func TestYAMLOrdersShadowQuicListenerKeys(t *testing.T) {
	config, err := inboundConfig(shadowQuicInbound(ShadowQuicSettings{
		JLSAddr: "example.com:443", JLSSNI: "cdn.example.com", JLSProxy: "DIRECT", JLSRateLimit: 1048576,
		ALPN: "h3", QUICVersions: "v1,v2", ZeroRTT: true,
		CongestionController: "bbr", CWND: 48, BBRProfile: "standard", Up: "200 Mbps", Down: "500 Mbps",
		MaxIdleTime: 30000, MaxDatagramFrameSize: 1400, RecvWindowConn: 1048576, RecvWindow: 2097152,
		DisableMTUDiscovery: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalYAML(map[string]any{"listeners": []any{config}})
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(data)
	for _, pair := range []struct{ first, second string }{
		{"name: \"sq-a\"", "type: \"shadowquic\""},
		{"type: \"shadowquic\"", "listen: \"0.0.0.0\""},
		{"listen: \"0.0.0.0\"", "port: 18493"},
		{"port: 18493", "users:"},
		{"users:", "- username: \"one\""},
		{"- username: \"one\"", "password: \"sq-secret\""},
		{"password: \"sq-secret\"", "jls-upstream:"},
		// The nested block gets its own order, keyed off `addr`.
		{"jls-upstream:", "addr: \"example.com:443\""},
		{"addr: \"example.com:443\"", "sni: \"cdn.example.com\""},
		{"sni: \"cdn.example.com\"", "proxy: \"DIRECT\""},
		{"proxy: \"DIRECT\"", "rate-limit: 1048576"},
		{"rate-limit: 1048576", "up: \"200 Mbps\""},
		{"up: \"200 Mbps\"", "down: \"500 Mbps\""},
		{"down: \"500 Mbps\"", "alpn:"},
		{"alpn:", "quic-versions:"},
		{"quic-versions:", "zero-rtt: true"},
		{"zero-rtt: true", "congestion-controller: \"bbr\""},
		{"congestion-controller: \"bbr\"", "cwnd: 48"},
		{"cwnd: 48", "bbr-profile: \"standard\""},
		{"bbr-profile: \"standard\"", "recv-window-conn: 1048576"},
		{"recv-window-conn: 1048576", "recv-window: 2097152"},
		{"recv-window: 2097152", "max-idle-time: 30000"},
		{"max-idle-time: 30000", "max-datagram-frame-size: 1400"},
		{"max-datagram-frame-size: 1400", "disable-mtu-discovery: true"},
	} {
		if strings.Index(rendered, pair.first) >= strings.Index(rendered, pair.second) {
			t.Fatalf("expected %q before %q:\n%s", pair.first, pair.second, rendered)
		}
	}
}

func trustTunnelInbound(settings TrustTunnelSettings) Inbound {
	if strings.TrimSpace(settings.Network) == "" {
		settings.Network = "tcp"
	}
	return Inbound{
		Name: "tt-a", Type: "trusttunnel", Listen: "0.0.0.0", Port: 18494, Enabled: true,
		Certificate: "/etc/ssl/tt.crt", PrivateKey: "/etc/ssl/tt.key",
		Clients:     []Client{{Name: "one", Username: "one", Password: "tt-secret", Enabled: true}},
		TrustTunnel: settings,
	}
}

func TestTrustTunnelListenerRendersEveryMihomoOption(t *testing.T) {
	config, err := inboundConfig(trustTunnelInbound(TrustTunnelSettings{
		Network: " TCP , UDP ", CongestionController: " BBR ", CWND: 48, BBRProfile: " Aggressive ",
	}))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"certificate": "/etc/ssl/tt.crt", "private-key": "/etc/ssl/tt.key",
		"congestion-controller": "bbr", "cwnd": 48, "bbr-profile": "aggressive",
	} {
		if got := config[key]; got != want {
			t.Fatalf("trusttunnel %s = %#v, want %#v", key, got, want)
		}
	}
	// trusttunnel.New lowercases and prefix-matches the list, so the panel writes
	// the canonical spellings and both sockets share the port.
	if networks, ok := config["network"].([]string); !ok || len(networks) != 2 || networks[0] != "tcp" || networks[1] != "udp" {
		t.Fatalf("trusttunnel network = %#v, want [tcp udp]", config["network"])
	}
	users, ok := config["users"].([]map[string]any)
	if !ok || len(users) != 1 || users[0]["username"] != "one" || users[0]["password"] != "tt-secret" {
		t.Fatalf("trusttunnel users = %#v, want the Basic auth table", config["users"])
	}
	// sing.NewListenerHandler is built without a MuxOption, the HTTP/3 server pins
	// its QUIC versions and windows, and the entry has no udp switch of its own.
	for _, unsupported := range []string{"udp", "mux-option", "allow-insecure", "alpn", "quic-versions", "zero-rtt", "max-idle-time", "recv-window", "token", "key"} {
		if _, ok := config[unsupported]; ok {
			t.Fatalf("trusttunnel listener must not carry %q: %#v", unsupported, config)
		}
	}
}

// An empty network list creates no socket at all — silently — so the renderer
// always states one and the panel defaults to tcp.
func TestTrustTunnelAlwaysStatesItsNetwork(t *testing.T) {
	config := map[string]any{}
	applyTrustTunnelConfig(config, TrustTunnelSettings{})
	if networks, ok := config["network"].([]string); !ok || len(networks) != 1 || networks[0] != "tcp" {
		t.Fatalf("network = %#v, want [tcp]", config["network"])
	}
	for _, omitted := range []string{"congestion-controller", "cwnd", "bbr-profile"} {
		if _, ok := config[omitted]; ok {
			t.Fatalf("unset trusttunnel option %q must fall through to the quic-go default: %#v", omitted, config)
		}
	}
}

func TestTrustTunnelAcceptsSupportedConfigurations(t *testing.T) {
	for name, settings := range map[string]TrustTunnelSettings{
		"tcp only":     {Network: "tcp"},
		"udp only":     {Network: "udp"},
		"both":         {Network: "tcp,udp"},
		"quic cubic":   {Network: "udp", CongestionController: "cubic"},
		"quic bbr":     {Network: "tcp,udp", CongestionController: "bbr", CWND: 32, BBRProfile: "standard"},
		"quic meta v1": {Network: "udp", CongestionController: "bbr_meta_v1", CWND: 64},
	} {
		if err := validateInbound(trustTunnelInbound(settings)); err != nil {
			t.Fatalf("%s must be accepted: %v", name, err)
		}
	}
	// Every network combination the panel offers has to survive validation.
	for _, network := range trustTunnelNetworks {
		if err := validateInbound(trustTunnelInbound(TrustTunnelSettings{Network: network})); err != nil {
			t.Fatalf("network %q must be accepted: %v", network, err)
		}
	}
}

func TestTrustTunnelRejectsUnsupportedCombinations(t *testing.T) {
	for name, testCase := range map[string]struct {
		mutate func(*Inbound)
		want   string
	}{
		"no certificate":   {func(i *Inbound) { i.Certificate = "" }, "必须配置证书和私钥"},
		"no key":           {func(i *Inbound) { i.PrivateKey = "" }, "必须配置证书和私钥"},
		"no network":       {func(i *Inbound) { i.TrustTunnel.Network = "  " }, "必须选择监听的网络类型"},
		"unknown network":  {func(i *Inbound) { i.TrustTunnel.Network = "quic" }, "只支持 tcp 和 udp"},
		"unknown control":  {func(i *Inbound) { i.TrustTunnel.CongestionController = "brutal" }, "Congestion Controller"},
		"profile on cubic": {func(i *Inbound) { i.TrustTunnel.CongestionController, i.TrustTunnel.BBRProfile = "cubic", "standard" }, "BBR Profile 仅"},
		"bad profile":      {func(i *Inbound) { i.TrustTunnel.CongestionController, i.TrustTunnel.BBRProfile = "bbr", "turbo" }, "BBR Profile 无效"},
		"cwnd without bbr": {func(i *Inbound) { i.TrustTunnel.CWND = 32 }, "Init CWND 仅"},
		"tcp only control": {func(i *Inbound) { i.TrustTunnel.CongestionController = "bbr" }, "仅在监听 udp"},
		"no client":        {func(i *Inbound) { i.Clients = nil }, "至少需要一个启用的客户端"},
		"nameless client":  {func(i *Inbound) { i.Clients[0].Name, i.Clients[0].Username = "", "" }, "必须有用户名"},
		"mux":              {func(i *Inbound) { i.Mux.BrutalEnabled = true }, "没有 mux-option"},
		"allow insecure":   {func(i *Inbound) { i.AllowInsecure = true }, "没有 allow-insecure"},
		"shadow tls":       {func(i *Inbound) { i.ShadowTLS.Enabled = true }, "不支持 Reality"},
	} {
		inbound := trustTunnelInbound(TrustTunnelSettings{Network: "tcp"})
		testCase.mutate(&inbound)
		err := validateInbound(inbound)
		if err == nil || !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("%s error = %v, want %q", name, err, testCase.want)
		}
	}
}

func TestYAMLOrdersTrustTunnelListenerKeys(t *testing.T) {
	config, err := inboundConfig(trustTunnelInbound(TrustTunnelSettings{
		Network: "tcp,udp", CongestionController: "bbr", CWND: 48, BBRProfile: "standard",
	}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalYAML(map[string]any{"listeners": []any{config}})
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(data)
	for _, pair := range []struct{ first, second string }{
		{"name: \"tt-a\"", "type: \"trusttunnel\""},
		{"type: \"trusttunnel\"", "listen: \"0.0.0.0\""},
		{"listen: \"0.0.0.0\"", "port: 18494"},
		{"port: 18494", "network:"},
		{"network:", "- \"tcp\""},
		{"- \"udp\"", "users:"},
		{"users:", "- username: \"one\""},
		{"password: \"tt-secret\"", "certificate: \"/etc/ssl/tt.crt\""},
		{"certificate: \"/etc/ssl/tt.crt\"", "private-key: \"/etc/ssl/tt.key\""},
		{"private-key: \"/etc/ssl/tt.key\"", "congestion-controller: \"bbr\""},
		{"congestion-controller: \"bbr\"", "cwnd: 48"},
		{"cwnd: 48", "bbr-profile: \"standard\""},
	} {
		if strings.Index(rendered, pair.first) >= strings.Index(rendered, pair.second) {
			t.Fatalf("expected %q before %q:\n%s", pair.first, pair.second, rendered)
		}
	}
}

// The three listeners added last are the only ones whose enums Mihomo either
// silently ignores (an unknown trusttunnel network creates no socket) or reports
// only per connection, so the drawer has to spell them exactly like the kernel.
func TestPanelOffersEveryChoiceForTheRendezvousAndQUICListeners(t *testing.T) {
	page, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(page)
	choices := append([]string{}, shadowQuicVersions...)
	choices = append(choices, trustTunnelNetworks...)
	choices = append(choices, quicCongestionControllers...)
	choices = append(choices, "conservative", "standard", "aggressive")
	for _, choice := range choices {
		if !strings.Contains(rendered, `<option value="`+choice+`">`) {
			t.Fatalf("web/index.html must offer the %q option", choice)
		}
	}
	// A cleared Realm Name Pattern means "no restriction", but Mihomo pre-fills its
	// own pattern, so the drawer has to be able to put the kernel's spelling back.
	if !strings.Contains(rendered, hysteria2RealmDefaultNamePattern) {
		t.Fatalf("web/index.html must carry Mihomo's realm name pattern %q", hysteria2RealmDefaultNamePattern)
	}
	// Omitting the key is not quic-go's default everywhere: listener/parse.go
	// pre-fills bbr for tuic and shadowquic but hands trusttunnel a bare option
	// struct, and the drawer must not claim otherwise.
	for _, needle := range []string{
		`<option value="" data-i18n="proto.defaultBbr">Default (bbr)</option>`,
		`<option value="" data-i18n="proto.defaultBbr">默认（bbr）</option>`,
		`<option value="" data-i18n="proto.defaultQuicGo">默认（quic-go）</option>`,
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("web/index.html must contain %q", needle)
		}
	}
	// Those labels are translated at runtime now, so the kernel's own spelling
	// has to survive in both halves of the dictionary as well.
	dictionary, err := os.ReadFile("web/i18n.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{
		`'proto.defaultBbr': '默认（bbr）'`,
		`'proto.defaultBbr': 'Default (bbr)'`,
		`'proto.defaultQuicGo': '默认（quic-go）'`,
		`'proto.defaultQuicGo': 'Default (quic-go)'`,
	} {
		if !strings.Contains(string(dictionary), entry) {
			t.Fatalf("web/i18n.js must keep the kernel's spelling: %s", entry)
		}
	}
}

// tlsMirrorTestKey decodes to exactly 32 bytes, which is what
// transport/tlsmirror.DecodePrimaryKey insists on.
var tlsMirrorTestKey = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

func tlsMirrorInbound(settings TLSMirrorSettings) Inbound {
	return Inbound{
		Name: "mirror-a", Type: "vmess", Listen: "0.0.0.0", Port: 18496, Enabled: true,
		Clients:   []Client{{Name: "one", UUID: "b0f5f0f6-1c2d-4a9b-8f4e-2f5a6b7c8d9e", Enabled: true}},
		TLSMirror: settings,
	}
}

func tlsMirrorBlock(t *testing.T, inbound Inbound) map[string]any {
	t.Helper()
	config, err := inboundConfig(inbound)
	if err != nil {
		t.Fatal(err)
	}
	block, ok := config["tlsmirror-config"].(map[string]any)
	if !ok {
		t.Fatalf("vmess listener has no tlsmirror-config block: %#v", config)
	}
	return block
}

func TestTLSMirrorListenerRendersEveryMihomoOption(t *testing.T) {
	block := tlsMirrorBlock(t, tlsMirrorInbound(TLSMirrorSettings{
		Enabled: true, PrimaryKey: " " + tlsMirrorTestKey + " ", Dest: " www.gstatic.com:443 ",
		Proxy: " upstream ", ExplicitNonceCipherSuites: " 156, 157 49195 ",
		DeferBaseNanoseconds: 1500, DeferRandomNanoseconds: 700,
		TransportLayerPadding: true, ConnectionEnrolment: true, SequenceWatermarkingEnabled: true,
	}))
	for key, want := range map[string]any{
		"primary-key": tlsMirrorTestKey, "dest": "www.gstatic.com:443",
		"proxy": "upstream", "sequence-watermarking-enabled": true,
	} {
		if got := block[key]; got != want {
			t.Fatalf("tlsmirror-config %s = %#v, want %#v", key, got, want)
		}
	}
	// The kernel decodes the suites into []uint16, so they have to leave the panel
	// as numbers rather than as the string the drawer collects.
	suites, ok := block["explicit-nonce-ciphersuites"].([]any)
	if !ok || len(suites) != 3 || suites[0] != 156 || suites[1] != 157 || suites[2] != 49195 {
		t.Fatalf("explicit-nonce-ciphersuites = %#v, want 156/157/49195 as numbers", block["explicit-nonce-ciphersuites"])
	}
	writeTime, ok := block["defer-instance-derived-write-time"].(map[string]any)
	if !ok || writeTime["base-nanoseconds"] != uint64(1500) || writeTime["uniform-random-multiplier-nanoseconds"] != uint64(700) {
		t.Fatalf("defer-instance-derived-write-time = %#v, want both nanosecond values", block["defer-instance-derived-write-time"])
	}
	padding, ok := block["transport-layer-padding"].(map[string]any)
	if !ok || padding["enabled"] != true {
		t.Fatalf("transport-layer-padding = %#v, want the nested enabled switch", block["transport-layer-padding"])
	}
	// server.go only nil-checks connection-enrolment: its two members are read by
	// the client half, so an empty mapping is the entire switch.
	enrolment, ok := block["connection-enrolment"].(map[string]any)
	if !ok || len(enrolment) != 0 {
		t.Fatalf("connection-enrolment = %#v, want an empty mapping", block["connection-enrolment"])
	}
	if len(block) != 8 {
		t.Fatalf("tlsmirror-config has %d keys, want the 8 the kernel reads: %#v", len(block), block)
	}
}

// The primary key and the carrier address are the only unconditional keys: the
// key is how securityModes decides tlsmirror is on at all, and listener/tlsmirror
// hands every accepted connection to inner.HandleTcp with dest, so a missing one
// kills each connection at runtime instead of at startup.
func TestTLSMirrorOmitsUnsetOptionsButStatesTheCarrier(t *testing.T) {
	block := tlsMirrorBlock(t, tlsMirrorInbound(TLSMirrorSettings{
		Enabled: true, PrimaryKey: tlsMirrorTestKey, Dest: "www.gstatic.com:443",
	}))
	if block["primary-key"] != tlsMirrorTestKey || block["dest"] != "www.gstatic.com:443" {
		t.Fatalf("tlsmirror-config = %#v, want the primary key and the carrier", block)
	}
	if len(block) != 2 {
		t.Fatalf("tlsmirror-config = %#v, want only primary-key/dest", block)
	}
	// One of the two nanosecond values on its own still has to reach the kernel.
	deferred := tlsMirrorBlock(t, tlsMirrorInbound(TLSMirrorSettings{
		Enabled: true, PrimaryKey: tlsMirrorTestKey, Dest: "www.gstatic.com:443", DeferRandomNanoseconds: 700,
	}))
	writeTime, ok := deferred["defer-instance-derived-write-time"].(map[string]any)
	if !ok || len(writeTime) != 1 || writeTime["uniform-random-multiplier-nanoseconds"] != uint64(700) {
		t.Fatalf("defer-instance-derived-write-time = %#v, want just the random multiplier", deferred["defer-instance-derived-write-time"])
	}
}

func TestTLSMirrorOnlyRendersForVmess(t *testing.T) {
	inbound := tlsMirrorInbound(TLSMirrorSettings{Enabled: true, PrimaryKey: tlsMirrorTestKey, Dest: "www.gstatic.com:443"})
	inbound.Type = "vless"
	config, err := inboundConfig(inbound)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := config["tlsmirror-config"]; ok {
		t.Fatalf("only the vmess listener declares tlsmirror-config: %#v", config)
	}
	err = validateInbound(inbound)
	if err == nil || !strings.Contains(err.Error(), "TLSMirror 不支持") {
		t.Fatalf("validateInbound = %v, want it to reject tlsmirror outside vmess", err)
	}
}

// tlsmirror never sets tcpOnlySecurityMode in sing_vmess, so unlike ShadowTLS it
// coexists with every VMess transport and the panel must not pretend otherwise.
func TestTLSMirrorAcceptsSupportedConfigurations(t *testing.T) {
	suites := make([]string, 0, len(tlsMirrorRecommendedCipherSuites))
	for _, suite := range tlsMirrorRecommendedCipherSuites {
		suites = append(suites, strconv.Itoa(suite))
	}
	for name, mutate := range map[string]func(*Inbound){
		"plain":              func(i *Inbound) {},
		"proxy":              func(i *Inbound) { i.TLSMirror.Proxy = "upstream" },
		"ipv6 carrier":       func(i *Inbound) { i.TLSMirror.Dest = "[2001:db8::1]:443" },
		"recommended suites": func(i *Inbound) { i.TLSMirror.ExplicitNonceCipherSuites = strings.Join(suites, ",") },
		"zero suite":         func(i *Inbound) { i.TLSMirror.ExplicitNonceCipherSuites = "0" },
		"every switch": func(i *Inbound) {
			i.TLSMirror.TransportLayerPadding, i.TLSMirror.ConnectionEnrolment, i.TLSMirror.SequenceWatermarkingEnabled = true, true, true
		},
		"websocket":          func(i *Inbound) { i.Network, i.WSPath = "ws", "/mirror" },
		"grpc":               func(i *Inbound) { i.Network, i.GRPCServiceName = "grpc", "mirror" },
		"mkcp":               func(i *Inbound) { i.Network, i.MKCP = "mkcp", MKCPSettings{Enabled: true, Header: "srtp"} },
		"udp":                func(i *Inbound) { i.UDP = true },
		"panel-only sni":     func(i *Inbound) { i.TLSServerName = "www.gstatic.com" },
		"mux without brutal": func(i *Inbound) { i.Mux = MuxSettings{Padding: true} },
		"routing":            func(i *Inbound) { i.Proxy, i.RoutingMark = "upstream", 255 },
	} {
		inbound := tlsMirrorInbound(TLSMirrorSettings{Enabled: true, PrimaryKey: tlsMirrorTestKey, Dest: "www.gstatic.com:443"})
		mutate(&inbound)
		if err := validateInbound(inbound); err != nil {
			t.Fatalf("%s: validateInbound = %v, want nil", name, err)
		}
	}
}

func TestTLSMirrorRejectsUnsupportedCombinations(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		mutate  func(*Inbound)
		wantErr string
	}{
		{"missing key", func(i *Inbound) { i.TLSMirror.PrimaryKey = "   " }, "必须配置 Primary Key"},
		{"not base64", func(i *Inbound) { i.TLSMirror.PrimaryKey = "not-base64!!" }, "标准 base64"},
		{"short key", func(i *Inbound) { i.TLSMirror.PrimaryKey = base64.StdEncoding.EncodeToString([]byte("too short")) }, "32 字节"},
		{"missing dest", func(i *Inbound) { i.TLSMirror.Dest = " " }, "载体地址"},
		{"dest without port", func(i *Inbound) { i.TLSMirror.Dest = "www.gstatic.com" }, "host:port"},
		{"dest port zero", func(i *Inbound) { i.TLSMirror.Dest = "www.gstatic.com:0" }, "端口无效"},
		{"dest port name", func(i *Inbound) { i.TLSMirror.Dest = "www.gstatic.com:https" }, "端口无效"},
		{"suite not a number", func(i *Inbound) { i.TLSMirror.ExplicitNonceCipherSuites = "156,TLS_AES_128_GCM_SHA256" }, "0-65535"},
		{"suite out of range", func(i *Inbound) { i.TLSMirror.ExplicitNonceCipherSuites = "65536" }, "0-65535"},
		{"certificate", func(i *Inbound) { i.Certificate, i.PrivateKey = "/etc/ssl/a.crt", "/etc/ssl/a.key" }, "不能同时配置证书和私钥"},
		{"tls", func(i *Inbound) { i.TLS = true }, "TLS"},
		{"reality", func(i *Inbound) { i.Reality.Enabled = true }, "互斥"},
		{"shadow-tls", func(i *Inbound) { i.ShadowTLS.Enabled = true }, "互斥"},
		{"restls", func(i *Inbound) { i.RestTLS.Enabled = true }, "互斥"},
		{"jls", func(i *Inbound) {
			i.JLS = JLSSettings{Enabled: true, Dest: "www.gstatic.com:443", Username: "jls", Password: "jls-pw"}
		}, "互斥"},
		{"wrong protocol", func(i *Inbound) {
			i.Type = "trojan"
			i.Clients[0].Password = "trojan-secret"
		}, "TLSMirror 不支持"},
	} {
		inbound := tlsMirrorInbound(TLSMirrorSettings{Enabled: true, PrimaryKey: tlsMirrorTestKey, Dest: "www.gstatic.com:443"})
		testCase.mutate(&inbound)
		err := validateInbound(inbound)
		if err == nil {
			t.Fatalf("%s: validateInbound = nil, want an error", testCase.name)
		}
		if !strings.Contains(err.Error(), testCase.wantErr) {
			t.Fatalf("%s: validateInbound = %v, want it to mention %q", testCase.name, err, testCase.wantErr)
		}
	}
}

func TestYAMLOrdersTLSMirrorListenerKeys(t *testing.T) {
	inbound := tlsMirrorInbound(TLSMirrorSettings{
		Enabled: true, PrimaryKey: tlsMirrorTestKey, Dest: "www.gstatic.com:443",
		Proxy: "upstream", ExplicitNonceCipherSuites: "156,157",
		DeferBaseNanoseconds: 1500, DeferRandomNanoseconds: 700,
		TransportLayerPadding: true, ConnectionEnrolment: true, SequenceWatermarkingEnabled: true,
	})
	inbound.Network, inbound.WSPath = "ws", "/mirror"
	config, err := inboundConfig(inbound)
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalYAML(map[string]any{"listeners": []any{config}})
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(data)
	for _, pair := range []struct{ first, second string }{
		{"ws-path: \"/mirror\"", "tlsmirror-config:"},
		{"tlsmirror-config:", "primary-key: \"" + tlsMirrorTestKey + "\""},
		{"primary-key: \"" + tlsMirrorTestKey + "\"", "dest: \"www.gstatic.com:443\""},
		{"dest: \"www.gstatic.com:443\"", "proxy: \"upstream\""},
		{"proxy: \"upstream\"", "explicit-nonce-ciphersuites:"},
		{"explicit-nonce-ciphersuites:", "- 156"},
		{"- 156", "- 157"},
		{"- 157", "defer-instance-derived-write-time:"},
		{"defer-instance-derived-write-time:", "base-nanoseconds: 1500"},
		{"base-nanoseconds: 1500", "uniform-random-multiplier-nanoseconds: 700"},
		{"uniform-random-multiplier-nanoseconds: 700", "transport-layer-padding:"},
		{"transport-layer-padding:", "enabled: true"},
		{"enabled: true", "connection-enrolment: {}"},
		{"connection-enrolment: {}", "sequence-watermarking-enabled: true"},
	} {
		if strings.Index(rendered, pair.first) >= strings.Index(rendered, pair.second) {
			t.Fatalf("expected %q before %q:\n%s", pair.first, pair.second, rendered)
		}
	}
}

// tlsmirror is a VMess-only security mode, and the v2rayN base64 payload has no
// field that could carry it, so the drawer has to offer it to VMess alone, drop it
// when the protocol changes underneath, and refuse to build a share link.
func TestPanelOffersTLSMirrorToVmessOnly(t *testing.T) {
	page, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`if(type==='vmess')return['none','tls','reality','tlsmirror','shadowtls','restls','jls'];`,
		`if(type==='vless')return['none','tls','reality','shadowtls','restls','jls'];`,
		`<option value="tlsmirror">TLSMirror</option>`,
		`if(security==='tlsmirror'&&type!=='vmess')security='none';`,
		`showFields('.security-tlsmirror',security==='tlsmirror');`,
		`inbound.type==='vmess'&&inbound.tlsMirror?.enabled`,
		// DecodePrimaryKey wants standard base64, so the generator must keep +/= .
		`function randomBase64Key(length=32){const bytes=new Uint8Array(length);crypto.getRandomValues(bytes);return btoa(String.fromCharCode(...bytes));}`,
		`attachInputTool(primaryKey,'tlsmirror-regenerate-key'`,
	} {
		if !strings.Contains(string(page), needle) {
			t.Fatalf("web/index.html must contain %q", needle)
		}
	}
}

// Nothing in the kernel applies the recommended list on its own, so the drawer
// carries a one-click filler; this pins that copy to the backend's list.
func TestPanelPinsTLSMirrorRecommendedCipherSuites(t *testing.T) {
	page, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	suites := make([]string, 0, len(tlsMirrorRecommendedCipherSuites))
	for _, suite := range tlsMirrorRecommendedCipherSuites {
		suites = append(suites, strconv.Itoa(suite))
	}
	needle := `const TLSMIRROR_RECOMMENDED_CIPHERSUITES = '` + strings.Join(suites, ",") + `'`
	if !strings.Contains(string(page), needle) {
		t.Fatalf("web/index.html must carry the kernel's recommended suites verbatim: %s", needle)
	}
}

func hysteriaRealmBlock(t *testing.T, settings HysteriaRealmOptions) map[string]any {
	t.Helper()
	config, err := inboundConfig(hysteria2Inbound(HysteriaSettings{Realm: settings}))
	if err != nil {
		t.Fatal(err)
	}
	block, ok := config["realm-opts"].(map[string]any)
	if !ok {
		t.Fatalf("hysteria2 listener has no realm-opts block: %#v", config)
	}
	return block
}

func TestHysteriaRealmOptsRenderEveryMihomoOption(t *testing.T) {
	block := hysteriaRealmBlock(t, HysteriaRealmOptions{
		Enabled: true, ServerURL: " https://realm.example.com:8443 ", Token: " realm-token ",
		RealmID: " realm-one ", STUNServers: " stun.example.com:3478, stun2.example.com:19302 ",
		SNI: " realm.example.com ", SkipCertVerify: true, NameCertVerify: " pinned.example.com ",
		Fingerprint: " " + strings.Repeat("ab", 32) + " ", Certificate: " /etc/ssl/realm.crt ", PrivateKey: " /etc/ssl/realm.key ",
		ALPN: " h3, h2 ", Proxy: " realm-upstream ",
	})
	for key, want := range map[string]any{
		"enable": true, "server-url": "https://realm.example.com:8443", "token": "realm-token",
		"realm-id": "realm-one", "sni": "realm.example.com", "skip-cert-verify": true,
		"name-cert-verify": "pinned.example.com", "fingerprint": strings.Repeat("ab", 32),
		"certificate": "/etc/ssl/realm.crt", "private-key": "/etc/ssl/realm.key", "proxy": "realm-upstream",
	} {
		if got := block[key]; got != want {
			t.Fatalf("realm-opts %s = %#v, want %#v", key, got, want)
		}
	}
	for key, want := range map[string][]string{
		"stun-servers": {"stun.example.com:3478", "stun2.example.com:19302"},
		"alpn":         {"h3", "h2"},
	} {
		got, ok := block[key].([]string)
		if !ok || len(got) != len(want) || got[0] != want[0] || got[len(got)-1] != want[len(want)-1] {
			t.Fatalf("realm-opts %s = %#v, want %#v", key, block[key], want)
		}
	}
	if len(block) != 13 {
		t.Fatalf("realm-opts has %d keys, want the 13 the kernel reads: %#v", len(block), block)
	}
}

// `enable` is the only gate the kernel reads, and the rendezvous identity is
// useless in pieces, so those four are always written; listener/parse.go hands
// hysteria2 a bare option struct, so everything else can stay out at zero.
func TestHysteriaRealmOptsOmitUnsetOptionsButStateTheirIdentity(t *testing.T) {
	block := hysteriaRealmBlock(t, HysteriaRealmOptions{
		Enabled: true, ServerURL: "https://realm.example.com", Token: "realm-token", RealmID: "realm-one",
	})
	for key, want := range map[string]any{
		"enable": true, "server-url": "https://realm.example.com", "token": "realm-token", "realm-id": "realm-one",
	} {
		if got := block[key]; got != want {
			t.Fatalf("realm-opts %s = %#v, want %#v", key, got, want)
		}
	}
	if len(block) != 4 {
		t.Fatalf("realm-opts = %#v, want only enable/server-url/token/realm-id", block)
	}
}

// The realm client is a plain extra of the hysteria2 listener: with the switch off
// the whole block has to disappear rather than be rendered as enable: false, and
// nothing else about the listener may change.
func TestHysteriaRealmOptsOnlyRenderWhenEnabled(t *testing.T) {
	config, err := inboundConfig(hysteria2Inbound(HysteriaSettings{
		Up: "100 Mbps", Realm: HysteriaRealmOptions{
			ServerURL: "https://realm.example.com", Token: "realm-token", RealmID: "realm-one",
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := config["realm-opts"]; ok {
		t.Fatalf("realm-opts must stay out while the switch is off: %#v", config)
	}
	if config["up"] != "100 Mbps" {
		t.Fatalf("up = %#v, want the listener untouched", config["up"])
	}
}

func TestHysteriaRealmOptsAcceptSupportedConfigurations(t *testing.T) {
	for name, mutate := range map[string]func(*HysteriaRealmOptions){
		"plain":           func(r *HysteriaRealmOptions) {},
		"http rendezvous": func(r *HysteriaRealmOptions) { r.ServerURL = "http://realm.example.com:8080" },
		"path and query":  func(r *HysteriaRealmOptions) { r.ServerURL = "https://realm.example.com/api?v=1" },
		"stun servers":    func(r *HysteriaRealmOptions) { r.STUNServers = "stun.example.com:3478 stun2.example.com:19302" },
		"ipv6 stun":       func(r *HysteriaRealmOptions) { r.STUNServers = "[2001:db8::1]:3478" },
		"pinned cert":     func(r *HysteriaRealmOptions) { r.Fingerprint = strings.ToUpper(strings.Repeat("ab", 32)) },
		"colon fingerprint": func(r *HysteriaRealmOptions) {
			r.Fingerprint = strings.TrimSuffix(strings.Repeat("ab:", 32), ":")
		},
		"client certificate": func(r *HysteriaRealmOptions) {
			r.Certificate, r.PrivateKey = "/etc/ssl/realm.crt", "/etc/ssl/realm.key"
		},
		"skip verify": func(r *HysteriaRealmOptions) { r.SkipCertVerify = true },
		"retargeted name": func(r *HysteriaRealmOptions) {
			r.SNI, r.NameCertVerify = "realm.example.com", "pinned.example.com"
		},
		"alpn":  func(r *HysteriaRealmOptions) { r.ALPN = "h2,http/1.1" },
		"proxy": func(r *HysteriaRealmOptions) { r.Proxy = "realm-upstream" },
	} {
		realm := HysteriaRealmOptions{
			Enabled: true, ServerURL: "https://realm.example.com", Token: "realm-token", RealmID: "realm-one",
		}
		mutate(&realm)
		if err := validateInbound(hysteria2Inbound(HysteriaSettings{Realm: realm})); err != nil {
			t.Fatalf("%s: validateInbound = %v, want nil", name, err)
		}
	}
}

func TestHysteriaRealmOptsRejectUnsupportedCombinations(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		mutate  func(*HysteriaRealmOptions)
		wantErr string
	}{
		{"no server url", func(r *HysteriaRealmOptions) { r.ServerURL = "  " }, "必须配置 Server URL"},
		{"bare host", func(r *HysteriaRealmOptions) { r.ServerURL = "realm.example.com" }, "http:// 或 https://"},
		{"wrong scheme", func(r *HysteriaRealmOptions) { r.ServerURL = "quic://realm.example.com" }, "http:// 或 https://"},
		{"no token", func(r *HysteriaRealmOptions) { r.Token = " " }, "必须配置 Token"},
		{"no realm id", func(r *HysteriaRealmOptions) { r.RealmID = " " }, "必须配置 Realm ID"},
		{"stun without port", func(r *HysteriaRealmOptions) { r.STUNServers = "stun.example.com" }, "host:port"},
		{"stun port zero", func(r *HysteriaRealmOptions) { r.STUNServers = "stun.example.com:0" }, "端口无效"},
		{"browser fingerprint", func(r *HysteriaRealmOptions) { r.Fingerprint = "chrome" }, "不能填浏览器指纹名"},
		{"fingerprint not hex", func(r *HysteriaRealmOptions) { r.Fingerprint = "zz:" + strings.Repeat("ab", 31) }, "十六进制"},
		{"short fingerprint", func(r *HysteriaRealmOptions) { r.Fingerprint = strings.Repeat("ab", 16) }, "32 字节"},
		{"certificate alone", func(r *HysteriaRealmOptions) { r.Certificate = "/etc/ssl/realm.crt" }, "必须同时配置"},
		{"private key alone", func(r *HysteriaRealmOptions) { r.PrivateKey = "/etc/ssl/realm.key" }, "必须同时配置"},
		{"sni with port", func(r *HysteriaRealmOptions) { r.SNI = "realm.example.com:8443" }, "SNI 只能填写域名"},
		{"name with url", func(r *HysteriaRealmOptions) { r.NameCertVerify = "https://pinned.example.com" }, "Name Cert Verify 只能填写域名"},
	} {
		realm := HysteriaRealmOptions{
			Enabled: true, ServerURL: "https://realm.example.com", Token: "realm-token", RealmID: "realm-one",
		}
		testCase.mutate(&realm)
		err := validateInbound(hysteria2Inbound(HysteriaSettings{Realm: realm}))
		if err == nil {
			t.Fatalf("%s: validateInbound = nil, want an error", testCase.name)
		}
		if !strings.Contains(err.Error(), testCase.wantErr) {
			t.Fatalf("%s: validateInbound = %v, want it to mention %q", testCase.name, err, testCase.wantErr)
		}
	}
}

func TestYAMLOrdersHysteriaRealmOptsKeys(t *testing.T) {
	config, err := inboundConfig(hysteria2Inbound(HysteriaSettings{
		ALPN: "h3", UdpMTU: 1200,
		Realm: HysteriaRealmOptions{
			Enabled: true, ServerURL: "https://realm.example.com:8443", Token: "realm-token",
			RealmID: "realm-one", STUNServers: "stun.example.com:3478", SNI: "realm.example.com",
			SkipCertVerify: true, NameCertVerify: "pinned.example.com", Fingerprint: strings.Repeat("ab", 32),
			Certificate: "/etc/ssl/realm.crt", PrivateKey: "/etc/ssl/realm.key", ALPN: "h2", Proxy: "realm-upstream",
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalYAML(map[string]any{"listeners": []any{config}})
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(data)
	for _, pair := range []struct{ first, second string }{
		{"udp-mtu: 1200", "realm-opts:"},
		{"realm-opts:", "enable: true"},
		{"enable: true", "server-url: \"https://realm.example.com:8443\""},
		{"server-url: \"https://realm.example.com:8443\"", "token: \"realm-token\""},
		{"token: \"realm-token\"", "realm-id: \"realm-one\""},
		{"realm-id: \"realm-one\"", "stun-servers:"},
		{"stun-servers:", "- \"stun.example.com:3478\""},
		{"- \"stun.example.com:3478\"", "sni: \"realm.example.com\""},
		{"sni: \"realm.example.com\"", "skip-cert-verify: true"},
		{"skip-cert-verify: true", "name-cert-verify: \"pinned.example.com\""},
		{"name-cert-verify: \"pinned.example.com\"", "fingerprint: \"" + strings.Repeat("ab", 32) + "\""},
		{"fingerprint: \"" + strings.Repeat("ab", 32) + "\"", "certificate: \"/etc/ssl/realm.crt\""},
		{"certificate: \"/etc/ssl/realm.crt\"", "private-key: \"/etc/ssl/realm.key\""},
		{"private-key: \"/etc/ssl/realm.key\"", "- \"h2\""},
		{"- \"h2\"", "proxy: \"realm-upstream\""},
	} {
		if strings.Index(rendered, pair.first) >= strings.Index(rendered, pair.second) {
			t.Fatalf("expected %q before %q:\n%s", pair.first, pair.second, rendered)
		}
	}
}

// common/convert rebuilds server-url as "https://"+URL.Host, so a rendezvous that
// is not a plain https origin cannot be expressed in a hysteria2+realm:// link at
// all - the drawer has to fall back to the ordinary hysteria2:// link instead of
// emitting a URI that would silently point the client somewhere else.
func TestPanelBuildsRealmShareLinksOnlyWhenTheyRoundTrip(t *testing.T) {
	page, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`if(parsed.protocol!=='https:'||parsed.username||parsed.password||parsed.search||parsed.hash)return'';`,
		`if(parsed.pathname&&parsed.pathname!=='/')return'';return parsed.host;}`,
		`realmTarget=realm.enabled?realmShareTarget(realm.serverUrl):''`,
		// userinfo carries the realm token, ?auth= carries the hysteria2 password.
		`params.set('auth',password);for(const stun of splitShareList(realm.stunServers))params.append('stun',stun);`,
		"hysteria2+realm://${encodeURIComponent(String(realm.token||'').trim())}@${realmTarget}/${encodeURIComponent(realmId)}",
		`showFields('.protocol-hysteria-realm',hysteria&&!!form.elements.hysteriaRealmEnabled?.checked);`,
		`form.elements.hysteriaRealmEnabled.onchange=syncHysteriaUI;`,
	} {
		if !strings.Contains(string(page), needle) {
			t.Fatalf("web/index.html must contain %q", needle)
		}
	}
}

func TestHostPortFromAddress(t *testing.T) {
	for address, want := range map[string]struct {
		host string
		port int
	}{
		"0.0.0.0:2053":     {"0.0.0.0", 2053},
		"127.0.0.1:9093":   {"127.0.0.1", 9093},
		" 127.0.0.1:9093 ": {"127.0.0.1", 9093},
		":2053":            {"", 2053},
		"2053":             {"", 2053},
		"[::1]:80":         {"::1", 80},
		"":                 {"", 0},
		"not-an-address":   {"", 0},
		"0.0.0.0:0":        {"", 0},
		"0.0.0.0:70000":    {"", 0},
	} {
		host, port := hostPortFromAddress(address)
		if host != want.host || port != want.port {
			t.Fatalf("hostPortFromAddress(%q) = (%q, %d), want (%q, %d)", address, host, port, want.host, want.port)
		}
	}
}

// A duplicate listener name is not a per-inbound problem: parseListeners keys
// listeners by name and aborts the whole config, so nothing binds at all.
func TestSaveInboundRejectsDuplicateName(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir(), state: State{Settings: Settings{Username: "admin", Password: "hash"}}}
	first := Inbound{Name: "same-name", Type: "socks", Listen: "0.0.0.0", Port: 18801, Enabled: true,
		Clients: []Client{{Name: "one", Username: "one", Password: "pw-one", Enabled: true}}}
	if err := m.saveInbound(first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID = ""
	second.Port = 18802
	err := m.saveInbound(second)
	if err == nil || !strings.Contains(err.Error(), "已被占用") {
		t.Fatalf("duplicate name error = %v, want a rejection", err)
	}
	if len(m.state.Inbounds) != 1 {
		t.Fatalf("inbound count = %d, want the duplicate to be dropped", len(m.state.Inbounds))
	}
	saved := m.state.Inbounds[0]
	saved.Port = 18803
	if err := m.saveInbound(saved); err != nil {
		t.Fatalf("re-saving an inbound under its own name must stay allowed: %v", err)
	}
}

// mihomo -t cannot see these collisions and the core only logs "listen err" for
// the losing socket, so the panel has to refuse them up front.
func TestSaveInboundRejectsPanelAndCoreAPIPorts(t *testing.T) {
	settings := Settings{Username: "admin", Password: "hash", PanelListen: "0.0.0.0:2053", APIAddress: "127.0.0.1:9093"}
	inbound := func(name, listen string, port int) Inbound {
		return Inbound{Name: name, Type: "socks", Listen: listen, Port: port, Enabled: true,
			Clients: []Client{{Name: name, Username: name, Password: "pw", Enabled: true}}}
	}
	for name, testCase := range map[string]struct {
		in      Inbound
		wantErr string
	}{
		"panel port on wildcard":  {inbound("panel-clash", "0.0.0.0", 2053), "面板监听地址"},
		"panel port on interface": {inbound("panel-clash", "127.0.0.1", 2053), "面板监听地址"},
		"core api port":           {inbound("api-clash", "127.0.0.1", 9093), "Mihomo API 地址"},
		"core api via wildcard":   {inbound("api-clash", "0.0.0.0", 9093), "Mihomo API 地址"},
	} {
		m := &CoreManager{dataDir: t.TempDir(), state: State{Settings: settings}}
		err := m.saveInbound(testCase.in)
		if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
			t.Fatalf("%s: error = %v, want one mentioning %q", name, err, testCase.wantErr)
		}
	}
	m := &CoreManager{dataDir: t.TempDir(), state: State{Settings: settings}}
	if err := m.saveInbound(inbound("other-interface", "127.0.0.2", 9093)); err != nil {
		t.Fatalf("a different interface on the API port must stay allowed: %v", err)
	}
}

// clientMap/tuic render `users:` as a map, so a duplicate identity silently
// swallows a client instead of failing; catch it while the operator is looking.
func TestValidateInboundRejectsDuplicateClientIdentities(t *testing.T) {
	client := func(name, uuid string) Client {
		return Client{ID: name, Name: name, Username: name, Password: "pw-" + name, UUID: uuid, Enabled: true}
	}
	for name, testCase := range map[string]struct {
		in      Inbound
		wantErr string
	}{
		"hysteria2 duplicate name": {Inbound{Name: "hy", Type: "hysteria2", Port: 1, TLS: true, Certificate: "cert.pem", PrivateKey: "key.pem",
			Clients: []Client{client("dup", ""), client("dup", "")}}, "客户端名称"},
		"trojan duplicate name": {Inbound{Name: "tj", Type: "trojan", Port: 1, TLS: true, Certificate: "cert.pem", PrivateKey: "key.pem",
			Clients: []Client{client("dup", ""), client("dup", "")}}, "客户端名称"},
		"disabled duplicate still rejected": {Inbound{Name: "hy", Type: "hysteria2", Port: 1, TLS: true, Certificate: "cert.pem", PrivateKey: "key.pem",
			Clients: []Client{client("dup", ""), {ID: "off", Name: "dup", Username: "dup", Password: "pw", Enabled: false}}}, "客户端名称"},
		"tuic duplicate uuid": {Inbound{Name: "tu", Type: "tuic", Port: 1, TLS: true, Certificate: "cert.pem", PrivateKey: "key.pem",
			Clients: []Client{client("one", "11111111-2222-3333-4444-555555555555"), client("two", "11111111-2222-3333-4444-555555555555")}}, "客户端 UUID"},
		"vmess duplicate uuid": {Inbound{Name: "vm", Type: "vmess", Port: 1,
			Clients: []Client{client("one", "11111111-2222-3333-4444-555555555555"), client("two", "11111111-2222-3333-4444-555555555555")}}, "客户端 UUID"},
	} {
		err := validateInbound(testCase.in)
		if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
			t.Fatalf("%s: error = %v, want one mentioning %q", name, err, testCase.wantErr)
		}
	}
	distinct := Inbound{Name: "hy", Type: "hysteria2", Port: 1, TLS: true, Certificate: "cert.pem", PrivateKey: "key.pem",
		Clients: []Client{client("one", ""), client("two", "")}}
	if err := validateInbound(distinct); err != nil {
		t.Fatalf("distinct clients must stay valid: %v", err)
	}
}

// The drawer posts the whole client list from a panel snapshot that may be
// seconds old; counters are the daemon's bookkeeping, never the request's.
func TestSaveInboundKeepsServerSideClientCounters(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir(), state: State{Settings: Settings{Username: "admin", Password: "hash"}}}
	inbound := Inbound{ID: "keep", Name: "counters", Type: "hysteria2", Listen: "0.0.0.0", Port: 18804, Enabled: true,
		TLS: true, Certificate: "cert.pem", PrivateKey: "key.pem",
		Clients: []Client{{ID: "client-one", Name: "one", Username: "one", Password: "pw-one", Enabled: true}}}
	if err := m.saveInbound(inbound); err != nil {
		t.Fatal(err)
	}
	m.state.Inbounds[0].Traffic = TrafficStats{Up: 11, Down: 22}
	m.state.Inbounds[0].Clients[0].Traffic = TrafficStats{Up: 700, Down: 800, AllTimeUp: 900, AllTimeDown: 1000}
	m.state.Inbounds[0].Clients[0].LastOnline = "2026-01-02T03:04:05Z"
	m.state.Inbounds[0].Clients[0].FirstOnline = "2026-01-01T00:00:00Z"
	stale := inbound
	stale.Clients = []Client{{ID: "client-one", Name: "one", Username: "one", Password: "pw-rotated", Enabled: true, Total: 4096}}
	if err := m.saveInbound(stale); err != nil {
		t.Fatal(err)
	}
	saved := m.state.Inbounds[0]
	if saved.Traffic != (TrafficStats{Up: 11, Down: 22}) {
		t.Fatalf("inbound traffic = %#v, want the stored counters", saved.Traffic)
	}
	client := saved.Clients[0]
	if client.Traffic != (TrafficStats{Up: 700, Down: 800, AllTimeUp: 900, AllTimeDown: 1000}) {
		t.Fatalf("client traffic = %#v, want the stored counters", client.Traffic)
	}
	if client.LastOnline != "2026-01-02T03:04:05Z" || client.FirstOnline != "2026-01-01T00:00:00Z" {
		t.Fatalf("online timestamps were overwritten: %#v", client)
	}
	if client.Password != "pw-rotated" || client.Total != 4096 {
		t.Fatalf("editable client fields were not applied: %#v", client)
	}
	fresh := inbound
	fresh.Clients = append(fresh.Clients, Client{ID: "client-two", Name: "two", Username: "two", Password: "pw-two", Enabled: true})
	if err := m.saveInbound(fresh); err != nil {
		t.Fatal(err)
	}
	if got := m.state.Inbounds[0].Clients[1]; got.Traffic != (TrafficStats{}) || got.Name != "two" {
		t.Fatalf("new client = %#v, want zeroed counters", got)
	}
}

func TestSaveClientRejectsDuplicateIdentity(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir(), state: State{Settings: Settings{Username: "admin", Password: "hash"},
		Inbounds: []Inbound{{ID: "hy", Name: "hy", Type: "hysteria2", Listen: "0.0.0.0", Port: 18805, Enabled: true,
			TLS: true, Certificate: "cert.pem", PrivateKey: "key.pem",
			Clients: []Client{{ID: "one", Name: "one", Username: "one", Password: "pw-one", Enabled: true}}}}}}
	err := m.saveClient("hy", Client{Name: "one", Username: "one", Password: "pw-two", Enabled: true})
	if err == nil || !strings.Contains(err.Error(), "客户端名称") {
		t.Fatalf("duplicate client error = %v, want a rejection", err)
	}
	if len(m.state.Inbounds[0].Clients) != 1 {
		t.Fatalf("client count = %d, want the duplicate to be dropped", len(m.state.Inbounds[0].Clients))
	}
	if err := m.saveClient("hy", Client{Name: "two", Username: "two", Password: "pw-two", Enabled: true}); err != nil {
		t.Fatalf("a distinct client must still be accepted: %v", err)
	}
	renamed := m.state.Inbounds[0].Clients[0]
	renamed.Username = "two"
	if err := m.saveClient("hy", renamed); err == nil || !strings.Contains(err.Error(), "客户端名称") {
		t.Fatalf("renaming onto an existing client error = %v, want a rejection", err)
	}
	kept := m.state.Inbounds[0].Clients[0]
	if kept.Username != "one" {
		t.Fatalf("rejected rename leaked into state: %#v", kept)
	}
}

func TestInboundCreationHardeningInWebAssets(t *testing.T) {
	page, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		// Clones must reset every server-owned field, exactly like importInboundFile.
		"clone.traffic={};clone.createdAt='';for(const client of clone.clients||[]){client.id='';client.traffic={};client.lastOnline='';client.firstOnline='';client.createdAt='';}",
		"clone.name=availableInboundName(`${item.name} copy`)",
		"function availableInboundName(preferred)",
		// The "default inbound port" setting is now what prefills a new inbound.
		"function preferredInboundPort()",
		"set('port',item?.port||preferredInboundPort());",
		// vmess:// must describe the transport it actually listens on.
		"const transport=inbound.mekya?.enabled?'mekya':inbound.mkcp?.enabled?'mkcp':inbound.grpcServiceName?'grpc':inbound.wsPath?'ws':inbound.network||'tcp';",
		"if(transport==='mekya')return'';",
		"if(transport==='grpc'){config.path=inbound.grpcServiceName||'';config.type='gun';}",
		"if(transport==='mkcp'){config.path=inbound.mkcp?.seed||'';config.type=inbound.mkcp?.header||'none';}",
		// ShadowTLS / RestTLS / JLS / Trojan-SS have no share-URI form at all.
		"||inbound.shadowTls?.enabled||inbound.restTls?.enabled||inbound.jls?.enabled||inbound.trojanSS?.enabled)return'';",
	} {
		if !strings.Contains(string(page), needle) {
			t.Fatalf("web/index.html must contain %q", needle)
		}
	}
}

// A state that predates the duplicate-name check must fail loudly in the panel
// rather than writing a config Mihomo refuses in full.
func TestRenderConfigRejectsDuplicateListenerNames(t *testing.T) {
	inbound := func(id string, port int, enabled bool) Inbound {
		return Inbound{ID: id, Name: "same-name", Type: "socks", Listen: "0.0.0.0", Port: port, Enabled: enabled,
			Clients: []Client{{ID: id, Name: id, Username: id, Password: "pw-" + id, Enabled: true}}}
	}
	m := &CoreManager{dataDir: t.TempDir(), state: State{Settings: Settings{Username: "admin", Password: "hash"},
		Inbounds: []Inbound{inbound("one", 18806, true), inbound("two", 18807, true)}}}
	if _, err := m.renderConfig(); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("renderConfig error = %v, want a duplicate-name rejection", err)
	}
	m.state.Inbounds[1].Enabled = false
	data, err := m.renderConfig()
	if err != nil {
		t.Fatalf("a disabled twin is not rendered, so it must not block: %v", err)
	}
	if strings.Count(string(data), "name: \"same-name\"") != 1 {
		t.Fatalf("expected exactly one rendered listener:\n%s", data)
	}
}

func TestNormalizePanelPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "/"},
		{"   ", "/"},
		{"/", "/"},
		{"///", "/"},
		{"abc", "/abc/"},
		{"/abc", "/abc/"},
		{"abc/", "/abc/"},
		{"//a//b//", "/a/b/"},
		{"  /a-b_c~.d/  ", "/a-b_c~.d/"},
	}
	for _, item := range cases {
		if got := normalizePanelPath(item.in); got != item.want {
			t.Fatalf("normalizePanelPath(%q) = %q, want %q", item.in, got, item.want)
		}
	}
}

func TestValidatePanelPath(t *testing.T) {
	for _, allowed := range []string{"/", "/abc/", "/a-b_c.d~e/", "/one/two/"} {
		if err := validatePanelPath(allowed); err != nil {
			t.Fatalf("validatePanelPath(%q) rejected: %v", allowed, err)
		}
	}
	// Anything that a browser would have to percent-encode, plus the two dot
	// segments, would stop matching the prefix the router compares against.
	for _, rejected := range []string{"/a b/", "/中文/", "/../", "/./", "/a%20b/", "/a?b/", "/a#b/"} {
		if err := validatePanelPath(rejected); err == nil {
			t.Fatalf("validatePanelPath(%q) accepted", rejected)
		}
	}
}

func TestUpdateSettingsNormalizesPanelPath(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir(), state: defaultState()}
	settings := m.state.Settings
	settings.Password = ""
	settings.PanelPath = "  my-hidden-panel  "
	if err := m.updateSettings(settings); err != nil {
		t.Fatalf("updateSettings: %v", err)
	}
	if m.state.Settings.PanelPath != "/my-hidden-panel/" {
		t.Fatalf("stored panelPath = %q, want /my-hidden-panel/", m.state.Settings.PanelPath)
	}
	if got := m.panelPath(); got != "/my-hidden-panel/" {
		t.Fatalf("panelPath() = %q, want /my-hidden-panel/", got)
	}
	settings.PanelPath = "/not a path/"
	if err := m.updateSettings(settings); err == nil {
		t.Fatal("a path with a space must be rejected")
	}
	if m.state.Settings.PanelPath != "/my-hidden-panel/" {
		t.Fatalf("a rejected path must not overwrite the stored one, got %q", m.state.Settings.PanelPath)
	}
}

func TestPanelPathLoadsAsRootForLegacyStates(t *testing.T) {
	dir := t.TempDir()
	state := defaultState()
	state.Settings.PanelPath = ""
	m := &CoreManager{dataDir: dir, state: state}
	if err := m.saveLocked(); err != nil {
		t.Fatal(err)
	}
	loaded := &CoreManager{dataDir: dir}
	if err := loaded.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.state.Settings.PanelPath != "/" {
		t.Fatalf("legacy state panelPath = %q, want /", loaded.state.Settings.PanelPath)
	}
}

func TestWithPanelPathServesPrefixOnly(t *testing.T) {
	app := &App{manager: &CoreManager{dataDir: t.TempDir(), state: State{
		Settings: Settings{Username: "admin", Password: "hash", PanelPath: "/hidden-panel/"}}}, session: "token"}
	handler := app.withPanelPath(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen-Path", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	serve := func(target string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		return recorder
	}
	got := serve("/hidden-panel/api/state")
	if got.Code != http.StatusOK || got.Header().Get("X-Seen-Path") != "/api/state" {
		t.Fatalf("prefixed request: code %d, mux saw %q", got.Code, got.Header().Get("X-Seen-Path"))
	}
	if got := serve("/hidden-panel/"); got.Header().Get("X-Seen-Path") != "/" {
		t.Fatalf("panel root should reach the mux as /, got %q", got.Header().Get("X-Seen-Path"))
	}
	// A request without the prefix must get a bare 404. Redirecting it to the
	// real path would hand the secret to whatever is scanning the port.
	for _, target := range []string{"/", "/login", "/api/state", "/other/api/state"} {
		if got := serve(target); got.Code != http.StatusNotFound || got.Header().Get("Location") != "" {
			t.Fatalf("%s: code %d, Location %q, want a bare 404", target, got.Code, got.Header().Get("Location"))
		}
	}
	if got := serve("/hidden-panel"); got.Code != http.StatusFound || got.Header().Get("Location") != "/hidden-panel/" {
		t.Fatalf("missing trailing slash: code %d, Location %q", got.Code, got.Header().Get("Location"))
	}
	app.manager.state.Settings.PanelPath = "/"
	if got := serve("/api/state"); got.Code != http.StatusOK || got.Header().Get("X-Seen-Path") != "/api/state" {
		t.Fatalf("root panel: code %d, mux saw %q", got.Code, got.Header().Get("X-Seen-Path"))
	}
}

func TestPanelPathAppliesToRedirectsAndSessionCookie(t *testing.T) {
	hash, err := hashPassword("panel-secret")
	if err != nil {
		t.Fatal(err)
	}
	app := &App{manager: &CoreManager{dataDir: t.TempDir(), state: State{
		Settings: Settings{Username: "admin", Password: hash, PanelPath: "/hidden-panel/"}}}, session: "token"}
	recorder := httptest.NewRecorder()
	app.handleIndex(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusFound || recorder.Header().Get("Location") != "/hidden-panel/login" {
		t.Fatalf("anonymous index: code %d, Location %q", recorder.Code, recorder.Header().Get("Location"))
	}
	recorder = httptest.NewRecorder()
	login := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"panel-secret"}`))
	app.handleLogin(recorder, login)
	cookie := recorder.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, "Path=/hidden-panel/") {
		t.Fatalf("session cookie must be scoped to the panel prefix, got %q", cookie)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"redirect":"/hidden-panel/"`) {
		t.Fatalf("login redirect must carry the prefix, got %s", body)
	}
	recorder = httptest.NewRecorder()
	loggedIn := httptest.NewRequest(http.MethodGet, "/login", nil)
	loggedIn.AddCookie(&http.Cookie{Name: "mui_session", Value: "token"})
	app.handleLoginPage(recorder, loggedIn)
	if recorder.Code != http.StatusFound || recorder.Header().Get("Location") != "/hidden-panel/" {
		t.Fatalf("logged-in login page: code %d, Location %q", recorder.Code, recorder.Header().Get("Location"))
	}
}

func TestPanelPathWiringInWebAssets(t *testing.T) {
	index, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`id="panel-path-banner"`,
		`id="dismiss-path-banner"`,
		`name="panelPath"`,
		"function panelURL(path)",
		"function normalizePanelPath(path)",
		"function renderPanelPathBanner()",
		"fetch(panelURL(path)",
		`src="static/qrious2.min.js"`,
	} {
		if !strings.Contains(string(index), needle) {
			t.Fatalf("web/index.html is missing %s", needle)
		}
	}
	// The banner has to stay its own element below the TLS one, never merged.
	tls := strings.Index(string(index), `id="panel-security-banner"`)
	path := strings.Index(string(index), `id="panel-path-banner"`)
	if tls < 0 || path < tls {
		t.Fatalf("the path banner must follow the TLS banner (tls=%d path=%d)", tls, path)
	}
	// Root-absolute panel URLs break as soon as the panel moves off "/".
	for _, forbidden := range []string{`fetch('/api`, `href="/api`, `src="/static`, `location.href='/login'`} {
		if strings.Contains(string(index), forbidden) {
			t.Fatalf("web/index.html still uses a root-absolute URL: %s", forbidden)
		}
	}
	login, err := os.ReadFile("web/login.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(login), "base + 'api/auth/login'") {
		t.Fatal("web/login.html must post to the login endpoint relative to the panel prefix")
	}
	if strings.Contains(string(login), `fetch('/api`) {
		t.Fatal("web/login.html still posts to a root-absolute URL")
	}
}

// 设置页的 Language 只管登录页的语言，面板内部保持中文。登录页是静态资源，
// 所以这个值得靠 handleLoginPage 注进 data-default-language 才能到没访问过面板的浏览器。
func TestLoginPageCarriesTheConfiguredLanguage(t *testing.T) {
	login, err := os.ReadFile("web/login.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(login), `data-default-language="zh-CN"`) {
		t.Fatal("web/login.html must ship the zh-CN placeholder that handleLoginPage rewrites")
	}
	if !strings.Contains(string(login), "document.documentElement.dataset.defaultLanguage") {
		t.Fatal("web/login.html must fall back to the injected language when localStorage is empty")
	}
	manager := &CoreManager{dataDir: t.TempDir(), state: defaultState()}
	app := &App{manager: manager}
	serve := func() string {
		recorder := httptest.NewRecorder()
		app.handleLoginPage(recorder, httptest.NewRequest(http.MethodGet, "/login", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("login page: status = %d", recorder.Code)
		}
		return recorder.Body.String()
	}
	if page := serve(); !strings.Contains(page, `data-default-language="zh-CN"`) {
		t.Fatal("the default language must stay zh-CN")
	}
	manager.state.Settings.Language = "en"
	page := serve()
	if !strings.Contains(page, `data-default-language="en"`) {
		t.Fatal("the login page must carry the stored language")
	}
	if strings.Contains(page, `data-default-language="zh-CN"`) {
		t.Fatal("the placeholder must be replaced, not duplicated")
	}
	if !strings.Contains(page, `<html lang="zh-CN"`) {
		t.Fatal("the rewrite must not touch the document lang attribute")
	}
	// 存了个不认识的值（手改 state.json）时不能把这段注释成空的。
	manager.state.Settings.Language = "fr"
	if page := serve(); !strings.Contains(page, `data-default-language="zh-CN"`) {
		t.Fatal("an unsupported stored language must fall back to zh-CN")
	}
}

func TestUpdateSettingsNormalizesBehaviourFields(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir(), state: defaultState()}
	original := m.state.Settings
	settings := m.state.Settings
	settings.Password = ""
	settings.RemarkModel = "@eii"
	settings.SessionMaxAge = 0
	settings.PageSize = 0
	settings.ExpireDiff = 3
	settings.TrafficDiff = 0
	settings.TimeLocation = "  "
	settings.Language = ""
	settings.PanelDomain = "panel.example.com"
	if err := m.updateSettings(settings); err != nil {
		t.Fatalf("updateSettings: %v", err)
	}
	stored := m.state.Settings
	if stored.RemarkModel != "@ei" {
		t.Fatalf("remarkModel = %q, want @ei (deduped, click order kept)", stored.RemarkModel)
	}
	if stored.SessionMaxAge != defaultSessionMaxAge {
		t.Fatalf("sessionMaxAge = %d, want the default %d", stored.SessionMaxAge, defaultSessionMaxAge)
	}
	// 0 是"关闭分页"，必须原样存下来，不能被当成"没填"补成默认值。
	if stored.PageSize != 0 {
		t.Fatalf("pageSize = %d, want 0 to stay disabled", stored.PageSize)
	}
	if stored.ExpireDiff != 3 || stored.TrafficDiff != 0 {
		t.Fatalf("notification thresholds = %d/%d, want 3/0", stored.ExpireDiff, stored.TrafficDiff)
	}
	if stored.TimeLocation != defaultTimeLocation {
		t.Fatalf("timeLocation = %q, want %q", stored.TimeLocation, defaultTimeLocation)
	}
	if stored.Language != defaultLanguage {
		t.Fatalf("language = %q, want %q", stored.Language, defaultLanguage)
	}
	if stored.PanelDomain != "panel.example.com" {
		t.Fatalf("panelDomain = %q", stored.PanelDomain)
	}
	// General 页签不再提交账号密码，留空必须理解成"保持现有的"。
	if stored.Username != original.Username || stored.Password != original.Password {
		t.Fatalf("credentials must survive a settings save: %q/%q", stored.Username, stored.Password)
	}
}

func TestUpdateSettingsPersistsOptionalSubscriptionPort(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir(), state: defaultState()}
	settings := m.state.Settings
	settings.Password = ""
	settings.SubscriptionPort = 2096
	if err := m.updateSettings(settings); err != nil {
		t.Fatalf("save dedicated subscription port: %v", err)
	}
	if got := m.state.Settings.SubscriptionPort; got != 2096 {
		t.Fatalf("stored subscription port = %d, want 2096", got)
	}

	settings = m.state.Settings
	settings.Password = ""
	settings.SubscriptionPort = 0
	if err := m.updateSettings(settings); err != nil {
		t.Fatalf("restore panel-port fallback: %v", err)
	}
	if got := m.state.Settings.SubscriptionPort; got != 0 {
		t.Fatalf("stored subscription port = %d, want the unset fallback", got)
	}
}

func TestUpdateSettingsRejectsInvalidBehaviourFields(t *testing.T) {
	cases := map[string]func(*Settings){
		"remark model without a separator": func(s *Settings) { s.RemarkModel = "ie" },
		"remark model with unknown field":  func(s *Settings) { s.RemarkModel = "-io" },
		"session below the floor":          func(s *Settings) { s.SessionMaxAge = minSessionMaxAge - 1 },
		"negative page size":               func(s *Settings) { s.PageSize = -1 },
		"page size above the cap":          func(s *Settings) { s.PageSize = maxPageSize + 1 },
		"negative expiry threshold":        func(s *Settings) { s.ExpireDiff = -1 },
		"negative traffic threshold":       func(s *Settings) { s.TrafficDiff = -1 },
		"traffic inform without a uri":     func(s *Settings) { s.ExternalTrafficInformEnable = true },
		"traffic inform with a bare host": func(s *Settings) {
			s.ExternalTrafficInformEnable = true
			s.ExternalTrafficInformURI = "example.com/hook"
		},
		"unknown time zone":                func(s *Settings) { s.TimeLocation = "Mars/Olympus" },
		"unsupported language":             func(s *Settings) { s.Language = "fr" },
		"listen domain carrying a scheme":  func(s *Settings) { s.PanelDomain = "https://panel.example.com" },
		"listen domain carrying a port":    func(s *Settings) { s.PanelDomain = "panel.example.com:2999" },
		"listen domain with an underscore": func(s *Settings) { s.PanelDomain = "panel_example.com" },
		"panel listen without a port":      func(s *Settings) { s.PanelListen = "0.0.0.0" },
		"panel listen on port zero":        func(s *Settings) { s.PanelListen = "0.0.0.0:0" },
		"panel listen above the port cap":  func(s *Settings) { s.PanelListen = "0.0.0.0:70000" },
		"panel listen on a domain":         func(s *Settings) { s.PanelListen = "panel.example.com:2999" },
		"api address without a port":       func(s *Settings) { s.APIAddress = "127.0.0.1" },
	}
	for name, mutate := range cases {
		m := &CoreManager{dataDir: t.TempDir(), state: defaultState()}
		before := m.state.Settings
		settings := m.state.Settings
		settings.Password = ""
		mutate(&settings)
		if err := m.updateSettings(settings); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
		if m.state.Settings.RemarkModel != before.RemarkModel || m.state.Settings.PageSize != before.PageSize || m.state.Settings.PanelListen != before.PanelListen {
			t.Fatalf("%s must not partially overwrite the stored settings", name)
		}
	}
}

// 面板监听地址一旦存成 "0.0.0.0" 这种少了端口的写法，下次启动 http.Server 会直接
// 报 missing port in address 退出，操作员只能手改 state.json，所以这些写法必须能存进去。
func TestUpdateSettingsAcceptsEveryUsableListenSpelling(t *testing.T) {
	for _, listen := range []string{":2999", "0.0.0.0:2999", "127.0.0.1:2999", "localhost:2999", "[::]:2999", "[::1]:2999", " 0.0.0.0:2999 "} {
		m := &CoreManager{dataDir: t.TempDir(), state: defaultState()}
		settings := m.state.Settings
		settings.Password = ""
		settings.PanelListen = listen
		if err := m.updateSettings(settings); err != nil {
			t.Fatalf("panel listen %q must be accepted: %v", listen, err)
		}
		if got := m.state.Settings.PanelListen; got != strings.TrimSpace(listen) {
			t.Fatalf("panel listen %q stored as %q", listen, got)
		}
		if _, port := hostPortFromAddress(m.state.Settings.PanelListen); port != 2999 {
			t.Fatalf("panel listen %q must keep port 2999, got %d", listen, port)
		}
	}
}

func TestCredentialsEndpointChecksCurrentPairAndRotatesSession(t *testing.T) {
	manager := &CoreManager{dataDir: t.TempDir(), state: defaultState()}
	app := &App{manager: manager, session: "old-token"}
	post := func(body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		app.handleCredentials(recorder, httptest.NewRequest(http.MethodPost, "/api/auth/credentials", strings.NewReader(body)))
		return recorder
	}
	if recorder := post(`{"oldUsername":"admin","oldPassword":"admin","newUsername":"","newPassword":""}`); recorder.Code != http.StatusBadRequest {
		t.Fatalf("empty new pair: status = %d, want 400", recorder.Code)
	}
	if recorder := post(`{"oldUsername":"admin","oldPassword":"nope","newUsername":"root","newPassword":"s3cret"}`); recorder.Code != http.StatusBadRequest {
		t.Fatalf("wrong current password: status = %d, want 400", recorder.Code)
	}
	if manager.state.Settings.Username != "admin" {
		t.Fatalf("a rejected change must keep the stored username, got %q", manager.state.Settings.Username)
	}
	recorder := post(`{"oldUsername":"admin","oldPassword":"admin","newUsername":" root ","newPassword":"s3cret"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("valid change: status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if manager.state.Settings.Username != "root" {
		t.Fatalf("username = %q, want root (trimmed)", manager.state.Settings.Username)
	}
	if !verifyPassword(manager.state.Settings.Password, "s3cret") {
		t.Fatal("the new password must be stored as a verifiable hash")
	}
	token, _ := app.currentSession()
	if token == "old-token" || token == "" {
		t.Fatalf("the session token must be rotated, got %q", token)
	}
	if cookies := recorder.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != "mui_session" || cookies[0].MaxAge >= 0 {
		t.Fatalf("the response must clear the session cookie, got %+v", cookies)
	}
}

func TestSessionExpiresAfterSessionMaxAge(t *testing.T) {
	manager := &CoreManager{dataDir: t.TempDir(), state: defaultState()}
	app := &App{manager: manager, session: "live-token"}
	token := app.refreshSession(50 * time.Millisecond)
	request := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	request.AddCookie(&http.Cookie{Name: "mui_session", Value: token})
	if !app.isLoggedIn(request) {
		t.Fatal("a fresh session must be accepted")
	}
	time.Sleep(80 * time.Millisecond)
	if app.isLoggedIn(request) {
		t.Fatal("an expired session must be rejected")
	}
	// 直接构造的 App 没有过期时间，测试里当成"永不过期"，别退化成"立刻过期"。
	plain := &App{manager: manager, session: "token"}
	plainRequest := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	plainRequest.AddCookie(&http.Cookie{Name: "mui_session", Value: "token"})
	if !plain.isLoggedIn(plainRequest) {
		t.Fatal("a session without an expiry must stay valid")
	}
}

func TestSessionMaxAgeComesFromSettings(t *testing.T) {
	manager := &CoreManager{dataDir: t.TempDir(), state: defaultState()}
	app := &App{manager: manager}
	if got := app.sessionMaxAgeMinutes(); got != defaultSessionMaxAge {
		t.Fatalf("sessionMaxAgeMinutes() = %d, want %d", got, defaultSessionMaxAge)
	}
	manager.state.Settings.SessionMaxAge = 90
	if got := app.sessionMaxAgeMinutes(); got != 90 {
		t.Fatalf("sessionMaxAgeMinutes() = %d, want 90", got)
	}
	manager.state.Settings.SessionMaxAge = 0
	if got := app.sessionMaxAgeMinutes(); got != defaultSessionMaxAge {
		t.Fatalf("a zero setting must fall back to %d, got %d", defaultSessionMaxAge, got)
	}
}

func TestPanelRestartEndpointSignalsMain(t *testing.T) {
	app := &App{manager: &CoreManager{dataDir: t.TempDir(), state: defaultState()}, restart: make(chan struct{}, 1)}
	recorder := httptest.NewRecorder()
	app.handlePanelRestart(recorder, httptest.NewRequest(http.MethodPost, "/api/panel/restart", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	select {
	case <-app.restart:
	case <-time.After(2 * time.Second):
		t.Fatal("the restart signal never arrived")
	}
	// 没人监听时不能阻塞：signalRestart 用的是带缓冲 + select/default。
	full := &App{manager: app.manager, restart: make(chan struct{}, 1)}
	full.signalRestart()
	full.signalRestart()
	(&App{manager: app.manager}).signalRestart()
}

func TestTrafficResetDueFollowsTheConfiguredZone(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation: %v (时区数据必须随二进制打包)", err)
	}
	last := time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)
	// UTC 还是 3-5，但东八区已经跨到 3-6 的凌晨，daily 必须判定为到期。
	crossed := time.Date(2026, 3, 5, 23, 30, 0, 0, time.UTC)
	if trafficResetDue("daily", last, crossed) {
		t.Fatal("同一个 UTC 日期不应该触发 daily 重置")
	}
	if !trafficResetDue("daily", last.In(shanghai), crossed.In(shanghai)) {
		t.Fatal("东八区已经跨天，daily 必须触发重置")
	}
	sameDay := time.Date(2026, 3, 5, 14, 0, 0, 0, time.UTC)
	if trafficResetDue("daily", last.In(shanghai), sameDay.In(shanghai)) {
		t.Fatal("东八区同一天不应该触发 daily 重置")
	}
	monthLast := time.Date(2026, 2, 28, 10, 0, 0, 0, time.UTC)
	monthNow := time.Date(2026, 2, 28, 17, 0, 0, 0, time.UTC)
	if trafficResetDue("monthly", monthLast, monthNow) {
		t.Fatal("UTC 还在 2 月，monthly 不应该触发重置")
	}
	if !trafficResetDue("monthly", monthLast.In(shanghai), monthNow.In(shanghai)) {
		t.Fatal("东八区已经进入 3 月，monthly 必须触发重置")
	}
	if trafficResetDue("never", last, crossed) || trafficResetDue("", last, crossed) {
		t.Fatal("never/空周期不应该触发重置")
	}
}

func TestApplyScheduledTrafficResetsClearsOnlyPeriodCounters(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir(), state: defaultState()}
	m.state.Settings.TimeLocation = "Asia/Tokyo"
	m.state.Inbounds = []Inbound{{
		ID: "a", Name: "in", TrafficReset: "daily",
		LastTrafficReset: time.Now().Add(-48 * time.Hour).Format(time.RFC3339),
		Traffic:          TrafficStats{Up: 5, Down: 6, AllTimeUp: 50, AllTimeDown: 60},
		Clients:          []Client{{ID: "c", Name: "alice", Traffic: TrafficStats{Up: 1, Down: 2, AllTimeUp: 10, AllTimeDown: 20}}},
	}, {
		ID: "b", Name: "keep", TrafficReset: "monthly",
		LastTrafficReset: time.Now().Format(time.RFC3339),
		Traffic:          TrafficStats{Up: 7, Down: 8},
	}}
	m.applyScheduledTrafficResets()
	due, kept := m.state.Inbounds[0], m.state.Inbounds[1]
	if due.Traffic.Up != 0 || due.Traffic.Down != 0 {
		t.Fatalf("周期流量没有清零: %+v", due.Traffic)
	}
	if due.Traffic.AllTimeUp != 50 || due.Traffic.AllTimeDown != 60 {
		t.Fatalf("历史累计流量不能被清掉: %+v", due.Traffic)
	}
	if due.Clients[0].Traffic.Up != 0 || due.Clients[0].Traffic.AllTimeUp != 10 {
		t.Fatalf("客户端流量重置有误: %+v", due.Clients[0].Traffic)
	}
	if parsed, err := time.Parse(time.RFC3339, due.LastTrafficReset); err != nil || time.Since(parsed) > time.Minute {
		t.Fatalf("重置时间戳没有刷新: %q (%v)", due.LastTrafficReset, err)
	}
	if kept.Traffic.Up != 7 || kept.Traffic.Down != 8 {
		t.Fatalf("未到期的 inbound 不能被重置: %+v", kept.Traffic)
	}
}

func TestInformExternalTrafficMatchesXrayWireShape(t *testing.T) {
	type inboundWire struct {
		IsInbound  bool
		IsOutbound bool
		Tag        string
		Up         int64
		Down       int64
	}
	type wire struct {
		ClientTraffics  []externalClientTraffic `json:"clientTraffics"`
		InboundTraffics []inboundWire           `json:"inboundTraffics"`
	}
	received := make(chan wire, 1)
	contentType := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType <- r.Header.Get("Content-Type")
		var body wire
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		received <- body
	}))
	defer server.Close()
	m := &CoreManager{dataDir: t.TempDir(), state: defaultState()}
	m.informExternalTraffic(server.URL,
		[]externalInboundTraffic{{IsInbound: true, Tag: "tokyo", Up: 10, Down: 20}},
		[]externalClientTraffic{{ID: "c1", InboundID: "in1", Enable: true, Email: "alice", UUID: "u", Up: 1, Down: 2, AllTime: 30, Total: 100, ExpiryTime: 7, LastOnline: 9}})
	if got := <-contentType; got != "application/json; charset=UTF-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	body := <-received
	if len(body.InboundTraffics) != 1 || body.InboundTraffics[0].Tag != "tokyo" || !body.InboundTraffics[0].IsInbound ||
		body.InboundTraffics[0].Up != 10 || body.InboundTraffics[0].Down != 20 || body.InboundTraffics[0].IsOutbound {
		t.Fatalf("inboundTraffics 必须照 3x-ui 的大写字段发出: %+v", body.InboundTraffics)
	}
	if len(body.ClientTraffics) != 1 {
		t.Fatalf("clientTraffics = %+v", body.ClientTraffics)
	}
	client := body.ClientTraffics[0]
	if client.ID != "c1" || client.InboundID != "in1" || client.Email != "alice" || !client.Enable ||
		client.Up != 1 || client.Down != 2 || client.AllTime != 30 || client.Total != 100 {
		t.Fatalf("clientTraffics 字段不对: %+v", client)
	}
}

func TestExternalTrafficBuffersDeltasAndFlushesOnPeriod(t *testing.T) {
	posts := make(chan int, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts <- 1 }))
	defer server.Close()
	m := &CoreManager{dataDir: t.TempDir(), state: defaultState()}
	client := Client{ID: "c1", Name: "alice", Enabled: true, Total: 100, Traffic: TrafficStats{AllTimeUp: 3, AllTimeDown: 4}}

	// 开关关着的时候一个字节都不该缓存。
	m.recordExternalInboundDeltaLocked("in1", "tokyo", 1, 2)
	m.recordExternalClientDeltaLocked("in1", client, 1, 2)
	if len(m.externalTrafficInbounds) != 0 || len(m.externalTrafficClients) != 0 {
		t.Fatal("关闭上报时不应该缓存增量")
	}

	m.state.Settings.ExternalTrafficInformEnable = true
	m.state.Settings.ExternalTrafficInformURI = server.URL
	m.recordExternalInboundDeltaLocked("in1", "tokyo", 1, 2)
	m.recordExternalInboundDeltaLocked("in1", "tokyo", 3, 4)
	m.recordExternalClientDeltaLocked("in1", client, 5, 6)
	if entry := m.externalTrafficInbounds["in1"]; !entry.IsInbound || entry.Tag != "tokyo" || entry.Up != 4 || entry.Down != 6 {
		t.Fatalf("多次轮询的增量必须累加: %+v", entry)
	}
	if entry := m.externalTrafficClients["in1:c1"]; entry.Email != "alice" || entry.Up != 5 || entry.Down != 6 || entry.AllTime != 7 {
		t.Fatalf("客户端增量不对: %+v", entry)
	}

	// 第一次 flush 只是对齐时间基准，不发也不清。
	start := time.Now()
	m.flushExternalTrafficLocked(start)
	if len(m.externalTrafficInbounds) != 1 {
		t.Fatal("第一次 flush 不应该清空缓存")
	}
	m.flushExternalTrafficLocked(start.Add(externalTrafficInformPeriod - time.Second))
	if len(m.externalTrafficInbounds) != 1 {
		t.Fatal("不到一个周期不应该上报")
	}
	m.flushExternalTrafficLocked(start.Add(externalTrafficInformPeriod))
	if len(m.externalTrafficInbounds) != 0 || len(m.externalTrafficClients) != 0 {
		t.Fatal("上报之后必须清空缓存，避免重复计数")
	}
	select {
	case <-posts:
	case <-time.After(3 * time.Second):
		t.Fatal("满一个周期后应该发出一次上报")
	}

	// 关掉开关时把残留数据丢掉，别等下次打开一股脑发出去。
	m.state.Settings.ExternalTrafficInformEnable = true
	m.recordExternalInboundDeltaLocked("in1", "tokyo", 9, 9)
	m.state.Settings.ExternalTrafficInformEnable = false
	m.flushExternalTrafficLocked(time.Now())
	if m.externalTrafficInbounds != nil || m.externalTrafficClients != nil {
		t.Fatal("关闭上报后必须丢掉缓存")
	}
}

func TestSettingsPageReplicates3xUiLayout(t *testing.T) {
	index, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	content := string(index)
	start := strings.Index(content, `id="view-settings"`)
	if start < 0 {
		t.Fatal(`web/index.html is missing id="view-settings"`)
	}
	end := strings.Index(content[start:], "</section>")
	if end < 0 {
		t.Fatal("the settings view is not closed")
	}
	view := content[start : start+end]
	for _, needle := range []string{
		`id="save-settings"`, ">Save</button>", `id="restart-panel"`, ">Restart Panel</button>",
		"Every change made here needs to be saved. Please restart the panel to apply changes.",
		`data-settings-tab="general"`, `data-settings-tab="authentication"`, ">General</button>", ">Authentication</button>",
		`id="settings-pane-general"`, `id="settings-pane-authentication"`,
		"<summary>General</summary>", "<summary>Notifications</summary>", "<summary>Certificaties</summary>",
		"<summary>External Traffic</summary>", "<summary>Date and Time</summary>", "<summary>Mihomo Core</summary>",
		"<summary>Admin credentials</summary>",
		"Remark Model &amp; Separation Character", "Sample Remark:", `id="remark-sample"`,
		`data-remark-token="i"`, `data-remark-token="e"`, `id="remark-separator"`,
		`name="remarkModel"`, `name="listenIp"`, `name="panelDomain"`, `name="listenPort"`, `name="panelPath"`,
		`name="sessionMaxAge"`, `name="pageSize"`, `name="language"`,
		`name="expireDiff"`, `name="trafficDiff"`, `name="panelCertFile"`, `name="panelKeyFile"`,
		`name="externalTrafficInformEnable"`, `name="externalTrafficInformURI"`, `name="timeLocation"`,
		`name="corePath"`, `name="apiAddress"`, `name="apiSecret"`, `name="mixedPort"`, `name="mode"`, `name="logLevel"`, `name="allowLan"`,
		`name="oldUsername"`, `name="oldPassword"`, `name="newUsername"`, `name="newPassword"`, `id="save-credentials"`,
		"The IP address for the web panel. (leave blank to listen on all IPs)",
		"The port number for the web panel. (must be an unused port)",
		"The duration for which you can stay logged in. (unit: minute)",
		"Define page size for inbounds table. (0 = disable)",
		"Get notified about expiration date when reaching this threshold. (unit: day)",
		"Get notified about traffic cap when reaching this threshold. (unit: GB)",
		"The public key file path for the web panel. (begins with ‘/‘)",
		"Inform external API on every traffic update.", "Traffic updates are sent to this URI.",
		"Scheduled tasks will run based on this time zone.",
	} {
		if !strings.Contains(view, needle) {
			t.Fatalf("the settings view is missing %s", needle)
		}
	}
	// 用户明确说了不做的东西：LDAP、2FA、Telegram Bot 和 Calendar Type。
	for _, absent := range []string{"LDAP", "2FA", "Two Factor", "Telegram", "Calendar"} {
		if strings.Contains(view, absent) {
			t.Fatalf("the settings view must not mention %s", absent)
		}
	}
	// 账号密码只在 Authentication 页签改，General 页签不能再带这两个字段。
	general := view[strings.Index(view, `id="settings-pane-general"`):strings.Index(view, `id="settings-pane-authentication"`)]
	for _, absent := range []string{`name="username"`, `name="password"`} {
		if strings.Contains(general, absent) {
			t.Fatalf("the General tab must not carry %s any more", absent)
		}
	}
	for _, needle := range []string{
		"'/api/auth/credentials'", "'/api/panel/restart'",
		"function renderSettings()", "function syncSettingsButtons()", "function applyRemarkModel(model)",
		"function syncRemarkModel()", "function remarkLabel(inbound,client)", "function splitPanelListen(value)",
		"function joinPanelListen(ip,port)", "function inboundPager(current,pages,total)",
		"async function saveCredentials()", "async function restartPanel()",
		"localStorage.setItem('mui-language'",
	} {
		if !strings.Contains(content, needle) {
			t.Fatalf("web/index.html is missing %s", needle)
		}
	}
	if strings.Contains(content, "ensurePanelCertificateSettings") {
		t.Fatal("the certificate rows are part of the markup now, the JS shim must be gone")
	}
	for _, needle := range []string{
		"setupSubscriptionUI", "setupSubscriptionPortUI", "Subscription Service Port", `name="subscriptionPort"`, "Subscription Path", "Clash / Mihomo Path", "Manage Tokens", "Export All Subscriptions",
		`id="all-links-modal"`, `id="all-subscriptions-modal"`, "function openAllLinksModal()", "function openAllSubscriptionsModal()",
		"regenerate-subscription-path", "muiT('tool.generateSubPath')", "'/api/tools/subscription-path'",
	} {
		if !strings.Contains(content, needle) {
			t.Fatalf("the settings page is missing subscription feature %s", needle)
		}
	}
	dictionary, err := os.ReadFile("web/i18n.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dictionary), "'tool.generateSubPath': 'Generate new Subscription Path'") {
		t.Fatal("the regenerate tooltip wording is missing from web/i18n.js")
	}
	pathStart := strings.Index(content, "function subscriptionPublicPath(")
	if pathStart < 0 {
		t.Fatal("the frontend subscription path helpers are missing")
	}
	pathEnd := strings.Index(content[pathStart:], "function allSubscriptionLines(")
	if pathEnd < 0 {
		t.Fatal("the frontend subscription path helpers are missing")
	}
	if pathHelper := content[pathStart : pathStart+pathEnd]; strings.Contains(pathHelper, "panelPath") {
		t.Fatal("public subscription URLs must not include the panel URI path")
	}
	linesStart := strings.Index(content, "function allSubscriptionLines(")
	if linesStart < 0 {
		t.Fatal("the all-subscriptions export helper is missing")
	}
	linesEnd := strings.Index(content[linesStart:], "function allInboundURLLines(")
	if linesEnd < 0 {
		t.Fatal("the all-subscriptions export helper is missing")
	}
	if linesHelper := content[linesStart : linesStart+linesEnd]; strings.Contains(strings.ToLower(linesHelper), "clash") {
		t.Fatal("Export All Subscriptions must list only the normal subscription URL")
	}
}
