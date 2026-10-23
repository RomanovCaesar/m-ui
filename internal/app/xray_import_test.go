package app

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func xrayTestPEM(t *testing.T) (string, string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "example.com"}, DNSNames: []string{"example.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

// xrayTestConfig fills the PEM placeholders of testdata/xui/config.json.
func xrayTestConfig(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("internal/app/testdata/xui/config.json")
	if err != nil {
		t.Fatal(err)
	}
	cert, key := xrayTestPEM(t)
	lines := func(text string) string {
		data, _ := json.Marshal(strings.Split(strings.TrimSpace(text), "\n"))
		return strings.TrimSuffix(strings.TrimPrefix(string(data), "["), "]")
	}
	text := strings.ReplaceAll(string(raw), `"@CERT@"`, lines(cert))
	text = strings.ReplaceAll(text, `"@KEY@"`, lines(key))
	return []byte(text)
}

func newXrayImportManager(t *testing.T) *CoreManager {
	t.Helper()
	m := newStateTestManager(t.TempDir())
	m.state = defaultState()
	m.state.Settings.SubscriptionPath = "/sub"
	m.state.Settings.PanelListen = "0.0.0.0:54321"
	// Pre-existing content that must survive the import untouched.
	m.state.Inbounds = []Inbound{
		{ID: "keep-port", Name: "mine-7000", Type: "socks", Listen: "0.0.0.0", Port: 7000, Enabled: true, TrafficReset: "never"},
		{ID: "keep-name", Name: "inbound-8443", Type: "socks", Listen: "0.0.0.0", Port: 7001, Enabled: true, TrafficReset: "never"},
	}
	warp, err := mihomoOutboundFromYAMLMap(map[string]any{"name": "warp", "type": "socks5", "server": "127.0.0.1", "port": 40000}, false)
	if err != nil {
		t.Fatal(err)
	}
	m.state.Outbounds = []MihomoOutbound{warp}
	m.state.RoutingRules = []MihomoRoutingRule{
		{ID: "r1", Type: "GEOIP", Payload: "LAN", Target: "REJECT", NoResolve: true},
		{ID: "r2", Type: "MATCH", Target: "DIRECT"},
	}
	if err := m.saveLocked(); err != nil {
		t.Fatal(err)
	}
	return m
}

func findInbound(m *CoreManager, name string) *Inbound {
	for index := range m.state.Inbounds {
		if m.state.Inbounds[index].Name == name {
			return &m.state.Inbounds[index]
		}
	}
	return nil
}

func TestXrayConfigImport(t *testing.T) {
	m := newXrayImportManager(t)
	source, err := parseXrayImportSource(xrayTestConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	result, err := m.importXray(source, "en")
	if err != nil {
		t.Fatalf("%v\n%s", err, result.Log)
	}
	t.Log(result.Log)

	// Kept: the panel's own inbounds, outbound and rules.
	if findInbound(m, "mine-7000") == nil || findInbound(m, "inbound-8443").Port != 7001 {
		t.Fatal("existing inbounds were changed")
	}
	if m.state.Outbounds[0].Name != "warp" || m.state.Outbounds[0].Type != "socks5" {
		t.Fatal("the existing outbound was changed")
	}
	last := m.state.RoutingRules[len(m.state.RoutingRules)-1]
	if last.Type != "MATCH" || last.Target != "DIRECT" || m.state.RoutingRules[0].ID != "r1" {
		t.Fatalf("existing rules were reordered: %+v", m.state.RoutingRules)
	}

	expectImported := map[string]string{
		"inbound-443": "vless", "inbound-8443-xray": "vless", "inbound-2053": "vless", "inbound-2083": "vless",
		"inbound-10086": "vmess", "inbound-10088": "vmess", "inbound-2096": "trojan", "inbound-8388": "shadowsocks",
		"inbound-8389": "shadowsocks", "inbound-1080": "mixed", "inbound-3128": "http", "inbound-4443": "hysteria2",
	}
	for name, typ := range expectImported {
		inbound := findInbound(m, name)
		if inbound == nil || inbound.Type != typ {
			t.Fatalf("inbound %s (%s) missing: %+v", name, typ, inbound)
		}
	}
	for _, skipped := range []string{"api", "inbound-10087", "inbound-8390", "inbound-9000", "inbound-51820", "inbound-7000", "panel-egress"} {
		if findInbound(m, skipped) != nil {
			t.Fatalf("inbound %s should have been skipped", skipped)
		}
	}
	if result.Inbounds != len(expectImported) {
		t.Fatalf("imported %d inbounds, want %d", result.Inbounds, len(expectImported))
	}

	reality := findInbound(m, "inbound-443")
	if !reality.Reality.Enabled || reality.Reality.Dest != "www.apple.com:443" || len(reality.Clients) != 2 {
		t.Fatalf("reality inbound: %+v", reality.Reality)
	}
	for _, client := range reality.Clients {
		if client.Flow != "xtls-rprx-vision" || client.Username != client.Name {
			t.Fatalf("client %+v", client)
		}
	}
	ws := findInbound(m, "inbound-8443-xray")
	if ws.Network != "ws" || ws.WSPath != "/vlws" || !ws.TLS || ws.CertificateMode != "content" {
		t.Fatalf("ws/tls inbound: network=%s path=%s tls=%v mode=%s", ws.Network, ws.WSPath, ws.TLS, ws.CertificateMode)
	}
	if xhttp := findInbound(m, "inbound-2053"); xhttp.Network != "xhttp" || xhttp.XHTTP.Path != "/xh" {
		t.Fatalf("xhttp inbound: %+v", xhttp.XHTTP)
	}
	if ss := findInbound(m, "inbound-8389"); ss.Cipher != "aes-256-gcm" || ss.Clients[0].Password != "judy-ss" || ss.UDP {
		t.Fatalf("shadowsocks inbound: cipher=%s udp=%v", ss.Cipher, ss.UDP)
	}
	if hy := findInbound(m, "inbound-4443"); hy.Hysteria.Obfs != "salamander" || hy.Hysteria.ObfsPassword != "obfs-pass" || len(hy.Clients) != 1 {
		t.Fatalf("hysteria inbound: %+v", hy.Hysteria)
	}

	names := map[string]MihomoOutbound{}
	for _, outbound := range m.state.Outbounds {
		names[outbound.Name] = outbound
	}
	if names["warp-xray"].Type != "wireguard" || names["hk-reality"].Type != "vless" || names["hk-socks"].Type != "socks5" {
		t.Fatalf("outbounds: %+v", names)
	}
	if names["hk-balancer"].Kind != "group" || names["hk-balancer"].Type != "url-test" {
		t.Fatalf("balancer group: %+v", names["hk-balancer"])
	}

	rules := map[string]MihomoRoutingRule{}
	for _, rule := range m.state.RoutingRules {
		rules[rule.Type+","+rule.Payload] = rule
	}
	want := map[string]string{
		"AND,((OR,((GEOSITE,openai),(DOMAIN-SUFFIX,claude.ai))),(IN-NAME,inbound-443/inbound-8443-xray))": "warp-xray",
		"IN-USER,bob@hk":             "hk-reality",
		"DST-PORT,25/465/587":        "REJECT",
		"GEOSITE,cn":                 "DIRECT",
		"DOMAIN,www.baidu.com":       "DIRECT",
		"DOMAIN-KEYWORD,qq":          "DIRECT",
		"DOMAIN-KEYWORD,example.org": "hk-balancer",
		"AND,((OR,((NOT,((GEOIP,CN,no-resolve))),(IP-CIDR,1.1.1.1/32,no-resolve))),(NETWORK,udp))": "hk-socks",
	}
	for key, target := range want {
		rule, ok := rules[key]
		if !ok || rule.Target != target {
			t.Fatalf("rule %s → %s missing; have %v", key, target, keys(rules))
		}
	}
	if _, ok := rules["GEOIP,LAN"]; !ok {
		t.Fatal("the existing GEOIP,LAN rule disappeared")
	}
	for key := range rules {
		if strings.Contains(key, "inbound-10087") || strings.Contains(key, "dns.google") || strings.Contains(key, "google@ads") {
			t.Fatalf("rule %s should have been skipped", key)
		}
	}
	for _, needle := range []string{"bittorrent", "TCP with HTTP header", "Shadowsocks 2022 with per-client keys", "HTTPUpgrade", "WireGuard server", "port 7000 is already used", "renamed", "DNS outbounds", "fallbacks"} {
		if !strings.Contains(result.Log, needle) {
			t.Fatalf("log does not mention %q", needle)
		}
	}

	if core := os.Getenv("MUI_TEST_CORE"); core != "" {
		config, err := m.renderConfig()
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		_ = os.WriteFile(path, config, 0600)
		output, err := exec.Command(core, "-t", "-d", dir, "-f", path).CombinedOutput()
		if err != nil || !strings.Contains(string(output), "test is successful") {
			t.Fatalf("mihomo -t rejected the imported config: %v\n%s\n%s", err, output, config)
		}
	}
}

func keys(m map[string]MihomoRoutingRule) []string {
	var out []string
	for key := range m {
		out = append(out, key)
	}
	return out
}

func newEmptyImportManager(t *testing.T) *CoreManager {
	t.Helper()
	m := newStateTestManager(t.TempDir())
	m.state = defaultState()
	m.state.Settings.SubscriptionPath = "/sub"
	m.state.Settings.PanelListen = "0.0.0.0:54321"
	if err := m.saveLocked(); err != nil {
		t.Fatal(err)
	}
	return m
}

func importXUIFixture(t *testing.T, name string) (*CoreManager, xrayImportResult) {
	t.Helper()
	data, err := os.ReadFile("internal/app/testdata/xui/" + name)
	if err != nil {
		t.Fatal(err)
	}
	source, err := parseXrayImportSource(data)
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != "x-ui.db" {
		t.Fatalf("source kind %q", source.Kind)
	}
	m := newEmptyImportManager(t)
	result, err := m.importXray(source, "en")
	if err != nil {
		t.Fatalf("%v\n%s", err, result.Log)
	}
	t.Log(result.Log)
	return m, result
}

func clientByName(inbound *Inbound, name string) *Client {
	for index := range inbound.Clients {
		if inbound.Clients[index].Name == name {
			return &inbound.Clients[index]
		}
	}
	return nil
}

// x-ui.db carries what config.json loses: limits, expiry, remarks, disabled
// clients, traffic and subscription ids.
func TestXUIDatabaseImportV2(t *testing.T) {
	m, result := importXUIFixture(t, "xui_v2.db")
	main := findInbound(m, "Main Reality")
	if main == nil || main.Type != "vless" || main.TrafficReset != "monthly" || !main.Reality.Enabled {
		t.Fatalf("main inbound: %+v", main)
	}
	vip := clientByName(main, "vip@main")
	if vip == nil || vip.Username != "abcdefgh12345678" || vip.Total != 107374182400 || vip.ExpiryTime != 1893456000000 ||
		!vip.Enabled || vip.Notes != "paid until 2030" || vip.Traffic.Up != 5_000_000 || vip.Traffic.Down != 7_000_000 ||
		vip.Traffic.AllTimeUp+vip.Traffic.AllTimeDown != 20_000_000 || vip.Flow != "xtls-rprx-vision" {
		t.Fatalf("vip client: %+v", vip)
	}
	trial := clientByName(main, "trial@main")
	if trial == nil || trial.ExpireAfterDays != 7 || trial.ExpiryTime != 0 || trial.RenewDays != 30 {
		t.Fatalf("trial client (expire 7 days after first use): %+v", trial)
	}
	banned := clientByName(main, "banned@main")
	if banned == nil || banned.Enabled || banned.Username != "banned@main" {
		t.Fatalf("disabled client: %+v", banned)
	}
	paused := findInbound(m, "Paused")
	if paused == nil || paused.Enabled || paused.Clients[0].Username != "abcdefgh12345678" {
		t.Fatalf("a disabled inbound must stay disabled and share the subId username: %+v", paused)
	}
	if m.state.SubscriptionTokens["abcdefgh12345678"] != "abcdefgh12345678" || m.state.SubscriptionTokens["trialsub00000001"] != "trialsub00000001" {
		t.Fatalf("subscription ids were not kept as tokens: %v", m.state.SubscriptionTokens)
	}
	var upstream bool
	for _, outbound := range m.state.Outbounds {
		upstream = upstream || (outbound.Name == "upstream" && outbound.Type == "trojan")
	}
	if !upstream {
		t.Fatal("the outbound from xrayTemplateConfig was not imported")
	}
	var found bool
	for _, rule := range m.state.RoutingRules {
		if rule.Type == "AND" && rule.Payload == "((GEOSITE,netflix),(IN-USER,abcdefgh12345678))" && rule.Target == "upstream" {
			found = true
		}
		if rule.Type == "GEOIP" && rule.NoResolve {
			t.Fatal("domainStrategy IPIfNonMatch must not add no-resolve")
		}
	}
	if !found {
		t.Fatalf("user rule missing: %+v", m.state.RoutingRules)
	}
	if !strings.Contains(result.Log, "limitIp") {
		t.Fatal("the dropped IP limit is not reported")
	}
}

func TestXUIDatabaseImportV3(t *testing.T) {
	m, result := importXUIFixture(t, "xui_v3.db")
	main := findInbound(m, "V3 Reality")
	if main == nil || main.Total != 53687091200 || main.ExpiryTime != 1900000000000 || len(main.Clients) != 2 {
		t.Fatalf("v3 inbound: %+v", main)
	}
	anna := clientByName(main, "anna")
	if anna == nil || anna.Total != 214748364800 || anna.ExpiryTime != 1950000000000 || anna.Flow != "xtls-rprx-vision" ||
		anna.Notes != "from clients table" || anna.Username != "annasub000000001" || anna.Traffic.Up != 100 {
		t.Fatalf("anna must come from the clients table: %+v", anna)
	}
	if main.TrafficReset != "monthly" || main.TrafficResetDay != 20 {
		t.Fatalf("inbound monthly reset on the 20th: %q %d", main.TrafficReset, main.TrafficResetDay)
	}
	if anna.RenewDay != 1 || anna.RenewMax != 12 || anna.RenewCount != 3 || anna.TrafficReset != "" {
		t.Fatalf("anna renews on the 1st, 3 of 12 renewals used: %+v", anna)
	}
	ben := clientByName(main, "ben")
	if ben == nil || ben.TrafficReset != "monthly" || ben.TrafficResetDay != 15 || ben.RenewDay != 0 {
		t.Fatalf("ben resets monthly on the 15th: %+v", ben)
	}
	if ben.Enabled {
		t.Fatalf("ben (disabled, only in the clients table): %+v", ben)
	}
	if findInbound(m, "On node") != nil {
		t.Fatal("an inbound of a remote node was imported")
	}
	tuic := findInbound(m, "TUIC")
	if tuic == nil || tuic.Type != "tuic" || len(tuic.Clients) != 1 || tuic.Clients[0].Name != "tina" || tuic.Clients[0].Password != "tuic-pass" {
		t.Fatalf("tuic inbound: %+v", tuic)
	}
	// 3.7+ disable_flow, 3.3+ origin_node_guid and the 3.1+ inbound_fallbacks table.
	if noFlow := findInbound(m, "No Flow"); noFlow == nil || len(noFlow.Clients) != 1 || noFlow.Clients[0].Flow != "" {
		t.Fatalf("disable_flow must drop the flow: %+v", noFlow)
	}
	if findInbound(m, "Synced") != nil {
		t.Fatal("an inbound synced from a node was imported")
	}
	if child := findInbound(m, "WS child"); child == nil || len(child.Clients) != 1 || child.Clients[0].Flow != "" {
		t.Fatalf("vision flow must not reach a ws inbound: %+v", child)
	}
	for _, needle := range []string{"remote 3x-ui node", "mtproto is a 3x-ui add-on", "/etc/x-ui/cert.pem does not exist",
		"in-443-tcp: fallbacks are not supported", "in-20001-ws: it was a fallback target of in-443-tcp",
		"anna keeps its traffic reset / renewal schedule"} {
		if !strings.Contains(result.Log, needle) {
			t.Fatalf("log does not mention %q", needle)
		}
	}
}
