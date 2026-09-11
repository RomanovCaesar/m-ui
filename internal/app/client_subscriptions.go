package app

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// 本文件把 Sub-Store 有而 3x-ui 没有的客户端订阅格式移植成 Go 生产者，
// 逐格式对照 backend/src/core/proxy-utils/producers/*.js：
//   - Surge / Surfboard / SurgeMac：ini 逐行（surge.js / surfboard.js / surgemac.js）
//   - Loon / Quantumult X：ini 逐行（loon.js / qx.js）
//   - Stash / Egern：Clash 系 YAML（stash.js / egern.js）
//
// 所有生产者都从 subscriptionProxies 的 clash/mihomo proxy map 出发（协议支持、
// 字段提取、reality/传输归一都在 subscriptionProxy 里且已有单测），这里只做
// "目标客户端方言"的转换。各客户端不支持的协议/传输直接跳过该行——sub-store
// 对 ini 家族是 throw 后丢弃整个节点，语义相同。

const (
	clientSubscriptionTextContentType = "text/plain; charset=utf-8"
	clientSubscriptionYAMLContentType = "application/yaml; charset=utf-8"
)

// buildClientSubscription renders the sub-store-style client formats.
func buildClientSubscription(snapshot subscriptionSnapshot, server string, format subscriptionFormat) ([]byte, string, error) {
	switch format {
	case subscriptionFormatSurge:
		data, err := buildSurgeFamilySubscription(snapshot, server, "Surge", false)
		return data, clientSubscriptionTextContentType, err
	case subscriptionFormatSurgeMac:
		data, err := buildSurgeMacSubscription(snapshot, server)
		return data, clientSubscriptionTextContentType, err
	case subscriptionFormatSurfboard:
		data, err := buildSurgeFamilySubscription(snapshot, server, "Surfboard", true)
		return data, clientSubscriptionTextContentType, err
	case subscriptionFormatLoon:
		data, err := buildLineSubscription(snapshot, server, "Loon", loonProxyLine)
		return data, clientSubscriptionTextContentType, err
	case subscriptionFormatQX:
		data, err := buildLineSubscription(snapshot, server, "Quantumult X", qxProxyLine)
		return data, clientSubscriptionTextContentType, err
	case subscriptionFormatStash:
		data, err := buildStashSubscription(snapshot, server)
		return data, clientSubscriptionYAMLContentType, err
	case subscriptionFormatEgern:
		data, err := buildEgernSubscription(snapshot, server)
		return data, clientSubscriptionYAMLContentType, err
	}
	return nil, "", fmt.Errorf("未知的客户端订阅格式")
}

func buildLineSubscription(snapshot subscriptionSnapshot, server, label string, lineFn func(map[string]any) (string, bool)) ([]byte, error) {
	lines := make([]string, 0)
	for _, proxy := range subscriptionProxies(snapshot, server) {
		if line, ok := lineFn(proxy); ok {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("此用户没有 %s 可用节点", label)
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

// ---- proxy map 访问小工具（clash 键名） ----

func proxyStr(proxy map[string]any, key string) string {
	value, _ := proxy[key].(string)
	return value
}

func proxyHas(proxy map[string]any, key string) bool {
	_, ok := proxy[key]
	return ok
}

func proxyBool(proxy map[string]any, key string) bool {
	value, _ := proxy[key].(bool)
	return value
}

func proxyInt(proxy map[string]any, key string) int {
	switch value := proxy[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	}
	return 0
}

func mapStr(source map[string]any, key string) string {
	value, _ := source[key].(string)
	return value
}

// proxySNI reads the SNI whichever key the producer family uses: sub-store's
// internal model says `sni`, our clash maps say `servername`.
func proxySNI(proxy map[string]any) string {
	if sni := proxyStr(proxy, "sni"); sni != "" {
		return sni
	}
	return proxyStr(proxy, "servername")
}

func wsOptsMap(proxy map[string]any) map[string]any {
	opts, _ := proxy["ws-opts"].(map[string]any)
	return opts
}

func realityOptsMap(proxy map[string]any) map[string]any {
	opts, _ := proxy["reality-opts"].(map[string]any)
	return opts
}

// iniName mirrors sub-store's `proxy.name.replace(/=|,/g, ”)`: both characters
// would terminate or split an ini field.
func iniName(proxy map[string]any) string {
	return strings.Map(func(r rune) rune {
		if r == '=' || r == ',' {
			return -1
		}
		return r
	}, proxyStr(proxy, "name"))
}

func iniPort(proxy map[string]any) string {
	return strconv.Itoa(proxyInt(proxy, "port"))
}

// iniDigits mirrors `${value}`.match(/\d+/)?.[0] || 0.
func iniDigits(value string) string {
	for start := 0; start < len(value); start++ {
		if value[start] < '0' || value[start] > '9' {
			continue
		}
		end := start
		for end < len(value) && value[end] >= '0' && value[end] <= '9' {
			end++
		}
		return value[start:end]
	}
	return "0"
}

// appendIfPresentBool mirrors Result.appendIfPresent: the parameter is written
// whenever the key exists, including false values.
func appendIfPresentBool(builder *strings.Builder, param string, proxy map[string]any, key string) {
	if value, ok := proxy[key].(bool); ok {
		builder.WriteString(param + strconv.FormatBool(value))
	}
}

// appendIfTrueBool mirrors loon's `if (proxy.udp) append(',udp=true')`.
func appendIfTrueBool(builder *strings.Builder, param string, proxy map[string]any, key string) {
	if proxyBool(proxy, key) {
		builder.WriteString(param + "true")
	}
}

// ---- Surge / Surfboard（surge.js / surfboard.js） ----

var surgeSSCiphers = []string{
	"aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "xchacha20-ietf-poly1305",
	"rc4", "rc4-md5", "aes-128-cfb", "aes-192-cfb", "aes-256-cfb", "aes-128-ctr", "aes-192-ctr",
	"aes-256-ctr", "bf-cfb", "camellia-128-cfb", "camellia-192-cfb", "camellia-256-cfb", "cast5-cfb",
	"des-cfb", "idea-cfb", "rc2-cfb", "seed-cfb", "salsa20", "chacha20", "chacha20-ietf", "none",
	"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm",
}

var surfboardSSCiphers = []string{
	"aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "xchacha20-ietf-poly1305",
	"rc4", "rc4-md5", "aes-128-cfb", "aes-192-cfb", "aes-256-cfb", "aes-128-ctr", "aes-192-ctr",
	"aes-256-ctr", "bf-cfb", "camellia-128-cfb", "camellia-192-cfb", "camellia-256-cfb",
	"salsa20", "chacha20", "chacha20-ietf", "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm",
}

// surgeTLSParams mirrors appendTlsProxyParams: sni is quoted, skip-cert-verify
// is a bare bool. Neither dialect carries alpn/fingerprint for our fields.
func surgeTLSParams(builder *strings.Builder, proxy map[string]any, enabled bool) {
	if !enabled {
		return
	}
	if sni := proxySNI(proxy); sni != "" {
		builder.WriteString(`,sni="` + sni + `"`)
	}
	appendIfPresentBool(builder, ",skip-cert-verify=", proxy, "skip-cert-verify")
}

// surgeTransportParams mirrors handleTransport: only ws survives; anything else
// (grpc/xhttp/mkcp/mekya) makes sub-store throw, so we skip the node.
func surgeTransportParams(proxy map[string]any, surfboard bool) (string, bool) {
	network := proxyStr(proxy, "network")
	if network == "" {
		return "", true
	}
	if network != "ws" {
		return "", false
	}
	var builder strings.Builder
	builder.WriteString(",ws=true")
	opts := wsOptsMap(proxy)
	if path := mapStr(opts, "path"); path != "" {
		builder.WriteString(",ws-path=" + path)
	}
	if headers, ok := opts["headers"].(map[string]any); ok && len(headers) > 0 {
		keys := make([]string, 0, len(headers))
		for key := range headers {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			value := mapStr(headers, key)
			if surfboard && key != "Host" {
				parts = append(parts, key+":"+value)
				continue
			}
			parts = append(parts, key+`:"`+value+`"`)
		}
		joined := strings.Join(parts, "|")
		if surfboard {
			// surfboard.js leaves the whole ws-headers value unquoted.
			builder.WriteString(",ws-headers=" + joined)
		} else {
			builder.WriteString(`,ws-headers="` + joined + `"`)
		}
	}
	return builder.String(), true
}

// surgeProxyLine renders one Surge/Surfboard proxy line. Reality nodes are
// skipped outright: surge.js throws `reality is unsupported` on tcp and silently
// drops reality-opts on ws (producing a node that can never connect), so
// skipping is the faithful-and-sane reading.
func surgeProxyLine(proxy map[string]any, surfboard bool) (string, bool) {
	if proxyHas(proxy, "reality-opts") {
		return "", false
	}
	transport, ok := surgeTransportParams(proxy, surfboard)
	if !ok {
		return "", false
	}
	name, server := iniName(proxy), proxyStr(proxy, "server")
	port := iniPort(proxy)
	var builder strings.Builder
	switch proxyStr(proxy, "type") {
	case "ss":
		ciphers := surgeSSCiphers
		if surfboard {
			ciphers = surfboardSSCiphers
		}
		cipher := proxyStr(proxy, "cipher")
		if !containsString(ciphers, cipher) {
			return "", false
		}
		builder.WriteString(name + "=ss," + server + "," + port + ",encrypt-method=" + cipher)
		if password := proxyStr(proxy, "password"); password != "" {
			builder.WriteString(`,password="` + password + `"`)
		}
		if proxyHas(proxy, "plugin") {
			if proxyStr(proxy, "plugin") != "obfs" {
				return "", false
			}
			opts, _ := proxy["plugin-opts"].(map[string]any)
			builder.WriteString(",obfs=" + mapStr(opts, "mode"))
			if host := mapStr(opts, "host"); host != "" {
				builder.WriteString(",obfs-host=" + host)
			}
			if path := mapStr(opts, "path"); path != "" {
				builder.WriteString(",obfs-uri=" + path)
			}
		}
		appendIfPresentBool(&builder, ",udp-relay=", proxy, "udp")
	case "vmess":
		builder.WriteString(name + "=vmess," + server + "," + port)
		builder.WriteString(",username=" + proxyStr(proxy, "uuid"))
		// formatSurgeVmessEncryptMethod only emits aes-128-gcm/chacha20-ietf-poly1305;
		// our vmess cipher is always "auto", which maps to "omit the parameter".
		if !surfboard {
			if cipher := proxyStr(proxy, "cipher"); cipher == "aes-128-gcm" {
				builder.WriteString(",encrypt-method=aes-128-gcm")
			} else if cipher == "chacha20-poly1305" || cipher == "chacha20-ietf-poly1305" {
				builder.WriteString(",encrypt-method=chacha20-ietf-poly1305")
			}
		}
		builder.WriteString(transport)
		builder.WriteString(",vmess-aead=" + strconv.FormatBool(proxyInt(proxy, "alterId") == 0))
		if proxyBool(proxy, "tls") {
			builder.WriteString(",tls=true")
		}
		surgeTLSParams(&builder, proxy, proxyBool(proxy, "tls"))
		appendIfPresentBool(&builder, ",udp-relay=", proxy, "udp")
	case "trojan":
		builder.WriteString(name + "=trojan," + server + "," + port)
		if surfboard {
			// surfboard.js appends the trojan password unquoted.
			builder.WriteString(",password=" + proxyStr(proxy, "password"))
		} else {
			builder.WriteString(`,password="` + proxyStr(proxy, "password") + `"`)
		}
		builder.WriteString(transport)
		if proxyBool(proxy, "tls") {
			builder.WriteString(",tls=true")
		}
		surgeTLSParams(&builder, proxy, true)
		appendIfPresentBool(&builder, ",udp-relay=", proxy, "udp")
	case "http":
		proxyType := "http"
		if proxyBool(proxy, "tls") {
			proxyType = "https"
		}
		builder.WriteString(name + "=" + proxyType + "," + server + "," + port)
		if username := proxyStr(proxy, "username"); username != "" {
			builder.WriteString(`,username="` + username + `"`)
		}
		if password := proxyStr(proxy, "password"); password != "" {
			builder.WriteString(`,password="` + password + `"`)
		}
		surgeTLSParams(&builder, proxy, proxyBool(proxy, "tls"))
		appendIfPresentBool(&builder, ",udp-relay=", proxy, "udp")
	case "socks5":
		proxyType := "socks5"
		if proxyBool(proxy, "tls") {
			proxyType = "socks5-tls"
		}
		builder.WriteString(name + "=" + proxyType + "," + server + "," + port)
		if username := proxyStr(proxy, "username"); username != "" {
			builder.WriteString(`,username="` + username + `"`)
		}
		if password := proxyStr(proxy, "password"); password != "" {
			builder.WriteString(`,password="` + password + `"`)
		}
		surgeTLSParams(&builder, proxy, proxyBool(proxy, "tls"))
		appendIfPresentBool(&builder, ",udp-relay=", proxy, "udp")
	case "tuic":
		// m-ui only ever produces tuic v5 (no shared token).
		builder.WriteString(name + "=tuic-v5," + server + "," + port)
		builder.WriteString(",uuid=" + proxyStr(proxy, "uuid"))
		builder.WriteString(`,password="` + proxyStr(proxy, "password") + `"`)
		surgeTLSParams(&builder, proxy, true)
		if surfboard {
			appendIfPresentBool(&builder, ",udp-relay=", proxy, "udp")
		}
	case "hysteria2":
		obfs, obfsPassword := proxyStr(proxy, "obfs"), proxyStr(proxy, "obfs-password")
		if surfboard {
			// surfboard.js: only salamander obfs survives, and an obfs-password
			// without salamander throws.
			if (obfs != "" || obfsPassword != "") && obfs != "salamander" {
				return "", false
			}
		} else if obfsPassword != "" && obfs != "salamander" && obfs != "gecko" {
			return "", false
		}
		builder.WriteString(name + "=hysteria2," + server + "," + port)
		builder.WriteString(`,password="` + proxyStr(proxy, "password") + `"`)
		if obfsPassword != "" {
			field := "salamander-password"
			if !surfboard && obfs == "gecko" {
				field = "gecko-password"
			}
			builder.WriteString(`,` + field + `="` + obfsPassword + `"`)
		}
		surgeTLSParams(&builder, proxy, true)
		if surfboard {
			appendIfPresentBool(&builder, ",udp-relay=", proxy, "udp")
		}
		if down := proxyStr(proxy, "down"); down != "" {
			builder.WriteString(",download-bandwidth=" + iniDigits(down))
		}
	case "anytls":
		builder.WriteString(name + "=anytls," + server + "," + port)
		builder.WriteString(`,password="` + proxyStr(proxy, "password") + `"`)
		surgeTLSParams(&builder, proxy, true)
		appendIfPresentBool(&builder, ",udp-relay=", proxy, "udp")
	default:
		// vless / mieru / snell / … have no Surge or Surfboard case.
		return "", false
	}
	return builder.String(), true
}

func buildSurgeFamilySubscription(snapshot subscriptionSnapshot, server, label string, surfboard bool) ([]byte, error) {
	return buildLineSubscription(snapshot, server, label, func(proxy map[string]any) (string, bool) {
		return surgeProxyLine(proxy, surfboard)
	})
}

// surgeMacExternalPort is where the mihomo external-proxy-program countdown
// starts (surgemac.js: `opts.localPort || 65535`, then localPort-1 per node).
const surgeMacExternalPort = 65535

// buildSurgeMacSubscription mirrors surgemac.js: nodes Surge supports natively
// become normal Surge lines; the rest fall back to an `external` line that
// spawns /usr/local/bin/mihomo with a base64 single-node config embedded, so
// vless/reality/… still work on macOS through the external proxy program.
func buildSurgeMacSubscription(snapshot subscriptionSnapshot, server string) ([]byte, error) {
	lines := make([]string, 0)
	nextPort := surgeMacExternalPort
	for _, proxy := range subscriptionProxies(snapshot, server) {
		if line, ok := surgeProxyLine(proxy, false); ok {
			lines = append(lines, line)
			continue
		}
		localPort := nextPort
		nextPort--
		embedded := map[string]any{
			"mixed-port": localPort,
			"ipv6":       true,
			"mode":       "global",
			"dns": map[string]any{
				"enable":             true,
				"ipv6":               true,
				"default-nameserver": []any{"180.76.76.76", "52.80.52.52", "119.28.28.28", "223.6.6.6"},
				"nameserver":         []any{"https://doh.pub/dns-query", "https://dns.alidns.com/dns-query", "https://doh-pure.onedns.net/dns-query"},
			},
			"proxies": []any{func() map[string]any {
				single := make(map[string]any, len(proxy)+1)
				for key, value := range proxy {
					single[key] = value
				}
				single["name"] = "proxy"
				return single
			}()},
			"proxy-groups": []any{map[string]any{"name": "GLOBAL", "type": "select", "proxies": []any{"proxy"}}},
		}
		config, err := json.Marshal(embedded)
		if err != nil {
			return nil, err
		}
		var builder strings.Builder
		builder.WriteString(iniName(proxy) + `=external,exec="/usr/local/bin/mihomo",local-port=` + strconv.Itoa(localPort))
		builder.WriteString(`,args="-config",args="` + base64.StdEncoding.EncodeToString(config) + `"`)
		if ip := net.ParseIP(proxyStr(proxy, "server")); ip != nil {
			builder.WriteString(",addresses=" + proxyStr(proxy, "server"))
		}
		builder.WriteString(",udp-relay=true")
		lines = append(lines, builder.String())
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("此用户没有 Surge Mac 可用节点")
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

// ---- Loon（loon.js） ----

var loonSSCiphers = []string{
	"rc4", "rc4-md5", "aes-128-cfb", "aes-192-cfb", "aes-256-cfb", "aes-128-ctr", "aes-192-ctr",
	"aes-256-ctr", "bf-cfb", "camellia-128-cfb", "camellia-192-cfb", "camellia-256-cfb",
	"salsa20", "chacha20", "chacha20-ietf", "aes-128-gcm", "aes-192-gcm", "aes-256-gcm",
	"chacha20-ietf-poly1305", "xchacha20-ietf-poly1305", "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm",
}

// loonRealityOrSNI mirrors appendReality / the tls-name fallback.
func loonRealityOrSNI(builder *strings.Builder, proxy map[string]any) {
	if reality := realityOptsMap(proxy); reality != nil {
		if sni := proxySNI(proxy); sni != "" {
			builder.WriteString(",sni=" + sni)
		}
		if publicKey := mapStr(reality, "public-key"); publicKey != "" {
			builder.WriteString(`,public-key="` + publicKey + `"`)
		}
		if shortID := mapStr(reality, "short-id"); shortID != "" {
			builder.WriteString(",short-id=" + shortID)
		}
		return
	}
	if sni := proxySNI(proxy); sni != "" {
		builder.WriteString(",tls-name=" + sni)
	}
}

// loonTransport mirrors loon's per-protocol transport switch: vmess/vless take
// ws (http never occurs in our maps), trojan only ws, everything else skips.
func loonTransport(builder *strings.Builder, proxy map[string]any, allowTCPFallback bool) bool {
	network := proxyStr(proxy, "network")
	switch network {
	case "":
		if allowTCPFallback {
			builder.WriteString(",transport=tcp")
		}
		return true
	case "ws":
		builder.WriteString(",transport=ws")
		opts := wsOptsMap(proxy)
		if path := mapStr(opts, "path"); path != "" {
			builder.WriteString(",path=" + path)
		}
		if headers, ok := opts["headers"].(map[string]any); ok {
			if host := mapStr(headers, "Host"); host != "" {
				builder.WriteString(",host=" + host)
			}
		}
		return true
	}
	return false
}

func loonProxyLine(proxy map[string]any) (string, bool) {
	name, server := iniName(proxy), proxyStr(proxy, "server")
	port := iniPort(proxy)
	var builder strings.Builder
	switch proxyStr(proxy, "type") {
	case "ss":
		cipher := proxyStr(proxy, "cipher")
		if !containsString(loonSSCiphers, cipher) {
			return "", false
		}
		builder.WriteString(name + "=shadowsocks," + server + "," + port + "," + cipher + `,"` + proxyStr(proxy, "password") + `"`)
		if proxyHas(proxy, "plugin") {
			if proxyStr(proxy, "plugin") != "obfs" {
				return "", false
			}
			opts, _ := proxy["plugin-opts"].(map[string]any)
			builder.WriteString(",obfs-name=" + mapStr(opts, "mode"))
			if host := mapStr(opts, "host"); host != "" {
				builder.WriteString(",obfs-host=" + host)
			}
			if path := mapStr(opts, "path"); path != "" {
				builder.WriteString(",obfs-uri=" + path)
			}
		}
		appendIfTrueBool(&builder, ",udp=", proxy, "udp")
	case "trojan":
		builder.WriteString(name + "=trojan," + server + "," + port + `,"` + proxyStr(proxy, "password") + `"`)
		if !loonTransport(&builder, proxy, false) {
			return "", false
		}
		appendIfPresentBool(&builder, ",skip-cert-verify=", proxy, "skip-cert-verify")
		loonRealityOrSNI(&builder, proxy)
		appendIfTrueBool(&builder, ",udp=", proxy, "udp")
	case "vmess":
		// formatLoonVmessSecurity("auto") == "auto".
		builder.WriteString(name + `=vmess,` + server + "," + port + `,auto,"` + proxyStr(proxy, "uuid") + `"`)
		if !loonTransport(&builder, proxy, true) {
			return "", false
		}
		if proxyBool(proxy, "tls") {
			builder.WriteString(",over-tls=true")
		}
		appendIfPresentBool(&builder, ",skip-cert-verify=", proxy, "skip-cert-verify")
		loonRealityOrSNI(&builder, proxy)
		builder.WriteString(",alterId=" + strconv.Itoa(proxyInt(proxy, "alterId")))
		appendIfTrueBool(&builder, ",udp=", proxy, "udp")
	case "vless":
		if proxyStr(proxy, "encryption") != "" {
			return "", false
		}
		flow := proxyStr(proxy, "flow")
		if flow != "" && flow != "xtls-rprx-vision" {
			return "", false
		}
		builder.WriteString(name + "=vless," + server + "," + port + `,"` + proxyStr(proxy, "uuid") + `"`)
		if !loonTransport(&builder, proxy, true) {
			return "", false
		}
		if proxyBool(proxy, "tls") {
			builder.WriteString(",over-tls=true")
		}
		appendIfPresentBool(&builder, ",skip-cert-verify=", proxy, "skip-cert-verify")
		if flow != "" {
			builder.WriteString(",flow=" + flow)
		}
		loonRealityOrSNI(&builder, proxy)
		appendIfTrueBool(&builder, ",udp=", proxy, "udp")
	case "anytls":
		builder.WriteString(name + "=anytls," + server + "," + port + `,"` + proxyStr(proxy, "password") + `"`)
		appendIfPresentBool(&builder, ",skip-cert-verify=", proxy, "skip-cert-verify")
		loonRealityOrSNI(&builder, proxy)
		appendIfTrueBool(&builder, ",udp=", proxy, "udp")
	case "http":
		proxyType := "http"
		if proxyBool(proxy, "tls") {
			proxyType = "https"
		}
		builder.WriteString(name + "=" + proxyType + "," + server + "," + port)
		if username := proxyStr(proxy, "username"); username != "" {
			builder.WriteString("," + username)
		}
		if password := proxyStr(proxy, "password"); password != "" {
			builder.WriteString(`,"` + password + `"`)
		}
		if sni := proxySNI(proxy); sni != "" {
			builder.WriteString(",sni=" + sni)
		}
		appendIfPresentBool(&builder, ",skip-cert-verify=", proxy, "skip-cert-verify")
	case "socks5":
		builder.WriteString(name + "=socks5," + server + "," + port)
		if username := proxyStr(proxy, "username"); username != "" {
			builder.WriteString("," + username)
		}
		if password := proxyStr(proxy, "password"); password != "" {
			builder.WriteString(`,"` + password + `"`)
		}
		if proxyBool(proxy, "tls") {
			builder.WriteString(",over-tls=true")
		}
		if sni := proxySNI(proxy); sni != "" {
			builder.WriteString(",sni=" + sni)
		}
		appendIfPresentBool(&builder, ",skip-cert-verify=", proxy, "skip-cert-verify")
		appendIfTrueBool(&builder, ",udp=", proxy, "udp")
	case "hysteria2":
		if proxyStr(proxy, "obfs-password") != "" && proxyStr(proxy, "obfs") != "salamander" {
			return "", false
		}
		builder.WriteString(name + "=Hysteria2," + server + "," + port)
		if password := proxyStr(proxy, "password"); password != "" {
			builder.WriteString(`,"` + password + `"`)
		}
		if sni := proxySNI(proxy); sni != "" {
			builder.WriteString(",tls-name=" + sni)
		}
		appendIfPresentBool(&builder, ",skip-cert-verify=", proxy, "skip-cert-verify")
		if proxyStr(proxy, "obfs-password") != "" {
			builder.WriteString(",salamander-password=" + proxyStr(proxy, "obfs-password"))
		}
		appendIfTrueBool(&builder, ",udp=", proxy, "udp")
		if down := proxyStr(proxy, "down"); down != "" {
			builder.WriteString(",download-bandwidth=" + iniDigits(down))
		}
	default:
		// tuic / snell / mieru have no Loon case.
		return "", false
	}
	return builder.String(), true
}

// ---- Quantumult X（qx.js） ----

var qxSSCiphers = []string{
	"none", "rc4-md5", "rc4-md5-6", "aes-128-cfb", "aes-192-cfb", "aes-256-cfb", "aes-128-ctr",
	"aes-192-ctr", "aes-256-ctr", "bf-cfb", "cast5-cfb", "des-cfb", "rc2-cfb", "salsa20",
	"chacha20", "chacha20-ietf", "aes-128-gcm", "aes-192-gcm", "aes-256-gcm",
	"chacha20-ietf-poly1305", "xchacha20-ietf-poly1305", "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm",
}

// qxTLSVerification writes `,tls-verification=<!skip-cert-verify>` when the
// skip-cert-verify key exists (appendTlsVerification in qx.js: the flag is
// inverted relative to clash's skip-cert-verify).
func qxTLSVerification(builder *strings.Builder, proxy map[string]any) {
	if skip, ok := proxy["skip-cert-verify"].(bool); ok {
		builder.WriteString(",tls-verification=" + strconv.FormatBool(!skip))
	}
}

// qxObfsTransport mirrors QX's obfs= encoding of the transport layer.
func qxObfsTransport(builder *strings.Builder, proxy map[string]any) bool {
	network := proxyStr(proxy, "network")
	tls := proxyBool(proxy, "tls")
	switch network {
	case "":
		if tls {
			builder.WriteString(",obfs=over-tls")
		}
		return true
	case "ws":
		if tls {
			builder.WriteString(",obfs=wss")
		} else {
			builder.WriteString(",obfs=ws")
		}
	case "tcp":
		if tls {
			builder.WriteString(",obfs=over-tls")
		}
	default:
		return false
	}
	opts := wsOptsMap(proxy)
	if path := mapStr(opts, "path"); path != "" && network == "ws" {
		builder.WriteString(",obfs-uri=" + path)
	}
	if headers, ok := opts["headers"].(map[string]any); ok && network == "ws" {
		if host := mapStr(headers, "Host"); host != "" {
			builder.WriteString(",obfs-host=" + host)
		}
	}
	return true
}

func qxProxyLine(proxy map[string]any) (string, bool) {
	flow := proxyStr(proxy, "flow")
	if flow != "" && flow != "xtls-rprx-vision" {
		return "", false
	}
	server, port := proxyStr(proxy, "server"), iniPort(proxy)
	name := iniName(proxy)
	var builder strings.Builder
	switch proxyStr(proxy, "type") {
	case "ss":
		cipher := proxyStr(proxy, "cipher")
		if !containsString(qxSSCiphers, cipher) {
			return "", false
		}
		builder.WriteString("shadowsocks=" + server + ":" + port + ",method=" + cipher + ",password=" + proxyStr(proxy, "password"))
		if proxyHas(proxy, "plugin") {
			if proxyStr(proxy, "plugin") != "obfs" {
				return "", false
			}
			opts, _ := proxy["plugin-opts"].(map[string]any)
			builder.WriteString(",obfs=" + mapStr(opts, "mode"))
			if host := mapStr(opts, "host"); host != "" {
				builder.WriteString(",obfs-host=" + host)
			}
			if path := mapStr(opts, "path"); path != "" {
				builder.WriteString(",obfs-uri=" + path)
			}
		}
		appendIfPresentBool(&builder, ",udp-relay=", proxy, "udp")
	case "trojan":
		builder.WriteString("trojan=" + server + ":" + port + ",password=" + proxyStr(proxy, "password"))
		if !qxObfsTransport(&builder, proxy) {
			return "", false
		}
		if proxyStr(proxy, "network") != "ws" && proxyBool(proxy, "tls") {
			builder.WriteString(",over-tls=true")
		}
		if proxyBool(proxy, "tls") {
			qxTLSVerification(&builder, proxy)
			if sni := proxySNI(proxy); sni != "" {
				builder.WriteString(",tls-host=" + sni)
			}
		}
		appendIfPresentBool(&builder, ",udp-relay=", proxy, "udp")
	case "vmess":
		// formatQXVmessMethod falls back to chacha20-poly1305 for "auto".
		builder.WriteString("vmess=" + server + ":" + port + ",method=chacha20-poly1305,password=" + proxyStr(proxy, "uuid"))
		if !qxObfsTransport(&builder, proxy) {
			return "", false
		}
		if proxyBool(proxy, "tls") {
			qxTLSVerification(&builder, proxy)
			if sni := proxySNI(proxy); sni != "" {
				builder.WriteString(",tls-host=" + sni)
			}
		}
		builder.WriteString(",aead=" + strconv.FormatBool(proxyInt(proxy, "alterId") == 0))
		appendIfPresentBool(&builder, ",udp-relay=", proxy, "udp")
	case "vless":
		if proxyStr(proxy, "encryption") != "" {
			return "", false
		}
		builder.WriteString("vless=" + server + ":" + port + ",method=none,password=" + proxyStr(proxy, "uuid"))
		if !qxObfsTransport(&builder, proxy) {
			return "", false
		}
		if proxyBool(proxy, "tls") {
			qxTLSVerification(&builder, proxy)
			if sni := proxySNI(proxy); sni != "" {
				builder.WriteString(",tls-host=" + sni)
			}
		}
		if flow != "" {
			builder.WriteString(",vless-flow=" + flow)
		}
		appendIfPresentBool(&builder, ",udp-relay=", proxy, "udp")
	case "anytls":
		builder.WriteString("anytls=" + server + ":" + port + ",password=" + proxyStr(proxy, "password") + ",over-tls=true")
		qxTLSVerification(&builder, proxy)
		if sni := proxySNI(proxy); sni != "" {
			builder.WriteString(",tls-host=" + sni)
		}
		appendIfPresentBool(&builder, ",udp-relay=", proxy, "udp")
	case "http":
		builder.WriteString("http=" + server + ":" + port)
		if username := proxyStr(proxy, "username"); username != "" {
			builder.WriteString(",username=" + username)
		}
		if password := proxyStr(proxy, "password"); password != "" {
			builder.WriteString(",password=" + password)
		}
		if proxyBool(proxy, "tls") {
			builder.WriteString(",over-tls=true")
			qxTLSVerification(&builder, proxy)
			if sni := proxySNI(proxy); sni != "" {
				builder.WriteString(",tls-host=" + sni)
			}
		}
		appendIfPresentBool(&builder, ",udp-relay=", proxy, "udp")
	case "socks5":
		builder.WriteString("socks5=" + server + ":" + port)
		if username := proxyStr(proxy, "username"); username != "" {
			builder.WriteString(",username=" + username)
		}
		if password := proxyStr(proxy, "password"); password != "" {
			builder.WriteString(",password=" + password)
		}
		if proxyBool(proxy, "tls") {
			builder.WriteString(",over-tls=true")
			qxTLSVerification(&builder, proxy)
			if sni := proxySNI(proxy); sni != "" {
				builder.WriteString(",tls-host=" + sni)
			}
		}
		appendIfPresentBool(&builder, ",udp-relay=", proxy, "udp")
	default:
		// hysteria2 / tuic / snell / mieru have no QX case.
		return "", false
	}
	builder.WriteString(",tag=" + name)
	if reality := realityOptsMap(proxy); reality != nil {
		if publicKey := mapStr(reality, "public-key"); publicKey != "" {
			builder.WriteString(",reality-base64-pubkey=" + publicKey)
		}
		if shortID := mapStr(reality, "short-id"); shortID != "" {
			builder.WriteString(",reality-hex-shortid=" + shortID)
		}
	}
	return builder.String(), true
}

// ---- Stash（stash.js） ----

var stashSSCiphers = []string{
	"aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "aes-128-cfb", "aes-192-cfb", "aes-256-cfb",
	"aes-128-ctr", "aes-192-ctr", "aes-256-ctr", "rc4-md5", "chacha20-ietf", "xchacha20",
	"chacha20-ietf-poly1305", "xchacha20-ietf-poly1305", "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm",
}

func stashKeepsProxy(proxy map[string]any) bool {
	proxyType := proxyStr(proxy, "type")
	if proxyType == "ss" && !containsString(stashSSCiphers, proxyStr(proxy, "cipher")) {
		return false
	}
	network := proxyStr(proxy, "network")
	if proxyType == "vless" && proxyHas(proxy, "reality-opts") && network != "" && network != "tcp" {
		return false
	}
	if proxyType == "anytls" && (network != "" && network != "tcp" || proxyHas(proxy, "reality-opts")) {
		return false
	}
	if proxyType != "vless" && network == "xhttp" {
		return false
	}
	return true
}

// stashNormalizeProxy applies stash.js's field rewrites to a clash proxy map.
func stashNormalizeProxy(proxy map[string]any) {
	switch proxyStr(proxy, "type") {
	case "tuic":
		if !proxyHas(proxy, "alpn") {
			proxy["alpn"] = []any{"h3"}
		}
		if !proxyHas(proxy, "version") {
			proxy["version"] = 5
		}
	case "hysteria2":
		if password := proxyStr(proxy, "password"); password != "" && !proxyHas(proxy, "auth") {
			proxy["auth"] = password
			delete(proxy, "password")
		}
		if down := proxyStr(proxy, "down"); down != "" {
			proxy["down-speed"] = iniDigits(down)
			delete(proxy, "down")
		}
		if up := proxyStr(proxy, "up"); up != "" {
			proxy["up-speed"] = iniDigits(up)
			delete(proxy, "up")
		}
	}
	switch proxyStr(proxy, "type") {
	case "trojan", "tuic", "hysteria2", "anytls":
		delete(proxy, "tls")
	}
}

func buildStashSubscription(snapshot subscriptionSnapshot, server string) ([]byte, error) {
	list := make([]any, 0)
	for _, proxy := range subscriptionProxies(snapshot, server) {
		if !stashKeepsProxy(proxy) {
			continue
		}
		stashNormalizeProxy(proxy)
		list = append(list, proxy)
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("此用户没有 Stash 可用节点")
	}
	return marshalYAML(map[string]any{"proxies": list})
}

// ---- Egern（egern.js） ----

var egernSSCiphers = []string{
	"chacha20-ietf-poly1305", "chacha20-poly1305", "aes-256-gcm", "aes-128-gcm", "none", "tbale",
	"rc4", "rc4-md5", "aes-128-cfb", "aes-192-cfb", "aes-256-cfb", "aes-128-ctr", "aes-192-ctr",
	"aes-256-ctr", "bf-cfb", "camellia-128-cfb", "camellia-192-cfb", "camellia-256-cfb", "cast5-cfb",
	"des-cfb", "idea-cfb", "rc2-cfb", "seed-cfb", "salsa20", "chacha20", "chacha20-ietf",
	"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm",
}

func egernReality(proxy map[string]any) map[string]any {
	reality := realityOptsMap(proxy)
	if reality == nil {
		return nil
	}
	out := map[string]any{}
	if publicKey := mapStr(reality, "public-key"); publicKey != "" {
		out["public_key"] = publicKey
	}
	if shortID := mapStr(reality, "short-id"); shortID != "" {
		out["short_id"] = shortID
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// egernTransport builds egern's nested transport object; empty branches are
// dropped exactly like egern.js's prune loop.
func egernTransport(proxy map[string]any, withReality bool) map[string]any {
	network := proxyStr(proxy, "network")
	tls := proxyBool(proxy, "tls")
	inner := map[string]any{}
	key := ""
	switch network {
	case "ws":
		key = "ws"
		if tls {
			key = "wss"
		}
		ws := map[string]any{}
		if path := mapStr(wsOptsMap(proxy), "path"); path != "" {
			ws["path"] = path
		}
		if headers, ok := wsOptsMap(proxy)["headers"].(map[string]any); ok {
			if host := mapStr(headers, "Host"); host != "" {
				ws["headers"] = map[string]any{"Host": host}
			}
		}
		if tls {
			if sni := proxySNI(proxy); sni != "" {
				ws["sni"] = sni
			}
			if proxyHas(proxy, "skip-cert-verify") {
				ws["skip_tls_verify"] = proxyBool(proxy, "skip-cert-verify")
			}
		}
		inner[key] = ws
	case "grpc":
		grpc := map[string]any{}
		if opts, ok := proxy["grpc-opts"].(map[string]any); ok {
			if service := mapStr(opts, "grpc-service-name"); service != "" {
				grpc["service_name"] = service
			}
		}
		if sni := proxySNI(proxy); sni != "" {
			grpc["sni"] = sni
		}
		// getGrpcTransport carries reality for vmess and vless alike.
		if reality := egernReality(proxy); reality != nil {
			grpc["reality"] = reality
		}
		if proxyHas(proxy, "skip-cert-verify") {
			grpc["skip_tls_verify"] = proxyBool(proxy, "skip-cert-verify")
		}
		inner["grpc"] = grpc
	case "", "tcp":
		if tls || (withReality && proxyHas(proxy, "reality-opts")) {
			key = "tcp"
			if tls {
				key = "tls"
			}
			entry := map[string]any{}
			if sni := proxySNI(proxy); sni != "" {
				entry["sni"] = sni
			}
			if proxyHas(proxy, "skip-cert-verify") {
				entry["skip_tls_verify"] = proxyBool(proxy, "skip-cert-verify")
			}
			if withReality {
				if reality := egernReality(proxy); reality != nil {
					entry["reality"] = reality
				}
			}
			inner[key] = entry
		}
	default:
		return nil
	}
	for transportKey, value := range inner {
		if entry, ok := value.(map[string]any); ok && transportKey != "grpc" && len(entry) == 0 {
			delete(inner, transportKey)
		}
	}
	if len(inner) == 0 {
		return nil
	}
	return inner
}

// egernProxyEntry converts one clash proxy map into egern's nested
// `{ <type>: { …snake_case fields… } }` item; ok=false skips the node.
func egernProxyEntry(proxy map[string]any) (map[string]any, bool) {
	proxyType := proxyStr(proxy, "type")
	network := proxyStr(proxy, "network")
	if proxyBool(proxy, "tls") && proxySNI(proxy) == "" {
		proxy["servername"] = proxyStr(proxy, "server")
	}
	fields := map[string]any{"name": proxyStr(proxy, "name"), "server": proxyStr(proxy, "server"), "port": proxyInt(proxy, "port")}
	egernType := proxyType
	switch proxyType {
	case "ss":
		cipher := proxyStr(proxy, "cipher")
		if !containsString(egernSSCiphers, cipher) {
			return nil, false
		}
		if proxyHas(proxy, "plugin") {
			if proxyStr(proxy, "plugin") != "obfs" {
				return nil, false
			}
			opts, _ := proxy["plugin-opts"].(map[string]any)
			if mode := mapStr(opts, "mode"); mode != "http" && mode != "tls" {
				return nil, false
			} else {
				fields["obfs"] = mode
				if host := mapStr(opts, "host"); host != "" {
					fields["obfs_host"] = host
				}
				if path := mapStr(opts, "path"); path != "" {
					fields["obfs_uri"] = path
				}
			}
		}
		if cipher == "chacha20-ietf-poly1305" {
			cipher = "chacha20-poly1305"
		}
		egernType = "shadowsocks"
		fields["method"] = cipher
		fields["password"] = proxyStr(proxy, "password")
		fields["tfo"] = false
		fields["udp_relay"] = proxyBool(proxy, "udp")
	case "vmess", "vless":
		if network != "" && !containsString([]string{"h2", "http", "ws", "tcp", "grpc"}, network) {
			return nil, false
		}
		if proxyType == "vless" {
			if proxyStr(proxy, "encryption") != "" {
				return nil, false
			}
			if flow := proxyStr(proxy, "flow"); flow != "" && flow != "xtls-rprx-vision" {
				return nil, false
			}
		}
		fields["user_id"] = proxyStr(proxy, "uuid")
		if proxyType == "vmess" {
			fields["security"] = "auto"
			fields["legacy"] = proxyInt(proxy, "alterId") != 0
		} else if cipher := proxyStr(proxy, "cipher"); cipher != "" {
			fields["security"] = cipher
		}
		fields["tfo"] = false
		if proxyHas(proxy, "udp") {
			fields["udp_relay"] = proxyBool(proxy, "udp")
		}
		if transport := egernTransport(proxy, proxyType == "vless"); transport != nil {
			fields["transport"] = transport
		}
		if proxyType == "vless" && (network == "" || network == "tcp") {
			if flow := proxyStr(proxy, "flow"); flow != "" {
				fields["flow"] = flow
			}
		}
	case "trojan":
		if network != "" && !containsString([]string{"http", "ws", "tcp"}, network) {
			return nil, false
		}
		fields["password"] = proxyStr(proxy, "password")
		fields["tfo"] = false
		if proxyHas(proxy, "udp") {
			fields["udp_relay"] = proxyBool(proxy, "udp")
		}
		if sni := proxySNI(proxy); sni != "" {
			fields["sni"] = sni
		}
		if proxyHas(proxy, "skip-cert-verify") {
			fields["skip_tls_verify"] = proxyBool(proxy, "skip-cert-verify")
		}
		if reality := egernReality(proxy); reality != nil {
			fields["reality"] = reality
		}
		if network == "ws" {
			ws := map[string]any{}
			if path := mapStr(wsOptsMap(proxy), "path"); path != "" {
				ws["path"] = path
			}
			if headers, ok := wsOptsMap(proxy)["headers"].(map[string]any); ok {
				if host := mapStr(headers, "Host"); host != "" {
					ws["host"] = host
				}
			}
			fields["websocket"] = ws
		}
	case "hysteria2":
		fields["auth"] = proxyStr(proxy, "password")
		if up := proxyStr(proxy, "up"); up != "" {
			bandwidth, _ := strconv.Atoi(iniDigits(up))
			fields["bandwidth"] = bandwidth
		}
		fields["tfo"] = false
		if sni := proxySNI(proxy); sni != "" {
			fields["sni"] = sni
		}
		if proxyHas(proxy, "skip-cert-verify") {
			fields["skip_tls_verify"] = proxyBool(proxy, "skip-cert-verify")
		}
		if proxyStr(proxy, "obfs-password") != "" && proxyStr(proxy, "obfs") == "salamander" {
			fields["obfs"] = "salamander"
			fields["obfs_password"] = proxyStr(proxy, "obfs-password")
		}
	case "tuic":
		fields["uuid"] = proxyStr(proxy, "uuid")
		fields["password"] = proxyStr(proxy, "password")
		if sni := proxySNI(proxy); sni != "" {
			fields["sni"] = sni
		}
		fields["alpn"] = []any{"h3"}
		if proxyHas(proxy, "skip-cert-verify") {
			fields["skip_tls_verify"] = proxyBool(proxy, "skip-cert-verify")
		}
	case "anytls":
		if network != "" && network != "tcp" {
			return nil, false
		}
		fields["password"] = proxyStr(proxy, "password")
		fields["tfo"] = false
		if sni := proxySNI(proxy); sni != "" {
			fields["sni"] = sni
		}
		if proxyHas(proxy, "skip-cert-verify") {
			fields["skip_tls_verify"] = proxyBool(proxy, "skip-cert-verify")
		}
		if reality := egernReality(proxy); reality != nil {
			fields["reality"] = reality
		}
	case "socks5", "mixed":
		if proxyBool(proxy, "tls") {
			egernType = "socks5_tls"
		} else {
			egernType = "socks5"
		}
		fields["username"] = proxyStr(proxy, "username")
		fields["password"] = proxyStr(proxy, "password")
		fields["tfo"] = false
		if proxyHas(proxy, "udp") {
			fields["udp_relay"] = proxyBool(proxy, "udp")
		}
		if proxyBool(proxy, "tls") {
			if sni := proxySNI(proxy); sni != "" {
				fields["sni"] = sni
			}
			if proxyHas(proxy, "skip-cert-verify") {
				fields["skip_tls_verify"] = proxyBool(proxy, "skip-cert-verify")
			}
			if reality := egernReality(proxy); reality != nil {
				fields["reality"] = reality
			}
		}
	case "http":
		if proxyBool(proxy, "tls") {
			egernType = "https"
		}
		fields["username"] = proxyStr(proxy, "username")
		fields["password"] = proxyStr(proxy, "password")
		fields["tfo"] = false
		if proxyBool(proxy, "tls") {
			if sni := proxySNI(proxy); sni != "" {
				fields["sni"] = sni
			}
			if proxyHas(proxy, "skip-cert-verify") {
				fields["skip_tls_verify"] = proxyBool(proxy, "skip-cert-verify")
			}
			if reality := egernReality(proxy); reality != nil {
				fields["reality"] = reality
			}
		}
	default:
		// mieru / snell / … are not in egern's whitelist.
		return nil, false
	}
	return map[string]any{egernType: fields}, true
}

func buildEgernSubscription(snapshot subscriptionSnapshot, server string) ([]byte, error) {
	list := make([]any, 0)
	for _, proxy := range subscriptionProxies(snapshot, server) {
		if entry, ok := egernProxyEntry(proxy); ok {
			list = append(list, entry)
		}
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("此用户没有 Egern 可用节点")
	}
	return marshalYAML(map[string]any{"proxies": list})
}
