package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestVPNGateManagedOutboundNormalization(t *testing.T) {
	item, err := buildVPNGateOutbound("home-jp", VPNGateConfig{Country: "jp", ISP: "SoftBank", ISPKeyword: "discarded", Slot: 0})
	if err != nil {
		t.Fatal(err)
	}
	if item.Type != "direct" || item.Kind != "proxy" || item.InterfaceName != "vpn_vpn0" || item.RoutingMark != 100 || item.IPVersion != "ipv4" {
		t.Fatalf("slot 0 was not forced into its managed shape: %#v", item)
	}
	if item.VPNGate.Country != "JP" || item.VPNGate.ISP != "softbank" || item.VPNGate.ISPKeyword != "" {
		t.Fatalf("config was not canonicalised: %#v", item.VPNGate)
	}
	config := mihomoProxyConfig(item)
	for key, want := range map[string]any{"name": "home-jp", "type": "direct", "interface-name": "vpn_vpn0", "routing-mark": 100, "ip-version": "ipv4"} {
		if got := config[key]; !reflect.DeepEqual(got, want) {
			t.Fatalf("config[%q] = %#v, want %#v", key, got, want)
		}
	}
	if _, present := config["server"]; present {
		t.Fatalf("managed direct must not have a server: %#v", config)
	}

	item.Native = map[string]any{"name": "home-jp", "type": "direct", "interface-name": "eth0", "routing-mark": 1}
	item.DialerProxy = "DIRECT"
	items, err := normalizeMihomoOutbounds([]MihomoOutbound{item})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Native != nil || items[0].InterfaceName != "vpn_vpn0" || items[0].RoutingMark != 100 || items[0].DialerProxy != "" {
		t.Fatalf("native YAML escaped VPNGate management: %#v", items[0])
	}

	last, err := buildVPNGateOutbound("home-kr", VPNGateConfig{Country: "KR", ISP: "kt", Slot: 9})
	if err != nil {
		t.Fatal(err)
	}
	if last.InterfaceName != "vpn_vpn9" || last.RoutingMark != 109 || vpnGateRouteTable(last.VPNGate.Slot) != 109 {
		t.Fatalf("slot 9 mapping is wrong: %#v", last)
	}
}

func TestVPNGateUnavailableSavedOutboundHasExplicitErrorStatus(t *testing.T) {
	item, err := buildVPNGateOutbound("offline", VPNGateConfig{Country: "JP", ISP: "kddi", Slot: 4})
	if err != nil {
		t.Fatal(err)
	}
	m := &CoreManager{dataDir: t.TempDir(), state: State{Outbounds: []MihomoOutbound{item}}}
	view := makeVPNGateView(m)
	if len(view.Statuses) != 1 || view.Statuses[0].State != "error" || view.Statuses[0].LastError == "" || view.Statuses[0].Interface != "vpn_vpn4" || view.Statuses[0].Table != 104 {
		t.Fatalf("unavailable outbound was not explicit: %#v", view.Statuses)
	}
}

func TestVPNGateManagedRoutesRecognizeLegacyBGPName(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		iface      string
		wantGuard  bool
		wantActive bool
	}{
		{name: "legacy symbolic guard", line: "unreachable default proto bgp metric 42760", iface: "vpn_vpn1", wantGuard: true},
		{name: "legacy numeric guard", line: "unreachable default proto 186 metric 42760", iface: "vpn_vpn1", wantGuard: true},
		{name: "current numeric guard", line: "unreachable default proto 242 metric 42760", iface: "vpn_vpn1", wantGuard: true},
		{name: "numeric route type guard", line: "7 default proto 242 metric 42760", iface: "vpn_vpn1", wantGuard: true},
		{name: "legacy active", line: "default via 10.211.254.254 dev vpn_vpn1 proto bgp metric 10", iface: "vpn_vpn1", wantActive: true},
		{name: "current active", line: "default via 10.211.254.254 dev vpn_vpn1 proto 242 metric 10", iface: "vpn_vpn1", wantActive: true},
		{name: "foreign protocol", line: "unreachable default proto static metric 42760", iface: "vpn_vpn1"},
		{name: "foreign interface", line: "default via 10.0.0.1 dev eth0 proto 242 metric 10", iface: "vpn_vpn1"},
		{name: "foreign metric", line: "unreachable default proto bgp metric 99", iface: "vpn_vpn1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			guard, active := vpnGateManagedRoute(test.line, test.iface)
			if guard != test.wantGuard || active != test.wantActive {
				t.Fatalf("vpnGateManagedRoute(%q) = guard %v active %v, want %v %v", test.line, guard, active, test.wantGuard, test.wantActive)
			}
		})
	}
}

func TestVPNGateAccountConnectedStatus(t *testing.T) {
	for name, test := range map[string]struct {
		output string
		want   bool
	}{
		"connected":   {output: "Connection Status | Connected\n", want: true},
		"established": {output: "Session Status | Connection Established\n", want: true},
		"completed":   {output: "Connection Status | Connection Completed\n", want: true},
		"online":      {output: "Account Status | Online\n", want: true},
		"connecting":  {output: "Connection Status | Connecting\n"},
		"offline":     {output: "Connection Status | Offline\n"},
		"negative":    {output: "Connection Status | Not Connected\n"},
		"unrelated":   {output: "Server Name | connected.example\n"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := vpnGateAccountConnected(test.output); got != test.want {
				t.Fatalf("vpnGateAccountConnected(%q) = %v, want %v", test.output, got, test.want)
			}
		})
	}
}

func TestVPNGateValidationAndLiteralMatching(t *testing.T) {
	cases := []VPNGateConfig{
		{Country: "J", ISP: "kddi", Slot: 0},
		{Country: "JP", ISP: "unknown", Slot: 0},
		{Country: "KR", ISPKeyword: "Korea Telecom", Slot: 0},
		{Country: "US", Slot: 0},
		{Country: "US", ISPKeyword: "Comcast", Slot: -1},
		{Country: "US", ISPKeyword: "Comcast", Slot: 10},
	}
	for _, input := range cases {
		copy := input
		if err := normalizeVPNGateConfig(&copy); err == nil {
			t.Fatalf("accepted invalid config: %#v", input)
		}
	}
	literal := VPNGateConfig{Country: "US", ISPKeyword: ".*", Slot: 1}
	if err := normalizeVPNGateConfig(&literal); err != nil {
		t.Fatal(err)
	}
	if vpnGateASNMatches(literal, "ordinary network") || !vpnGateASNMatches(literal, "Example .* Network") {
		t.Fatal("custom keyword must be treated as a literal substring, not a regexp")
	}
	if !vpnGateASNMatches(VPNGateConfig{Country: "KR", ISP: "kt"}, "Korea Telecom") ||
		!vpnGateASNMatches(VPNGateConfig{Country: "JP", ISP: "chubu"}, "Chubu Telecommunications Co.") {
		t.Fatal("carrier aliases did not match representative ASN names")
	}

	first, _ := buildVPNGateOutbound("first", VPNGateConfig{Country: "JP", ISP: "kddi", Slot: 2})
	second, _ := buildVPNGateOutbound("second", VPNGateConfig{Country: "KR", ISP: "sk", Slot: 2})
	if _, err := normalizeMihomoOutbounds([]MihomoOutbound{first, second}); err == nil {
		t.Fatal("duplicate VPNGate slots must be rejected")
	}
}

func TestVPNGatePersistenceAndIPInfoSecretSemantics(t *testing.T) {
	state := defaultState()
	m := &CoreManager{dataDir: t.TempDir(), state: state}
	item, err := buildVPNGateOutbound("jp-kddi", VPNGateConfig{Country: "JP", ISP: "kddi", Slot: 3})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.updateMihomoSettings(mihomoSettingsPayload{Outbounds: []MihomoOutbound{item}}); err != nil {
		t.Fatal(err)
	}
	reloaded := &CoreManager{dataDir: m.dataDir}
	if err := reloaded.load(); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.state.Outbounds) != 1 || reloaded.state.Outbounds[0].VPNGate == nil || reloaded.state.Outbounds[0].VPNGate.Slot != 3 {
		t.Fatalf("VPNGate metadata did not survive state.json: %#v", reloaded.state.Outbounds)
	}

	settings := reloaded.state.Settings
	settings.Username, settings.Password = "", ""
	settings.IPInfoToken = "private-token"
	if err := reloaded.updateSettings(settings); err != nil {
		t.Fatal(err)
	}
	masked := reloaded.state.Settings.maskSecrets()
	if masked.IPInfoToken != "" || !masked.IPInfoTokenSet || masked.Password != "" {
		t.Fatalf("settings response leaked a secret or lost its presence flag: %#v", masked)
	}
	settings = reloaded.state.Settings
	settings.Username, settings.Password, settings.IPInfoToken = "", "", ""
	if err := reloaded.updateSettings(settings); err != nil {
		t.Fatal(err)
	}
	if reloaded.state.Settings.IPInfoToken != "private-token" {
		t.Fatal("blank settings submission must preserve the saved token")
	}
	settings = reloaded.state.Settings
	settings.Username, settings.Password, settings.IPInfoToken = "", "", "replacement"
	settings.IPInfoTokenClear = true
	if err := reloaded.updateSettings(settings); err != nil {
		t.Fatal(err)
	}
	if reloaded.state.Settings.IPInfoToken != "" {
		t.Fatal("explicit clear must win over a submitted token")
	}
}

func TestVPNGateCSVParsingAndPublicAddressChecks(t *testing.T) {
	profile := func(port string) string {
		return base64.StdEncoding.EncodeToString([]byte("client\nremote 8.8.8.8 " + port + " tcp\n"))
	}
	row := func(ip, country, score, encoded string) string {
		fields := make([]string, 15)
		fields[1], fields[2], fields[6], fields[14] = ip, score, country, encoded
		return strings.Join(fields, ",")
	}
	body := "header\nheader2\n" + row("8.8.8.8", "JP", "20", profile("1443")) + "\n" +
		row("8.8.8.8", "JP", "10", profile("2443")) + "\n" +
		row("10.0.0.1", "JP", "99", profile("443")) + "\n" +
		row("1.1.1.1", "bad", "99", profile("443")) + "\n"
	nodes := parseVPNGateList(body)
	if len(nodes) != 2 || nodes[0].Port != 1443 || nodes[1].Port != 2443 {
		t.Fatalf("unexpected parsed nodes: %#v", nodes)
	}
	for _, address := range []string{"0.0.0.1", "10.0.0.1", "100.64.0.1", "192.0.2.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "224.0.0.1"} {
		if isPublicIPv4(address) {
			t.Fatalf("reserved address %s was accepted", address)
		}
	}
	if !isPublicIPv4("8.8.8.8") {
		t.Fatal("public IPv4 was rejected")
	}
	malicious := base64.StdEncoding.EncodeToString([]byte("up /tmp/payload\nremote host not-a-port\n"))
	if port := vpnGateRemotePort(malicious); port != 443 {
		t.Fatalf("invalid profile port = %d, want safe default 443", port)
	}
}

type vpnGateRoundTripFunc func(*http.Request) (*http.Response, error)

func (f vpnGateRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func vpnGateHTTPResponse(status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

func TestVPNGateASNQuotaFallbackAndTokenIsolation(t *testing.T) {
	now := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	var hosts []string
	resolver := newVPNGateASNResolver(nil)
	resolver.now = func() time.Time { return now }
	resolver.sleep = func(time.Duration) {}
	resolver.client = &http.Client{Transport: vpnGateRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		hosts = append(hosts, request.URL.Host)
		if request.URL.Host == "ipinfo.io" {
			return vpnGateHTTPResponse(http.StatusTooManyRequests, `{}`, http.Header{"Retry-After": []string{"7"}}), nil
		}
		return vpnGateHTTPResponse(http.StatusOK, `{"status":"success","as":"AS2516 KDDI CORPORATION","regionName":"Tokyo"}`, nil), nil
	})}
	value, err := resolver.Lookup(context.Background(), "8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	if value.ASN != "AS2516" || value.Name != "KDDI CORPORATION" || value.Region != "Tokyo" || !reflect.DeepEqual(hosts, []string{"ipinfo.io", "ip-api.com"}) {
		t.Fatalf("unexpected quota fallback: value=%#v hosts=%v", value, hosts)
	}
	if want := now.Add(7 * time.Second); !resolver.ipinfoUntil.Equal(want) {
		t.Fatalf("Retry-After was not respected: got %s want %s", resolver.ipinfoUntil, want)
	}

	hosts = nil
	resolver = newVPNGateASNResolver(func() string { return "secret-token" })
	resolver.now = func() time.Time { return now }
	resolver.sleep = func(time.Duration) {}
	resolver.client = &http.Client{Transport: vpnGateRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		hosts = append(hosts, request.URL.Host)
		if got := request.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Fatalf("IPinfo Authorization = %q", got)
		}
		return vpnGateHTTPResponse(http.StatusTooManyRequests, `{}`, nil), nil
	})}
	if _, err := resolver.Lookup(context.Background(), "8.8.4.4"); err == nil {
		t.Fatal("token-backed quota failure must be returned")
	}
	if !reflect.DeepEqual(hosts, []string{"ipinfo.io"}) {
		t.Fatalf("a configured token must disable ip-api fallback, hosts=%v", hosts)
	}

	hosts = nil
	resolver = newVPNGateASNResolver(nil)
	resolver.now = func() time.Time { return now }
	resolver.sleep = func(time.Duration) {}
	resolver.client = &http.Client{Transport: vpnGateRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		hosts = append(hosts, request.URL.Host)
		return vpnGateHTTPResponse(http.StatusInternalServerError, `{}`, nil), nil
	})}
	if _, err := resolver.Lookup(context.Background(), "1.1.1.1"); err == nil {
		t.Fatal("ordinary IPinfo failure must be returned")
	}
	if !reflect.DeepEqual(hosts, []string{"ipinfo.io"}) {
		t.Fatalf("ip-api may only follow an explicit quota response, hosts=%v", hosts)
	}
}

func TestVPNGateSelectionTokyoAndSameCountryFallback(t *testing.T) {
	config := VPNGateConfig{Country: "JP", ISP: "softbank", Slot: 0}
	nodes := []VPNGateNode{{IP: "8.8.8.8", Port: 443, Country: "JP"}, {IP: "8.8.4.4", Port: 443, Country: "JP"}, {IP: "1.1.1.1", Port: 443, Country: "US"}}
	task := &vpnGateTask{
		config: config,
		fetch:  func(context.Context) ([]VPNGateNode, error) { return nodes, nil },
		lookup: func(_ context.Context, ip string) (VPNGateASN, error) {
			if ip == "8.8.8.8" {
				return VPNGateASN{ASN: "AS17676", Name: "SoftBank Corp.", Region: "Osaka"}, nil
			}
			return VPNGateASN{ASN: "AS17676", Name: "SoftBank Corp.", Region: "Tokyo"}, nil
		},
		failed: make(map[string]time.Time),
	}
	node, _, fallback, err := task.pick(context.Background())
	if err != nil || fallback || node.IP != "8.8.4.4" {
		t.Fatalf("Tokyo SoftBank node was not preferred: node=%#v fallback=%v err=%v", node, fallback, err)
	}
	task.config = VPNGateConfig{Country: "JP", ISP: "kddi", Slot: 0}
	node, asn, fallback, err := task.pick(context.Background())
	if err != nil || !fallback || node.Country != "JP" || asn.Excluded() {
		t.Fatalf("same-country fallback failed: node=%#v asn=%#v fallback=%v err=%v", node, asn, fallback, err)
	}
}

type vpnGateFakeLink struct {
	mu          sync.Mutex
	connects    int
	verifies    int
	disconnects int
	reconnected chan struct{}
}

func (l *vpnGateFakeLink) Connect(context.Context, int, VPNGateNode) error {
	l.mu.Lock()
	l.connects++
	count := l.connects
	l.mu.Unlock()
	if count == 1 && l.reconnected != nil {
		close(l.reconnected)
	}
	return nil
}

func (l *vpnGateFakeLink) Verify(context.Context, int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.verifies++
	if l.verifies == 1 {
		return errors.New("probe failed")
	}
	return nil
}

func (l *vpnGateFakeLink) Disconnect(context.Context, int) error {
	l.mu.Lock()
	l.disconnects++
	l.mu.Unlock()
	return nil
}

func (*vpnGateFakeLink) Cleanup(context.Context, int) error { return nil }

func TestVPNGateHealthFailureSoftReconnectsCurrentNode(t *testing.T) {
	reconnected := make(chan struct{})
	link := &vpnGateFakeLink{reconnected: reconnected}
	task := &vpnGateTask{
		manager:        &CoreManager{state: defaultState()},
		slot:           2,
		link:           link,
		healthInterval: time.Millisecond,
		status:         VPNGateStatus{Slot: 2, State: "connected", Connected: true},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = task.hold(ctx, VPNGateNode{IP: "8.8.8.8", Port: 443, Country: "JP"}, VPNGateASN{ASN: "AS2516", Name: "KDDI"}, false)
		close(done)
	}()
	select {
	case <-reconnected:
		cancel()
	case <-time.After(time.Second):
		cancel()
		t.Fatal("health failure did not soft reconnect the current node")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("health loop did not stop after cancellation")
	}
	link.mu.Lock()
	defer link.mu.Unlock()
	if link.connects != 1 || link.verifies < 2 || link.disconnects < 1 {
		t.Fatalf("unexpected reconnect sequence: connects=%d verifies=%d disconnects=%d", link.connects, link.verifies, link.disconnects)
	}
}

func writeVPNGateTestArchive(t *testing.T, entries []tar.Header, bodies [][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.tar.gz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	for index := range entries {
		header := entries[index]
		if err := tw.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if index < len(bodies) && len(bodies[index]) > 0 {
			if _, err := tw.Write(bodies[index]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVPNGateArchiveAndAtomicDownloadLimits(t *testing.T) {
	content := []byte("licensed client")
	archive := writeVPNGateTestArchive(t, []tar.Header{
		{Name: "vpnclient/", Typeflag: tar.TypeDir, Mode: 0755},
		{Name: "vpnclient/file", Typeflag: tar.TypeReg, Mode: 0755, Size: int64(len(content))},
	}, [][]byte{nil, content})
	destination := t.TempDir()
	if err := extractVPNGateArchive(archive, destination); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(destination, "vpnclient", "file")); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("valid archive was not extracted: got=%q err=%v", got, err)
	}

	for name, header := range map[string]tar.Header{
		"traversal": {Name: "../escape", Typeflag: tar.TypeReg, Mode: 0600, Size: 1},
		"symlink":   {Name: "vpnclient/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		"setuid":    {Name: "vpnclient/setuid", Typeflag: tar.TypeReg, Mode: 04755, Size: 1},
	} {
		t.Run(name, func(t *testing.T) {
			body := []byte(nil)
			if header.Typeflag == tar.TypeReg {
				body = []byte("x")
			}
			bad := writeVPNGateTestArchive(t, []tar.Header{header}, [][]byte{body})
			if err := extractVPNGateArchive(bad, t.TempDir()); err == nil {
				t.Fatal("unsafe archive entry was accepted")
			}
		})
	}

	target := filepath.Join(t.TempDir(), "download")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeStreamAtomically(target, strings.NewReader("12345"), 0600, 4); err == nil {
		t.Fatal("oversized stream was accepted")
	}
	if got, _ := os.ReadFile(target); string(got) != "old" {
		t.Fatalf("failed atomic write damaged the old file: %q", got)
	}
}

func TestVPNGateEnglishAPIStatusTranslation(t *testing.T) {
	app := &App{manager: &CoreManager{state: State{Settings: Settings{Language: "en"}}}}
	view := app.trVPNGateView(vpngateView{
		Install:  VPNGateInstallState{Reason: "VPNGate 只支持 Linux 服务器"},
		Statuses: []VPNGateStatus{{LastError: "IPinfo 查询额度已用尽"}},
	})
	if view.Install.Reason != "VPNGate is supported only on Linux servers" || view.Statuses[0].LastError != "The IPinfo query quota is exhausted" {
		t.Fatalf("VPNGate status was not translated: %#v", view)
	}
}
