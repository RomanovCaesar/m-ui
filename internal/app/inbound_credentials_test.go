package app

import (
	"reflect"
	"strings"
	"testing"
)

func importTestInbound(t *testing.T, m *CoreManager, yaml string) {
	t.Helper()
	inbound, err := parseMihomoInboundYAML(yaml)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.saveInbound(inbound); err != nil {
		t.Fatal(err)
	}
}

func renderedUsers(t *testing.T, inbound Inbound) []map[string]any {
	t.Helper()
	config, err := inboundConfig(inbound)
	if err != nil {
		t.Fatal(err)
	}
	users, ok := config["users"].([]map[string]any)
	if !ok {
		t.Fatalf("users = %#v", config["users"])
	}
	return users
}

// FRA1, 2026-11-24: inbounds imported from YAML without usernames counted
// inbound traffic but none for their clients, and client edits never reached
// the config.
func TestImportedInboundFollowsItsClients(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir(), state: defaultState()}
	const first, second = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	importTestInbound(t, m, "name: imported\ntype: vless\nport: 21001\nlisten: 127.0.0.1\nallow-insecure: true\nusers:\n  - uuid: "+first+"\n    flow: xtls-rprx-vision\n    custom-option: kept\n")
	id := m.state.Inbounds[0].ID

	users := renderedUsers(t, m.state.Inbounds[0])
	if len(users) != 1 || users[0]["username"] == nil || !clientMatchesUser(m.state.Inbounds[0].Clients[0], users[0]["username"].(string)) {
		t.Fatalf("the core cannot report this client: %v", users)
	}
	alice := m.state.Inbounds[0].Clients[0]
	alice.Name, alice.Username = "alice", "alice"
	if err := m.saveClient(id, alice); err != nil {
		t.Fatal(err)
	}
	if err := m.saveClient(id, Client{ID: "b", Name: "bob", Username: "bob", UUID: second, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	users = renderedUsers(t, m.state.Inbounds[0])
	if len(users) != 2 || users[0]["username"] != "alice" || users[0]["uuid"] != first || users[0]["flow"] != "xtls-rprx-vision" || users[1]["username"] != "bob" {
		t.Fatalf("users after rename and add = %v", users)
	}
	if users[0]["custom-option"] != "kept" {
		t.Fatalf("an imported per-user option was lost: %v", users[0])
	}

	// Disabled and used-up clients are cut off.
	alice.Enabled = false
	if err := m.saveClient(id, alice); err != nil {
		t.Fatal(err)
	}
	if users = renderedUsers(t, m.state.Inbounds[0]); len(users) != 1 || users[0]["username"] != "bob" {
		t.Fatalf("a disabled client can still connect: %v", users)
	}
	m.state.Inbounds[0].Clients[1].Total, m.state.Inbounds[0].Clients[1].Traffic.Up = 10, 10
	if config, err := renderStateConfig(m.state); err != nil || strings.Contains(string(config), second) || strings.Contains(string(config), first) {
		t.Fatalf("an inbound whose clients are all cut off was rendered (err %v)", err)
	}
	// Deleting the last client must not bring the imported entry back.
	m.state.Inbounds[0].Clients = nil
	if config, err := renderStateConfig(m.state); err != nil || strings.Contains(string(config), first) {
		t.Fatalf("a deleted client came back (err %v)", err)
	}
}

func TestImportedShadowsocksFollowsItsClients(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir(), state: defaultState()}
	importTestInbound(t, m, "name: ss\ntype: shadowsocks\nport: 21002\nlisten: 127.0.0.1\ncipher: aes-256-gcm\npassword: one-pass\n")
	if err := m.saveClient(m.state.Inbounds[0].ID, Client{ID: "b", Name: "bob", Password: "bob-pass", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	config, err := inboundConfig(m.state.Inbounds[0])
	if err != nil {
		t.Fatal(err)
	}
	users, _ := config["users"].(map[string]any)
	if _, ok := config["password"]; ok || len(users) != 2 || users["bob"] != "bob-pass" {
		t.Fatalf("imported shadowsocks listener = %v", config)
	}
}

// Nobody allowed means no listener: no placeholder user, no open proxy.
func TestInboundWithoutAllowedClientsIsNotRendered(t *testing.T) {
	for _, inbound := range []Inbound{
		{Type: "vless", Clients: []Client{{Name: "a", UUID: "u"}}},
		{Type: "vmess", Clients: []Client{{Name: "a", UUID: "u"}}},
		{Type: "trojan", Clients: []Client{{Name: "a", Password: "p"}}},
		{Type: "hysteria2", Clients: []Client{{Name: "a", Password: "p"}}},
		{Type: "tuic", Clients: []Client{{Name: "a", UUID: "u", Password: "p"}}},
		{Type: "anytls", Clients: []Client{{Name: "a", Password: "p"}}},
		{Type: "mieru", Clients: []Client{{Name: "a", Password: "p"}}},
		{Type: "mixed", SimpleAuthEnabled: true, SimpleAuthConfigured: true, Clients: []Client{{Name: "a", Password: "p"}}},
		{Type: "vless"},
	} {
		if !inboundLocksEveryoneOut(inbound) {
			t.Errorf("%s with no allowed client would be rendered", inbound.Type)
		}
	}
	for _, inbound := range []Inbound{
		{Type: "mixed"},
		{Type: "mixed", SimpleAuthConfigured: true, Clients: []Client{{Name: "a", Password: "p"}}},
		{Type: "redir"},
		{Type: "hysteria2-realm"},
		{Type: "tuic", Native: map[string]any{"token": []any{"t"}}},
		{Type: "vless", Clients: []Client{{Name: "a", UUID: "u", Enabled: true}}},
	} {
		if inboundLocksEveryoneOut(inbound) {
			t.Errorf("%+v must stay in the config", inbound)
		}
	}
}

// One failing inbound fails the whole config, so the overlay must work for
// every protocol that can be imported, and must match what the form renders.
func TestInboundCredentialsCoverEveryProtocol(t *testing.T) {
	client := Client{Name: "alice", Username: "alice", Password: "secret", UUID: "11111111-1111-4111-8111-111111111111", Enabled: true}
	for _, inboundType := range supportedInboundTypes {
		inbound := Inbound{Name: "in", Type: inboundType, Port: 1, Cipher: "aes-256-gcm", SimpleAuthEnabled: true, SimpleAuthConfigured: true, Clients: []Client{client},
			Native: map[string]any{"name": "in", "type": inboundType, "port": 1, "users": []any{}, "unknown-option": "kept"}}
		native, err := inboundConfig(inbound)
		if err != nil {
			t.Errorf("%s: %v", inboundType, err)
			continue
		}
		if native["unknown-option"] != "kept" {
			t.Errorf("%s lost an imported option", inboundType)
		}
		for _, key := range inboundCredentialKeys(inboundType) {
			inbound.Native = nil
			managed, err := managedInboundConfig(inbound)
			if err != nil {
				t.Errorf("%s: %v", inboundType, err)
				break
			}
			if !reflect.DeepEqual(native[key], managed[key]) {
				t.Errorf("%s %s = %v, want %v", inboundType, key, native[key], managed[key])
			}
		}
	}
}
