package app

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// 模版到各客户端的转换。Mihomo / Stash 直接在 YAML 节点上改（保留模版原有的
// 键顺序和写法），其余客户端先把模版解析成 subTemplateModel，再逐个翻译成
// 各自的分组和规则语法。客户端表达不了的规则跳过，ini 格式里留一行注释说明。
//
// GEOSITE 只有 Mihomo / Stash 原生支持，其它客户端改用 MetaCubeX/meta-rules-dat
// 的远程规则集（和 Mihomo 用的 GeoSite.dat 同源）。

const (
	metaRulesClassicalGeosite = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/meta/geo/geosite/classical/"
	metaRulesSingGeosite      = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/"
	metaRulesSingGeoip        = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geoip/"
	defaultTemplateTestURL    = "http://www.gstatic.com/generate_204"
)

// privateCIDRs replaces GEOIP,private for clients whose GeoIP has no "private".
var privateCIDRs = []string{
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "169.254.0.0/16", "100.64.0.0/10",
	"224.0.0.0/4", "fc00::/7", "fe80::/10", "::1/128",
}

type subTemplateGroup struct {
	Name          string
	Type          string
	Members       []string
	IncludeNodes  bool
	Filter        string
	ExcludeFilter string
	ExcludeType   string
	URL           string
	Interval      int
	Tolerance     int
}

type subTemplateRule struct {
	Type    string
	Value   string
	Target  string
	Options []string
	Sub     []subTemplateRule
}

type subTemplateModel struct {
	Groups []subTemplateGroup
	Rules  []subTemplateRule
}

// templateNode is one rendered node as the template sees it: the name the
// client config uses and the Mihomo proxy type (for exclude-type).
type templateNode struct {
	Name string
	Type string
}

// ---- rule parsing ----

// splitRuleTopLevel splits on commas outside parentheses.
func splitRuleTopLevel(text string) []string {
	parts := []string{}
	depth, start := 0, 0
	for i, r := range text {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(text[start:i]))
				start = i + 1
			}
		}
	}
	return append(parts, strings.TrimSpace(text[start:]))
}

func isLogicalRule(ruleType string) bool {
	return ruleType == "AND" || ruleType == "OR" || ruleType == "NOT"
}

func parseTemplateRule(text string) (subTemplateRule, error) {
	parts := splitRuleTopLevel(strings.TrimSpace(text))
	rule := subTemplateRule{Type: strings.ToUpper(parts[0])}
	switch {
	case rule.Type == "MATCH" || rule.Type == "FINAL":
		if len(parts) < 2 || parts[1] == "" {
			return rule, fmt.Errorf("规则格式无效: %s", text)
		}
		rule.Type, rule.Target = "MATCH", parts[1]
		return rule, nil
	case len(parts) < 3 || parts[1] == "" || parts[2] == "":
		return rule, fmt.Errorf("规则格式无效: %s", text)
	}
	rule.Value, rule.Target, rule.Options = parts[1], parts[2], parts[3:]
	if isLogicalRule(rule.Type) {
		sub, err := parseLogicalPayload(rule.Value)
		if err != nil {
			return rule, fmt.Errorf("规则格式无效: %s", text)
		}
		rule.Value, rule.Sub = "", sub
	}
	return rule, nil
}

// parseLogicalPayload parses "((TYPE,value),(TYPE,value))".
func parseLogicalPayload(payload string) ([]subTemplateRule, error) {
	if !strings.HasPrefix(payload, "(") || !strings.HasSuffix(payload, ")") {
		return nil, fmt.Errorf("bad logical payload")
	}
	items := splitRuleTopLevel(payload[1 : len(payload)-1])
	rules := make([]subTemplateRule, 0, len(items))
	for _, item := range items {
		if !strings.HasPrefix(item, "(") || !strings.HasSuffix(item, ")") {
			return nil, fmt.Errorf("bad logical item")
		}
		parts := splitRuleTopLevel(item[1 : len(item)-1])
		sub := subTemplateRule{Type: strings.ToUpper(parts[0])}
		if len(parts) < 2 || parts[1] == "" {
			return nil, fmt.Errorf("bad logical item")
		}
		sub.Value, sub.Options = parts[1], parts[2:]
		if isLogicalRule(sub.Type) {
			nested, err := parseLogicalPayload(sub.Value)
			if err != nil {
				return nil, err
			}
			sub.Value, sub.Sub = "", nested
		}
		rules = append(rules, sub)
	}
	return rules, nil
}

// String renders the rule back in Mihomo syntax.
func (rule subTemplateRule) String() string {
	if rule.Type == "MATCH" {
		return "MATCH," + rule.Target
	}
	parts := []string{rule.Type, rule.payload(), rule.Target}
	return strings.Join(append(parts, rule.Options...), ",")
}

func (rule subTemplateRule) payload() string {
	if rule.Sub == nil {
		return rule.Value
	}
	items := make([]string, 0, len(rule.Sub))
	for _, sub := range rule.Sub {
		items = append(items, "("+strings.Join(append([]string{sub.Type, sub.payload()}, sub.Options...), ",")+")")
	}
	return "(" + strings.Join(items, ",") + ")"
}

func (rule subTemplateRule) hasOption(option string) bool {
	return slices.ContainsFunc(rule.Options, func(value string) bool { return strings.EqualFold(value, option) })
}

// ---- model ----

func yamlInt(node *yaml.Node) int {
	if node == nil {
		return 0
	}
	value, _ := strconv.Atoi(strings.TrimSpace(node.Value))
	return value
}

func yamlScalarValue(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return node.Value
}

func buildSubTemplateModel(root *yaml.Node) (subTemplateModel, error) {
	var model subTemplateModel
	groups := yamlMapGet(root, "proxy-groups")
	if groups == nil || groups.Kind != yaml.SequenceNode || len(groups.Content) == 0 {
		return model, fmt.Errorf("缺少 proxy-groups 分组")
	}
	names := map[string]bool{}
	for _, node := range groups.Content {
		group := subTemplateGroup{
			Name: yamlScalarValue(yamlMapGet(node, "name")), Type: strings.ToLower(yamlScalarValue(yamlMapGet(node, "type"))),
			IncludeNodes: yamlBool(yamlMapGet(node, "include-all-proxies")) || yamlBool(yamlMapGet(node, "include-all")),
			Filter:       yamlScalarValue(yamlMapGet(node, "filter")), ExcludeFilter: yamlScalarValue(yamlMapGet(node, "exclude-filter")),
			ExcludeType: yamlScalarValue(yamlMapGet(node, "exclude-type")), URL: yamlScalarValue(yamlMapGet(node, "url")),
			Interval: yamlInt(yamlMapGet(node, "interval")), Tolerance: yamlInt(yamlMapGet(node, "tolerance")),
		}
		if group.Name == "" || names[group.Name] {
			return model, fmt.Errorf("分组名缺失或重复: %s", group.Name)
		}
		if group.Type == "" {
			return model, fmt.Errorf("分组缺少 type: %s", group.Name)
		}
		names[group.Name] = true
		if members := yamlMapGet(node, "proxies"); members != nil {
			if members.Kind != yaml.SequenceNode {
				return model, fmt.Errorf("分组的 proxies 必须是列表: %s", group.Name)
			}
			for _, member := range members.Content {
				group.Members = append(group.Members, member.Value)
			}
		}
		model.Groups = append(model.Groups, group)
	}
	known := func(name string) bool {
		return names[name] || slices.Contains(mihomoBuiltinTargets, strings.ToUpper(name))
	}
	for _, group := range model.Groups {
		for _, member := range group.Members {
			if !known(member) {
				return model, fmt.Errorf("分组引用了不存在的分组或节点: %s → %s", group.Name, member)
			}
		}
		if len(group.Members) == 0 && !group.IncludeNodes {
			return model, fmt.Errorf("分组是空的: %s", group.Name)
		}
	}
	rules := yamlMapGet(root, "rules")
	if rules == nil || rules.Kind != yaml.SequenceNode || len(rules.Content) == 0 {
		return model, fmt.Errorf("缺少 rules 规则")
	}
	for _, node := range rules.Content {
		rule, err := parseTemplateRule(node.Value)
		if err != nil {
			return model, err
		}
		if rule.Type != "SUB-RULE" && !known(rule.Target) {
			return model, fmt.Errorf("规则指向不存在的分组: %s → %s", node.Value, rule.Target)
		}
		model.Rules = append(model.Rules, rule)
	}
	return model, nil
}

// mihomoAdapterTypes maps clash proxy types to the adapter names exclude-type
// is written with.
var mihomoAdapterTypes = map[string]string{
	"ss": "shadowsocks", "ssr": "shadowsocksr", "vmess": "vmess", "vless": "vless", "trojan": "trojan",
	"hysteria": "hysteria", "hysteria2": "hysteria2", "tuic": "tuic", "anytls": "anytls", "socks5": "socks5",
	"http": "http", "wireguard": "wireguard", "ssh": "ssh", "snell": "snell", "mieru": "mieru",
}

// compileGroupFilter compiles a Mihomo filter (several regexes joined by a
// backtick). Patterns Go cannot compile (look-arounds) are ignored rather
// than dropping every node.
func compileGroupFilter(filter string) []*regexp.Regexp {
	var patterns []*regexp.Regexp
	for _, part := range strings.Split(filter, "`") {
		if part == "" {
			continue
		}
		if pattern, err := regexp.Compile(part); err == nil {
			patterns = append(patterns, pattern)
		}
	}
	return patterns
}

// expand returns the group's members with the nodes filled in, deduplicated,
// falling back to DIRECT so no group is ever empty.
func (group subTemplateGroup) expand(nodes []templateNode) []string {
	members := slices.Clone(group.Members)
	if group.IncludeNodes {
		include, exclude := compileGroupFilter(group.Filter), compileGroupFilter(group.ExcludeFilter)
		excludedTypes := map[string]bool{}
		for _, name := range strings.Split(group.ExcludeType, "|") {
			if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
				excludedTypes[name] = true
			}
		}
		matchAny := func(patterns []*regexp.Regexp, name string) bool {
			return slices.ContainsFunc(patterns, func(pattern *regexp.Regexp) bool { return pattern.MatchString(name) })
		}
		for _, node := range nodes {
			if len(include) > 0 && !matchAny(include, node.Name) || matchAny(exclude, node.Name) {
				continue
			}
			if excludedTypes[mihomoAdapterTypes[node.Type]] || excludedTypes[node.Type] {
				continue
			}
			if !slices.Contains(members, node.Name) {
				members = append(members, node.Name)
			}
		}
	}
	if len(members) == 0 {
		members = []string{"DIRECT"}
	}
	return members
}

// ---- Mihomo / Stash ----

// renderMihomoTemplate fills proxies into a clone of the template document.
func renderMihomoTemplate(template *compiledSubTemplate, proxies []map[string]any) ([]byte, error) {
	if len(proxies) == 0 {
		return nil, fmt.Errorf("此用户没有 Mihomo 可用节点")
	}
	doc := cloneYAMLNode(template.Doc)
	root := yamlRoot(doc)
	list, err := marshalYAML(map[string]any{"proxies": proxies})
	if err != nil {
		return nil, err
	}
	var proxiesDoc yaml.Node
	if err := yaml.Unmarshal(list, &proxiesDoc); err != nil {
		return nil, err
	}
	yamlMapSet(root, "proxies", yamlMapGet(yamlRoot(&proxiesDoc), "proxies"), "proxy-groups")
	nodes := make([]templateNode, 0, len(proxies))
	for _, proxy := range proxies {
		nodes = append(nodes, templateNode{Name: proxyStr(proxy, "name"), Type: proxyStr(proxy, "type")})
	}
	groups := yamlMapGet(root, "proxy-groups")
	for i, node := range groups.Content {
		group := template.Model.Groups[i]
		if !group.IncludeNodes {
			continue
		}
		members := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, name := range group.expand(nodes) {
			members.Content = append(members.Content, yamlStr(name))
		}
		yamlMapDelete(node, "include-all-proxies", "include-all", "filter", "exclude-filter", "exclude-type")
		yamlMapSet(node, "proxies", members, "")
	}
	return encodeYAMLNode(doc)
}

// ---- sing-box ----

func singboxTemplateTarget(target string) string {
	if strings.EqualFold(target, "DIRECT") {
		return "direct"
	}
	return target
}

type singboxRuleSets struct {
	detour string
	order  []string
	sets   map[string]map[string]any
}

func (sets *singboxRuleSets) use(tag, url string) {
	if _, ok := sets.sets[tag]; ok {
		return
	}
	entry := map[string]any{"type": "remote", "tag": tag, "format": "binary", "url": url}
	if sets.detour != "" {
		entry["download_detour"] = sets.detour
	}
	sets.sets[tag] = entry
	sets.order = append(sets.order, tag)
}

// singboxRuleMatch converts one rule's matching part; ok is false when
// sing-box cannot express it.
func singboxRuleMatch(rule subTemplateRule, sets *singboxRuleSets) (map[string]any, bool) {
	value := rule.Value
	switch rule.Type {
	case "DOMAIN":
		return map[string]any{"domain": []any{value}}, true
	case "DOMAIN-SUFFIX":
		return map[string]any{"domain_suffix": []any{value}}, true
	case "DOMAIN-KEYWORD":
		return map[string]any{"domain_keyword": []any{value}}, true
	case "DOMAIN-REGEX":
		return map[string]any{"domain_regex": []any{value}}, true
	case "IP-CIDR", "IP-CIDR6":
		return map[string]any{"ip_cidr": []any{value}}, true
	case "SRC-IP-CIDR":
		return map[string]any{"source_ip_cidr": []any{value}}, true
	case "PROCESS-NAME":
		return map[string]any{"process_name": []any{value}}, true
	case "NETWORK":
		return map[string]any{"network": []any{strings.ToLower(value)}}, true
	case "DST-PORT", "SRC-PORT":
		key := "port"
		if rule.Type == "SRC-PORT" {
			key = "source_port"
		}
		if port, err := strconv.Atoi(value); err == nil {
			return map[string]any{key: []any{port}}, true
		}
		if from, to, ok := strings.Cut(value, "-"); ok {
			return map[string]any{key + "_range": []any{from + ":" + to}}, true
		}
		return nil, false
	case "GEOIP":
		code := strings.ToLower(value)
		if code == "private" || code == "lan" {
			return map[string]any{"ip_is_private": true}, true
		}
		sets.use("geoip-"+code, metaRulesSingGeoip+code+".srs")
		return map[string]any{"rule_set": []any{"geoip-" + code}}, true
	case "GEOSITE":
		name := strings.ToLower(value)
		if strings.Contains(name, "@") {
			return nil, false
		}
		sets.use("geosite-"+name, metaRulesSingGeosite+name+".srs")
		return map[string]any{"rule_set": []any{"geosite-" + name}}, true
	case "AND", "OR", "NOT":
		subRules := make([]any, 0, len(rule.Sub))
		for _, sub := range rule.Sub {
			converted, ok := singboxRuleMatch(sub, sets)
			if !ok {
				return nil, false
			}
			subRules = append(subRules, converted)
		}
		mode := "and"
		if rule.Type == "OR" {
			mode = "or"
		}
		logical := map[string]any{"type": "logical", "mode": mode, "rules": subRules}
		if rule.Type == "NOT" {
			logical["invert"] = true
		}
		return logical, true
	}
	return nil, false
}

// renderSingboxTemplate builds the sing-box config: node outbounds, one
// outbound per template group and route rules with remote rule sets.
func renderSingboxTemplate(template *compiledSubTemplate, outbounds []any, tags []string) map[string]any {
	model := template.Model
	nodes := make([]templateNode, 0, len(tags))
	for i, tag := range tags {
		nodeType, _ := outbounds[i].(map[string]any)["type"].(string)
		if nodeType == "shadowsocks" {
			nodeType = "ss"
		}
		nodes = append(nodes, templateNode{Name: tag, Type: nodeType})
	}
	final := "direct"
	for _, rule := range model.Rules {
		if rule.Type == "MATCH" {
			final = singboxTemplateTarget(rule.Target)
		}
	}
	sets := &singboxRuleSets{sets: map[string]map[string]any{}}
	if final != "direct" && !slices.Contains(mihomoBuiltinTargets, strings.ToUpper(final)) {
		sets.detour = final
	} else {
		final = "direct"
	}
	all := slices.Clone(outbounds)
	for _, group := range model.Groups {
		members := make([]any, 0)
		for _, member := range group.expand(nodes) {
			switch strings.ToUpper(member) {
			case "REJECT", "REJECT-DROP", "PASS", "COMPATIBLE", "GLOBAL":
				continue
			}
			members = append(members, singboxTemplateTarget(member))
		}
		if len(members) == 0 {
			members = append(members, "direct")
		}
		if group.Type == "select" || group.Type == "relay" {
			all = append(all, map[string]any{"type": "selector", "tag": group.Name, "outbounds": members, "default": members[0]})
			continue
		}
		test := map[string]any{"type": "urltest", "tag": group.Name, "outbounds": members, "url": firstNonEmpty(group.URL, defaultTemplateTestURL)}
		if group.Interval > 0 {
			test["interval"] = strconv.Itoa(group.Interval) + "s"
		}
		if group.Tolerance > 0 {
			test["tolerance"] = group.Tolerance
		}
		all = append(all, test)
	}
	all = append(all, map[string]any{"type": "direct", "tag": "direct"})
	rules := []any{
		map[string]any{"action": "sniff"},
		map[string]any{"protocol": "dns", "action": "hijack-dns"},
	}
	for _, rule := range model.Rules {
		if rule.Type == "MATCH" {
			continue
		}
		converted, ok := singboxRuleMatch(rule, sets)
		if !ok {
			continue
		}
		switch strings.ToUpper(rule.Target) {
		case "REJECT":
			converted["action"] = "reject"
		case "REJECT-DROP":
			converted["action"], converted["method"] = "reject", "drop"
		case "PASS", "COMPATIBLE", "GLOBAL":
			continue
		default:
			converted["outbound"] = singboxTemplateTarget(rule.Target)
		}
		rules = append(rules, converted)
	}
	route := map[string]any{"rules": rules, "final": final, "auto_detect_interface": true}
	if len(sets.order) > 0 {
		ruleSets := make([]any, 0, len(sets.order))
		for _, tag := range sets.order {
			ruleSets = append(ruleSets, sets.sets[tag])
		}
		route["rule_set"] = ruleSets
	}
	return map[string]any{
		"log":       map[string]any{"level": "info"},
		"inbounds":  singboxDefaultInbounds(),
		"outbounds": all,
		"route":     route,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// ---- ini clients: Surge, Surfboard, Loon, Quantumult X ----

type clientNode struct {
	Name string
	Type string
	Line string
}

type iniDialect int

const (
	dialectSurge iniDialect = iota
	dialectSurfboard
	dialectLoon
	dialectQX
)

func iniSafeName(name string) string {
	return strings.Map(func(r rune) rune {
		if r == '=' || r == ',' {
			return -1
		}
		return r
	}, name)
}

// iniPolicyName maps a template policy to the dialect's spelling.
func iniPolicyName(name string, dialect iniDialect) (string, bool) {
	switch strings.ToUpper(name) {
	case "DIRECT":
		if dialect == dialectQX {
			return "direct", true
		}
		return "DIRECT", true
	case "REJECT", "REJECT-DROP":
		if dialect == dialectQX {
			return "reject", true
		}
		if dialect == dialectSurge && strings.ToUpper(name) == "REJECT-DROP" {
			return "REJECT-DROP", true
		}
		return "REJECT", true
	case "PASS", "COMPATIBLE", "GLOBAL":
		return "", false
	}
	return iniSafeName(name), true
}

// iniRule converts a non-logical rule's matching part to "TYPE,value[,opts]"
// in the dialect, or reports it unsupported. GEOSITE is handled by the caller.
func iniRuleMatch(rule subTemplateRule, dialect iniDialect) (string, bool) {
	value := rule.Value
	noResolve := rule.hasOption("no-resolve") && dialect != dialectQX
	withOption := func(text string) string {
		if noResolve {
			return text + ",no-resolve"
		}
		return text
	}
	if dialect == dialectQX {
		switch rule.Type {
		case "DOMAIN":
			return "host, " + value, true
		case "DOMAIN-SUFFIX":
			return "host-suffix, " + value, true
		case "DOMAIN-KEYWORD":
			return "host-keyword, " + value, true
		case "IP-CIDR":
			return "ip-cidr, " + value, true
		case "IP-CIDR6":
			return "ip6-cidr, " + value, true
		case "GEOIP":
			return "geoip, " + strings.ToLower(value), true
		}
		return "", false
	}
	switch rule.Type {
	case "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD":
		return rule.Type + "," + value, true
	case "IP-CIDR", "IP-CIDR6", "GEOIP":
		if rule.Type == "GEOIP" {
			value = strings.ToUpper(value)
		}
		return withOption(rule.Type + "," + value), true
	case "IP-ASN":
		if dialect == dialectSurfboard {
			return "", false
		}
		return withOption("IP-ASN," + value), true
	case "DOMAIN-WILDCARD", "PROCESS-NAME", "SRC-PORT":
		if dialect != dialectSurge {
			return "", false
		}
		return rule.Type + "," + value, true
	case "SRC-IP-CIDR":
		if dialect != dialectSurge {
			return "", false
		}
		return "SRC-IP," + value, true
	case "DST-PORT":
		if dialect == dialectLoon {
			return "", false
		}
		return "DEST-PORT," + value, true
	case "NETWORK":
		if dialect != dialectSurge {
			return "", false
		}
		return "PROTOCOL," + strings.ToUpper(value), true
	}
	return "", false
}

func iniLogicalMatch(rule subTemplateRule) (string, bool) {
	items := make([]string, 0, len(rule.Sub))
	for _, sub := range rule.Sub {
		var text string
		var ok bool
		if isLogicalRule(sub.Type) {
			text, ok = iniLogicalMatch(sub)
		} else {
			text, ok = iniRuleMatch(sub, dialectSurge)
		}
		if !ok {
			return "", false
		}
		items = append(items, "("+text+")")
	}
	return rule.Type + ",(" + strings.Join(items, ",") + ")", true
}

func isPrivateGeoIP(rule subTemplateRule) bool {
	value := strings.ToLower(rule.Value)
	return rule.Type == "GEOIP" && (value == "private" || value == "lan")
}

func ipCIDRType(cidr string) string {
	if strings.Contains(cidr, ":") {
		return "IP-CIDR6"
	}
	return "IP-CIDR"
}

// iniRuleLines converts one template rule into zero or more dialect lines.
// remote collects GEOSITE rule sets for dialects that list them separately.
func iniRuleLines(rule subTemplateRule, dialect iniDialect, remote *[]string) []string {
	policy, ok := iniPolicyName(rule.Target, dialect)
	if !ok {
		return []string{"# m-ui: skipped " + rule.String()}
	}
	join := func(match string) string {
		if dialect == dialectQX {
			return match + ", " + policy
		}
		if strings.HasSuffix(match, ",no-resolve") {
			return strings.TrimSuffix(match, ",no-resolve") + "," + policy + ",no-resolve"
		}
		return match + "," + policy
	}
	switch {
	case rule.Type == "MATCH":
		if dialect == dialectQX {
			return []string{"final, " + policy}
		}
		if dialect == dialectSurge {
			return []string{"FINAL," + policy + ",dns-failed"}
		}
		return []string{"FINAL," + policy}
	case isPrivateGeoIP(rule):
		lines := make([]string, 0, len(privateCIDRs))
		for _, cidr := range privateCIDRs {
			match, _ := iniRuleMatch(subTemplateRule{Type: ipCIDRType(cidr), Value: cidr, Options: []string{"no-resolve"}}, dialect)
			lines = append(lines, join(match))
		}
		return lines
	case rule.Type == "GEOSITE":
		name := strings.ToLower(rule.Value)
		if dialect == dialectQX || strings.Contains(name, "@") {
			return []string{"# m-ui: skipped " + rule.String()}
		}
		url := metaRulesClassicalGeosite + name + ".list"
		if dialect == dialectLoon {
			*remote = append(*remote, url+", policy="+policy+", tag=geosite-"+name+", enabled=true")
			return nil
		}
		return []string{"RULE-SET," + url + "," + policy}
	case isLogicalRule(rule.Type):
		if dialect == dialectSurge {
			if match, ok := iniLogicalMatch(rule); ok {
				return []string{match + "," + policy}
			}
		}
		return []string{"# m-ui: skipped " + rule.String()}
	}
	match, ok := iniRuleMatch(rule, dialect)
	if !ok {
		return []string{"# m-ui: skipped " + rule.String()}
	}
	return []string{join(match)}
}

// iniGroupLine renders one policy group.
func iniGroupLine(group subTemplateGroup, nodes []clientNode, dialect iniDialect) string {
	templateNodes := make([]templateNode, 0, len(nodes))
	for _, node := range nodes {
		templateNodes = append(templateNodes, templateNode{Name: node.Name, Type: node.Type})
	}
	members := make([]string, 0)
	for _, member := range group.expand(templateNodes) {
		if name, ok := iniPolicyName(member, dialect); ok && !slices.Contains(members, name) {
			members = append(members, name)
		}
	}
	if len(members) == 0 {
		members = []string{iniPolicyNameMust("DIRECT", dialect)}
	}
	name := iniSafeName(group.Name)
	url := firstNonEmpty(group.URL, defaultTemplateTestURL)
	interval := group.Interval
	if interval <= 0 {
		interval = 600
	}
	kind := "select"
	switch group.Type {
	case "url-test":
		kind = "url-test"
	case "fallback":
		kind = "fallback"
	case "load-balance":
		kind = "load-balance"
	}
	if dialect == dialectQX {
		qxKind := map[string]string{"select": "static", "url-test": "url-latency-benchmark", "fallback": "available", "load-balance": "round-robin"}[kind]
		line := qxKind + "=" + name + ", " + strings.Join(members, ", ")
		if kind == "url-test" {
			line += ", check-interval=" + strconv.Itoa(interval)
			if group.Tolerance > 0 {
				line += ", tolerance=" + strconv.Itoa(group.Tolerance)
			}
		}
		return line
	}
	if dialect == dialectLoon && kind == "load-balance" {
		kind = "url-test"
	}
	separator := ", "
	if dialect == dialectLoon {
		separator = ","
	}
	parts := append([]string{kind}, members...)
	if kind != "select" {
		parts = append(parts, "url="+url, "interval="+strconv.Itoa(interval))
		if kind == "url-test" && group.Tolerance > 0 {
			parts = append(parts, "tolerance="+strconv.Itoa(group.Tolerance))
		}
	}
	return name + " = " + strings.Join(parts, separator)
}

func iniPolicyNameMust(name string, dialect iniDialect) string {
	value, _ := iniPolicyName(name, dialect)
	return value
}

const iniSkipProxy = "127.0.0.1, 192.168.0.0/16, 10.0.0.0/8, 172.16.0.0/12, 100.64.0.0/10, localhost, *.local"

// renderIniTemplate renders a complete Surge / Surfboard / Loon / Quantumult X
// profile from the template. managedURL makes Surge-family clients refresh the
// profile from the subscription.
func renderIniTemplate(template *compiledSubTemplate, nodes []clientNode, dialect iniDialect, managedURL string) []byte {
	var builder strings.Builder
	line := func(text string) { builder.WriteString(text + "\n") }
	if managedURL != "" && (dialect == dialectSurge || dialect == dialectSurfboard) {
		line("#!MANAGED-CONFIG " + managedURL + " interval=86400 strict=false")
		line("")
	}
	remote := []string{}
	rules := []string{}
	for _, rule := range template.Model.Rules {
		rules = append(rules, iniRuleLines(rule, dialect, &remote)...)
	}
	groups := make([]string, 0, len(template.Model.Groups))
	for _, group := range template.Model.Groups {
		groups = append(groups, iniGroupLine(group, nodes, dialect))
	}
	if dialect == dialectQX {
		line("[general]")
		line("server_check_url=" + defaultTemplateTestURL)
		line("")
		line("[policy]")
		for _, group := range groups {
			line(group)
		}
		line("")
		line("[server_local]")
		for _, node := range nodes {
			line(node.Line)
		}
		line("")
		line("[filter_local]")
		for _, rule := range rules {
			line(rule)
		}
		return []byte(builder.String())
	}
	line("[General]")
	switch dialect {
	case dialectLoon:
		line("ipv6 = true")
		line("skip-proxy = " + strings.ReplaceAll(iniSkipProxy, ", ", ","))
		line("dns-server = system")
		line("proxy-test-url = " + defaultTemplateTestURL)
	default:
		line("loglevel = notify")
		line("ipv6 = true")
		line("skip-proxy = " + iniSkipProxy)
		line("dns-server = system")
		if dialect == dialectSurge {
			line("internet-test-url = " + defaultTemplateTestURL)
		}
		line("proxy-test-url = " + defaultTemplateTestURL)
	}
	line("")
	line("[Proxy]")
	for _, node := range nodes {
		line(node.Line)
	}
	line("")
	line("[Proxy Group]")
	for _, group := range groups {
		line(group)
	}
	line("")
	line("[Rule]")
	for _, rule := range rules {
		line(rule)
	}
	if dialect == dialectLoon && len(remote) > 0 {
		line("")
		line("[Remote Rule]")
		for _, entry := range remote {
			line(entry)
		}
	}
	return []byte(builder.String())
}

// ---- Egern ----

func egernPolicyName(name string) (string, bool) {
	switch strings.ToUpper(name) {
	case "DIRECT":
		return "DIRECT", true
	case "REJECT", "REJECT-DROP":
		return "REJECT", true
	case "PASS", "COMPATIBLE", "GLOBAL":
		return "", false
	}
	return name, true
}

func egernRuleMatch(rule subTemplateRule) (string, map[string]any, bool) {
	fields := map[string]any{"match": rule.Value}
	if rule.hasOption("no-resolve") {
		fields["no_resolve"] = true
	}
	key := map[string]string{
		"DOMAIN": "domain", "DOMAIN-SUFFIX": "domain_suffix", "DOMAIN-KEYWORD": "domain_keyword",
		"DOMAIN-REGEX": "domain_regex", "DOMAIN-WILDCARD": "domain_wildcard", "IP-CIDR": "ip_cidr",
		"IP-CIDR6": "ip_cidr6", "GEOIP": "geoip", "IP-ASN": "asn", "DST-PORT": "dest_port",
	}[rule.Type]
	if key == "" {
		return "", nil, false
	}
	if rule.Type == "GEOIP" {
		fields["match"] = strings.ToUpper(rule.Value)
	}
	return key, fields, true
}

func renderEgernTemplate(template *compiledSubTemplate, entries []map[string]any, nodes []clientNode) ([]byte, error) {
	templateNodes := make([]templateNode, 0, len(nodes))
	for _, node := range nodes {
		templateNodes = append(templateNodes, templateNode{Name: node.Name, Type: node.Type})
	}
	groups := make([]any, 0, len(template.Model.Groups))
	for _, group := range template.Model.Groups {
		members := make([]any, 0)
		for _, member := range group.expand(templateNodes) {
			if name, ok := egernPolicyName(member); ok && !slices.Contains(members, any(name)) {
				members = append(members, name)
			}
		}
		if len(members) == 0 {
			members = append(members, "DIRECT")
		}
		fields := map[string]any{"name": group.Name, "policies": members}
		kind := "select"
		switch group.Type {
		case "url-test", "load-balance":
			kind = "auto_test"
		case "fallback":
			kind = "fallback"
		}
		if kind != "select" {
			interval := group.Interval
			if interval <= 0 {
				interval = 600
			}
			fields["interval"] = interval
			if kind == "auto_test" && group.Tolerance > 0 {
				fields["tolerance"] = group.Tolerance
			}
		}
		groups = append(groups, map[string]any{kind: fields})
	}
	rules := make([]any, 0, len(template.Model.Rules))
	for _, rule := range template.Model.Rules {
		policy, ok := egernPolicyName(rule.Target)
		if !ok {
			continue
		}
		switch {
		case rule.Type == "MATCH":
			rules = append(rules, map[string]any{"default": map[string]any{"policy": policy}})
		case isPrivateGeoIP(rule):
			for _, cidr := range privateCIDRs {
				key := "ip_cidr"
				if strings.Contains(cidr, ":") {
					key = "ip_cidr6"
				}
				rules = append(rules, map[string]any{key: map[string]any{"match": cidr, "policy": policy, "no_resolve": true}})
			}
		case rule.Type == "GEOSITE":
			name := strings.ToLower(rule.Value)
			if strings.Contains(name, "@") {
				continue
			}
			rules = append(rules, map[string]any{"rule_set": map[string]any{"match": metaRulesClassicalGeosite + name + ".list", "policy": policy, "update_interval": 86400}})
		default:
			key, fields, ok := egernRuleMatch(rule)
			if !ok {
				continue
			}
			fields["policy"] = policy
			rules = append(rules, map[string]any{key: fields})
		}
	}
	list := make([]any, 0, len(entries))
	for _, entry := range entries {
		list = append(list, entry)
	}
	return marshalYAML(map[string]any{"proxies": list, "policy_groups": groups, "rules": rules})
}
