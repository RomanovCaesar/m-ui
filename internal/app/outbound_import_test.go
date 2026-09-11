package app

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const nativeImportFixture = `name: native-vless
type: vless
server: example.com
port: 443
uuid: 00000000-0000-0000-0000-000000000001
tls: true
udp: true
network: ws
servername: tls.example.com
client-fingerprint: chrome
reality-opts:
  public-key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
  short-id: "1234"
ws-opts:
  path: /ws
  headers:
    Host: ws.example.com
    X-Test: retained
  max-early-data: 2048
smux:
  enabled: false
`

func TestOutboundImportNativeRoundTrip(t *testing.T) {
	items, output, err := convertOutboundInput(mihomoYAMLParseRequest{YAML: nativeImportFixture})
	if err != nil {
		t.Fatal(err)
	}
	var expected, actual map[string]any
	if err = yaml.Unmarshal([]byte(nativeImportFixture), &expected); err != nil {
		t.Fatal(err)
	}
	if err = yaml.Unmarshal(output, &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("native configuration changed:\n%s", output)
	}
	manager := &CoreManager{dataDir: t.TempDir(), state: defaultState()}
	if err = manager.updateMihomoSettings(mihomoSettingsPayload{Outbounds: items}); err != nil {
		t.Fatal(err)
	}
	loaded := &CoreManager{dataDir: manager.dataDir}
	if err = loaded.load(); err != nil {
		t.Fatal(err)
	}
	config, err := loaded.renderConfig()
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = yaml.Unmarshal(config, &document); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expected, document["proxies"].([]any)[0]) {
		t.Fatalf("persistence lost native options:\n%s", config)
	}
}

func TestOutboundImportGroupsAndDraftReferences(t *testing.T) {
	context, _, err := convertOutboundInput(mihomoYAMLParseRequest{YAML: nativeImportFixture})
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		"name: Pick\ntype: select\nproxies: [native-vless, DIRECT]\n",
		"- name: Pick\n  type: select\n  proxies: [native-vless, DIRECT]\n",
		"proxy-groups:\n- name: Pick\n  type: select\n  proxies: [native-vless, DIRECT]\n",
	} {
		items, output, err := convertOutboundInput(mihomoYAMLParseRequest{YAML: source, Context: context})
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 || items[0].Kind != "group" || !strings.Contains(string(output), "native-vless") {
			t.Fatalf("bad group import: %#v", items)
		}
	}
	group, _, err := convertOutboundInput(mihomoYAMLParseRequest{YAML: "name: Pick\ntype: select\nproxies: [native-vless, DIRECT]\ndefault-selected: native-vless\n", Context: context})
	if err != nil {
		t.Fatal(err)
	}
	context = append(context, group...)
	renamed := strings.Replace(nativeImportFixture, "name: native-vless", "name: renamed", 1)
	items, _, err := convertOutboundInput(mihomoYAMLParseRequest{YAML: renamed, Context: context, EditingID: context[0].ID})
	if err != nil || items[0].ID != context[0].ID {
		t.Fatalf("rename: %v", err)
	}
	if context[1].Proxies[0] != "native-vless" {
		t.Fatal("preview mutated existing draft")
	}
}

func TestOutboundImportRejectsMalformedAndDuplicateData(t *testing.T) {
	cases := []string{
		"", "42", "[]", "proxies: []", "name: [one]\ntype: ss", "name: a\nname: b\ntype: ss",
		nativeImportFixture + "---\nname: ignored\ntype: direct",
		strings.Replace(nativeImportFixture, "port: 443", "port: 4.5", 1),
		strings.Replace(nativeImportFixture, "udp: true", "udp: maybe", 1),
		"name: Group\ntype: select\nproxies: [missing]",
		"proxy-groups:\n- name: a\n  type: select\n  proxies: [b]\n- name: b\n  type: select\n  proxies: [a]",
		"proxies: []\nrules: [MATCH,DIRECT]",
	}
	for _, source := range cases {
		if _, _, err := convertOutboundInput(mihomoYAMLParseRequest{YAML: source}); err == nil {
			t.Fatalf("accepted invalid input %q", source)
		}
	}
	items, _, err := convertOutboundInput(mihomoYAMLParseRequest{YAML: nativeImportFixture})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = convertOutboundInput(mihomoYAMLParseRequest{YAML: nativeImportFixture, Context: items}); err == nil {
		t.Fatal("duplicate name accepted")
	}
}

func TestOutboundFormKeepsNativeOptions(t *testing.T) {
	items, _, err := convertOutboundInput(mihomoYAMLParseRequest{YAML: nativeImportFixture})
	if err != nil {
		t.Fatal(err)
	}
	item := items[0]
	item.Server, item.WSPath = "new.example.com", "/new"
	_, output, err := convertOutboundInput(mihomoYAMLParseRequest{Outbound: &item})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseMihomoOutboundYAML(string(output))
	if err != nil {
		t.Fatal(err)
	}
	got := parsed[0]
	if got.Server != "new.example.com" || got.WSPath != "/new" || !got.Reality || !got.TLS {
		t.Fatalf("form edits not applied: %s", output)
	}
	if !strings.Contains(string(output), "X-Test") || !strings.Contains(string(output), "max-early-data: 2048") || !strings.Contains(string(output), "smux:") {
		t.Fatalf("form lost advanced fields: %s", output)
	}
}

func TestOutboundPreviewAllowsIncompleteDraftButApplyRejectsIt(t *testing.T) {
	for _, source := range []string{
		"name: ''\ntype: ss\nserver: ''\nport: 0\npassword: ''\ncipher: aes-256-gcm\n",
		"name: partial\ntype: vless\nserver: example.com\nport: 443\nuuid: not-a-uuid\n",
		"name: partial\ntype: select\nproxies: [missing]\n",
		"name: partial\ntype: ss\nserver: example.com\nport: -1\n",
	} {
		t.Run(source, func(t *testing.T) {
			items, originalYAML, err := convertOutboundInput(mihomoYAMLParseRequest{YAML: source, Preview: true})
			if err != nil || len(items) != 1 {
				t.Fatalf("preview rejected incomplete draft: %v", err)
			}
			if string(originalYAML) != source {
				t.Fatal("preview rewrote the YAML draft")
			}
			_, exported, err := convertOutboundInput(mihomoYAMLParseRequest{Outbound: &items[0]})
			if err != nil {
				t.Fatalf("draft could not return to YAML: %v", err)
			}
			if _, _, err := convertOutboundInput(mihomoYAMLParseRequest{YAML: string(exported)}); err == nil {
				t.Fatal("Apply accepted incomplete draft")
			}
		})
	}
}

func TestOutboundPreviewCanCompleteBlankFormWithNativeFields(t *testing.T) {
	source := "type: ss\nname: ''\nserver: ''\nport: 443\ncipher: aes-256-gcm\npassword: ''\nudp-over-tcp: true\n"
	items, _, err := convertOutboundInput(mihomoYAMLParseRequest{YAML: source, Preview: true})
	if err != nil {
		t.Fatal(err)
	}
	item := items[0]
	item.Name, item.Server, item.Password = "completed", "example.com", "secret"
	_, exported, err := convertOutboundInput(mihomoYAMLParseRequest{Outbound: &item})
	if err != nil {
		t.Fatal(err)
	}
	items, _, err = convertOutboundInput(mihomoYAMLParseRequest{YAML: string(exported)})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Native["udp-over-tcp"] != true || items[0].Name != "completed" {
		t.Fatalf("completion lost draft data: %s", exported)
	}
}

func TestOutboundShareLinks(t *testing.T) {
	vmess := base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"vmess ws","add":"example.com","port":"443","id":"00000000-0000-0000-0000-000000000001","aid":0,"net":"ws","path":"/ws","host":"cdn.example.com","tls":"tls"}`))
	cases := []struct{ link, kind, name, password string }{
		{"ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:p:a:ss")) + "@example.com:443#SS", "ss", "SS", "p:a:ss"},
		{"ss://" + base64.RawStdEncoding.EncodeToString([]byte("aes-256-gcm:pass@example.com:443")) + "#Legacy", "ss", "Legacy", "pass"},
		{"socks5://user:p%3Aa%40ss@example.com:1080#SOCKS", "socks5", "SOCKS", "p:a@ss"},
		{"https://user:pass@example.com:443#HTTP", "http", "HTTP", "pass"},
		{"vmess://" + vmess, "vmess", "vmess ws", ""},
		{"vless://00000000-0000-0000-0000-000000000001@[2001:db8::1]:443?security=reality&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=1234&sni=example.com&type=grpc&serviceName=svc#Reality", "vless", "Reality", ""},
		{"trojan://p%3Ass@example.com:443?type=ws&path=%2Fws&host=cdn.example.com#Trojan", "trojan", "Trojan", "p:ss"},
		{"hy2://p%3Ass@example.com:443?sni=example.com&insecure=1#Hy2", "hysteria2", "Hy2", "p:ss"},
		{"tuic://00000000-0000-0000-0000-000000000001:pass@example.com:443?congestion_control=bbr#TUIC", "tuic", "TUIC", "pass"},
	}
	for _, tc := range cases {
		t.Run(tc.kind+tc.name, func(t *testing.T) {
			items, output, err := convertOutboundInput(mihomoYAMLParseRequest{Link: tc.link})
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].Type != tc.kind || items[0].Name != tc.name || items[0].Password != tc.password {
				t.Fatalf("bad conversion: %s", output)
			}
			if tc.name == "Reality" && (!items[0].TLS || !strings.Contains(string(output), "reality-opts:")) {
				t.Fatalf("missing Reality TLS: %s", output)
			}
		})
	}
	for _, link := range []string{"invalid", "vmess://invalid", "vless://", "ss://", "https://example.com/subscription", "unknown://example.com:443"} {
		if _, _, err := convertOutboundInput(mihomoYAMLParseRequest{Link: link}); err == nil {
			t.Fatalf("invalid link accepted: %s", link)
		}
	}
}

func TestOutboundImportAPIAuthAndNoMutation(t *testing.T) {
	manager := &CoreManager{dataDir: t.TempDir(), state: defaultState()}
	app := &App{manager: manager, session: "test-session"}
	data, _ := json.Marshal(mihomoYAMLParseRequest{YAML: nativeImportFixture})
	for _, authorized := range []bool{false, true} {
		req := httptest.NewRequest(http.MethodPost, "/api/mihomo/outbound/parse", strings.NewReader(string(data)))
		if authorized {
			req.AddCookie(&http.Cookie{Name: "mui_session", Value: "test-session"})
		}
		response := httptest.NewRecorder()
		app.routes().ServeHTTP(response, req)
		want := http.StatusUnauthorized
		if authorized {
			want = http.StatusOK
		}
		if response.Code != want {
			t.Fatalf("status %d: %s", response.Code, response.Body.String())
		}
	}
	if len(manager.state.Outbounds) != 0 {
		t.Fatal("parse endpoint persisted an outbound")
	}
}

func TestImportedOutboundConfigWithMihomo(t *testing.T) {
	core := os.Getenv("MUI_TEST_CORE")
	if core == "" {
		t.Skip("MUI_TEST_CORE is not set")
	}
	items, _, err := convertOutboundInput(mihomoYAMLParseRequest{Link: "vless://00000000-0000-0000-0000-000000000001@example.com:443?security=reality&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=1234&sni=example.com&type=tcp#Native"})
	if err != nil {
		t.Fatal(err)
	}
	state := defaultState()
	state.Inbounds = nil
	state.Outbounds = items
	manager := &CoreManager{dataDir: t.TempDir(), state: state}
	data, err := manager.renderConfig()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(manager.dataDir, "config.yaml")
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(core, "-d", manager.dataDir, "-t", "-f", file)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Mihomo parse: %v\n%s", err, output)
	}
}
