package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestMihomoBasicsLegacyDefaultsAndPersistence(t *testing.T) {
	state := defaultState()
	state.Settings.Mode = "global"
	state.Settings.LogLevel = "warning"
	basics := effectiveMihomoBasics(state)
	if basics.Mode != "global" || basics.LogLevel != "warning" || !basics.IPv6 || basics.TrafficSaveSeconds != 5 || len(basics.BlockIPs) != 0 {
		t.Fatalf("legacy defaults: %#v", basics)
	}
	m := &CoreManager{dataDir: t.TempDir(), state: state}
	basics.IPv6 = false
	basics.Mode = "rule"
	basics.OutboundUploadStatistics = true
	basics.BlockIPs = []string{"private", "192.168.1.4", "192.168.1.4/32"}
	if err := m.updateMihomoSettings(mihomoSettingsPayload{Basics: &basics}); err != nil {
		t.Fatal(err)
	}
	if len(m.state.MihomoBasics.BlockIPs) != 2 {
		t.Fatal("Block IPs must normalize and deduplicate")
	}
	loaded := &CoreManager{dataDir: m.dataDir}
	if err := loaded.load(); err != nil {
		t.Fatal(err)
	}
	got := effectiveMihomoBasics(loaded.state)
	if got.IPv6 || !got.OutboundUploadStatistics || got.Mode != "rule" || got.LogLevel != "warning" {
		t.Fatalf("reloaded basics: %#v", got)
	}
	if err := loaded.updateMihomoSettings(mihomoSettingsPayload{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, effectiveMihomoBasics(loaded.state)) {
		t.Fatal("old client settings update must preserve Basics")
	}
	settings := loaded.state.Settings
	settings.Password = ""
	settings.LogLevel = "debug"
	if err := loaded.updateSettings(settings); err != nil {
		t.Fatal(err)
	}
	if effectiveMihomoBasics(loaded.state).LogLevel != "debug" {
		t.Fatal("Panel Settings and Basics diverged")
	}
}

func TestMihomoBasicsCompilationPreservesSources(t *testing.T) {
	basics := defaultMihomoBasics()
	basics.DirectIPVersion = "ipv4-prefer"
	basics.BlockIPs = []string{"private", "10.0.0.0/8"}
	basics.BlockDomains = []string{"full:blocked.example.com", "geosite:category-ads-all"}
	basics.IPv4Domains = []string{"example.net"}
	group := MihomoOutbound{Kind: "group", Name: "Pick", Type: "select", Native: map[string]any{"name": "Pick", "type": "select", "proxies": []any{"DIRECT"}, "default-selected": "DIRECT"}}
	before, _ := json.Marshal(group)
	proxies, groups, rules, err := compileMihomoBasics(basics, []MihomoOutbound{group}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"GEOIP,LAN,REJECT", "IP-CIDR,10.0.0.0/8,REJECT", "DOMAIN,blocked.example.com,REJECT", "GEOSITE,category-ads-all,REJECT", "DOMAIN-SUFFIX,example.net,MUI-IPV4", "MATCH,MUI-DIRECT"}
	if !reflect.DeepEqual(rules, want) {
		t.Fatalf("compiled rule order: %#v", rules)
	}
	if len(proxies) != 2 || proxies[0]["ip-version"] != "ipv4-prefer" || proxies[1]["ip-version"] != "ipv4" {
		t.Fatalf("managed proxies: %#v", proxies)
	}
	if groups[0]["proxies"].([]any)[0] != basicsDirectName || groups[0]["default-selected"] != basicsDirectName {
		t.Fatal("DIRECT group references were not adapted")
	}
	after, _ := json.Marshal(group)
	if string(before) != string(after) {
		t.Fatal("compiler mutated native source")
	}
}

func TestMihomoBasicsRejectsInvalidWithoutSaving(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir(), state: defaultState()}
	for name, mutate := range map[string]func(*MihomoBasics){
		"mode": func(b *MihomoBasics) { b.Mode = "invalid" }, "log": func(b *MihomoBasics) { b.LogLevel = "trace" },
		"url": func(b *MihomoBasics) { b.OutboundTestURL = "file:///etc/passwd" }, "sample": func(b *MihomoBasics) { b.TrafficSampleSeconds = 0 },
		"save": func(b *MihomoBasics) { b.TrafficSaveSeconds = -1 }, "buffer": func(b *MihomoBasics) { b.LogBufferSize = 1 },
		"ip": func(b *MihomoBasics) { b.BlockIPs = []string{"not-an-ip"} }, "injection": func(b *MihomoBasics) { b.BlockDomains = []string{"example.com,REJECT"} },
		"domain": func(b *MihomoBasics) { b.IPv4Domains = []string{"https://example.com"} },
	} {
		b := defaultMihomoBasics()
		mutate(&b)
		if err := m.updateMihomoSettings(mihomoSettingsPayload{Basics: &b}); err == nil {
			t.Fatalf("accepted invalid %s", name)
		}
		if m.state.MihomoBasics != nil {
			t.Fatalf("invalid %s updated the state", name)
		}
	}
	b := defaultMihomoBasics()
	b.DirectIPVersion = "ipv4"
	if _, _, _, err := compileMihomoBasics(b, []MihomoOutbound{{Name: basicsDirectName}}, nil); err == nil {
		t.Fatal("managed name conflict accepted")
	}
}

func TestMihomoBasicsOutboundStatsKeepQuotaAccounting(t *testing.T) {
	b := defaultMihomoBasics()
	b.OutboundUploadStatistics = true
	b.TrafficSaveSeconds = 300
	m := &CoreManager{dataDir: t.TempDir(), state: State{MihomoBasics: &b, Inbounds: []Inbound{{Name: "test", Clients: []Client{{Username: "alice"}}}}}, lastTrafficSave: time.Now()}
	snapshot := func(up, down uint64) map[string]any {
		return map[string]any{"uploadTotal": up, "downloadTotal": down, "connections": []any{map[string]any{"id": "c1", "upload": up, "download": down, "chains": []any{"node", "Pick", "node"}, "metadata": map[string]any{"inboundName": "test", "inboundUser": "alice"}}}}
	}
	m.applyTrafficSnapshot(snapshot(100, 200))
	if m.state.OutboundTraffic["node"].Up != 100 || m.state.OutboundTraffic["Pick"].Up != 100 || m.state.OutboundTraffic["node"].Down != 0 {
		t.Fatalf("outbound counters: %#v", m.state.OutboundTraffic)
	}
	if m.state.Inbounds[0].Clients[0].Traffic.Down != 200 {
		t.Fatal("outbound switch disabled client quota accounting")
	}
	previous := m.state.OutboundTraffic
	b.OutboundDownloadStatistics = true
	m.applyTrafficSnapshot(snapshot(150, 270))
	if m.state.OutboundTraffic["node"].Up != 150 || m.state.OutboundTraffic["node"].Down != 70 {
		t.Fatal("enable counted historical bytes instead of new deltas")
	}
	if previous["node"].Up != 100 {
		t.Fatal("poll mutated a published API snapshot")
	}
}

func TestMihomoBasicsLogBufferAndMask(t *testing.T) {
	b := defaultMihomoBasics()
	b.LogBufferSize = 100
	b.MaskLogAddress = true
	m := &CoreManager{state: State{MihomoBasics: &b}}
	for i := 0; i < 110; i++ {
		m.addLogLocked("[TCP] 192.168.1.2:1000 --> [2001:db8::1]:443 at 12:30:00")
	}
	if len(m.logs) != 100 {
		t.Fatalf("log limit: %d", len(m.logs))
	}
	if strings.Contains(m.logs[0], "192.168.1.2") || strings.Contains(m.logs[0], "2001:db8") || !strings.Contains(m.logs[0], "12:30:00") {
		t.Fatalf("mask: %s", m.logs[0])
	}
}

func TestMihomoBasicsDelayUsesConfiguredURL(t *testing.T) {
	const testURL = "https://example.com/probe?code=204"
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("url") != testURL || r.Header.Get("Authorization") != "Bearer test-secret" || r.URL.Path != "/proxies/DIRECT/delay" {
			t.Errorf("wrong core request: %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"delay":21}`))
	}))
	defer core.Close()
	b := defaultMihomoBasics()
	b.OutboundTestURL = testURL
	m := &CoreManager{state: State{Settings: Settings{APIAddress: strings.TrimPrefix(core.URL, "http://"), APISecret: "test-secret"}, MihomoBasics: &b}, cmd: &exec.Cmd{}}
	a := &App{manager: m, session: "session"}
	req := httptest.NewRequest(http.MethodPost, "/api/mihomo/outbound/delay", strings.NewReader(`{"name":"DIRECT"}`))
	req.AddCookie(&http.Cookie{Name: "mui_session", Value: "session"})
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"delay":21`) {
		t.Fatalf("delay response: %d %s", w.Code, w.Body.String())
	}
}

func TestMihomoBasicsNativeCoreConfig(t *testing.T) {
	core := os.Getenv("MUI_TEST_CORE")
	if core == "" {
		t.Skip("MUI_TEST_CORE not set")
	}
	state := defaultState()
	state.Inbounds = nil
	b := defaultMihomoBasics()
	b.DirectIPVersion = "ipv4-prefer"
	b.BlockIPs = []string{"private"}
	b.BlockDomains = []string{"blocked.example.com"}
	b.IPv4Domains = []string{"example.net"}
	b.TCPConcurrent = true
	b.UnifiedDelay = true
	state.MihomoBasics = &b
	m := &CoreManager{dataDir: t.TempDir(), state: state}
	data, err := m.renderConfig()
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config["tcp-concurrent"] != true || config["ipv6"] != true || config["unified-delay"] != true {
		t.Fatal("native general flags missing")
	}
	file := filepath.Join(m.dataDir, "config.yaml")
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(core, "-d", m.dataDir, "-t", "-f", file).CombinedOutput(); err != nil {
		t.Fatalf("Mihomo rejected Basics: %v\n%s", err, output)
	}
}
