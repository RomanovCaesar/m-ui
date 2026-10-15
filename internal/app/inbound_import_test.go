package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseMihomoInboundYAMLWithReality(t *testing.T) {
	keys, err := generateX25519KeyPair()
	if err != nil {
		t.Fatal(err)
	}
	source := `- name: vless-reality-in-21313
  type: vless
  port: 21313
  listen: 0.0.0.0
  users:
    - uuid: e3832839-84fa-4b59-aead-bd3534246c61
      flow: xtls-rprx-vision
  reality-config:
    dest: hk.art.museum:443
    private-key: ` + keys.PrivateKey + `
    server-names:
      - hk.art.museum
    short-id:
      - 20220701
`
	inbound, err := parseMihomoInboundYAML(source)
	if err != nil {
		t.Fatal(err)
	}
	if inbound.Name != "vless-reality-in-21313" || inbound.Type != "vless" || inbound.Port != 21313 || inbound.Listen != "0.0.0.0" {
		t.Fatalf("identity was not imported: %#v", inbound)
	}
	if len(inbound.Clients) != 1 || inbound.Clients[0].UUID != "e3832839-84fa-4b59-aead-bd3534246c61" || inbound.Clients[0].Flow != "xtls-rprx-vision" {
		t.Fatalf("VLESS client was not imported: %#v", inbound.Clients)
	}
	if !inbound.Reality.Enabled || inbound.Reality.Dest != "hk.art.museum:443" || inbound.Reality.PrivateKey != keys.PrivateKey || inbound.Reality.ServerNames != "hk.art.museum" || inbound.Reality.ShortIDs != "20220701" {
		t.Fatalf("Reality settings were not imported: %#v", inbound.Reality)
	}
	if inbound.Native["reality-config"] == nil {
		t.Fatal("the original native Reality block was not retained")
	}
	rendered, err := inboundConfig(inbound)
	if err != nil {
		t.Fatal(err)
	}
	if rendered["name"] != inbound.Native["name"] || rendered["type"] != "vless" {
		t.Fatalf("native config changed during import: %#v", rendered)
	}
}

func TestInboundYAMLImportEndpointPersistsInbound(t *testing.T) {
	source := `name: imported-vless
type: vless
listen: 0.0.0.0
port: 21314
users:
  - uuid: e3832839-84fa-4b59-aead-bd3534246c61
`
	m := &CoreManager{dataDir: t.TempDir(), state: defaultState()}
	a := &App{manager: m, session: "session"}
	encoded, err := json.Marshal(map[string]string{"yaml": source})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/inbounds/import", strings.NewReader(string(encoded)))
	request.AddCookie(&http.Cookie{Name: "mui_session", Value: "session"})
	response := httptest.NewRecorder()
	a.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("import response: %d %s", response.Code, response.Body.String())
	}
	if len(m.state.Inbounds) != 1 || m.state.Inbounds[0].Name != "imported-vless" || m.state.Inbounds[0].Native == nil {
		t.Fatalf("imported state: %#v", m.state.Inbounds)
	}
	config, err := m.renderConfig()
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = yaml.Unmarshal(config, &document); err != nil {
		t.Fatal(err)
	}
	if _, ok := document["listeners"]; !ok || !strings.Contains(string(config), "imported-vless") {
		t.Fatalf("imported listener missing from rendered config: %s", config)
	}
}

func TestInboundYAMLImportEndpointAcceptsRealityListener(t *testing.T) {
	keys, err := generateX25519KeyPair()
	if err != nil {
		t.Fatal(err)
	}
	source := `- name: reality-import
  type: vless
  port: 21316
  listen: 0.0.0.0
  users:
    - uuid: e3832839-84fa-4b59-aead-bd3534246c61
      flow: xtls-rprx-vision
  reality-config:
    dest: hk.art.museum:443
    private-key: ` + keys.PrivateKey + `
    server-names: [hk.art.museum]
    short-id: [20220701]
`
	m := &CoreManager{dataDir: t.TempDir(), state: defaultState()}
	a := &App{manager: m, session: "session"}
	encoded, err := json.Marshal(map[string]string{"yaml": source})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/inbounds/import", strings.NewReader(string(encoded)))
	request.AddCookie(&http.Cookie{Name: "mui_session", Value: "session"})
	response := httptest.NewRecorder()
	a.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("Reality import response: %d %s", response.Code, response.Body.String())
	}
	config, err := m.renderConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "reality-import") || !strings.Contains(string(config), keys.PrivateKey) {
		t.Fatalf("Reality listener was not retained: %s", config)
	}
}

func TestAvailableInboundPortAvoidsReservedPorts(t *testing.T) {
	m := &CoreManager{state: defaultState()}
	m.state.Settings.PanelListen = "127.0.0.1:2053"
	m.state.Settings.APIAddress = "127.0.0.1:9093"
	m.state.Settings.SubscriptionPort = 2096
	m.state.Inbounds = []Inbound{{Port: 21315}}
	port, err := m.availableInboundPort()
	if err != nil {
		t.Fatal(err)
	}
	if port < minimumRandomInboundPort || port > maximumInboundPort || containsInt([]int{2053, 9093, 2096, 21315}, port) {
		t.Fatalf("bad available port %d", port)
	}
}

func TestDefaultStateStartsWithoutMixedInbound(t *testing.T) {
	state := defaultState()
	if len(state.Inbounds) != 0 {
		t.Fatalf("default state contains %d inbounds, want none: %#v", len(state.Inbounds), state.Inbounds)
	}
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// Mihomo turns `udp` on for mixed/socks/shadowsocks/snell when the key is missing, so an
// unchecked box has to reach the core as an explicit false, and an imported listener without
// the key has to come in with UDP on.
func TestInboundUDPFollowsMihomoDefault(t *testing.T) {
	for _, typ := range []string{"mixed", "socks", "shadowsocks", "snell"} {
		for _, udp := range []bool{true, false} {
			inbound := Inbound{Name: typ, Type: typ, Listen: "0.0.0.0", Port: 18000, Enabled: true, UDP: udp, Cipher: "aes-256-gcm",
				Clients: []Client{{Name: "a", Username: "a", Password: "secret", Enabled: true}}}
			config, err := inboundConfig(inbound)
			if err != nil {
				t.Fatalf("%s: %v", typ, err)
			}
			if got, ok := config["udp"]; !ok || got != udp {
				t.Fatalf("%s udp=%v rendered as %v (present %v)", typ, udp, got, ok)
			}
		}
	}
	config, err := inboundConfig(Inbound{Name: "vless", Type: "vless", Listen: "0.0.0.0", Port: 18001, Enabled: true, UDP: false,
		Clients: []Client{{Name: "a", UUID: "e3832839-84fa-4b59-aead-bd3534246c61", Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := config["udp"]; ok {
		t.Fatalf("vless has no udp option, got %#v", config["udp"])
	}

	for _, tc := range []struct {
		yaml string
		want bool
	}{
		{"name: ss\ntype: shadowsocks\nport: 18002\nlisten: 0.0.0.0\ncipher: aes-256-gcm\npassword: secret\n", true},
		{"name: ss\ntype: shadowsocks\nport: 18002\nlisten: 0.0.0.0\ncipher: aes-256-gcm\npassword: secret\nudp: false\n", false},
		{"name: sn\ntype: snell\nport: 18003\nlisten: 0.0.0.0\npsk: secret\nversion: 4\n", true},
		{"name: sn\ntype: snell\nport: 18003\nlisten: 0.0.0.0\npsk: secret\nversion: 2\n", false},
		{"name: mx\ntype: mixed\nport: 18004\nlisten: 0.0.0.0\n", true},
	} {
		inbound, err := parseMihomoInboundYAML(tc.yaml)
		if err != nil {
			t.Fatalf("%q: %v", tc.yaml, err)
		}
		if inbound.UDP != tc.want {
			t.Fatalf("%q imported UDP=%v, want %v", tc.yaml, inbound.UDP, tc.want)
		}
	}
}
