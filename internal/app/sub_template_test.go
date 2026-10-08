package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const sampleFullSubscription = `# exported from a client
mixed-port: 7890
mode: rule
proxies:
  - name: "HK 01"
    type: ss
    server: 1.2.3.4
    port: 8388
    cipher: aes-256-gcm
    password: secret
  - name: "JP 01"
    type: vless
    server: 5.6.7.8
    port: 443
    uuid: 00000000-0000-0000-0000-000000000000
proxy-providers:
  airport:
    type: http
    url: https://example.com/sub?token=abc
proxy-groups:
  - name: Proxy
    type: select
    proxies: [Auto, HK 01, JP 01, DIRECT]
  - name: Auto
    type: url-test
    use: [airport]
    url: http://www.gstatic.com/generate_204
    interval: 300
  - name: Media
    type: select
    proxies: [Proxy, DIRECT]
rules:
  - DOMAIN-SUFFIX,netflix.com,Media
  - DOMAIN,special.example,JP 01
  - GEOIP,CN,DIRECT
  - MATCH,Proxy
`

func templateTestClients() []subscriptionClient {
	return []subscriptionClient{
		{Inbound: Inbound{Name: "ss", Type: "shadowsocks", Port: 8388, Enabled: true, Cipher: "aes-256-gcm", UDP: true}, Client: Client{Name: "u", Password: "sp", Enabled: true}},
		{Inbound: Inbound{Name: "vlr", Type: "vless", Port: 443, Enabled: true, Reality: RealitySettings{Enabled: true, PublicKey: "PBK", ServerNames: "reality.example", ShortIDs: "ab"}}, Client: Client{Name: "u", UUID: "uuid-1", Enabled: true}},
		{Inbound: Inbound{Name: "tj", Type: "trojan", Port: 443, Enabled: true, WSPath: "/tws", TLS: true, TLSServerName: "t.com"}, Client: Client{Name: "u", Password: "tp", Enabled: true}},
		{Inbound: Inbound{Name: "hy", Type: "hysteria2", Port: 8443, Enabled: true, TLSServerName: "hy.com"}, Client: Client{Name: "u", Password: "hp", Enabled: true}},
	}
}

func compileTemplateForTest(t *testing.T, content string) *compiledSubTemplate {
	t.Helper()
	doc, model, err := parseSubTemplate([]byte(content))
	if err != nil {
		t.Fatalf("parse template: %v\n%s", err, content)
	}
	return &compiledSubTemplate{Doc: doc, Model: model}
}

func TestBuiltinTemplateVariants(t *testing.T) {
	expect := map[string][]string{
		"cn": {"节点选择", "自动选择", "GEOIP,cn,DIRECT,no-resolve", "GEOSITE,cn,DIRECT", "IP-CIDR,223.5.5.5/32,DIRECT,no-resolve", "MATCH,节点选择"},
		"ru": {"Proxy", "Auto", "GEOIP,ru,DIRECT,no-resolve", "GEOSITE,category-ru,DIRECT", "MATCH,Proxy"},
		"ir": {"Proxy", "Auto", "GEOIP,ir,DIRECT,no-resolve", "GEOSITE,category-ir,DIRECT", "MATCH,Proxy"},
	}
	for _, variant := range subTemplateVariants {
		content := builtinSubTemplateYAML(variant)
		template := compileTemplateForTest(t, content)
		for _, want := range expect[variant] {
			if !strings.Contains(content, want) {
				t.Fatalf("%s variant is missing %q", variant, want)
			}
		}
		if len(template.Model.Groups) != 2 || !template.Model.Groups[0].IncludeNodes || !template.Model.Groups[1].IncludeNodes {
			t.Fatalf("%s variant groups: %#v", variant, template.Model.Groups)
		}
		if variant != "cn" && (strings.Contains(content, "GEOSITE,cn") || strings.Contains(content, "223.5.5.5")) {
			t.Fatalf("%s variant still bypasses mainland China", variant)
		}
		// The built-in template is rebuilt from the user's sample config and
		// must never carry its nodes.
		if strings.Contains(content, "proxies:\n  -") || strings.Contains(content, "Tokyo") || strings.Contains(content, "jnu.edu.cn") {
			t.Fatalf("%s variant contains node or personal data", variant)
		}
	}
}

func TestCleanSubTemplateStripsNodes(t *testing.T) {
	cleaned, stats, err := cleanSubTemplate([]byte(sampleFullSubscription))
	if err != nil {
		t.Fatal(err)
	}
	text := string(cleaned)
	for _, leaked := range []string{"exported from", "1.2.3.4", "secret", "00000000-", "example.com/sub", "airport", "HK 01"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("cleaned template still contains %q:\n%s", leaked, text)
		}
	}
	if stats.RemovedProxies != 2 || stats.RemovedProviders != 1 || stats.NodeGroups != 2 || stats.Groups != 3 || stats.Rules != 4 || stats.RetargetedRules != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if !strings.HasPrefix(text, "mixed-port: 7890\nmode: rule\nproxy-groups:") {
		t.Fatalf("key order was not preserved:\n%s", text)
	}
	template := compileTemplateForTest(t, text)
	groups := template.Model.Groups
	if !groups[0].IncludeNodes || !slices.Equal(groups[0].Members, []string{"Auto", "DIRECT"}) {
		t.Fatalf("Proxy group: %#v", groups[0])
	}
	if !groups[1].IncludeNodes || len(groups[1].Members) != 0 {
		t.Fatalf("provider group must receive nodes: %#v", groups[1])
	}
	if groups[2].IncludeNodes {
		t.Fatalf("group without nodes must not receive them: %#v", groups[2])
	}
	if template.Model.Rules[1].Target != "Proxy" {
		t.Fatalf("rule pointing at a node must move to the first node group: %#v", template.Model.Rules[1])
	}
}

func TestCleanSubTemplateRejectsBrokenInput(t *testing.T) {
	cases := map[string]string{
		"empty":       "   ",
		"not mapping": "- a\n- b\n",
		"no groups":   "rules:\n  - MATCH,DIRECT\n",
		"no rules":    "proxy-groups:\n  - {name: P, type: select, proxies: [DIRECT]}\n",
		"bad target":  "proxy-groups:\n  - {name: P, type: select, proxies: [DIRECT]}\nrules:\n  - MATCH,Missing\n",
		"bad rule":    "proxy-groups:\n  - {name: P, type: select, proxies: [DIRECT]}\nrules:\n  - DOMAIN\n",
		"dup group":   "proxy-groups:\n  - {name: P, type: select}\n  - {name: P, type: select}\nrules:\n  - MATCH,P\n",
	}
	for name, content := range cases {
		if _, _, err := cleanSubTemplate([]byte(content)); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}
}

func TestParseTemplateRuleLogical(t *testing.T) {
	rule, err := parseTemplateRule("AND,((NETWORK,UDP),(DST-PORT,443)),REJECT")
	if err != nil {
		t.Fatal(err)
	}
	if rule.Type != "AND" || rule.Target != "REJECT" || len(rule.Sub) != 2 || rule.Sub[1].Type != "DST-PORT" || rule.Sub[1].Value != "443" {
		t.Fatalf("unexpected rule: %#v", rule)
	}
	if rule.String() != "AND,((NETWORK,UDP),(DST-PORT,443)),REJECT" {
		t.Fatalf("round trip: %s", rule.String())
	}
}

func TestGroupExpandFilters(t *testing.T) {
	nodes := []templateNode{{Name: "HK 01", Type: "ss"}, {Name: "JP 01", Type: "vless"}, {Name: "HK 02", Type: "vless"}}
	group := subTemplateGroup{Members: []string{"DIRECT"}, IncludeNodes: true, Filter: "HK", ExcludeType: "Shadowsocks"}
	if got := group.expand(nodes); !slices.Equal(got, []string{"DIRECT", "HK 02"}) {
		t.Fatalf("expand = %v", got)
	}
	if got := (subTemplateGroup{IncludeNodes: true, Filter: "^US"}).expand(nodes); !slices.Equal(got, []string{"DIRECT"}) {
		t.Fatalf("an emptied group must fall back to DIRECT: %v", got)
	}
}

func TestTemplateSubscriptionsForEveryClient(t *testing.T) {
	template := compileTemplateForTest(t, builtinSubTemplateYAML("cn"))
	snapshot := subscriptionSnapshot{Username: "u", Token: "abc123def4567890", Clients: templateTestClients(), Template: template, RequestURL: "https://panel.example/sub/abc/surge"}

	mihomo, err := buildMihomoSubscription(snapshot, "srv.example")
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := yaml.Unmarshal(mihomo, &config); err != nil {
		t.Fatalf("mihomo output is not YAML: %v\n%s", err, mihomo)
	}
	proxies, _ := config["proxies"].([]any)
	groups, _ := config["proxy-groups"].([]any)
	if len(proxies) != 4 || len(groups) != 2 {
		t.Fatalf("mihomo output proxies=%d groups=%d\n%s", len(proxies), len(groups), mihomo)
	}
	selectGroup := groups[0].(map[string]any)
	members := selectGroup["proxies"].([]any)
	if selectGroup["name"] != "节点选择" || members[0] != "自动选择" || members[1] != "DIRECT" || len(members) != 6 {
		t.Fatalf("select group not filled: %#v", selectGroup)
	}
	if _, left := selectGroup["include-all-proxies"]; left {
		t.Fatal("include-all-proxies must be expanded, not left for the client")
	}
	if text := string(mihomo); strings.Index(text, "proxies:") > strings.Index(text, "proxy-groups:") || !strings.HasPrefix(text, "mixed-port: 7890") {
		t.Fatalf("proxies must sit before proxy-groups and settings stay on top:\n%s", text)
	}

	singbox, err := buildSingboxSubscription(snapshot, "srv.example")
	if err != nil {
		t.Fatal(err)
	}
	var sb struct {
		Outbounds []map[string]any `json:"outbounds"`
		Route     map[string]any   `json:"route"`
	}
	if err := json.Unmarshal(singbox, &sb); err != nil {
		t.Fatal(err)
	}
	if sb.Route["final"] != "节点选择" {
		t.Fatalf("sing-box final = %v", sb.Route["final"])
	}
	ruleSets, _ := sb.Route["rule_set"].([]any)
	var setTags []string
	for _, set := range ruleSets {
		entry := set.(map[string]any)
		setTags = append(setTags, entry["tag"].(string))
		if entry["download_detour"] != "节点选择" || !strings.HasPrefix(entry["url"].(string), "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/") {
			t.Fatalf("rule set entry: %#v", entry)
		}
	}
	for _, want := range []string{"geosite-google", "geosite-category-ai-!cn", "geoip-cn", "geosite-cn"} {
		if !slices.Contains(setTags, want) {
			t.Fatalf("sing-box rule sets %v miss %s", setTags, want)
		}
	}
	singboxText := string(singbox)
	for _, want := range []string{`"ip_is_private": true`, `"mode": "and"`, `"action": "reject"`, `"type": "urltest"`} {
		if !strings.Contains(singboxText, want) {
			t.Fatalf("sing-box output misses %s", want)
		}
	}

	body := func(format subscriptionFormat) string {
		t.Helper()
		data, _, err := buildClientSubscription(snapshot, "srv.example", format)
		if err != nil {
			t.Fatalf("format %v: %v", format, err)
		}
		return string(data)
	}
	surge := body(subscriptionFormatSurge)
	for _, want := range []string{
		"#!MANAGED-CONFIG https://panel.example/sub/abc/surge interval=86400 strict=false",
		"[Proxy]", "[Proxy Group]", "节点选择 = select, 自动选择, DIRECT, ",
		"自动选择 = url-test, ", "url=https://www.gstatic.com/generate_204, interval=300, tolerance=50",
		"AND,((PROTOCOL,UDP),(DEST-PORT,443)),REJECT",
		"RULE-SET,https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/meta/geo/geosite/classical/google.list,节点选择",
		"IP-CIDR,192.168.0.0/16,DIRECT,no-resolve", "IP-CIDR6,fc00::/7,DIRECT,no-resolve",
		"GEOIP,CN,DIRECT,no-resolve", "FINAL,节点选择,dns-failed",
	} {
		if !strings.Contains(surge, want) {
			t.Fatalf("surge profile misses %q:\n%s", want, surge)
		}
	}
	surfboard := body(subscriptionFormatSurfboard)
	if !strings.Contains(surfboard, "# m-ui: skipped AND,") || strings.Contains(surfboard, "dns-failed") {
		t.Fatalf("surfboard must skip logical rules:\n%s", surfboard)
	}
	loon := body(subscriptionFormatLoon)
	for _, want := range []string{"[Remote Rule]", "classical/cn.list, policy=DIRECT, tag=geosite-cn, enabled=true", "节点选择 = select,自动选择,DIRECT,", "FINAL,节点选择"} {
		if !strings.Contains(loon, want) {
			t.Fatalf("loon profile misses %q:\n%s", want, loon)
		}
	}
	qx := body(subscriptionFormatQX)
	for _, want := range []string{"[policy]", "static=节点选择, 自动选择, direct, ", "url-latency-benchmark=自动选择, ", "[server_local]", "host-suffix, ipleak.net, 节点选择", "geoip, cn, direct", "ip-cidr, 223.5.5.5/32, direct", "final, 节点选择", "# m-ui: skipped GEOSITE,google"} {
		if !strings.Contains(qx, want) {
			t.Fatalf("quantumult x profile misses %q:\n%s", want, qx)
		}
	}
	if strings.Contains(qx, "no-resolve") {
		t.Fatal("quantumult x does not support no-resolve")
	}
	stash := body(subscriptionFormatStash)
	if !strings.Contains(stash, "GEOSITE,cn,DIRECT") || !strings.Contains(stash, "proxy-groups:") {
		t.Fatalf("stash profile must keep the mihomo rules:\n%s", stash)
	}
	egern := body(subscriptionFormatEgern)
	var egernConfig map[string]any
	if err := yaml.Unmarshal([]byte(egern), &egernConfig); err != nil {
		t.Fatalf("egern output is not YAML: %v", err)
	}
	for _, want := range []string{"policy_groups:", "auto_test:", "rule_set:", "default:", "geoip:"} {
		if !strings.Contains(egern, want) {
			t.Fatalf("egern profile misses %q:\n%s", want, egern)
		}
	}
	if surgemac := body(subscriptionFormatSurgeMac); !strings.Contains(surgemac, "=external,") || !strings.Contains(surgemac, "[Proxy Group]") {
		t.Fatalf("surge mac profile must keep external nodes:\n%s", surgemac)
	}

	if dir := os.Getenv("M_UI_DUMP_TEMPLATES"); dir != "" {
		for name, data := range map[string]string{"mihomo.yaml": string(mihomo), "sing-box.json": singboxText, "surge.conf": surge, "surfboard.conf": surfboard, "loon.conf": loon, "qx.conf": qx, "stash.yaml": stash, "egern.yaml": egern} {
			_ = os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644)
		}
		for _, variant := range []string{"ru", "ir"} {
			variantSnapshot := snapshot
			variantSnapshot.Template = compileTemplateForTest(t, builtinSubTemplateYAML(variant))
			if data, err := buildMihomoSubscription(variantSnapshot, "srv.example"); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "mihomo-"+variant+".yaml"), data, 0o644)
			}
			if data, err := buildSingboxSubscription(variantSnapshot, "srv.example"); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "sing-box-"+variant+".json"), data, 0o644)
			}
		}
	}
}

func TestSubTemplateManagerLifecycle(t *testing.T) {
	dir := t.TempDir()
	manager, err := newSubTemplateManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if active := manager.active(); active == nil || active.ID != subTemplateBuiltinID || active.Model.Groups[0].Name != "节点选择" {
		t.Fatalf("default template: %#v", active)
	}
	cleaned, _, err := cleanSubTemplate([]byte(sampleFullSubscription))
	if err != nil {
		t.Fatal(err)
	}
	record, err := manager.add("Airport", cleaned)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.mutate(func(disk *subTemplateDisk) error { disk.Active = record.ID; return nil }); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newSubTemplateManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if active := reloaded.active(); active == nil || active.ID != record.ID || active.Model.Groups[2].Name != "Media" {
		t.Fatalf("persisted active template: %#v", active)
	}
	if err := reloaded.mutate(func(disk *subTemplateDisk) error { disk.Variant = "ru"; disk.Active = subTemplateBuiltinID; return nil }); err != nil {
		t.Fatal(err)
	}
	if active := reloaded.active(); active.Model.Groups[0].Name != "Proxy" {
		t.Fatalf("variant switch did not recompile: %#v", active.Model.Groups[0])
	}
	disk := reloaded.exportDisk()
	disk.Templates[0].Content = "proxies: []\n"
	if err := reloaded.restoreDisk(disk); err == nil {
		t.Fatal("a template carrying nodes must be rejected on restore")
	}
	disk = reloaded.exportDisk()
	disk.Active = "missing"
	if err := reloaded.restoreDisk(disk); err != nil || reloaded.exportDisk().Active != subTemplateBuiltinID {
		t.Fatalf("unknown active template must fall back to the builtin: %v", err)
	}
}
