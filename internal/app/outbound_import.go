package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	convert "github.com/RomanovCaesar/m-ui/internal/mihomoconvert"
	"gopkg.in/yaml.v3"
)

type mihomoYAMLParseRequest struct {
	YAML      string           `json:"yaml"`
	Link      string           `json:"link"`
	Outbound  *MihomoOutbound  `json:"outbound,omitempty"`
	Context   []MihomoOutbound `json:"context,omitempty"`
	EditingID string           `json:"editingId,omitempty"`
	Preview   bool             `json:"preview,omitempty"`
}

func (a *App) handleMihomoOutboundYAML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	var input mihomoYAMLParseRequest
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: "invalid import request"})
		return
	}
	items, data, err := convertOutboundInput(input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: map[string]any{"outbounds": items, "yaml": string(data)}})
}

func convertOutboundInput(input mihomoYAMLParseRequest) ([]MihomoOutbound, []byte, error) {
	var items []MihomoOutbound
	var err error
	if input.Outbound != nil {
		// Exporting a partly filled form must not require valid credentials yet.
		item := *input.Outbound
		if item.Native != nil {
			item.Native, err = mergeOutboundForm(item)
			if err != nil {
				return nil, nil, err
			}
		}
		data, err := mihomoOutboundYAML([]MihomoOutbound{item})
		return []MihomoOutbound{item}, data, err
	}
	if input.Preview {
		items, err := previewMihomoOutboundYAML(input.YAML)
		return items, []byte(input.YAML), err
	}
	if strings.TrimSpace(input.Link) != "" {
		items, err = parseMihomoShareLink(input.Link)
	} else {
		items, err = parseMihomoOutboundYAML(input.YAML)
	}
	if err != nil {
		return nil, nil, err
	}
	if input.EditingID != "" && len(items) != 1 {
		return nil, nil, fmt.Errorf("编辑 Outbound 时 YAML 必须只包含一个节点或策略组")
	}
	context := make([]MihomoOutbound, 0, len(input.Context)+len(items))
	oldName := ""
	for _, item := range input.Context {
		if input.EditingID != "" && item.ID == input.EditingID {
			oldName = item.Name
		}
	}
	for _, item := range input.Context {
		if input.EditingID != "" && item.ID == input.EditingID {
			continue
		}
		if oldName != "" && len(items) == 1 && oldName != items[0].Name {
			data, _ := json.Marshal(item)
			var copied MihomoOutbound
			_ = json.Unmarshal(data, &copied)
			item = copied
			for i, member := range item.Proxies {
				if member == oldName {
					item.Proxies[i] = items[0].Name
				}
			}
			if item.DialerProxy == oldName {
				item.DialerProxy = items[0].Name
			}
			if item.DefaultSelected == oldName {
				item.DefaultSelected = items[0].Name
			}
			if item.Native != nil {
				if members, ok := item.Native["proxies"].([]any); ok {
					for i, member := range members {
						if member == oldName {
							members[i] = items[0].Name
						}
					}
				}
				for _, key := range []string{"dialer-proxy", "default-selected"} {
					if item.Native[key] == oldName {
						item.Native[key] = items[0].Name
					}
				}
			}
		}
		context = append(context, item)
	}
	// Resolve members against both the current unsaved draft and this import batch.
	normalized, err := normalizeMihomoOutbounds(append(context, items...))
	if err != nil {
		return nil, nil, err
	}
	items = normalized[len(context):]
	if input.EditingID != "" {
		items[0].ID = input.EditingID
		if items[0].Type == "wireguard" {
			for _, existing := range input.Context {
				if existing.ID == input.EditingID {
					items[0].WarpDeviceID = existing.WarpDeviceID
					break
				}
			}
		}
	}
	data, err := mihomoOutboundYAML(items)
	return items, data, err
}

func decodeOutboundYAML(source string) (any, error) {
	if len(source) > 512*1024 {
		return nil, fmt.Errorf("YAML 不能超过 512 KB")
	}
	decoder := yaml.NewDecoder(strings.NewReader(source))
	var document any
	if err := decoder.Decode(&document); err != nil {
		if err == io.EOF {
			return nil, nil
		}
		return nil, fmt.Errorf("YAML 解析失败: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("一次只能导入一个 YAML 文档")
	}
	return document, nil
}

// Tab navigation only projects a draft into form fields. Required fields,
// ranges and references are validated by the normal Apply path instead.
func previewMihomoOutboundYAML(source string) ([]MihomoOutbound, error) {
	document, err := decodeOutboundYAML(source)
	if err != nil {
		return nil, err
	}
	if document == nil {
		document = map[string]any{}
	}
	if root, ok := document.(map[string]any); ok && root["type"] == nil {
		if value, ok := root["proxy-groups"]; ok && len(root) == 1 {
			document = value
		}
		if value, ok := root["proxies"]; ok && len(root) == 1 {
			document = value
		}
	}
	if list, ok := document.([]any); ok {
		if len(list) != 1 {
			return nil, nil
		}
		document = list[0]
	}
	mapping, ok := document.(map[string]any)
	if !ok {
		return nil, nil
	}
	data, err := json.Marshal(mapping)
	if err != nil {
		return nil, err
	}
	var native map[string]any
	if err := json.Unmarshal(data, &native); err != nil {
		return nil, err
	}
	item, err := mihomoOutboundFromYAMLMap(native, isMihomoGroupType(outboundStringValue(native["type"])))
	item.Native = native
	return []MihomoOutbound{item}, err
}

func parseMihomoOutboundYAML(source string) ([]MihomoOutbound, error) {
	document, err := decodeOutboundYAML(source)
	if err != nil {
		return nil, err
	}
	var items []MihomoOutbound
	appendMap := func(mapping map[string]any, kind string) error {
		item, err := nativeOutbound(mapping)
		if err != nil {
			return err
		}
		if kind != "" && item.Kind != kind {
			return fmt.Errorf("%s 的类型 %s 与所在数组不符", item.Name, item.Type)
		}
		items = append(items, item)
		return nil
	}
	appendList := func(value any, kind string) error {
		list, ok := value.([]any)
		if !ok {
			return fmt.Errorf("proxies / proxy-groups 必须是 YAML 数组")
		}
		for i, entry := range list {
			mapping, ok := entry.(map[string]any)
			if !ok {
				return fmt.Errorf("第 %d 项必须是节点对象", i+1)
			}
			if err := appendMap(mapping, kind); err != nil {
				return fmt.Errorf("第 %d 项: %w", i+1, err)
			}
		}
		return nil
	}
	switch root := document.(type) {
	case map[string]any:
		if _, node := root["type"]; node {
			if err := appendMap(root, ""); err != nil {
				return nil, err
			}
		} else {
			for key := range root {
				if key != "proxies" && key != "proxy-groups" {
					return nil, fmt.Errorf("此编辑器仅接受 Outbound，不能导入顶层字段 %q", key)
				}
			}
			if value, ok := root["proxies"]; ok {
				if err := appendList(value, "proxy"); err != nil {
					return nil, err
				}
			}
			if value, ok := root["proxy-groups"]; ok {
				if err := appendList(value, "group"); err != nil {
					return nil, err
				}
			}
		}
	case []any:
		if err := appendList(root, ""); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("YAML 顶层必须是节点对象或数组")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("YAML 中没有 Outbound")
	}
	return items, nil
}

func nativeOutbound(mapping map[string]any) (MihomoOutbound, error) {
	// JSON-compatible data ensures nested YAML keys and values survive state.json.
	encoded, err := json.Marshal(mapping)
	if err != nil {
		return MihomoOutbound{}, fmt.Errorf("YAML 包含非字符串键或不支持的数据: %w", err)
	}
	var native map[string]any
	if err = json.Unmarshal(encoded, &native); err != nil {
		return MihomoOutbound{}, err
	}
	for _, key := range []string{"name", "type"} {
		value, ok := native[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return MihomoOutbound{}, fmt.Errorf("%s 必须是非空字符串", key)
		}
		native[key] = strings.TrimSpace(value)
	}
	native["type"] = strings.ToLower(native["type"].(string))
	for _, key := range []string{"server", "password", "uuid", "token", "username", "cipher", "dialer-proxy", "servername", "sni", "default-selected"} {
		if value, exists := native[key]; exists {
			if _, ok := value.(string); !ok {
				return MihomoOutbound{}, fmt.Errorf("%s 必须是字符串，纯数字值请加引号", key)
			}
		}
	}
	for _, key := range []string{"udp", "tls", "skip-cert-verify", "tfo", "mptcp", "lazy", "disable-udp"} {
		if value, exists := native[key]; exists {
			if _, ok := value.(bool); !ok {
				return MihomoOutbound{}, fmt.Errorf("%s 必须是 true 或 false", key)
			}
		}
	}
	for _, key := range []string{"port", "alterId", "interval", "timeout", "tolerance"} {
		if value, exists := native[key]; exists {
			parsed, err := strconv.Atoi(outboundStringValue(value))
			if err != nil || parsed < 0 {
				return MihomoOutbound{}, fmt.Errorf("%s 必须是非负整数", key)
			}
			native[key] = parsed
		}
	}
	if value, exists := native["proxies"]; exists {
		list, ok := value.([]any)
		if !ok {
			return MihomoOutbound{}, fmt.Errorf("策略组 proxies 必须是名称数组")
		}
		for _, member := range list {
			if name, ok := member.(string); !ok || strings.TrimSpace(name) == "" {
				return MihomoOutbound{}, fmt.Errorf("策略组成员必须是非空名称")
			}
		}
	}
	item, err := mihomoOutboundFromYAMLMap(native, isMihomoGroupType(outboundStringValue(native["type"])))
	item.Native = native
	return item, err
}

func parseMihomoShareLink(link string) ([]MihomoOutbound, error) {
	link = strings.TrimSpace(link)
	if link == "" || len(link) > 64*1024 || strings.ContainsAny(link, "\r\n") {
		return nil, fmt.Errorf("请填入一条有效的节点分享链接")
	}
	// The bundled Mihomo converter performs no network requests.
	mappings, err := convert.ConvertsV2Ray([]byte(link))
	if err != nil {
		return nil, fmt.Errorf("无法解析节点分享链接，请检查协议和格式")
	}
	parsed, _ := url.Parse(link)
	var items []MihomoOutbound
	for _, mapping := range mappings {
		if outboundStringValue(mapping["name"]) == "" {
			mapping["name"] = outboundStringValue(mapping["server"]) + ":" + outboundStringValue(mapping["port"])
		}
		if parsed != nil {
			query := parsed.Query()
			for _, key := range []string{"allowInsecure", "allow_insecure", "insecure"} {
				if value := query.Get(key); value != "" {
					mapping["skip-cert-verify"] = value == "1" || value == "true"
				}
			}
			if parsed.User != nil && (parsed.Scheme == "hysteria2" || parsed.Scheme == "hy2") {
				password := parsed.User.Username()
				if suffix, ok := parsed.User.Password(); ok {
					password += ":" + suffix
				}
				mapping["password"] = password
			}
			if containsString([]string{"socks", "socks5", "socks5h", "http", "https"}, parsed.Scheme) {
				mapping["skip-cert-verify"] = query.Get("insecure") == "1" || query.Get("allowInsecure") == "1"
				if parsed.User != nil {
					if password, ok := parsed.User.Password(); ok {
						mapping["username"], mapping["password"] = parsed.User.Username(), password
					}
				}
			}
			if mapping["type"] == "trojan" && query.Get("host") != "" {
				if ws := mapValue(mapping["ws-opts"]); ws != nil {
					ws["headers"] = map[string]any{"Host": query.Get("host")}
				}
			}
		}
		item, err := nativeOutbound(mapping)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// Update only form-owned fields that changed. Keep explicit defaults and all
// unknown native fields, including nested transport and TLS options.
func mergeOutboundForm(item MihomoOutbound) (map[string]any, error) {
	baseline, err := mihomoOutboundFromYAMLMap(item.Native, isMihomoGroupType(outboundStringValue(item.Native["type"])))
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(item.Native)
	var result map[string]any
	if err = json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	baseline.Native, item.Native = nil, nil
	before, after := mihomoProxyConfig(baseline), mihomoProxyConfig(item)
	if baseline.Kind == "group" {
		before = mihomoProxyGroupConfig(baseline)
	}
	if item.Kind == "group" {
		after = mihomoProxyGroupConfig(item)
	}
	// Canonicalize numeric and slice representations before comparison.
	data, _ = json.Marshal(before)
	_ = json.Unmarshal(data, &before)
	data, _ = json.Marshal(after)
	_ = json.Unmarshal(data, &after)
	patchNativeMap(result, before, after)
	return result, nil
}

func patchNativeMap(result, before, after map[string]any) {
	keys := map[string]bool{}
	for key := range before {
		keys[key] = true
	}
	for key := range after {
		keys[key] = true
	}
	for key := range keys {
		oldValue, oldExists := before[key]
		newValue, newExists := after[key]
		if oldExists == newExists && reflect.DeepEqual(oldValue, newValue) {
			continue
		}
		if !newExists {
			delete(result, key)
			continue
		}
		oldMap, oldOK := oldValue.(map[string]any)
		newMap, newOK := newValue.(map[string]any)
		nativeMap, nativeOK := result[key].(map[string]any)
		if oldOK && newOK && nativeOK {
			patchNativeMap(nativeMap, oldMap, newMap)
		} else {
			result[key] = newValue
		}
	}
}

func mihomoOutboundFromYAMLMap(mapping map[string]any, group bool) (MihomoOutbound, error) {
	item := MihomoOutbound{ID: "", Kind: "proxy"}
	if group {
		item.Kind = "group"
	}
	item.Name = outboundStringValue(mapping["name"])
	item.Type = strings.ToLower(outboundStringValue(mapping["type"]))
	item.Server = outboundStringValue(mapping["server"])
	item.Port = intValue(mapping["port"])
	item.Username = outboundStringValue(mapping["username"])
	item.Password = outboundStringValue(mapping["password"])
	item.UUID = outboundStringValue(mapping["uuid"])
	item.AlterID = intValue(mapping["alterId"])
	item.Cipher = outboundStringValue(mapping["cipher"])
	item.UDP = boolValue(mapping["udp"])
	item.TLS = boolValue(mapping["tls"])
	item.SkipCertVerify = boolValue(mapping["skip-cert-verify"])
	item.SNI = firstString(mapping, "sni", "servername")
	item.ALPN = strings.Join(stringList(mapping["alpn"]), ",")
	item.Network = strings.ToLower(outboundStringValue(mapping["network"]))
	item.Flow = outboundStringValue(mapping["flow"])
	item.Encryption = outboundStringValue(mapping["encryption"])
	item.ClientFingerprint = outboundStringValue(mapping["client-fingerprint"])
	if reality := mapValue(mapping["reality-opts"]); reality != nil {
		item.Reality = true
		item.RealityPublicKey = outboundStringValue(reality["public-key"])
		item.RealityShortID = outboundStringValue(reality["short-id"])
	}
	item.IPVersion = outboundStringValue(mapping["ip-version"])
	item.DialerProxy = outboundStringValue(mapping["dialer-proxy"])
	item.TFO = boolValue(mapping["tfo"])
	item.MPTCP = boolValue(mapping["mptcp"])
	item.Up = outboundStringValue(mapping["up"])
	item.Down = outboundStringValue(mapping["down"])
	item.Obfs = outboundStringValue(mapping["obfs"])
	item.ObfsPassword = outboundStringValue(mapping["obfs-password"])
	item.Token = outboundStringValue(mapping["token"])
	item.CongestionController = outboundStringValue(mapping["congestion-controller"])
	item.UDPRelayMode = outboundStringValue(mapping["udp-relay-mode"])
	item.Proxies = stringList(mapping["proxies"])
	item.TestURL = outboundStringValue(mapping["url"])
	item.Interval = intValue(mapping["interval"])
	item.Timeout = intValue(mapping["timeout"])
	item.Tolerance = intValue(mapping["tolerance"])
	item.Strategy = outboundStringValue(mapping["strategy"])
	item.DefaultSelected = outboundStringValue(mapping["default-selected"])
	item.DisableUDP = boolValue(mapping["disable-udp"])
	if ws := mapValue(mapping["ws-opts"]); ws != nil {
		item.WSPath = outboundStringValue(ws["path"])
		if headers := mapValue(ws["headers"]); headers != nil {
			item.WSHost = outboundStringValue(headers["Host"])
			if item.WSHost == "" {
				item.WSHost = outboundStringValue(headers["host"])
			}
		}
	}
	if grpc := mapValue(mapping["grpc-opts"]); grpc != nil {
		item.GRPCServiceName = outboundStringValue(grpc["grpc-service-name"])
	}
	return item, nil
}

func mihomoOutboundYAML(items []MihomoOutbound) ([]byte, error) {
	proxies, groups := mihomoOutboundConfig(items)
	if len(items) == 1 {
		if items[0].Kind == "group" {
			return marshalYAML(groups[0])
		}
		return marshalYAML(proxies[0])
	}
	document := map[string]any{}
	if len(proxies) > 0 {
		document["proxies"] = proxies
	}
	if len(groups) > 0 {
		document["proxy-groups"] = groups
	}
	return marshalYAML(document)
}

func isMihomoGroupType(value string) bool {
	return containsString(mihomoGroupTypes, strings.ToLower(strings.TrimSpace(value)))
}

func outboundStringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
}

func firstString(mapping map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := outboundStringValue(mapping[key]); value != "" {
			return value
		}
	}
	return ""
}

func intValue(value any) int {
	parsed, _ := strconv.Atoi(outboundStringValue(value))
	return parsed
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, _ := strconv.ParseBool(typed)
		return parsed
	default:
		return false
	}
}

func stringList(value any) []string {
	if value == nil {
		return nil
	}
	if text, ok := value.(string); ok {
		return splitList(text)
	}
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(list))
	for _, entry := range list {
		if text := outboundStringValue(entry); text != "" {
			result = append(result, text)
		}
	}
	return result
}

func mapValue(value any) map[string]any {
	mapping, _ := value.(map[string]any)
	return mapping
}
