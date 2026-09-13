package app

import (
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const defaultMihomoTestURL = "https://www.gstatic.com/generate_204"

var (
	mihomoProxyTypes = []string{"direct", "ss", "socks5", "http", "vmess", "vless", "trojan", "hysteria2", "tuic", "wireguard", "openvpn"}
	mihomoGroupTypes = []string{"select", "url-test", "fallback", "load-balance"}
	mihomoRuleTypes  = []string{
		"DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "DOMAIN-WILDCARD", "DOMAIN-REGEX", "GEOSITE",
		"IP-CIDR", "IP-CIDR6", "IP-SUFFIX", "IP-ASN", "GEOIP", "SRC-GEOIP", "SRC-IP-ASN", "SRC-IP-CIDR", "SRC-IP-SUFFIX",
		"DST-PORT", "SRC-PORT", "IN-PORT", "IN-TYPE", "IN-USER", "IN-NAME", "REMATCH-NAME",
		"PROCESS-PATH", "PROCESS-PATH-WILDCARD", "PROCESS-PATH-REGEX", "PROCESS-NAME", "PROCESS-NAME-WILDCARD", "PROCESS-NAME-REGEX",
		"UID", "NETWORK", "DSCP", "MATCH",
	}
	mihomoBuiltins = []string{"DIRECT", "REJECT", "REJECT-DROP", "PASS", "PASS-RULE", "COMPATIBLE", "GLOBAL"}
)

// MihomoOutbound represents either one entry in `proxies` or one entry in
// `proxy-groups`. Mihomo deliberately treats both as routing targets, so the UI
// manages them in a single ordered list while the renderer emits two arrays.
type MihomoOutbound struct {
	ID           string         `json:"id"`
	Kind         string         `json:"kind"` // proxy or group
	Name         string         `json:"name"`
	Type         string         `json:"type"`
	Native       map[string]any `json:"native,omitempty"`
	WarpDeviceID string         `json:"warpDeviceId,omitempty"`

	// Proxy node common fields.
	Server            string `json:"server,omitempty"`
	Port              int    `json:"port,omitempty"`
	Username          string `json:"username,omitempty"`
	Password          string `json:"password,omitempty"`
	UUID              string `json:"uuid,omitempty"`
	AlterID           int    `json:"alterId,omitempty"`
	Cipher            string `json:"cipher,omitempty"`
	UDP               bool   `json:"udp,omitempty"`
	TLS               bool   `json:"tls,omitempty"`
	SkipCertVerify    bool   `json:"skipCertVerify,omitempty"`
	SNI               string `json:"sni,omitempty"`
	ALPN              string `json:"alpn,omitempty"`
	Network           string `json:"network,omitempty"`
	WSPath            string `json:"wsPath,omitempty"`
	WSHost            string `json:"wsHost,omitempty"`
	GRPCServiceName   string `json:"grpcServiceName,omitempty"`
	Flow              string `json:"flow,omitempty"`
	Encryption        string `json:"encryption,omitempty"`
	ClientFingerprint string `json:"clientFingerprint,omitempty"`
	Reality           bool   `json:"reality,omitempty"`
	RealityPublicKey  string `json:"realityPublicKey,omitempty"`
	RealityShortID    string `json:"realityShortId,omitempty"`
	IPVersion         string `json:"ipVersion,omitempty"`
	DialerProxy       string `json:"dialerProxy,omitempty"`
	InterfaceName     string `json:"interfaceName,omitempty"`
	RoutingMark       int    `json:"routingMark,omitempty"`
	TFO               bool   `json:"tfo,omitempty"`
	MPTCP             bool   `json:"mptcp,omitempty"`

	// WireGuard fields. The form covers Mihomo's common single-peer form; the
	// YAML editor remains available for multi-peer and AmneziaWG options.
	WireGuardIP                      string   `json:"wireGuardIp,omitempty"`
	WireGuardIPv6                    string   `json:"wireGuardIpv6,omitempty"`
	WireGuardPrivateKey              string   `json:"wireGuardPrivateKey,omitempty"`
	WireGuardPublicKey               string   `json:"wireGuardPublicKey,omitempty"`
	WireGuardPreSharedKey            string   `json:"wireGuardPreSharedKey,omitempty"`
	WireGuardReserved                []int    `json:"wireGuardReserved,omitempty"`
	WireGuardPersistentKeepalive     int      `json:"wireGuardPersistentKeepalive,omitempty"`
	WireGuardMTU                     int      `json:"wireGuardMtu,omitempty"`
	WireGuardWorkers                 int      `json:"wireGuardWorkers,omitempty"`
	WireGuardRemoteDNSResolve        bool     `json:"wireGuardRemoteDnsResolve,omitempty"`
	WireGuardDNS                     []string `json:"wireGuardDns,omitempty"`
	WireGuardRefreshServerIPInterval int      `json:"wireGuardRefreshServerIpInterval,omitempty"`

	// OpenVPN fields map directly to Mihomo's in-process OpenVPN outbound.
	OpenVPNProto              string   `json:"openVpnProto,omitempty"`
	OpenVPNDev                string   `json:"openVpnDev,omitempty"`
	OpenVPNCipher             string   `json:"openVpnCipher,omitempty"`
	OpenVPNDataCiphers        []string `json:"openVpnDataCiphers,omitempty"`
	OpenVPNDataCipherFallback string   `json:"openVpnDataCipherFallback,omitempty"`
	OpenVPNAuth               string   `json:"openVpnAuth,omitempty"`
	OpenVPNCompLZO            string   `json:"openVpnCompLzo,omitempty"`
	OpenVPNCA                 string   `json:"openVpnCa,omitempty"`
	OpenVPNCert               string   `json:"openVpnCert,omitempty"`
	OpenVPNKey                string   `json:"openVpnKey,omitempty"`
	OpenVPNTLSAuth            string   `json:"openVpnTlsAuth,omitempty"`
	OpenVPNKeyDirection       string   `json:"openVpnKeyDirection,omitempty"`
	OpenVPNTLSCrypt           string   `json:"openVpnTlsCrypt,omitempty"`
	OpenVPNTLSCryptV2         string   `json:"openVpnTlsCryptV2,omitempty"`
	OpenVPNPing               int      `json:"openVpnPing,omitempty"`
	OpenVPNPingRestart        int      `json:"openVpnPingRestart,omitempty"`
	OpenVPNHandshakeTimeout   int      `json:"openVpnHandshakeTimeout,omitempty"`
	OpenVPNMTU                int      `json:"openVpnMtu,omitempty"`
	OpenVPNRemoteDNSResolve   bool     `json:"openVpnRemoteDnsResolve,omitempty"`
	OpenVPNDNS                []string `json:"openVpnDns,omitempty"`

	// Hysteria2 / TUIC fields.
	Up                   string `json:"up,omitempty"`
	Down                 string `json:"down,omitempty"`
	Obfs                 string `json:"obfs,omitempty"`
	ObfsPassword         string `json:"obfsPassword,omitempty"`
	Token                string `json:"token,omitempty"`
	CongestionController string `json:"congestionController,omitempty"`
	UDPRelayMode         string `json:"udpRelayMode,omitempty"`

	// Proxy group fields.
	Proxies         []string `json:"proxies,omitempty"`
	TestURL         string   `json:"testUrl,omitempty"`
	Interval        int      `json:"interval,omitempty"`
	Timeout         int      `json:"timeout,omitempty"`
	Tolerance       int      `json:"tolerance,omitempty"`
	Strategy        string   `json:"strategy,omitempty"`
	DefaultSelected string   `json:"defaultSelected,omitempty"`
	DisableUDP      bool     `json:"disableUdp,omitempty"`
}

type MihomoRoutingRule struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Payload   string `json:"payload,omitempty"`
	Target    string `json:"target"`
	NoResolve bool   `json:"noResolve,omitempty"`
}

type mihomoSettingsPayload struct {
	Basics       *MihomoBasics       `json:"basics,omitempty"`
	Outbounds    []MihomoOutbound    `json:"outbounds"`
	RoutingRules []MihomoRoutingRule `json:"routingRules"`
}

func defaultMihomoRoutingRules() []MihomoRoutingRule {
	return []MihomoRoutingRule{{ID: "default-match", Type: "MATCH", Target: "DIRECT"}}
}

func (a *App) handleMihomoSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.manager.mu.Lock()
		basics := effectiveMihomoBasics(a.manager.state)
		payload := mihomoSettingsPayload{
			Basics:       &basics,
			Outbounds:    append([]MihomoOutbound(nil), a.manager.state.Outbounds...),
			RoutingRules: append([]MihomoRoutingRule(nil), a.manager.state.RoutingRules...),
		}
		a.manager.mu.Unlock()
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: payload})
	case http.MethodPut:
		var input mihomoSettingsPayload
		if err := decodeJSON(r, &input); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{Message: "invalid Mihomo settings"})
			return
		}
		if err := a.manager.updateMihomoSettings(input); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
			return
		}
		if err := a.manager.writeConfig(); err != nil {
			writeJSON(w, http.StatusInternalServerError, apiResponse{Message: a.tr(err.Error())})
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Message: a.tr("Mihomo 分流设置已保存")})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
	}
}

func (m *CoreManager) updateMihomoSettings(input mihomoSettingsPayload) error {
	outbounds, err := normalizeMihomoOutbounds(input.Outbounds)
	if err != nil {
		return err
	}
	rules, err := normalizeMihomoRoutingRules(input.RoutingRules, outbounds)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	basics := effectiveMihomoBasics(m.state)
	if input.Basics != nil {
		basics = *input.Basics
	}
	basics, err = normalizeMihomoBasics(basics)
	if err != nil {
		return err
	}
	if _, _, _, err = compileMihomoBasics(basics, outbounds, rules); err != nil {
		return err
	}
	previous := m.state
	m.state.Outbounds, m.state.RoutingRules, m.state.MihomoBasics = outbounds, rules, &basics
	m.state.Settings.Mode, m.state.Settings.LogLevel = basics.Mode, basics.LogLevel
	err = m.saveLocked()
	if err != nil {
		m.state = previous
		return err
	}
	if basics.MaskLogAddress {
		for i, line := range m.logs {
			m.logs[i] = maskLogAddresses(line)
		}
	}
	if len(m.logs) > basics.LogBufferSize {
		m.logs = m.logs[len(m.logs)-basics.LogBufferSize:]
	}
	return err
}

func normalizeMihomoOutbounds(input []MihomoOutbound) ([]MihomoOutbound, error) {
	items := append([]MihomoOutbound(nil), input...)
	names := make(map[string]int, len(items))
	for index := range items {
		item := &items[index]
		if item.Native != nil {
			parsed, err := nativeOutbound(item.Native)
			if err != nil {
				return nil, fmt.Errorf("Outbound #%d: %w", index+1, err)
			}
			parsed.ID = item.ID
			parsed.WarpDeviceID = item.WarpDeviceID
			*item = parsed
		}
		item.Kind = strings.ToLower(strings.TrimSpace(item.Kind))
		item.Type = strings.ToLower(strings.TrimSpace(item.Type))
		item.Name = strings.TrimSpace(item.Name)
		item.Server = strings.TrimSpace(item.Server)
		item.Username = strings.TrimSpace(item.Username)
		item.UUID = strings.TrimSpace(item.UUID)
		item.Cipher = strings.TrimSpace(item.Cipher)
		item.SNI = strings.TrimSpace(item.SNI)
		item.ALPN = strings.TrimSpace(item.ALPN)
		item.Network = strings.ToLower(strings.TrimSpace(item.Network))
		item.WSPath = strings.TrimSpace(item.WSPath)
		item.WSHost = strings.TrimSpace(item.WSHost)
		item.GRPCServiceName = strings.TrimSpace(item.GRPCServiceName)
		item.Flow = strings.TrimSpace(item.Flow)
		item.Encryption = strings.TrimSpace(item.Encryption)
		item.ClientFingerprint = strings.TrimSpace(item.ClientFingerprint)
		item.IPVersion = strings.ToLower(strings.TrimSpace(item.IPVersion))
		item.DialerProxy = strings.TrimSpace(item.DialerProxy)
		item.InterfaceName = strings.TrimSpace(item.InterfaceName)
		item.WireGuardIP = strings.TrimSpace(item.WireGuardIP)
		item.WireGuardIPv6 = strings.TrimSpace(item.WireGuardIPv6)
		item.WireGuardPrivateKey = strings.TrimSpace(item.WireGuardPrivateKey)
		item.WireGuardPublicKey = strings.TrimSpace(item.WireGuardPublicKey)
		item.WireGuardPreSharedKey = strings.TrimSpace(item.WireGuardPreSharedKey)
		item.WireGuardDNS = normalizeOutboundStringList(item.WireGuardDNS)
		item.OpenVPNProto = normalizeOpenVPNProto(item.OpenVPNProto)
		item.OpenVPNDev = strings.ToLower(strings.TrimSpace(item.OpenVPNDev))
		item.OpenVPNCipher = normalizeOpenVPNCipher(item.OpenVPNCipher)
		item.OpenVPNDataCiphers = normalizeOpenVPNCipherList(item.OpenVPNDataCiphers)
		item.OpenVPNDataCipherFallback = normalizeOpenVPNCipher(item.OpenVPNDataCipherFallback)
		item.OpenVPNAuth = normalizeOpenVPNAuth(item.OpenVPNAuth)
		item.OpenVPNCompLZO = strings.ToLower(strings.TrimSpace(item.OpenVPNCompLZO))
		item.OpenVPNCA = strings.TrimSpace(item.OpenVPNCA)
		item.OpenVPNCert = strings.TrimSpace(item.OpenVPNCert)
		item.OpenVPNKey = strings.TrimSpace(item.OpenVPNKey)
		item.OpenVPNTLSAuth = strings.TrimSpace(item.OpenVPNTLSAuth)
		item.OpenVPNKeyDirection = strings.TrimSpace(item.OpenVPNKeyDirection)
		item.OpenVPNTLSCrypt = strings.TrimSpace(item.OpenVPNTLSCrypt)
		item.OpenVPNTLSCryptV2 = strings.TrimSpace(item.OpenVPNTLSCryptV2)
		item.OpenVPNDNS = normalizeOutboundStringList(item.OpenVPNDNS)
		item.Up = strings.TrimSpace(item.Up)
		item.Down = strings.TrimSpace(item.Down)
		item.Obfs = strings.ToLower(strings.TrimSpace(item.Obfs))
		item.Token = strings.TrimSpace(item.Token)
		item.CongestionController = strings.ToLower(strings.TrimSpace(item.CongestionController))
		item.UDPRelayMode = strings.ToLower(strings.TrimSpace(item.UDPRelayMode))
		item.TestURL = strings.TrimSpace(item.TestURL)
		item.Strategy = strings.ToLower(strings.TrimSpace(item.Strategy))
		item.DefaultSelected = strings.TrimSpace(item.DefaultSelected)
		if item.ID == "" {
			if err := randomToken(&item.ID); err != nil {
				return nil, err
			}
			item.ID = item.ID[:12]
		}
		if item.Name == "" || len(item.Name) > 128 || strings.ContainsAny(item.Name, ",\r\n") {
			return nil, fmt.Errorf("Outbound #%d 名称不能为空、包含逗号/换行或超过 128 个字符", index+1)
		}
		if previous, exists := names[item.Name]; exists {
			return nil, fmt.Errorf("Outbound 名称 %q 与第 %d 项重复", item.Name, previous+1)
		}
		if containsString(mihomoBuiltins[:len(mihomoBuiltins)-1], item.Name) {
			return nil, fmt.Errorf("Outbound 名称 %q 是 Mihomo 内置策略名", item.Name)
		}
		if item.Name == "GLOBAL" && item.Kind != "group" {
			return nil, fmt.Errorf("GLOBAL 只能定义为策略组")
		}
		names[item.Name] = index
		if item.Kind == "proxy" {
			if err := validateMihomoProxy(*item); err != nil {
				return nil, fmt.Errorf("Outbound %q: %w", item.Name, err)
			}
		} else if item.Kind == "group" {
			if err := normalizeMihomoGroup(item); err != nil {
				return nil, fmt.Errorf("策略组 %q: %w", item.Name, err)
			}
		} else {
			return nil, fmt.Errorf("Outbound %q 的 kind 必须是 proxy 或 group", item.Name)
		}
	}
	for _, item := range items {
		if item.Kind == "proxy" && item.DialerProxy != "" && !mihomoTargetExists(item.DialerProxy, names) {
			return nil, fmt.Errorf("Outbound %q 的 Dialer Proxy %q 不存在", item.Name, item.DialerProxy)
		}
		if item.DialerProxy == item.Name {
			return nil, fmt.Errorf("Outbound %q 不能通过自身拨号", item.Name)
		}
		if item.Kind != "group" {
			continue
		}
		for _, member := range item.Proxies {
			if !mihomoTargetExists(member, names) {
				return nil, fmt.Errorf("策略组 %q 引用了不存在的 Outbound %q", item.Name, member)
			}
		}
		if item.DefaultSelected != "" && !containsString(item.Proxies, item.DefaultSelected) {
			return nil, fmt.Errorf("策略组 %q 的 Default Selected 必须在成员列表中", item.Name)
		}
	}
	if err := validateMihomoGroupCycles(items); err != nil {
		return nil, err
	}
	return items, nil
}

func validateMihomoProxy(item MihomoOutbound) error {
	nativeTypes := []string{"ssr", "snell", "hysteria", "wireguard", "shadowquic", "gost-relay", "direct", "dns", "reject", "rematch", "ssh", "mieru", "anytls", "sudoku", "masque", "trusttunnel", "openvpn", "tailscale", "zerotier"}
	if !containsString(mihomoProxyTypes, item.Type) && !(item.Native != nil && containsString(nativeTypes, item.Type)) {
		return fmt.Errorf("不支持代理类型 %q", item.Type)
	}
	if item.Native != nil && containsString([]string{"dns", "reject", "rematch", "tailscale", "zerotier"}, item.Type) {
		return nil
	}
	if item.Native != nil && item.Type == "wireguard" && item.Native["peers"] != nil {
		return nil
	}
	if item.Type != "direct" && (item.Server == "" || strings.ContainsAny(item.Server, " \t/\r\n")) {
		return fmt.Errorf("Server 必须是域名或 IP，且不能包含协议、路径或空格")
	}
	if item.Type != "direct" && (item.Port < 1 || item.Port > 65535) {
		return fmt.Errorf("Port 必须在 1-65535 之间")
	}
	if item.RoutingMark < 0 {
		return fmt.Errorf("Routing Mark 不能为负数")
	}
	if len(item.InterfaceName) > 256 || strings.ContainsAny(item.InterfaceName, "\r\n") {
		return fmt.Errorf("Interface Name 不能包含换行，且不能超过 256 个字符")
	}
	if item.AlterID < 0 {
		return fmt.Errorf("Alter ID 不能为负数")
	}
	if item.IPVersion != "" && !containsString([]string{"dual", "ipv4", "ipv6", "ipv4-prefer", "ipv6-prefer"}, item.IPVersion) {
		return fmt.Errorf("IP Version 无效")
	}
	if item.Network != "" && !containsString([]string{"tcp", "ws", "grpc"}, item.Network) && item.Native == nil {
		return fmt.Errorf("Network 首版仅支持 tcp、ws 和 grpc")
	}
	if item.Network == "grpc" && item.GRPCServiceName == "" && item.Native == nil {
		return fmt.Errorf("gRPC Network 需要 Service Name")
	}
	if item.Network == "ws" && item.WSPath == "" {
		item.WSPath = "/"
	}
	switch item.Type {
	case "direct":
		if item.Server != "" || item.Port != 0 {
			return fmt.Errorf("Direct 不使用 Server 或 Port")
		}
	case "ss":
		if item.Password == "" || item.Cipher == "" {
			return fmt.Errorf("Shadowsocks 需要 Cipher 和 Password")
		}
	case "vmess", "vless":
		if !regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`).MatchString(item.UUID) {
			return fmt.Errorf("UUID 格式无效")
		}
	case "trojan", "hysteria2":
		if item.Password == "" {
			return fmt.Errorf("%s 需要 Password", item.Type)
		}
	case "tuic":
		if item.Token == "" && (item.UUID == "" || item.Password == "") {
			return fmt.Errorf("TUIC 需要 Token，或同时填写 UUID 和 Password")
		}
	case "wireguard":
		if err := validateWireGuardOutbound(item); err != nil {
			return err
		}
	case "openvpn":
		if err := validateOpenVPNOutbound(item); err != nil {
			return err
		}
	}
	if item.Reality {
		if !containsString([]string{"vmess", "vless", "trojan"}, item.Type) {
			return fmt.Errorf("该协议不支持 Reality")
		}
		if item.RealityPublicKey == "" {
			return fmt.Errorf("Reality 需要 Public Key")
		}
	}
	return nil
}

func normalizeOutboundStringList(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func normalizeOpenVPNProto(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "udp4":
		return "udp"
	case "tcp-client", "tcp4", "tcp4-client":
		return "tcp"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func normalizeOpenVPNCipher(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "AES-CBC" {
		return "AES-128-CBC"
	}
	return value
}

func normalizeOpenVPNCipherList(values []string) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = normalizeOpenVPNCipher(value)
	}
	return normalizeOutboundStringList(result)
}

func normalizeOpenVPNAuth(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "SHA-1" {
		return "SHA1"
	}
	return value
}

func validWireGuardKey(value string) bool {
	decoded, err := base64.StdEncoding.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func validWireGuardAddress(value string, ipv4 bool) bool {
	if value == "" {
		return true
	}
	host := value
	if strings.Contains(value, "/") {
		ip, _, err := net.ParseCIDR(value)
		return err == nil && (ip.To4() != nil) == ipv4
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.To4() != nil) == ipv4
}

func validateWireGuardOutbound(item MihomoOutbound) error {
	if item.WireGuardIP == "" && item.WireGuardIPv6 == "" {
		return fmt.Errorf("WireGuard 至少需要一个本地 IP 或 IPv6 地址")
	}
	if !validWireGuardAddress(item.WireGuardIP, true) {
		return fmt.Errorf("WireGuard IP 必须是有效的 IPv4 地址或 CIDR")
	}
	if !validWireGuardAddress(item.WireGuardIPv6, false) {
		return fmt.Errorf("WireGuard IPv6 必须是有效的 IPv6 地址或 CIDR")
	}
	if !validWireGuardKey(item.WireGuardPrivateKey) {
		return fmt.Errorf("WireGuard Private Key 必须是 32 字节 Base64")
	}
	if !validWireGuardKey(item.WireGuardPublicKey) {
		return fmt.Errorf("WireGuard Public Key 必须是 32 字节 Base64")
	}
	if item.WireGuardPreSharedKey != "" && !validWireGuardKey(item.WireGuardPreSharedKey) {
		return fmt.Errorf("WireGuard Pre-shared Key 必须是 32 字节 Base64")
	}
	if len(item.WireGuardReserved) != 0 && len(item.WireGuardReserved) != 3 {
		return fmt.Errorf("WireGuard Reserved 必须留空或填写 3 个字节")
	}
	for _, value := range item.WireGuardReserved {
		if value < 0 || value > 255 {
			return fmt.Errorf("WireGuard Reserved 每项必须在 0-255 之间")
		}
	}
	if item.WireGuardMTU < 0 || item.WireGuardWorkers < 0 || item.WireGuardPersistentKeepalive < 0 || item.WireGuardRefreshServerIPInterval < 0 {
		return fmt.Errorf("WireGuard MTU、Workers、Persistent Keepalive 和刷新间隔不能为负数")
	}
	if item.WireGuardRemoteDNSResolve && len(item.WireGuardDNS) == 0 {
		return fmt.Errorf("WireGuard Remote DNS Resolve 开启时必须填写 DNS")
	}
	return nil
}

var openVPNCiphers = []string{"AES-128-GCM", "AES-192-GCM", "AES-256-GCM", "AES-128-CBC", "AES-192-CBC", "AES-256-CBC", "CHACHA20-POLY1305"}
var openVPNAuths = []string{"MD5", "SHA1", "SHA256", "SHA384", "SHA512"}

func validPEM(value string) bool {
	block, _ := pem.Decode([]byte(value))
	return block != nil
}

func validateOpenVPNOutbound(item MihomoOutbound) error {
	proto := valueOr(item.OpenVPNProto, "udp")
	if !containsString([]string{"udp", "tcp"}, proto) {
		return fmt.Errorf("OpenVPN Proto 只能是 udp 或 tcp")
	}
	if dev := valueOr(item.OpenVPNDev, "tun"); dev != "tun" {
		return fmt.Errorf("OpenVPN 当前只支持 Dev tun")
	}
	cipher := valueOr(item.OpenVPNCipher, "AES-128-GCM")
	if !containsString(openVPNCiphers, cipher) {
		return fmt.Errorf("OpenVPN Cipher 无效")
	}
	for _, candidate := range append(append([]string{}, item.OpenVPNDataCiphers...), item.OpenVPNDataCipherFallback) {
		if candidate != "" && !containsString(openVPNCiphers, candidate) {
			return fmt.Errorf("OpenVPN Data Cipher %q 无效", candidate)
		}
	}
	if auth := valueOr(item.OpenVPNAuth, "SHA256"); !containsString(openVPNAuths, auth) {
		return fmt.Errorf("OpenVPN Auth 无效")
	}
	if item.OpenVPNCompLZO != "" && !containsString([]string{"yes", "no", "adaptive"}, item.OpenVPNCompLZO) {
		return fmt.Errorf("OpenVPN Comp LZO 只能是 yes、no 或 adaptive")
	}
	if !validPEM(item.OpenVPNCA) {
		return fmt.Errorf("OpenVPN CA 必须是有效的内联 PEM 内容")
	}
	if (item.OpenVPNCert == "") != (item.OpenVPNKey == "") {
		return fmt.Errorf("OpenVPN Cert 和 Key 必须同时填写")
	}
	if item.OpenVPNCert != "" && (!validPEM(item.OpenVPNCert) || !validPEM(item.OpenVPNKey)) {
		return fmt.Errorf("OpenVPN Cert 和 Key 必须是有效的内联 PEM 内容")
	}
	if item.OpenVPNCert == "" && item.Username == "" {
		return fmt.Errorf("OpenVPN 需要 Cert + Key，或填写 Username 使用 auth-user-pass")
	}
	keyBlocks := 0
	for _, value := range []string{item.OpenVPNTLSAuth, item.OpenVPNTLSCrypt, item.OpenVPNTLSCryptV2} {
		if value != "" {
			keyBlocks++
		}
	}
	if keyBlocks > 1 {
		return fmt.Errorf("OpenVPN TLS Auth、TLS Crypt 和 TLS Crypt v2 只能选择一种")
	}
	if item.OpenVPNKeyDirection != "" && item.OpenVPNKeyDirection != "0" && item.OpenVPNKeyDirection != "1" {
		return fmt.Errorf("OpenVPN Key Direction 只能是 0 或 1")
	}
	if item.OpenVPNPing < 0 || item.OpenVPNPingRestart < 0 || item.OpenVPNHandshakeTimeout < 0 || item.OpenVPNMTU < 0 {
		return fmt.Errorf("OpenVPN Ping、Ping Restart、Handshake Timeout 和 MTU 不能为负数")
	}
	if item.OpenVPNRemoteDNSResolve && len(item.OpenVPNDNS) == 0 {
		return fmt.Errorf("OpenVPN Remote DNS Resolve 开启时必须填写 DNS")
	}
	return nil
}

func normalizeMihomoGroup(item *MihomoOutbound) error {
	if !containsString(mihomoGroupTypes, item.Type) {
		return fmt.Errorf("不支持策略组类型 %q", item.Type)
	}
	members := make([]string, 0, len(item.Proxies))
	seen := map[string]bool{}
	for _, member := range item.Proxies {
		member = strings.TrimSpace(member)
		if member != "" && !seen[member] {
			seen[member] = true
			members = append(members, member)
		}
	}
	item.Proxies = members
	if len(item.Proxies) == 0 {
		return fmt.Errorf("至少需要一个代理或策略组成员")
	}
	if containsString(item.Proxies, item.Name) {
		return fmt.Errorf("不能引用自身")
	}
	if item.Type != "select" {
		if item.TestURL == "" {
			item.TestURL = defaultMihomoTestURL
		}
		parsed, err := url.ParseRequestURI(item.TestURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("Test URL 必须是完整的 http:// 或 https:// 地址")
		}
		if item.Interval == 0 && item.Native == nil {
			item.Interval = 300
		}
		if item.Interval < 0 || item.Timeout < 0 || item.Tolerance < 0 {
			return fmt.Errorf("Interval、Timeout 和 Tolerance 不能为负数")
		}
	}
	if item.Type == "load-balance" && item.Strategy != "" && !containsString([]string{"consistent-hashing", "round-robin", "sticky-sessions"}, item.Strategy) {
		return fmt.Errorf("Load Balance Strategy 无效")
	}
	return nil
}

func mihomoTargetExists(target string, names map[string]int) bool {
	if containsString(mihomoBuiltins, target) {
		return true
	}
	_, exists := names[target]
	return exists
}

func validateMihomoGroupCycles(items []MihomoOutbound) error {
	groups := map[string][]string{}
	for _, item := range items {
		if item.Kind == "group" {
			groups[item.Name] = item.Proxies
		}
	}
	state := map[string]int{}
	var visit func(string) error
	visit = func(name string) error {
		if state[name] == 1 {
			return fmt.Errorf("策略组存在循环引用，涉及 %q", name)
		}
		if state[name] == 2 {
			return nil
		}
		state[name] = 1
		for _, member := range groups[name] {
			if _, isGroup := groups[member]; isGroup {
				if err := visit(member); err != nil {
					return err
				}
			}
		}
		state[name] = 2
		return nil
	}
	for name := range groups {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}

func normalizeMihomoRoutingRules(input []MihomoRoutingRule, outbounds []MihomoOutbound) ([]MihomoRoutingRule, error) {
	rules := append([]MihomoRoutingRule(nil), input...)
	names := make(map[string]int, len(outbounds))
	for index, outbound := range outbounds {
		names[outbound.Name] = index
	}
	matchSeen := false
	for index := range rules {
		rule := &rules[index]
		rule.Type = strings.ToUpper(strings.TrimSpace(rule.Type))
		rule.Payload = strings.TrimSpace(rule.Payload)
		rule.Target = strings.TrimSpace(rule.Target)
		if rule.ID == "" {
			if err := randomToken(&rule.ID); err != nil {
				return nil, err
			}
			rule.ID = rule.ID[:12]
		}
		if !containsString(mihomoRuleTypes, rule.Type) {
			return nil, fmt.Errorf("Routing Rule #%d 类型 %q 不受支持", index+1, rule.Type)
		}
		if !mihomoTargetExists(rule.Target, names) {
			return nil, fmt.Errorf("Routing Rule #%d 引用了不存在的 Outbound %q", index+1, rule.Target)
		}
		if rule.Type == "MATCH" {
			if rule.Payload != "" || rule.NoResolve {
				return nil, fmt.Errorf("MATCH 规则不能包含 Payload 或 no-resolve")
			}
			if matchSeen || index != len(rules)-1 {
				return nil, fmt.Errorf("MATCH 只能出现一次且必须是最后一条规则")
			}
			matchSeen = true
			continue
		}
		if matchSeen {
			return nil, fmt.Errorf("MATCH 后面不能再添加规则")
		}
		if err := validateMihomoRulePayload(*rule); err != nil {
			return nil, fmt.Errorf("Routing Rule #%d: %w", index+1, err)
		}
	}
	if !matchSeen {
		rules = append(rules, defaultMihomoRoutingRules()[0])
	}
	return rules, nil
}

func validateMihomoRulePayload(rule MihomoRoutingRule) error {
	if rule.Payload == "" {
		return fmt.Errorf("%s 需要 Payload", rule.Type)
	}
	noResolveTypes := []string{"GEOIP", "IP-ASN", "IP-CIDR", "IP-CIDR6", "IP-SUFFIX"}
	if rule.NoResolve && !containsString(noResolveTypes, rule.Type) {
		return fmt.Errorf("no-resolve 只适用于目标 IP 类规则")
	}
	if !containsString([]string{"DOMAIN-REGEX", "PROCESS-NAME-REGEX", "PROCESS-PATH-REGEX"}, rule.Type) && strings.Contains(rule.Payload, ",") {
		return fmt.Errorf("Payload 不能包含逗号")
	}
	switch rule.Type {
	case "IP-CIDR", "IP-CIDR6", "SRC-IP-CIDR":
		if _, _, err := net.ParseCIDR(rule.Payload); err != nil {
			return fmt.Errorf("CIDR 格式无效")
		}
	case "NETWORK":
		if !containsString([]string{"tcp", "udp", "TCP", "UDP"}, rule.Payload) {
			return fmt.Errorf("NETWORK 只能是 tcp 或 udp")
		}
	case "DST-PORT", "SRC-PORT", "IN-PORT":
		if err := validateMihomoPortRange(rule.Payload); err != nil {
			return err
		}
	case "DOMAIN-REGEX", "PROCESS-NAME-REGEX", "PROCESS-PATH-REGEX":
		if _, err := regexp.Compile(rule.Payload); err != nil {
			return fmt.Errorf("正则表达式无效: %w", err)
		}
	case "IP-ASN", "SRC-IP-ASN", "UID", "DSCP":
		if _, err := strconv.ParseUint(rule.Payload, 10, 32); err != nil {
			return fmt.Errorf("Payload 必须是非负整数")
		}
	}
	return nil
}

func validateMihomoPortRange(value string) error {
	parts := strings.Split(value, "-")
	if len(parts) < 1 || len(parts) > 2 {
		return fmt.Errorf("端口必须是单个端口或 start-end")
	}
	ports := make([]int, 0, len(parts))
	for _, part := range parts {
		port, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("端口必须在 1-65535 之间")
		}
		ports = append(ports, port)
	}
	if len(ports) == 2 && ports[0] > ports[1] {
		return fmt.Errorf("端口范围起点不能大于终点")
	}
	return nil
}

func mihomoOutboundConfig(items []MihomoOutbound) (proxies, groups []map[string]any) {
	for _, item := range items {
		if item.Kind == "group" {
			groups = append(groups, mihomoProxyGroupConfig(item))
		} else {
			proxies = append(proxies, mihomoProxyConfig(item))
		}
	}
	return
}

func mihomoProxyConfig(item MihomoOutbound) map[string]any {
	if item.Native != nil {
		return item.Native
	}
	result := map[string]any{"name": item.Name, "type": item.Type}
	if item.Type != "direct" {
		result["server"], result["port"] = item.Server, item.Port
	}
	if item.IPVersion != "" {
		result["ip-version"] = item.IPVersion
	}
	if item.DialerProxy != "" {
		result["dialer-proxy"] = item.DialerProxy
	}
	if item.InterfaceName != "" {
		result["interface-name"] = item.InterfaceName
	}
	if item.RoutingMark != 0 {
		result["routing-mark"] = item.RoutingMark
	}
	if item.TFO {
		result["tfo"] = true
	}
	if item.MPTCP {
		result["mptcp"] = true
	}
	writeAuth := func() {
		if item.Username != "" {
			result["username"] = item.Username
		}
		if item.Password != "" {
			result["password"] = item.Password
		}
	}
	writeTLS := func(serverNameKey string) {
		if item.TLS {
			result["tls"] = true
		}
		if item.SNI != "" {
			result[serverNameKey] = item.SNI
		}
		if item.SkipCertVerify {
			result["skip-cert-verify"] = true
		}
		if alpn := splitList(item.ALPN); len(alpn) > 0 {
			result["alpn"] = alpn
		}
	}
	writeTransport := func() {
		network := valueOr(item.Network, "tcp")
		if network != "tcp" {
			result["network"] = network
		}
		if network == "ws" {
			ws := map[string]any{"path": valueOr(item.WSPath, "/")}
			if item.WSHost != "" {
				ws["headers"] = map[string]string{"Host": item.WSHost}
			}
			result["ws-opts"] = ws
		}
		if network == "grpc" {
			result["grpc-opts"] = map[string]any{"grpc-service-name": item.GRPCServiceName}
		}
	}
	switch item.Type {
	case "direct":
	case "ss":
		result["cipher"], result["password"] = item.Cipher, item.Password
		if item.UDP {
			result["udp"] = true
		}
	case "socks5", "http":
		writeAuth()
		writeTLS("sni")
		if item.Type == "socks5" && item.UDP {
			result["udp"] = true
		}
	case "vmess":
		result["uuid"], result["alterId"], result["cipher"] = item.UUID, item.AlterID, valueOr(item.Cipher, "auto")
		if item.UDP {
			result["udp"] = true
		}
		writeTransport()
		writeTLS("servername")
	case "vless":
		result["uuid"], result["encryption"] = item.UUID, valueOr(item.Encryption, "none")
		if item.Flow != "" {
			result["flow"] = item.Flow
		}
		if item.UDP {
			result["udp"] = true
		}
		writeTransport()
		writeTLS("servername")
		if item.Reality {
			result["reality-opts"] = map[string]any{"public-key": item.RealityPublicKey, "short-id": item.RealityShortID}
			result["tls"] = true
		}
	case "trojan":
		result["password"] = item.Password
		if item.UDP {
			result["udp"] = true
		}
		writeTransport()
		writeTLS("sni")
	case "hysteria2":
		result["password"] = item.Password
		writeTLS("sni")
		if item.Up != "" {
			result["up"] = item.Up
		}
		if item.Down != "" {
			result["down"] = item.Down
		}
		if item.Obfs != "" {
			result["obfs"], result["obfs-password"] = item.Obfs, item.ObfsPassword
		}
	case "tuic":
		if item.Token != "" {
			result["token"] = item.Token
		} else {
			result["uuid"], result["password"] = item.UUID, item.Password
		}
		writeTLS("sni")
		if item.CongestionController != "" {
			result["congestion-controller"] = item.CongestionController
		}
		if item.UDPRelayMode != "" {
			result["udp-relay-mode"] = item.UDPRelayMode
		}
	case "wireguard":
		result["ip"], result["private-key"], result["public-key"] = item.WireGuardIP, item.WireGuardPrivateKey, item.WireGuardPublicKey
		if item.WireGuardIPv6 != "" {
			result["ipv6"] = item.WireGuardIPv6
		}
		if item.WireGuardPreSharedKey != "" {
			result["pre-shared-key"] = item.WireGuardPreSharedKey
		}
		if len(item.WireGuardReserved) > 0 {
			reserved := make([]any, len(item.WireGuardReserved))
			for index, value := range item.WireGuardReserved {
				reserved[index] = value
			}
			result["reserved"] = reserved
		}
		if item.WireGuardPersistentKeepalive != 0 {
			result["persistent-keepalive"] = item.WireGuardPersistentKeepalive
		}
		if item.WireGuardMTU != 0 {
			result["mtu"] = item.WireGuardMTU
		}
		if item.WireGuardWorkers != 0 {
			result["workers"] = item.WireGuardWorkers
		}
		if item.UDP {
			result["udp"] = true
		}
		if item.WireGuardRemoteDNSResolve {
			result["remote-dns-resolve"] = true
			result["dns"] = append([]string(nil), item.WireGuardDNS...)
		}
		if item.WireGuardRefreshServerIPInterval != 0 {
			result["refresh-server-ip-interval"] = item.WireGuardRefreshServerIPInterval
		}
	case "openvpn":
		writeAuth()
		result["ca"] = item.OpenVPNCA
		if item.OpenVPNProto != "" {
			result["proto"] = item.OpenVPNProto
		}
		if item.OpenVPNDev != "" {
			result["dev"] = item.OpenVPNDev
		}
		if item.OpenVPNCipher != "" {
			result["cipher"] = item.OpenVPNCipher
		}
		if len(item.OpenVPNDataCiphers) > 0 {
			result["data-ciphers"] = append([]string(nil), item.OpenVPNDataCiphers...)
		}
		if item.OpenVPNDataCipherFallback != "" {
			result["data-ciphers-fallback"] = item.OpenVPNDataCipherFallback
		}
		if item.OpenVPNAuth != "" {
			result["auth"] = item.OpenVPNAuth
		}
		if item.OpenVPNCompLZO != "" {
			result["comp-lzo"] = item.OpenVPNCompLZO
		}
		for key, value := range map[string]string{
			"cert": item.OpenVPNCert, "key": item.OpenVPNKey, "tls-auth": item.OpenVPNTLSAuth,
			"key-direction": item.OpenVPNKeyDirection, "tls-crypt": item.OpenVPNTLSCrypt, "tls-crypt-v2": item.OpenVPNTLSCryptV2,
		} {
			if value != "" {
				result[key] = value
			}
		}
		for key, value := range map[string]int{
			"ping": item.OpenVPNPing, "ping-restart": item.OpenVPNPingRestart,
			"handshake-timeout": item.OpenVPNHandshakeTimeout, "mtu": item.OpenVPNMTU,
		} {
			if value != 0 {
				result[key] = value
			}
		}
		if item.UDP {
			result["udp"] = true
		}
		if item.OpenVPNRemoteDNSResolve {
			result["remote-dns-resolve"] = true
			result["dns"] = append([]string(nil), item.OpenVPNDNS...)
		}
	}
	if item.ClientFingerprint != "" && containsString([]string{"ss", "vmess", "vless", "trojan"}, item.Type) {
		result["client-fingerprint"] = item.ClientFingerprint
	}
	return result
}

func mihomoProxyGroupConfig(item MihomoOutbound) map[string]any {
	if item.Native != nil {
		return item.Native
	}
	result := map[string]any{"name": item.Name, "type": item.Type, "proxies": append([]string(nil), item.Proxies...)}
	if item.DisableUDP {
		result["disable-udp"] = true
	}
	if item.DefaultSelected != "" && item.Type == "select" {
		result["default-selected"] = item.DefaultSelected
	}
	if item.Type != "select" {
		result["url"] = valueOr(item.TestURL, defaultMihomoTestURL)
		result["interval"] = item.Interval
		result["lazy"] = true
		if item.Timeout > 0 {
			result["timeout"] = item.Timeout
		}
	}
	if item.Type == "url-test" && item.Tolerance > 0 {
		result["tolerance"] = item.Tolerance
	}
	if item.Type == "load-balance" && item.Strategy != "" {
		result["strategy"] = item.Strategy
	}
	return result
}

func mihomoRoutingConfig(rules []MihomoRoutingRule) []string {
	result := make([]string, 0, len(rules))
	if len(rules) == 0 {
		rules = defaultMihomoRoutingRules()
	}
	for _, rule := range rules {
		if rule.Type == "MATCH" {
			result = append(result, "MATCH,"+rule.Target)
			continue
		}
		line := rule.Type + "," + rule.Payload + "," + rule.Target
		if rule.NoResolve {
			line += ",no-resolve"
		}
		result = append(result, line)
	}
	return result
}
