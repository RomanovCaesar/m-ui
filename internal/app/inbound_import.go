package app

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

const (
	minimumRandomInboundPort = 10000
	maximumInboundPort       = 65535
)

var supportedInboundTypes = []string{
	"mixed", "socks", "http", "redir", "tproxy", "shadowsocks", "snell",
	"vmess", "vless", "trojan", "hysteria2", "hysteria2-realm", "tuic",
	"shadowquic", "anytls", "mieru", "sudoku", "trusttunnel",
}

type inboundYAMLImportRequest struct {
	YAML string `json:"yaml"`
}

func isSupportedInboundType(value string) bool {
	return containsString(supportedInboundTypes, strings.ToLower(strings.TrimSpace(value)))
}

func (a *App) handleInboundImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	var input inboundYAMLImportRequest
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: "invalid inbound import request"})
		return
	}
	inbound, err := parseMihomoInboundYAML(input.YAML)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
		return
	}
	if err = a.manager.saveInbound(inbound); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
		return
	}
	if err = a.manager.writeConfig(); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{Message: a.tr(err.Error())})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Message: a.tr("Inbound YAML 已导入"), Data: map[string]int{"count": 1}})
}

func (a *App) handleAvailableInboundPort(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	port, err := a.manager.availableInboundPort()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, apiResponse{Message: a.tr(err.Error())})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: map[string]int{"port": port}})
}

func (m *CoreManager) availableInboundPort() (int, error) {
	m.mu.Lock()
	settings := m.state.Settings
	inbounds := append([]Inbound(nil), m.state.Inbounds...)
	m.mu.Unlock()

	reserved := make(map[int]bool, len(inbounds)+3)
	for _, inbound := range inbounds {
		if inbound.Port > 0 {
			reserved[inbound.Port] = true
		}
	}
	for _, address := range []string{settings.PanelListen, settings.APIAddress} {
		if _, port := hostPortFromAddress(address); port > 0 {
			reserved[port] = true
		}
	}
	if settings.SubscriptionPort > 0 {
		reserved[settings.SubscriptionPort] = true
	}

	rangeSize := maximumInboundPort - minimumRandomInboundPort + 1
	for attempt := 0; attempt < 512; attempt++ {
		offset, err := rand.Int(rand.Reader, big.NewInt(int64(rangeSize)))
		if err != nil {
			return 0, err
		}
		port := minimumRandomInboundPort + int(offset.Int64())
		if !reserved[port] && inboundPortAvailable(port) {
			return port, nil
		}
	}
	for port := minimumRandomInboundPort; port <= maximumInboundPort; port++ {
		if !reserved[port] && inboundPortAvailable(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("找不到可用的五位数 Inbound 端口")
}

func inboundPortAvailable(port int) bool {
	address := net.JoinHostPort("", strconv.Itoa(port))
	tcpListener, err := net.Listen("tcp", address)
	if err != nil {
		return false
	}
	_ = tcpListener.Close()
	udpListener, err := net.ListenPacket("udp", address)
	if err != nil {
		return false
	}
	_ = udpListener.Close()
	return true
}

func parseMihomoInboundYAML(source string) (Inbound, error) {
	document, err := decodeOutboundYAML(source)
	if err != nil {
		return Inbound{}, err
	}
	if document == nil {
		return Inbound{}, fmt.Errorf("YAML 中没有 Inbound")
	}
	if root, ok := document.(map[string]any); ok && root["type"] == nil {
		listeners, exists := root["listeners"]
		if !exists {
			return Inbound{}, fmt.Errorf("YAML 顶层必须是一个 listener、单项数组或 listeners 数组")
		}
		document = listeners
	}
	if list, ok := document.([]any); ok {
		if len(list) != 1 {
			return Inbound{}, fmt.Errorf("每次只能导入一个 Inbound")
		}
		document = list[0]
	}
	mapping, ok := document.(map[string]any)
	if !ok {
		return Inbound{}, fmt.Errorf("Inbound YAML 必须是 listener 对象")
	}
	return inboundFromYAMLMap(mapping)
}

func inboundFromYAMLMap(mapping map[string]any) (Inbound, error) {
	data, err := json.Marshal(mapping)
	if err != nil {
		return Inbound{}, fmt.Errorf("Inbound YAML 包含不支持的数据: %w", err)
	}
	var native map[string]any
	if err = json.Unmarshal(data, &native); err != nil {
		return Inbound{}, err
	}
	inbound := Inbound{
		Native:         native,
		Name:           inboundString(native["name"]),
		Type:           strings.ToLower(inboundString(native["type"])),
		Listen:         inboundString(native["listen"]),
		Port:           intValue(native["port"]),
		Enabled:        true,
		UDP:            boolValue(native["udp"]),
		TrafficReset:   "never",
		Rule:           inboundString(native["rule"]),
		Proxy:          inboundString(native["proxy"]),
		RoutingMark:    intValue(native["routing-mark"]),
		Cipher:         inboundString(native["cipher"]),
		Certificate:    inboundString(native["certificate"]),
		PrivateKey:     inboundString(native["private-key"]),
		ClientAuthType: inboundString(native["client-auth-type"]),
		ClientAuthCert: inboundString(native["client-auth-cert"]),
		ECHKey:         inboundString(native["ech-key"]),
		AllowInsecure:  boolValue(native["allow-insecure"]),
		Decryption:     inboundString(native["decryption"]),
	}
	if inbound.Listen == "" {
		inbound.Listen = "0.0.0.0"
	}
	if inbound.Name == "" {
		return Inbound{}, fmt.Errorf("入口名称不能为空")
	}
	if !isSupportedInboundType(inbound.Type) {
		return Inbound{}, fmt.Errorf("不支持的类型 %q", inbound.Type)
	}
	if inbound.Port < 1 || inbound.Port > maximumInboundPort {
		return Inbound{}, fmt.Errorf("端口必须在 1-65535 之间")
	}
	if strings.Contains(inbound.Certificate, "-----BEGIN") || strings.Contains(inbound.PrivateKey, "-----BEGIN") {
		inbound.CertificateMode = "content"
	} else if inbound.Certificate != "" || inbound.PrivateKey != "" {
		inbound.CertificateMode = "file"
	}
	inbound.Clients = inboundClientsFromYAML(inbound.Type, native["users"], native)
	if containsString([]string{"mixed", "http", "socks"}, inbound.Type) {
		_, inbound.SimpleAuthConfigured = native["users"]
		inbound.SimpleAuthEnabled = len(inbound.Clients) > 0
	}
	applyInboundTransportFromYAML(&inbound, native)
	applyInboundSecurityFromYAML(&inbound, native)
	applyInboundProtocolFromYAML(&inbound, native)
	return inbound, nil
}

func inboundString(value any) string {
	return strings.TrimSpace(outboundStringValue(value))
}

func inboundStringList(value any) string {
	return strings.Join(stringList(value), ",")
}

func inboundUint64(value any) uint64 {
	parsed, _ := strconv.ParseUint(outboundStringValue(value), 10, 64)
	return parsed
}

func inboundClientsFromYAML(inboundType string, users any, listener map[string]any) []Client {
	makeClient := func(index int, name, username, password, uuid, flow string, alterID int) Client {
		if name == "" {
			name = username
		}
		if name == "" {
			name = fmt.Sprintf("client-%d", index+1)
		}
		return Client{Name: name, Username: username, Password: password, UUID: uuid, Flow: flow, AlterID: alterID, Enabled: true}
	}
	if inboundType == "shadowsocks" {
		return []Client{makeClient(0, "default", "", inboundString(listener["password"]), "", "", 0)}
	}
	if inboundType == "snell" {
		return []Client{makeClient(0, "default", "", inboundString(listener["psk"]), "", "", 0)}
	}
	if inboundType == "sudoku" {
		return []Client{makeClient(0, "default", "", inboundString(listener["key"]), "", "", 0)}
	}
	result := []Client{}
	if list, ok := users.([]any); ok {
		for index, value := range list {
			entry := mapValue(value)
			if entry == nil {
				continue
			}
			username := firstString(entry, "username", "email", "name")
			result = append(result, makeClient(index, username, username, inboundString(entry["password"]), inboundString(entry["uuid"]), inboundString(entry["flow"]), intValue(entry["alterId"])))
		}
		return result
	}
	if entries, ok := users.(map[string]any); ok {
		keys := make([]string, 0, len(entries))
		for key := range entries {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for index, key := range keys {
			uuid := ""
			if inboundType == "tuic" {
				uuid = key
			}
			result = append(result, makeClient(index, key, key, inboundString(entries[key]), uuid, "", 0))
		}
	}
	return result
}

func applyInboundTransportFromYAML(inbound *Inbound, listener map[string]any) {
	inbound.Network = "tcp"
	if _, exists := listener["ws-path"]; exists {
		inbound.Network = "ws"
		inbound.WSPath = inboundString(listener["ws-path"])
		inbound.Host = inboundString(listener["host"])
	}
	if _, exists := listener["grpc-service-name"]; exists {
		inbound.Network = "grpc"
		inbound.GRPCServiceName = inboundString(listener["grpc-service-name"])
	}
	if config := mapValue(listener["xhttp-config"]); config != nil {
		inbound.Network = "xhttp"
		inbound.XHTTP.Enabled = boolOrPresence(config, "enable")
		inbound.XHTTP.Path = inboundString(config["path"])
		inbound.XHTTP.Host = inboundString(config["host"])
		inbound.XHTTP.Mode = inboundString(config["mode"])
	}
	if config := mapValue(listener["mkcp-config"]); config != nil && boolOrPresence(config, "enable") {
		inbound.Network = "mkcp"
		inbound.MKCP.Enabled = true
		inbound.MKCP.MTU = uint32(intValue(config["mtu"]))
		inbound.MKCP.TTI = uint32(intValue(config["tti"]))
		inbound.MKCP.Seed = inboundString(config["seed"])
		inbound.MKCP.Header = inboundString(config["header"])
	}
	if config := mapValue(listener["mekya-config"]); config != nil && boolOrPresence(config, "enable") {
		inbound.Network = "mekya"
		inbound.Mekya.Enabled = true
		inbound.Mekya.URL = inboundString(config["url"])
		inbound.Mekya.H2PoolSize = intValue(config["h2-pool-size"])
		inbound.Mekya.MaxWriteDelay = intValue(config["max-write-delay"])
		inbound.Mekya.MaxRequestSize = intValue(config["max-request-size"])
	}
	if config := mapValue(listener["kcp-tun"]); config != nil && boolOrPresence(config, "enable") {
		inbound.Network = "kcptun"
		inbound.KcpTun.Enabled = true
		inbound.KcpTun.Key = inboundString(config["key"])
		inbound.KcpTun.Crypt = inboundString(config["crypt"])
		inbound.KcpTun.Mode = inboundString(config["mode"])
		inbound.KcpTun.MTU = intValue(config["mtu"])
	}
}

func applyInboundSecurityFromYAML(inbound *Inbound, listener map[string]any) {
	if reality := mapValue(listener["reality-config"]); reality != nil {
		inbound.Reality.Enabled = true
		inbound.Reality.Dest = inboundString(reality["dest"])
		inbound.Reality.PrivateKey = inboundString(reality["private-key"])
		inbound.Reality.ServerNames = inboundStringList(reality["server-names"])
		inbound.Reality.ShortIDs = inboundStringList(reality["short-id"])
		inbound.Reality.MaxTimeDifference = intValue(reality["max-time-difference"])
		inbound.Reality.Proxy = inboundString(reality["proxy"])
	}
	if shadow := mapValue(listener["shadow-tls"]); shadow != nil {
		inbound.ShadowTLS.Enabled = boolOrPresence(shadow, "enable")
		inbound.ShadowTLS.Version = intValue(shadow["version"])
		inbound.ShadowTLS.Password = inboundString(shadow["password"])
		if handshake := mapValue(shadow["handshake"]); handshake != nil {
			inbound.ShadowTLS.Dest = inboundString(handshake["dest"])
		}
	}
	if rest := mapValue(listener["res-tls"]); rest != nil {
		inbound.RestTLS.Enabled = boolOrPresence(rest, "enable")
		inbound.RestTLS.Dest = inboundString(rest["dest"])
		inbound.RestTLS.Password = inboundString(rest["password"])
		inbound.RestTLS.Script = inboundString(rest["restls-script"])
		inbound.RestTLS.MinRecordLen = intValue(rest["min-record-len"])
		inbound.RestTLS.RateLimit = inboundUint64(rest["rate-limit"])
	}
	if jls := mapValue(listener["jls-config"]); jls != nil {
		inbound.JLS.Enabled = boolOrPresence(jls, "enable")
		inbound.JLS.Dest = inboundString(jls["dest"])
		inbound.JLS.SNI = inboundString(jls["sni"])
		inbound.JLS.ALPN = inboundStringList(jls["alpn"])
		inbound.JLS.Proxy = inboundString(jls["proxy"])
		inbound.JLS.RateLimit = inboundUint64(jls["rate-limit"])
		if users, ok := jls["users"].([]any); ok && len(users) > 0 {
			if user := mapValue(users[0]); user != nil {
				inbound.JLS.Username = inboundString(user["username"])
				inbound.JLS.Password = inboundString(user["password"])
			}
		}
	}
	if option := mapValue(listener["ss-option"]); option != nil {
		inbound.TrojanSS.Enabled = boolOrPresence(option, "enabled")
		inbound.TrojanSS.Method = inboundString(option["method"])
		inbound.TrojanSS.Password = inboundString(option["password"])
	}
	inbound.TLS = inbound.Certificate != "" || inbound.PrivateKey != ""
}

func applyInboundProtocolFromYAML(inbound *Inbound, listener map[string]any) {
	if inbound.Type == "snell" {
		inbound.Snell.Version = intValue(listener["version"])
		if inbound.Snell.Version == 0 {
			inbound.Snell.Version = 4
		}
		if obfs := mapValue(listener["obfs-opts"]); obfs != nil {
			inbound.Snell.ObfsMode = inboundString(obfs["mode"])
			inbound.Snell.ObfsHost = inboundString(obfs["host"])
		}
	}
	if simple := mapValue(listener["simple-obfs"]); simple != nil {
		inbound.SimpleObfs.Enabled = boolOrPresence(simple, "enable")
		inbound.SimpleObfs.Mode = inboundString(simple["mode"])
	}
	if mux := mapValue(listener["mux-option"]); mux != nil {
		inbound.Mux.Padding = boolValue(mux["padding"])
		if brutal := mapValue(mux["brutal"]); brutal != nil {
			inbound.Mux.BrutalEnabled = boolValue(brutal["enabled"])
			inbound.Mux.Up = inboundString(brutal["up"])
			inbound.Mux.Down = inboundString(brutal["down"])
		}
	}
	if inbound.Type == "hysteria2" {
		inbound.Hysteria.Obfs = inboundString(listener["obfs"])
		inbound.Hysteria.ObfsPassword = inboundString(listener["obfs-password"])
		inbound.Hysteria.Up = inboundString(listener["up"])
		inbound.Hysteria.Down = inboundString(listener["down"])
		inbound.Hysteria.IgnoreClientBandwidth = boolValue(listener["ignore-client-bandwidth"])
		inbound.Hysteria.Masquerade = inboundString(listener["masquerade"])
		inbound.Hysteria.ALPN = inboundStringList(listener["alpn"])
		inbound.Hysteria.UdpMTU = intValue(listener["udp-mtu"])
	}
	if inbound.Type == "tuic" {
		inbound.TUIC.CongestionController = inboundString(listener["congestion-controller"])
		inbound.TUIC.MaxIdleTime = intValue(listener["max-idle-time"])
		inbound.TUIC.AuthenticationTimeout = intValue(listener["authentication-timeout"])
		inbound.TUIC.ALPN = inboundStringList(listener["alpn"])
		inbound.TUIC.MaxUDPRelayPacketSize = intValue(listener["max-udp-relay-packet-size"])
	}
	if inbound.Type == "anytls" {
		inbound.AnyTLS.PaddingScheme = inboundString(listener["padding-scheme"])
	}
	if inbound.Type == "mieru" {
		inbound.Mieru.Transport = inboundString(listener["transport"])
		inbound.Mieru.TrafficPattern = inboundString(listener["traffic-pattern"])
		inbound.Mieru.UserHintIsMandatory = boolValue(listener["user-hint-is-mandatory"])
	}
	if inbound.Type == "hysteria2-realm" {
		inbound.Hysteria2Realm.Token = inboundString(listener["token"])
		inbound.Hysteria2Realm.MaxRealms = intValue(listener["max-realms"])
		inbound.Hysteria2Realm.MaxRealmsPerIP = intValue(listener["max-realms-per-ip"])
		inbound.Hysteria2Realm.TrustedProxyHeader = inboundString(listener["trusted-proxy-header"])
		inbound.Hysteria2Realm.RealmNamePattern = inboundString(listener["realm-name-pattern"])
	}
	if inbound.Type == "trusttunnel" {
		inbound.TrustTunnel.Network = inboundStringList(listener["network"])
		inbound.TrustTunnel.CongestionController = inboundString(listener["congestion-controller"])
		inbound.TrustTunnel.CWND = intValue(listener["cwnd"])
		inbound.TrustTunnel.BBRProfile = inboundString(listener["bbr-profile"])
	}
}

func boolOrPresence(mapping map[string]any, key string) bool {
	value, exists := mapping[key]
	if !exists {
		return true
	}
	return boolValue(value)
}

func mergeInboundForm(item Inbound) (map[string]any, error) {
	baseline, err := inboundFromYAMLMap(item.Native)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(item.Native)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err = json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	baseline.Native, item.Native = nil, nil
	before, beforeErr := managedInboundConfig(baseline)
	after, afterErr := managedInboundConfig(item)
	if beforeErr == nil && afterErr == nil {
		data, _ = json.Marshal(before)
		_ = json.Unmarshal(data, &before)
		data, _ = json.Marshal(after)
		_ = json.Unmarshal(data, &after)
		patchNativeMap(result, before, after)
		return result, nil
	}
	// At minimum keep identity and socket fields editable for native listeners
	// whose advanced options cannot be represented by the form yet.
	for key, value := range map[string]any{"name": item.Name, "type": item.Type, "listen": item.Listen, "port": item.Port} {
		if !reflect.DeepEqual(result[key], value) {
			result[key] = value
		}
	}
	return result, nil
}
