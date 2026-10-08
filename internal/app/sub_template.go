package app

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// 订阅模版：Mihomo 格式的配置骨架（分组 + 规则 + 基础设置），不含任何节点。
// 生成订阅时把当前用户的节点填进模版里"放节点"的分组，再按客户端转换。
//
// "放节点"用 Mihomo 自己的 include-all-proxies 标记，所以存下来的模版本身仍是
// 合法的 Mihomo 配置；输出时再展开成显式列表，老客户端也认。

const (
	subTemplateFile        = "sub-templates.json"
	subTemplateFileVersion = 1
	subTemplateBuiltinID   = "builtin"
	subTemplateMaxBytes    = 2 << 20
	subTemplateMaxCount    = 50
)

// subTemplateVariants are the country variants of the built-in template; the
// first one is the default.
var subTemplateVariants = []string{"cn", "ru", "ir"}

// mihomoBuiltinTargets are policy names every Mihomo config understands without
// declaring them, so template cleaning never mistakes them for nodes.
var mihomoBuiltinTargets = []string{"DIRECT", "REJECT", "REJECT-DROP", "PASS", "COMPATIBLE", "GLOBAL"}

type subTemplateRecord struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type subTemplateDisk struct {
	Version   int                 `json:"version"`
	Active    string              `json:"active"`
	Variant   string              `json:"variant"`
	Templates []subTemplateRecord `json:"templates"`
}

// subTemplateStats describes what cleaning did to an imported document.
type subTemplateStats struct {
	RemovedProxies   int `json:"removedProxies"`
	RemovedProviders int `json:"removedProviders"`
	NodeGroups       int `json:"nodeGroups"`
	Groups           int `json:"groups"`
	Rules            int `json:"rules"`
	RetargetedRules  int `json:"retargetedRules"`
}

// compiledSubTemplate is a parsed, ready-to-render template. Doc is shared and
// must be cloned before it is modified.
type compiledSubTemplate struct {
	ID    string
	Name  string
	Doc   *yaml.Node
	Model subTemplateModel
}

type SubTemplateManager struct {
	mu       sync.Mutex
	path     string
	disk     subTemplateDisk
	compiled *compiledSubTemplate
}

func newSubTemplateManager(dataDir string) (*SubTemplateManager, error) {
	m := &SubTemplateManager{path: filepath.Join(dataDir, subTemplateFile), disk: defaultSubTemplateDisk()}
	data, err := os.ReadFile(m.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		var disk subTemplateDisk
		if err := json.Unmarshal(data, &disk); err != nil {
			return nil, fmt.Errorf("订阅模版文件无效: %w", err)
		}
		if err := validateSubTemplateDisk(&disk); err != nil {
			return nil, err
		}
		m.disk = disk
	}
	return m, nil
}

func defaultSubTemplateDisk() subTemplateDisk {
	return subTemplateDisk{Version: subTemplateFileVersion, Active: subTemplateBuiltinID, Variant: subTemplateVariants[0], Templates: []subTemplateRecord{}}
}

// validateSubTemplateDisk checks a document read from disk or a backup and
// repairs the selection fields that can safely fall back.
func validateSubTemplateDisk(disk *subTemplateDisk) error {
	if disk.Version != subTemplateFileVersion {
		return fmt.Errorf("不支持的订阅模版文件版本: %d", disk.Version)
	}
	if !slices.Contains(subTemplateVariants, disk.Variant) {
		disk.Variant = subTemplateVariants[0]
	}
	if disk.Templates == nil {
		disk.Templates = []subTemplateRecord{}
	}
	if len(disk.Templates) > subTemplateMaxCount {
		return fmt.Errorf("订阅模版数量超过上限")
	}
	seen := map[string]bool{}
	for _, record := range disk.Templates {
		if record.ID == "" || record.ID == subTemplateBuiltinID || seen[record.ID] {
			return fmt.Errorf("订阅模版 ID 无效")
		}
		seen[record.ID] = true
		if _, _, err := parseSubTemplate([]byte(record.Content)); err != nil {
			return fmt.Errorf("订阅模版无效: %s: %w", record.Name, err)
		}
	}
	if disk.Active != subTemplateBuiltinID && !seen[disk.Active] {
		disk.Active = subTemplateBuiltinID
	}
	return nil
}

func (m *SubTemplateManager) persistLocked() error {
	data, err := json.MarshalIndent(m.disk, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(m.path), ".sub-templates-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), m.path)
}

func (m *SubTemplateManager) exportDisk() subTemplateDisk {
	m.mu.Lock()
	defer m.mu.Unlock()
	copy := m.disk
	copy.Templates = slices.Clone(m.disk.Templates)
	return copy
}

func (m *SubTemplateManager) restoreDisk(disk subTemplateDisk) error {
	if err := validateSubTemplateDisk(&disk); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := m.disk
	m.disk = disk
	m.compiled = nil
	if err := m.persistLocked(); err != nil {
		m.disk = previous
		return err
	}
	return nil
}

// active returns the compiled template subscriptions should use. Records were
// validated when stored, so failures here only come from a corrupted builtin,
// which tests guard against.
func (m *SubTemplateManager) active() *compiledSubTemplate {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.compiled != nil {
		return m.compiled
	}
	id, name, content := subTemplateBuiltinID, "", builtinSubTemplateYAML(m.disk.Variant)
	if m.disk.Active != subTemplateBuiltinID {
		for _, record := range m.disk.Templates {
			if record.ID == m.disk.Active {
				id, name, content = record.ID, record.Name, record.Content
			}
		}
	}
	doc, model, err := parseSubTemplate([]byte(content))
	if err != nil {
		return nil
	}
	m.compiled = &compiledSubTemplate{ID: id, Name: name, Doc: doc, Model: model}
	return m.compiled
}

func newSubTemplateID() (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

// ---- built-in template ----

type builtinTemplateVariant struct {
	proxyGroup, autoGroup string
	direct                []string // country specific direct rules, before MATCH
}

var builtinTemplateVariantRules = map[string]builtinTemplateVariant{
	"cn": {proxyGroup: "节点选择", autoGroup: "自动选择", direct: []string{
		// 国内公共 DNS 直连，避免 DNS 查询被代理绕远。
		"IP-CIDR,223.5.5.5/32,DIRECT,no-resolve", "IP-CIDR,223.6.6.6/32,DIRECT,no-resolve",
		"IP-CIDR,2400:3200::1/128,DIRECT,no-resolve", "IP-CIDR,2400:3200:baba::1/128,DIRECT,no-resolve",
		"IP-CIDR,119.29.29.29/32,DIRECT,no-resolve", "IP-CIDR,1.12.12.12/32,DIRECT,no-resolve",
		"IP-CIDR,120.53.53.53/32,DIRECT,no-resolve", "IP-CIDR,2402:4e00::/128,DIRECT,no-resolve",
		"IP-CIDR,2402:4e00:1::/128,DIRECT,no-resolve", "IP-CIDR,180.76.76.76/32,DIRECT,no-resolve",
		"IP-CIDR,2400:da00::6666/128,DIRECT,no-resolve", "IP-CIDR,114.114.114.114/32,DIRECT,no-resolve",
		"IP-CIDR,114.114.115.115/32,DIRECT,no-resolve", "IP-CIDR,114.114.114.119/32,DIRECT,no-resolve",
		"IP-CIDR,114.114.115.119/32,DIRECT,no-resolve", "IP-CIDR,114.114.114.110/32,DIRECT,no-resolve",
		"IP-CIDR,114.114.115.110/32,DIRECT,no-resolve", "IP-CIDR,180.184.1.1/32,DIRECT,no-resolve",
		"IP-CIDR,180.184.2.2/32,DIRECT,no-resolve", "IP-CIDR,101.226.4.6/32,DIRECT,no-resolve",
		"IP-CIDR,218.30.118.6/32,DIRECT,no-resolve", "IP-CIDR,123.125.81.6/32,DIRECT,no-resolve",
		"IP-CIDR,140.207.198.6/32,DIRECT,no-resolve", "IP-CIDR,1.2.4.8/32,DIRECT,no-resolve",
		"IP-CIDR,210.2.4.8/32,DIRECT,no-resolve", "IP-CIDR,52.80.66.66/32,DIRECT,no-resolve",
		"IP-CIDR,117.50.22.22/32,DIRECT,no-resolve", "IP-CIDR,2400:7fc0:849e:200::4/128,DIRECT,no-resolve",
		"IP-CIDR,2404:c2c0:85d8:901::4/128,DIRECT,no-resolve", "IP-CIDR,117.50.10.10/32,DIRECT,no-resolve",
		"IP-CIDR,52.80.52.52/32,DIRECT,no-resolve", "IP-CIDR,2400:7fc0:849e:200::8/128,DIRECT,no-resolve",
		"IP-CIDR,2404:c2c0:85d8:901::8/128,DIRECT,no-resolve", "IP-CIDR,117.50.60.30/32,DIRECT,no-resolve",
		"IP-CIDR,52.80.60.30/32,DIRECT,no-resolve",
		"DOMAIN-SUFFIX,alidns.com,DIRECT", "DOMAIN-SUFFIX,doh.pub,DIRECT", "DOMAIN-SUFFIX,dot.pub,DIRECT",
		"DOMAIN-SUFFIX,360.cn,DIRECT", "DOMAIN-SUFFIX,onedns.net,DIRECT",
		"GEOIP,cn,DIRECT,no-resolve", "GEOSITE,cn,DIRECT",
	}},
	"ru": {proxyGroup: "Proxy", autoGroup: "Auto", direct: []string{
		// Yandex DNS.
		"IP-CIDR,77.88.8.8/32,DIRECT,no-resolve", "IP-CIDR,77.88.8.1/32,DIRECT,no-resolve",
		"IP-CIDR,2a02:6b8::feed:0ff/128,DIRECT,no-resolve", "IP-CIDR,2a02:6b8:0:1::feed:0ff/128,DIRECT,no-resolve",
		"DOMAIN-SUFFIX,dns.yandex,DIRECT",
		"GEOIP,ru,DIRECT,no-resolve", "GEOSITE,category-ru,DIRECT",
	}},
	"ir": {proxyGroup: "Proxy", autoGroup: "Auto", direct: []string{
		// Shecan DNS.
		"IP-CIDR,178.22.122.100/32,DIRECT,no-resolve", "IP-CIDR,185.51.200.2/32,DIRECT,no-resolve",
		"DOMAIN-SUFFIX,shecan.ir,DIRECT",
		"GEOIP,ir,DIRECT,no-resolve", "GEOSITE,category-ir,DIRECT",
	}},
}

// builtinSubTemplateYAML renders the built-in template for one country variant.
// The variants share settings, groups and proxy rules and only differ in which
// country's traffic goes direct.
func builtinSubTemplateYAML(variant string) string {
	spec, ok := builtinTemplateVariantRules[variant]
	if !ok {
		spec = builtinTemplateVariantRules[subTemplateVariants[0]]
	}
	quote := func(value string) string { data, _ := json.Marshal(value); return string(data) }
	proxy := spec.proxyGroup
	rules := []string{
		"AND,((NETWORK,UDP),(DST-PORT,443)),REJECT",
		"DOMAIN-SUFFIX,browserleaks.com," + proxy,
		"DOMAIN-SUFFIX,browserleaks.org," + proxy,
		"DOMAIN-SUFFIX,ipleak.net," + proxy,
		"DOMAIN-SUFFIX,googleapis.cn," + proxy,
		"DOMAIN-SUFFIX,gstatic.com," + proxy,
		"GEOSITE,google," + proxy,
		"GEOSITE,category-ai-!cn," + proxy,
		"GEOSITE,apple," + proxy,
		"GEOSITE,tiktok," + proxy,
		"GEOSITE,microsoft," + proxy,
		"GEOIP,private,DIRECT,no-resolve",
		"GEOSITE,private,DIRECT",
	}
	rules = append(rules, spec.direct...)
	rules = append(rules, "MATCH,"+proxy)
	var builder strings.Builder
	builder.WriteString(`mixed-port: 7890
allow-lan: false
mode: "rule"
log-level: "info"
ipv6: true
unified-delay: true
tcp-concurrent: true
external-controller: "127.0.0.1:9090"
geodata-mode: true
geo-auto-update: true
geo-update-interval: 24
profile:
  store-selected: true
proxy-groups:
`)
	fmt.Fprintf(&builder, "  - name: %s\n    type: \"select\"\n    proxies:\n      - %s\n      - \"DIRECT\"\n    include-all-proxies: true\n", quote(proxy), quote(spec.autoGroup))
	fmt.Fprintf(&builder, "  - name: %s\n    type: \"url-test\"\n    include-all-proxies: true\n    url: \"https://www.gstatic.com/generate_204\"\n    interval: 300\n    tolerance: 50\n", quote(spec.autoGroup))
	builder.WriteString("rules:\n")
	for _, rule := range rules {
		builder.WriteString("  - " + quote(rule) + "\n")
	}
	return builder.String()
}

// ---- yaml.Node helpers ----

func yamlMapIndex(node *yaml.Node, key string) int {
	if node == nil || node.Kind != yaml.MappingNode {
		return -1
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return i
		}
	}
	return -1
}

func yamlMapGet(node *yaml.Node, key string) *yaml.Node {
	if i := yamlMapIndex(node, key); i >= 0 {
		return node.Content[i+1]
	}
	return nil
}

func yamlMapDelete(node *yaml.Node, keys ...string) {
	for _, key := range keys {
		if i := yamlMapIndex(node, key); i >= 0 {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
		}
	}
}

// yamlMapSet replaces key's value, or inserts it before the key named before
// (appending when before is absent).
func yamlMapSet(node *yaml.Node, key string, value *yaml.Node, before string) {
	if i := yamlMapIndex(node, key); i >= 0 {
		node.Content[i+1] = value
		return
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	if i := yamlMapIndex(node, before); before != "" && i >= 0 {
		node.Content = append(node.Content[:i], append([]*yaml.Node{keyNode, value}, node.Content[i:]...)...)
		return
	}
	node.Content = append(node.Content, keyNode, value)
}

func yamlBool(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.ScalarNode && strings.EqualFold(node.Value, "true")
}

func yamlStr(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.DoubleQuotedStyle}
}

func yamlTrue() *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}
}

func cloneYAMLNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	copy := *node
	if node.Content != nil {
		copy.Content = make([]*yaml.Node, len(node.Content))
		for i, child := range node.Content {
			copy.Content[i] = cloneYAMLNode(child)
		}
	}
	copy.Alias = cloneYAMLNode(node.Alias)
	return &copy
}

// yamlRoot returns the top-level mapping of a document node.
func yamlRoot(doc *yaml.Node) *yaml.Node {
	if doc != nil && doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		return doc.Content[0]
	}
	return doc
}

func encodeYAMLNode(doc *yaml.Node) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(doc); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// ---- import: strip nodes, keep the skeleton ----

func decodeSubTemplateDocument(content []byte) (*yaml.Node, error) {
	if len(bytes.TrimSpace(content)) == 0 {
		return nil, fmt.Errorf("模版内容为空")
	}
	if len(content) > subTemplateMaxBytes {
		return nil, fmt.Errorf("模版超过 2 MB")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("YAML 解析失败: %w", err)
	}
	root := yamlRoot(&doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("不是 Mihomo 配置：顶层必须是键值映射")
	}
	// Anchors and merge keys would make node removal unreliable (a stripped
	// proxy could live on through an alias), so resolve them up front by a
	// decode/encode round trip through plain values.
	if containsYAMLAlias(root) {
		var plain any
		if err := doc.Decode(&plain); err != nil {
			return nil, fmt.Errorf("YAML 解析失败: %w", err)
		}
		data, err := yaml.Marshal(plain)
		if err != nil {
			return nil, err
		}
		doc = yaml.Node{}
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
	}
	return &doc, nil
}

func stripYAMLComments(node *yaml.Node) {
	if node == nil {
		return
	}
	node.HeadComment, node.LineComment, node.FootComment = "", "", ""
	for _, child := range node.Content {
		stripYAMLComments(child)
	}
}

func containsYAMLAlias(node *yaml.Node) bool {
	if node == nil {
		return false
	}
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return true
	}
	for _, child := range node.Content {
		if containsYAMLAlias(child) {
			return true
		}
	}
	return false
}

// cleanSubTemplate turns a full Mihomo subscription or a node-less template
// into a stored template: proxies and proxy-providers are removed, groups that
// listed nodes (or pulled them from providers) are marked include-all-proxies,
// and rules that pointed straight at a node are pointed at the first such group.
func cleanSubTemplate(content []byte) ([]byte, subTemplateStats, error) {
	var stats subTemplateStats
	doc, err := decodeSubTemplateDocument(content)
	if err != nil {
		return nil, stats, err
	}
	// Comments go too: every subscriber receives the template, and a header
	// comment in an exported config often names the source or the nodes.
	stripYAMLComments(doc)
	root := yamlRoot(doc)
	nodeNames := map[string]bool{}
	if proxies := yamlMapGet(root, "proxies"); proxies != nil && proxies.Kind == yaml.SequenceNode {
		for _, item := range proxies.Content {
			if name := yamlMapGet(item, "name"); name != nil {
				nodeNames[name.Value] = true
			}
		}
		stats.RemovedProxies = len(proxies.Content)
	}
	if providers := yamlMapGet(root, "proxy-providers"); providers != nil && providers.Kind == yaml.MappingNode {
		stats.RemovedProviders = len(providers.Content) / 2
	}
	yamlMapDelete(root, "proxies", "proxy-providers")

	groups := yamlMapGet(root, "proxy-groups")
	if groups == nil || groups.Kind != yaml.SequenceNode || len(groups.Content) == 0 {
		return nil, stats, fmt.Errorf("缺少 proxy-groups 分组")
	}
	groupNames := map[string]bool{}
	for _, group := range groups.Content {
		name := yamlMapGet(group, "name")
		if group.Kind != yaml.MappingNode || name == nil || strings.TrimSpace(name.Value) == "" {
			return nil, stats, fmt.Errorf("proxy-groups 里有缺少 name 的分组")
		}
		if groupNames[name.Value] {
			return nil, stats, fmt.Errorf("分组名重复: %s", name.Value)
		}
		groupNames[name.Value] = true
	}
	keep := func(member string) bool {
		return groupNames[member] || slices.Contains(mihomoBuiltinTargets, strings.ToUpper(member))
	}
	firstNodeGroup := ""
	for _, group := range groups.Content {
		receivesNodes := yamlBool(yamlMapGet(group, "include-all")) || yamlBool(yamlMapGet(group, "include-all-proxies")) ||
			yamlBool(yamlMapGet(group, "include-all-providers")) || yamlMapGet(group, "use") != nil
		members := yamlMapGet(group, "proxies")
		if members != nil && members.Kind == yaml.SequenceNode {
			kept := members.Content[:0]
			for _, member := range members.Content {
				if member.Kind == yaml.ScalarNode && keep(member.Value) {
					kept = append(kept, member)
					continue
				}
				receivesNodes = true
			}
			members.Content = kept
			if len(members.Content) == 0 {
				yamlMapDelete(group, "proxies")
				members = nil
			}
		}
		if members == nil && !receivesNodes {
			// A group with nothing in it can only have been meant for nodes.
			receivesNodes = true
		}
		yamlMapDelete(group, "use", "include-all", "include-all-providers")
		if receivesNodes {
			yamlMapSet(group, "include-all-proxies", yamlTrue(), "")
			stats.NodeGroups++
			if firstNodeGroup == "" {
				firstNodeGroup = yamlMapGet(group, "name").Value
			}
		} else {
			yamlMapDelete(group, "include-all-proxies")
		}
	}
	stats.Groups = len(groups.Content)

	rules := yamlMapGet(root, "rules")
	if rules == nil || rules.Kind != yaml.SequenceNode || len(rules.Content) == 0 {
		return nil, stats, fmt.Errorf("缺少 rules 规则")
	}
	for _, item := range rules.Content {
		if item.Kind != yaml.ScalarNode {
			return nil, stats, fmt.Errorf("rules 里有不是字符串的规则")
		}
		rule, err := parseTemplateRule(item.Value)
		if err != nil {
			return nil, stats, err
		}
		if rule.Target != "" && nodeNames[rule.Target] && !groupNames[rule.Target] {
			if firstNodeGroup == "" {
				return nil, stats, fmt.Errorf("规则指向节点，但没有可放节点的分组: %s", item.Value)
			}
			rule.Target = firstNodeGroup
			item.Value = rule.String()
			item.Style = yaml.DoubleQuotedStyle
			stats.RetargetedRules++
		}
	}
	stats.Rules = len(rules.Content)
	data, err := encodeYAMLNode(doc)
	if err != nil {
		return nil, stats, err
	}
	if _, _, err := parseSubTemplate(data); err != nil {
		return nil, stats, err
	}
	return data, stats, nil
}

// parseSubTemplate parses a stored (already cleaned) template.
func parseSubTemplate(content []byte) (*yaml.Node, subTemplateModel, error) {
	doc, err := decodeSubTemplateDocument(content)
	if err != nil {
		return nil, subTemplateModel{}, err
	}
	root := yamlRoot(doc)
	if yamlMapGet(root, "proxies") != nil || yamlMapGet(root, "proxy-providers") != nil {
		return nil, subTemplateModel{}, fmt.Errorf("模版里不能包含节点")
	}
	model, err := buildSubTemplateModel(root)
	if err != nil {
		return nil, subTemplateModel{}, err
	}
	return doc, model, nil
}

// ---- API ----

type subTemplateListItem struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Builtin   bool      `json:"builtin"`
	Groups    int       `json:"groups"`
	Rules     int       `json:"rules"`
	CreatedAt time.Time `json:"createdAt,omitzero"`
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
}

type subTemplateView struct {
	Active    string                `json:"active"`
	Variant   string                `json:"variant"`
	Variants  []string              `json:"variants"`
	Templates []subTemplateListItem `json:"templates"`
}

func (m *SubTemplateManager) view() subTemplateView {
	m.mu.Lock()
	defer m.mu.Unlock()
	view := subTemplateView{Active: m.disk.Active, Variant: m.disk.Variant, Variants: subTemplateVariants}
	_, builtin, _ := parseSubTemplate([]byte(builtinSubTemplateYAML(m.disk.Variant)))
	view.Templates = append(view.Templates, subTemplateListItem{ID: subTemplateBuiltinID, Builtin: true, Groups: len(builtin.Groups), Rules: len(builtin.Rules)})
	for _, record := range m.disk.Templates {
		item := subTemplateListItem{ID: record.ID, Name: record.Name, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
		if _, model, err := parseSubTemplate([]byte(record.Content)); err == nil {
			item.Groups, item.Rules = len(model.Groups), len(model.Rules)
		}
		view.Templates = append(view.Templates, item)
	}
	return view
}

func (m *SubTemplateManager) content(id, variant string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == subTemplateBuiltinID {
		if !slices.Contains(subTemplateVariants, variant) {
			variant = m.disk.Variant
		}
		return builtinSubTemplateYAML(variant), true
	}
	for _, record := range m.disk.Templates {
		if record.ID == id {
			return record.Content, true
		}
	}
	return "", false
}

func cleanSubTemplateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("模版名称不能为空")
	}
	if len([]rune(name)) > 64 {
		return "", fmt.Errorf("模版名称不能超过 64 个字符")
	}
	return name, nil
}

func (m *SubTemplateManager) add(name string, content []byte) (subTemplateRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.disk.Templates) >= subTemplateMaxCount {
		return subTemplateRecord{}, fmt.Errorf("订阅模版数量超过上限")
	}
	id, err := newSubTemplateID()
	if err != nil {
		return subTemplateRecord{}, err
	}
	now := time.Now().UTC()
	record := subTemplateRecord{ID: id, Name: name, Content: string(content), CreatedAt: now, UpdatedAt: now}
	m.disk.Templates = append(m.disk.Templates, record)
	if err := m.persistLocked(); err != nil {
		m.disk.Templates = m.disk.Templates[:len(m.disk.Templates)-1]
		return subTemplateRecord{}, err
	}
	return record, nil
}

func (m *SubTemplateManager) mutate(change func(disk *subTemplateDisk) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := m.disk
	previous.Templates = slices.Clone(m.disk.Templates)
	if err := change(&m.disk); err != nil {
		m.disk = previous
		return err
	}
	m.compiled = nil
	if err := m.persistLocked(); err != nil {
		m.disk = previous
		return err
	}
	return nil
}

func (m *SubTemplateManager) findLocked(id string) int {
	return slices.IndexFunc(m.disk.Templates, func(record subTemplateRecord) bool { return record.ID == id })
}

// handleSubTemplates serves the subscription template API:
//
//	GET    /api/sub-templates                  list + selection
//	GET    /api/sub-templates/content?id=&variant=  template YAML
//	POST   /api/sub-templates/import           {name, content, preview}
//	POST   /api/sub-templates/active           {id, variant}
//	PUT    /api/sub-templates/{id}             {name}
//	DELETE /api/sub-templates/{id}
//
// Failures carry an i18n key in message (subTemplates.err.*) plus the raw
// detail, so the page can translate them.
func (a *App) handleSubTemplates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	m := a.subTemplates
	if m == nil {
		writeJSON(w, http.StatusServiceUnavailable, apiResponse{Message: "subTemplates.err.unavailable"})
		return
	}
	fail := func(status int, key string, err error) {
		data := map[string]string{}
		if err != nil {
			data["detail"] = a.tr(err.Error())
		}
		writeJSON(w, status, apiResponse{Message: "subTemplates.err." + key, Data: data})
	}
	action := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/sub-templates"), "/")
	readBody := func(target any) bool {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, subTemplateMaxBytes+64<<10)).Decode(target); err != nil {
			fail(http.StatusBadRequest, "badRequest", err)
			return false
		}
		return true
	}
	switch {
	case r.Method == http.MethodGet && action == "":
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: m.view()})
	case r.Method == http.MethodGet && action == "content":
		content, ok := m.content(r.URL.Query().Get("id"), r.URL.Query().Get("variant"))
		if !ok {
			fail(http.StatusNotFound, "notFound", nil)
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: map[string]string{"content": content}})
	case r.Method == http.MethodPost && action == "import":
		var body struct {
			Name    string `json:"name"`
			Content string `json:"content"`
			Preview bool   `json:"preview"`
		}
		if !readBody(&body) {
			return
		}
		cleaned, stats, err := cleanSubTemplate([]byte(body.Content))
		if err != nil {
			fail(http.StatusBadRequest, "invalid", err)
			return
		}
		result := map[string]any{"stats": stats, "content": string(cleaned)}
		if body.Preview {
			writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: result})
			return
		}
		name, err := cleanSubTemplateName(body.Name)
		if err != nil {
			fail(http.StatusBadRequest, "name", err)
			return
		}
		record, err := m.add(name, cleaned)
		if err != nil {
			fail(http.StatusBadRequest, "save", err)
			return
		}
		result["id"] = record.ID
		result["view"] = m.view()
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: result})
	case r.Method == http.MethodPost && action == "active":
		var body struct {
			ID      string `json:"id"`
			Variant string `json:"variant"`
		}
		if !readBody(&body) {
			return
		}
		err := m.mutate(func(disk *subTemplateDisk) error {
			if body.ID != subTemplateBuiltinID && m.findLocked(body.ID) < 0 {
				return errNotFound
			}
			disk.Active = body.ID
			if body.Variant != "" {
				if !slices.Contains(subTemplateVariants, body.Variant) {
					return fmt.Errorf("未知的模版变体")
				}
				disk.Variant = body.Variant
			}
			return nil
		})
		if errors.Is(err, errNotFound) {
			fail(http.StatusNotFound, "notFound", nil)
			return
		}
		if err != nil {
			fail(http.StatusBadRequest, "save", err)
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: m.view()})
	case r.Method == http.MethodPut && action != "" && action != subTemplateBuiltinID:
		var body struct {
			Name string `json:"name"`
		}
		if !readBody(&body) {
			return
		}
		name, err := cleanSubTemplateName(body.Name)
		if err != nil {
			fail(http.StatusBadRequest, "name", err)
			return
		}
		err = m.mutate(func(disk *subTemplateDisk) error {
			index := m.findLocked(action)
			if index < 0 {
				return errNotFound
			}
			disk.Templates[index].Name = name
			disk.Templates[index].UpdatedAt = time.Now().UTC()
			return nil
		})
		if errors.Is(err, errNotFound) {
			fail(http.StatusNotFound, "notFound", nil)
			return
		}
		if err != nil {
			fail(http.StatusInternalServerError, "save", err)
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: m.view()})
	case r.Method == http.MethodDelete && action != "" && action != subTemplateBuiltinID:
		err := m.mutate(func(disk *subTemplateDisk) error {
			index := m.findLocked(action)
			if index < 0 {
				return errNotFound
			}
			disk.Templates = slices.Delete(disk.Templates, index, index+1)
			if disk.Active == action {
				disk.Active = subTemplateBuiltinID
			}
			return nil
		})
		if errors.Is(err, errNotFound) {
			fail(http.StatusNotFound, "notFound", nil)
			return
		}
		if err != nil {
			fail(http.StatusInternalServerError, "save", err)
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: m.view()})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
	}
}

var errNotFound = errors.New("not found")
