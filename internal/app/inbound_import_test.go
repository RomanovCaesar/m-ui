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
