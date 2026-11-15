package app

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

func ss2022Key(t *testing.T, cipher string) string {
	t.Helper()
	key, err := randomShadowsocks2022Key(cipher)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestShadowsocksLegacyAEADUsersListener(t *testing.T) {
	inbound := Inbound{Type: "shadowsocks", Name: "ss-aead", Cipher: "aes-256-gcm", Clients: []Client{
		{Name: "alice", Password: "alice-pass", Enabled: true},
		{Name: "bob", Password: "bob-pass", Enabled: true},
		{Name: "carol", Password: "carol-pass", Enabled: false},
	}}
	if err := normalizeShadowsocksInbound(&inbound); err != nil {
		t.Fatal(err)
	}
	if err := validateInbound(inbound); err != nil {
		t.Fatal(err)
	}
	config, err := inboundConfig(inbound)
	if err != nil {
		t.Fatal(err)
	}
	users, _ := config["users"].(map[string]any)
	if len(users) != 2 || users["alice"] != "alice-pass" || users["bob"] != "bob-pass" {
		t.Fatalf("users = %#v, want the two enabled clients", config["users"])
	}
	if _, ok := config["password"]; ok {
		t.Fatalf("a legacy AEAD users listener must not set password: %#v", config["password"])
	}
	// Legacy AEAD clients need nothing new: their links keep the plain password.
	if got := shadowsocksClientPassword(inbound, inbound.Clients[0]); got != "alice-pass" {
		t.Fatalf("client password = %q, want alice-pass", got)
	}
}

func TestShadowsocks2022SecondClientGetsServerPSK(t *testing.T) {
	const cipher = "2022-blake3-aes-256-gcm"
	m := &CoreManager{dataDir: t.TempDir(), state: State{Settings: Settings{Username: "admin", Password: "hash"}}}
	first := ss2022Key(t, cipher)
	if err := m.saveInbound(Inbound{ID: "ss", Name: "ss-2022", Type: "shadowsocks", Listen: "0.0.0.0", Port: 19082, Enabled: true, UDP: true, Cipher: cipher,
		Clients: []Client{{ID: "c1", Name: "alice", Password: first, Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	if got := m.state.Inbounds[0]; got.Password != "" || shadowsocksUsersMode(got) {
		t.Fatalf("a single 2022 client must stay in the single-user form: %+v", got)
	}
	second := ss2022Key(t, cipher)
	if err := m.saveClient("ss", Client{ID: "c2", Name: "bob", Password: second, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	inbound := m.state.Inbounds[0]
	if !validShadowsocks2022Key(cipher, inbound.Password) || !shadowsocksUsersMode(inbound) || len(inbound.Clients) != 2 {
		t.Fatalf("second client did not switch to multi-user: %+v", inbound)
	}
	config, err := inboundConfig(inbound)
	if err != nil {
		t.Fatal(err)
	}
	users, _ := config["users"].(map[string]any)
	if config["password"] != inbound.Password || users["alice"] != first || users["bob"] != second {
		t.Fatalf("multi-user 2022 listener = %#v", config)
	}

	// Every client format carries "iPSK:uPSK".
	want := inbound.Password + ":" + second
	bob := inbound.Clients[1]
	link := subscriptionShareURL(inbound, bob, "example.com")
	encoded := strings.TrimPrefix(link[:strings.Index(link, "@")], "ss://")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(decoded) != cipher+":"+want {
		t.Fatalf("ss:// link = %s (decoded %q)", link, decoded)
	}
	if proxy := subscriptionProxy(inbound, bob, "example.com"); proxy["password"] != want {
		t.Fatalf("Clash proxy password = %v, want %s", proxy["password"], want)
	}
	if out := singboxOutbound(inbound, bob, "example.com"); out["password"] != want {
		t.Fatalf("sing-box password = %v, want %s", out["password"], want)
	}

	// The server PSK stays once set, so links do not change back and forth.
	if err := m.deleteClient("ss", "c2"); err != nil {
		t.Fatal(err)
	}
	if inbound := m.state.Inbounds[0]; !shadowsocksUsersMode(inbound) || len(inbound.Clients) != 1 {
		t.Fatalf("deleting down to one client left multi-user mode: %+v", inbound)
	}
	if err := m.deleteClient("ss", "c1"); err == nil {
		t.Fatal("the last Shadowsocks client was deleted")
	}

	// The state survives a reload unchanged.
	reloaded := &CoreManager{dataDir: m.dataDir}
	if err := reloaded.load(); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.state.Inbounds[0]; got.Password != m.state.Inbounds[0].Password || len(got.Clients) != 1 {
		t.Fatalf("reloaded inbound = %+v", got)
	}
}

func TestShadowsocksUsersValidation(t *testing.T) {
	const cipher = "2022-blake3-aes-128-gcm"
	psk, a, b := ss2022Key(t, cipher), ss2022Key(t, cipher), ss2022Key(t, cipher)
	base := func(clients ...Client) Inbound {
		return Inbound{Type: "shadowsocks", Cipher: cipher, Password: psk, Clients: clients}
	}
	cases := map[string]Inbound{
		"short user key":      base(Client{Name: "a", Password: ss2022Key(t, "2022-blake3-aes-256-gcm"), Enabled: true}, Client{Name: "b", Password: b, Enabled: true}),
		"not base64":          base(Client{Name: "a", Password: "plain text", Enabled: true}, Client{Name: "b", Password: b, Enabled: true}),
		"duplicate key":       base(Client{Name: "a", Password: a, Enabled: true}, Client{Name: "b", Password: a, Enabled: true}),
		"user key = server":   base(Client{Name: "a", Password: psk, Enabled: true}, Client{Name: "b", Password: b, Enabled: true}),
		"bad server psk":      {Type: "shadowsocks", Cipher: cipher, Password: "nope", Clients: []Client{{Name: "a", Password: a, Enabled: true}, {Name: "b", Password: b, Enabled: true}}},
		"chacha cannot users": {Type: "shadowsocks", Cipher: "2022-blake3-chacha20-poly1305", Clients: []Client{{Name: "a", Password: a, Enabled: true}, {Name: "b", Password: b, Enabled: true}}},
		"legacy duplicate":    {Type: "shadowsocks", Cipher: "aes-128-gcm", Clients: []Client{{Name: "a", Password: "same", Enabled: true}, {Name: "b", Password: "same", Enabled: true}}},
		"no enabled client":   {Type: "shadowsocks", Cipher: "aes-128-gcm", Clients: []Client{{Name: "a", Password: "x", Enabled: false}, {Name: "b", Password: "y", Enabled: false}}},
	}
	for name, inbound := range cases {
		if err := validateInbound(inbound); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := validateInbound(base(Client{Name: "a", Password: a, Enabled: true}, Client{Name: "b", Password: b, Enabled: true})); err != nil {
		t.Fatalf("valid multi-user inbound: %v", err)
	}
}

// A multi-user listener whose users are all disabled would fall back to the
// server PSK alone, which every user's link contains; it is not rendered.
func TestShadowsocksUsersListenerSkippedWithoutEnabledClients(t *testing.T) {
	const cipher = "2022-blake3-aes-256-gcm"
	state := defaultState()
	state.Inbounds = []Inbound{{ID: "ss", Name: "ss-off", Type: "shadowsocks", Listen: "0.0.0.0", Port: 19083, Enabled: true, Cipher: cipher, Password: ss2022Key(t, cipher), Clients: []Client{
		{Name: "a", Password: ss2022Key(t, cipher), Enabled: false},
		{Name: "b", Password: ss2022Key(t, cipher), Enabled: true, Total: 10, Traffic: TrafficStats{Up: 10}},
	}}}
	data, err := renderStateConfig(state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "ss-off") {
		t.Fatalf("a multi-user listener without an enabled user was rendered:\n%s", data)
	}
}

func TestShadowsocksUsersNeedNewCore(t *testing.T) {
	multi := []Inbound{{Name: "ss", Type: "shadowsocks", Cipher: "aes-128-gcm", Clients: []Client{{Name: "a", Password: "x"}, {Name: "b", Password: "y"}}}}
	single := []Inbound{{Name: "ss", Type: "shadowsocks", Cipher: "aes-128-gcm", Clients: []Client{{Name: "a", Password: "x"}}}}
	for version, wantErr := range map[string]bool{"v1.19.33": true, "v1.18.10": true, "v1.19.34": false, "v1.20.0": false, "v2.0.0": false, "Unknown": false, "alpha-184857c": false} {
		if err := checkShadowsocksUsersCore(multi, version); (err != nil) != wantErr {
			t.Errorf("%s: err = %v, want error %v", version, err, wantErr)
		}
	}
	if err := checkShadowsocksUsersCore(single, "v1.19.33"); err != nil {
		t.Fatalf("a single-user listener must work on older cores: %v", err)
	}
}

func TestShadowsocksTrafficPerUser(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir(), connectionStats: map[string]connectionSample{}, state: State{Inbounds: []Inbound{
		{ID: "multi", Name: "ss-multi", Type: "shadowsocks", Cipher: "aes-128-gcm", Clients: []Client{
			{ID: "a", Name: "alice", Password: "x", Enabled: true}, {ID: "b", Name: "bob", Password: "y", Enabled: true},
		}},
		{ID: "single", Name: "ss-single", Type: "shadowsocks", Cipher: "2022-blake3-aes-256-gcm", Clients: []Client{
			{ID: "c", Name: "carol", Password: "z", Enabled: true},
		}},
	}}}
	m.applyTrafficSnapshot(map[string]any{"uploadTotal": 600.0, "downloadTotal": 60.0, "connections": []any{
		map[string]any{"id": "1", "upload": 100.0, "download": 10.0, "metadata": map[string]any{"inboundName": "ss-multi", "inboundUser": "bob"}},
		map[string]any{"id": "2", "upload": 200.0, "download": 20.0, "metadata": map[string]any{"inboundName": "ss-multi", "inboundUser": "alice"}},
		// The single-user listener reports no user; its only client owns the traffic.
		map[string]any{"id": "3", "upload": 300.0, "download": 30.0, "metadata": map[string]any{"inboundName": "ss-single", "inboundUser": ""}},
	}})
	multi, single := m.state.Inbounds[0], m.state.Inbounds[1]
	if multi.Clients[0].Traffic.Up != 200 || multi.Clients[1].Traffic.Up != 100 || multi.Traffic.Up != 300 {
		t.Fatalf("multi-user traffic: inbound %+v, clients %+v", multi.Traffic, multi.Clients)
	}
	if single.Clients[0].Traffic.Up != 300 || single.Clients[0].Traffic.Down != 30 || single.Clients[0].LastOnline == "" {
		t.Fatalf("single-user traffic did not reach its client: %+v", single.Clients[0])
	}
}

func TestImportShadowsocksUsersListener(t *testing.T) {
	const cipher = "2022-blake3-aes-256-gcm"
	psk, a, b := ss2022Key(t, cipher), ss2022Key(t, cipher), ss2022Key(t, cipher)
	inbound, err := inboundFromYAMLMap(map[string]any{"name": "ss-in", "type": "shadowsocks", "port": 10813, "cipher": cipher, "password": psk, "users": map[string]any{"user1": a, "user2": b}})
	if err != nil {
		t.Fatal(err)
	}
	if inbound.Password != psk || len(inbound.Clients) != 2 || inbound.Clients[0].Name != "user1" || inbound.Clients[1].Password != b || !shadowsocksUsersMode(inbound) {
		t.Fatalf("imported users listener = %+v", inbound)
	}
	legacy, err := inboundFromYAMLMap(map[string]any{"name": "ss-old", "type": "shadowsocks", "port": 10814, "cipher": cipher, "password": psk})
	if err != nil {
		t.Fatal(err)
	}
	if len(legacy.Clients) != 1 || legacy.Clients[0].Password != psk {
		t.Fatalf("single-user import = %+v", legacy)
	}
}

// The page builds share links and the multi-user switches itself; it must
// follow the same rules as the server.
func TestPanelShadowsocksUsersFrontend(t *testing.T) {
	page, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	text := string(page)
	for _, needle := range []string{
		"function singleSecretProto(type){return ['snell','sudoku'].includes(type);}",
		"const sharePassword=ssClientPassword(inbound,client);",
		`name="ssServerPsk"`,
		"password:type==='shadowsocks'&&SS_USERS_2022.includes(value('cipher').toLowerCase())?value('ssServerPsk').trim():''",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("web/index.html is missing %q", needle)
		}
	}
	for _, cipher := range shadowsocksLegacyAEADCiphers {
		if !strings.Contains(text, "'"+cipher+"'") {
			t.Fatalf("the page's legacy AEAD list lacks %s", cipher)
		}
	}
}

// Every error the multi-user code can return reaches the user translated.
func TestShadowsocksUsersMessagesAreTranslated(t *testing.T) {
	messages := []string{
		"Shadowsocks 至少需要一个客户端",
		"Shadowsocks 加密方式 2022-blake3-chacha20-poly1305 不支持多用户，只能有一个客户端；多用户请改用 2022-blake3-aes-* 或 AEAD 加密",
		"Shadowsocks 加密方式 2022-blake3-chacha20-poly1305 只支持一个客户端",
		"Shadowsocks 客户端 alice 没有密码",
		"Shadowsocks 至少需要一个启用且有密码的客户端",
		"多用户 Shadowsocks（2022-blake3-aes-256-gcm）需要服务端 PSK",
		"Shadowsocks 服务端 PSK 必须是 32 字节密钥的 Base64 编码",
		"Shadowsocks 客户端 alice smith 的密码必须是 16 字节密钥的 Base64 编码（多用户的用户 PSK）",
		"Shadowsocks 客户端 alice 的密码与服务端 PSK 相同",
		"Shadowsocks 客户端 bob 的密码与 alice 相同，多用户时每个 PSK 必须不同",
		"Shadowsocks 客户端 bob 的密码与 alice 相同，多用户时每个密码必须不同",
		"多用户 Shadowsocks 入口 ss-in 需要 Mihomo v1.19.34 或更高版本（当前 v1.19.33），请先在仪表盘切换内核版本",
	}
	for _, language := range []string{"en", "zh-TW", "ja", "ru", "fa", "vi", "es", "tr", "uk", "pt-BR"} {
		for _, message := range messages {
			translated := translateMessage(message, language)
			if translated == message || (language != "zh-TW" && language != "ja" && hasCJK(translated)) {
				t.Errorf("%s: %q -> %q", language, message, translated)
			}
		}
	}
}
