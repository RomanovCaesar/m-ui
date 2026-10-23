package app

import (
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Converting Xray inbounds, outbounds and routing rules into Mihomo listeners,
// proxies and rules. Everything Mihomo cannot express is reported, never guessed.

type xrayImportLog struct {
	language string
	lines    []string
	warnings int
}

func (l *xrayImportLog) info(format string, args ...any) {
	l.lines = append(l.lines, "  "+xt(format, args...).render(l.language))
}

func (l *xrayImportLog) warn(format string, args ...any) {
	l.warnings++
	l.lines = append(l.lines, "  ! "+xt(format, args...).render(l.language))
}

func (l *xrayImportLog) section(title string) {
	l.lines = append(l.lines, "", "== "+xt(title).render(l.language))
}

// ---- inbounds ----

// xrayListener converts one inbound to a Mihomo listener without its users (they
// depend on the usernames chosen when importing). A non-empty skip reason means
// the inbound cannot be imported.
func xrayListener(in xrayImportInbound, log *xrayImportLog) (map[string]any, xrayText) {
	switch in.Protocol {
	case "tunnel", "dokodemo-door":
		return nil, xt("m-ui has no tunnel (dokodemo-door) inbound")
	case "wireguard":
		return nil, xt("Mihomo has no WireGuard server (listener)")
	case "tun":
		return nil, xt("TUN inbounds are not imported")
	case "mtproto", "amneziawg", "masque":
		return nil, xt("%s is a 3x-ui add-on protocol that Mihomo does not provide", in.Protocol)
	case "vless", "vmess", "trojan", "shadowsocks", "socks", "mixed", "http", "hysteria", "hysteria2", "tuic":
	default:
		return nil, xt("unknown protocol %q", in.Protocol)
	}
	port, ok := xrayPort(in.Port)
	if !ok {
		return nil, xt("port %v is not a single port (port ranges are not supported)", in.Port)
	}
	listen := in.Listen
	if strings.HasPrefix(listen, "/") || strings.HasPrefix(listen, "@") {
		return nil, xt("listens on a Unix socket (%s)", listen)
	}
	if listen == "" {
		listen = "0.0.0.0"
	}
	listener := map[string]any{"name": in.Tag, "listen": listen, "port": port}
	settings, stream := in.Settings, in.Stream
	if settings == nil {
		settings = map[string]any{}
	}
	if stream == nil {
		stream = map[string]any{}
	}

	switch in.Protocol {
	case "shadowsocks":
		listener["type"] = "shadowsocks"
		method := strings.ToLower(outboundStringValue(settings["method"]))
		password := outboundStringValue(settings["password"])
		clients := in.Clients
		if strings.HasPrefix(method, "2022-") {
			if len(clients) > 0 {
				return nil, xt("Shadowsocks 2022 with per-client keys (multi-user) is not supported by Mihomo listeners")
			}
		} else {
			switch len(clients) {
			case 0:
			case 1:
				password = valueOr(clients[0].Password, password)
				method = valueOr(strings.ToLower(clients[0].Method), method)
			default:
				return nil, xt("Shadowsocks with several clients on one port is not supported by Mihomo listeners")
			}
		}
		if method == "" || password == "" {
			return nil, xt("Shadowsocks without a method or password")
		}
		listener["cipher"], listener["password"] = method, password
		listener["udp"] = strings.Contains(strings.ToLower(valueOr(outboundStringValue(settings["network"]), "tcp,udp")), "udp")
		if err := xrayRejectTransport(stream, "tcp"); err.set() {
			return nil, err
		}
		return listener, xrayText{}
	case "socks", "mixed", "http":
		listener["type"] = in.Protocol
		if in.Protocol != "http" {
			listener["udp"] = boolValue(settings["udp"])
		}
		var users []any
		for _, account := range mapList(settings["accounts"]) {
			users = append(users, map[string]any{"username": outboundStringValue(account["user"]), "password": outboundStringValue(account["pass"])})
		}
		if len(users) > 0 {
			listener["users"] = users
		} else {
			log.warn("%s: no accounts, so the imported %s inbound accepts anyone who can reach port %d", in.Tag, in.Protocol, port)
		}
		if err := xrayRejectTransport(stream, "tcp"); err.set() {
			return nil, err
		}
		return listener, xrayText{}
	case "hysteria", "hysteria2":
		if version := intValue(settings["version"]); version != 0 && version != 2 {
			return nil, xt("Hysteria version %d (only version 2 is supported)", version)
		}
		listener["type"] = "hysteria2"
		if err := xrayListenerTLS(listener, stream, in.Tag, log, true); err.set() {
			return nil, err
		}
		hysteria := mapValue(stream["hysteriaSettings"])
		if up := outboundStringValue(hysteria["up"]); up != "" && up != "0" {
			listener["up"] = up
		}
		if down := outboundStringValue(hysteria["down"]); down != "" && down != "0" {
			listener["down"] = down
		}
		if masquerade := mapValue(hysteria["masquerade"]); masquerade != nil {
			switch strings.ToLower(outboundStringValue(masquerade["type"])) {
			case "proxy":
				listener["masquerade"] = outboundStringValue(masquerade["url"])
			case "file":
				listener["masquerade"] = "file://" + outboundStringValue(masquerade["dir"])
			case "":
			default:
				log.warn("%s: Hysteria masquerade type %q is not supported and was left out", in.Tag, outboundStringValue(masquerade["type"]))
			}
		}
		if password := xraySalamanderPassword(stream); password != "" {
			listener["obfs"], listener["obfs-password"] = "salamander", password
		}
		if alpn := stringList(mapValue(stream["tlsSettings"])["alpn"]); len(alpn) > 0 {
			listener["alpn"] = alpn
		}
		return listener, xrayText{}
	case "tuic":
		// 3x-ui 3.x runs TUIC outside Xray; its settings mirror tuic-server's.
		server := settings
		if nested := mapValue(settings["server"]); nested != nil {
			server = nested
		}
		listener["type"] = "tuic"
		listener["certificate"] = outboundStringValue(server["certificate"])
		listener["private-key"] = outboundStringValue(server["private_key"])
		if listener["certificate"] == "" || listener["private-key"] == "" {
			return nil, xt("TUIC without a certificate")
		}
		xrayCheckCertificateFiles(listener, in.Tag, log)
		if congestion := outboundStringValue(server["congestion_control"]); congestion != "" {
			listener["congestion-controller"] = congestion
		}
		if alpn := stringList(server["alpn"]); len(alpn) > 0 {
			listener["alpn"] = alpn
		}
		if size := intValue(server["max_udp_relay_packet_size"]); size > 0 {
			listener["max-udp-relay-packet-size"] = size
		}
		return listener, xrayText{}
	}

	// vless / vmess / trojan
	listener["type"] = in.Protocol
	network := xrayNetwork(stream)
	switch network {
	case "tcp":
		if header := mapValue(mapValue(firstNonNil(stream["rawSettings"], stream["tcpSettings"]))["header"]); strings.EqualFold(outboundStringValue(header["type"]), "http") {
			return nil, xt("TCP with HTTP header obfuscation is not supported by Mihomo listeners")
		}
	case "ws":
		ws := mapValue(stream["wsSettings"])
		listener["ws-path"] = valueOr(outboundStringValue(ws["path"]), "/")
		if host := valueOr(outboundStringValue(ws["host"]), outboundStringValue(mapValue(ws["headers"])["Host"])); host != "" {
			listener["host"] = host
		}
	case "grpc":
		grpc := mapValue(stream["grpcSettings"])
		listener["grpc-service-name"] = outboundStringValue(grpc["serviceName"])
		if boolValue(grpc["multiMode"]) {
			log.info("%s: gRPC multiMode is decided by the client in Mihomo; the setting was dropped", in.Tag)
		}
	case "xhttp":
		if in.Protocol != "vless" {
			return nil, xt("XHTTP is only supported for VLESS listeners in Mihomo")
		}
		listener["xhttp-config"] = xrayXHTTPConfig(firstMap(stream["xhttpSettings"], stream["splithttpSettings"]), in.Tag, log)
	case "mkcp":
		if in.Protocol != "vmess" {
			return nil, xt("mKCP is only supported for VMess listeners in Mihomo")
		}
		listener["mkcp-config"] = xrayMKCPConfig(mapValue(stream["kcpSettings"]))
	case "httpupgrade":
		return nil, xt("HTTPUpgrade transport is not supported by Mihomo listeners")
	default:
		return nil, xt("transport %q is not supported by Mihomo listeners", network)
	}
	if boolValue(mapValue(stream["sockopt"])["acceptProxyProtocol"]) || boolValue(mapValue(stream["wsSettings"])["acceptProxyProtocol"]) || boolValue(mapValue(firstNonNil(stream["rawSettings"], stream["tcpSettings"]))["acceptProxyProtocol"]) {
		log.warn("%s: PROXY protocol (acceptProxyProtocol) is not supported by Mihomo listeners; a front proxy that sends it will break connections", in.Tag)
	}
	switch strings.ToLower(outboundStringValue(stream["security"])) {
	case "", "none":
	case "tls":
		if err := xrayListenerTLS(listener, stream, in.Tag, log, false); err.set() {
			return nil, err
		}
	case "reality":
		if in.Protocol != "vless" && in.Protocol != "trojan" && in.Protocol != "vmess" {
			return nil, xt("Reality is not supported for this protocol")
		}
		reality := mapValue(stream["realitySettings"])
		config := map[string]any{
			"dest":         xrayRealityTarget(firstNonNil(reality["target"], reality["dest"])),
			"private-key":  outboundStringValue(reality["privateKey"]),
			"short-id":     stringList(reality["shortIds"]),
			"server-names": stringList(reality["serverNames"]),
		}
		if config["dest"] == "" || config["private-key"] == "" {
			return nil, xt("Reality without a target or private key")
		}
		listener["reality-config"] = config
		for _, key := range []string{"xver", "minClientVer", "maxClientVer", "maxTimeDiff", "mldsa65Seed"} {
			if value, ok := reality[key]; ok && outboundStringValue(value) != "" && outboundStringValue(value) != "0" {
				log.info("%s: Reality %s is not supported by Mihomo and was dropped", in.Tag, key)
			}
		}
	default:
		return nil, xt("security %q is not supported", outboundStringValue(stream["security"]))
	}
	if in.Protocol == "vless" {
		if decryption := outboundStringValue(settings["decryption"]); decryption != "" && decryption != "none" {
			listener["decryption"] = decryption
		}
	}
	if len(anyList(settings["fallbacks"])) > 0 || in.Fallbacks > 0 {
		log.warn("%s: fallbacks are not supported by Mihomo listeners and were dropped", in.Tag)
	}
	return listener, xrayText{}
}

func xrayPort(value any) (int, bool) {
	text := strings.TrimSpace(outboundStringValue(value))
	port, err := strconv.Atoi(text)
	if err != nil || port < 1 || port > 65535 {
		return 0, false
	}
	return port, true
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstMap(values ...any) map[string]any {
	for _, value := range values {
		if mapping := mapValue(value); mapping != nil {
			return mapping
		}
	}
	return map[string]any{}
}

// xrayNetwork normalises the transport names Xray accepts.
func xrayNetwork(stream map[string]any) string {
	switch network := strings.ToLower(valueOr(outboundStringValue(stream["network"]), "tcp")); network {
	case "raw", "tcp":
		return "tcp"
	case "xhttp", "splithttp":
		return "xhttp"
	case "kcp", "mkcp":
		return "mkcp"
	default:
		return network
	}
}

func xrayRejectTransport(stream map[string]any, allowed string) xrayText {
	if network := xrayNetwork(stream); network != allowed {
		return xt("transport %q is not supported for this protocol", network)
	}
	if security := strings.ToLower(outboundStringValue(stream["security"])); security != "" && security != "none" {
		return xt("security %q is not supported for this protocol", security)
	}
	return xrayText{}
}

// xrayListenerTLS copies the first certificate of tlsSettings. Files are kept as
// paths (they must exist on this server); inline certificates become PEM content.
func xrayListenerTLS(listener map[string]any, stream map[string]any, tag string, log *xrayImportLog, required bool) xrayText {
	tls := mapValue(stream["tlsSettings"])
	certificates := mapList(tls["certificates"])
	if len(certificates) == 0 {
		if required || strings.EqualFold(outboundStringValue(stream["security"]), "tls") {
			return xt("TLS without a certificate")
		}
		return xrayText{}
	}
	first := certificates[0]
	certificate := outboundStringValue(first["certificateFile"])
	key := outboundStringValue(first["keyFile"])
	if lines := stringListRaw(first["certificate"]); len(lines) > 0 {
		certificate = strings.Join(lines, "\n")
	}
	if lines := stringListRaw(first["key"]); len(lines) > 0 {
		key = strings.Join(lines, "\n")
	}
	if certificate == "" || key == "" {
		return xt("TLS certificate or key is missing")
	}
	listener["certificate"], listener["private-key"] = certificate, key
	if len(certificates) > 1 {
		log.info("%s: only the first of %d TLS certificates was imported", tag, len(certificates))
	}
	if len(stringList(tls["echServerKeys"])) > 0 || outboundStringValue(tls["echServerKeys"]) != "" {
		log.info("%s: ECH server keys were not imported", tag)
	}
	xrayCheckCertificateFiles(listener, tag, log)
	return xrayText{}
}

// stringListRaw keeps every line of a JSON string array (PEM lines must not be
// split on commas the way stringList splits plain strings).
func stringListRaw(value any) []string {
	if text, ok := value.(string); ok {
		if text = strings.TrimSpace(text); text == "" {
			return nil
		}
		return []string{text}
	}
	var result []string
	for _, item := range anyList(value) {
		result = append(result, outboundStringValue(item))
	}
	return result
}

func xrayCheckCertificateFiles(listener map[string]any, tag string, log *xrayImportLog) {
	for _, key := range []string{"certificate", "private-key"} {
		path := outboundStringValue(listener[key])
		if path == "" || strings.Contains(path, "-----BEGIN") {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			log.warn("%s: %s file %s does not exist on this server; copy it here (same path) before starting Mihomo", tag, key, path)
		}
	}
}

func xrayRealityTarget(value any) string {
	switch typed := value.(type) {
	case int64, float64, int:
		return "127.0.0.1:" + outboundStringValue(typed)
	}
	return outboundStringValue(value)
}

func xraySalamanderPassword(stream map[string]any) string {
	finalmask := mapValue(stream["finalmask"])
	for _, mask := range mapList(finalmask["udp"]) {
		if strings.EqualFold(outboundStringValue(mask["type"]), "salamander") {
			return outboundStringValue(mapValue(mask["settings"])["password"])
		}
	}
	return ""
}

func xrayRangeString(value any) string {
	if mapping := mapValue(value); mapping != nil {
		from, to := outboundStringValue(mapping["from"]), outboundStringValue(mapping["to"])
		if from != "" && to != "" && from != to {
			return from + "-" + to
		}
		return valueOr(from, to)
	}
	return outboundStringValue(value)
}

func xrayXHTTPConfig(xhttp map[string]any, tag string, log *xrayImportLog) map[string]any {
	config := map[string]any{"path": valueOr(outboundStringValue(xhttp["path"]), "/")}
	if host := outboundStringValue(xhttp["host"]); host != "" {
		config["host"] = host
	}
	if mode := outboundStringValue(xhttp["mode"]); mode != "" {
		config["mode"] = mode
	}
	renamed := map[string]string{
		"xPaddingBytes": "x-padding-bytes", "xPaddingKey": "x-padding-key", "xPaddingHeader": "x-padding-header",
		"xPaddingPlacement": "x-padding-placement", "xPaddingMethod": "x-padding-method", "uplinkHTTPMethod": "uplink-http-method",
		"sessionPlacement": "session-placement", "sessionKey": "session-key", "seqPlacement": "seq-placement", "seqKey": "seq-key",
		"uplinkDataPlacement": "uplink-data-placement", "uplinkDataKey": "uplink-data-key", "uplinkChunkSize": "uplink-chunk-size",
		"scStreamUpServerSecs": "sc-stream-up-server-secs", "scMaxBufferedPosts": "sc-max-buffered-posts", "scMaxEachPostBytes": "sc-max-each-post-bytes",
	}
	for from, to := range renamed {
		if text := xrayRangeString(xhttp[from]); text != "" && text != "0" {
			config[to] = text
		}
	}
	if boolValue(xhttp["xPaddingObfsMode"]) {
		config["x-padding-obfs-mode"] = true
	}
	if boolValue(xhttp["noSSEHeader"]) {
		config["no-sse-header"] = true
	}
	for _, key := range []string{"headers", "xmux", "downloadSettings", "extra", "noGRPCHeader", "scMinPostsIntervalMs", "serverMaxHeaderBytes"} {
		if value, ok := xhttp[key]; ok && value != nil && outboundStringValue(value) != "false" {
			if mapping := mapValue(value); mapping != nil && len(mapping) == 0 {
				continue
			}
			log.info("%s: XHTTP %s is not supported by Mihomo listeners and was dropped", tag, key)
		}
	}
	return config
}

func xrayMKCPConfig(kcp map[string]any) map[string]any {
	config := map[string]any{"enable": true}
	for from, to := range map[string]string{"mtu": "mtu", "tti": "tti", "uplinkCapacity": "uplink-capacity", "downlinkCapacity": "downlink-capacity", "readBufferSize": "read-buffer", "writeBufferSize": "write-buffer"} {
		if value := intValue(kcp[from]); value > 0 {
			config[to] = value
		}
	}
	if boolValue(kcp["congestion"]) {
		config["congestion"] = true
	}
	if seed := outboundStringValue(kcp["seed"]); seed != "" {
		config["seed"] = seed
	}
	if header := outboundStringValue(mapValue(kcp["header"])["type"]); header != "" && header != "none" {
		config["header"] = header
	}
	return config
}

// ---- outbounds ----

// xrayOutbound converts an outbound. builtin is DIRECT or REJECT for freedom and
// blackhole, which need no proxy entry. dialer names another outbound tag.
func xrayOutbound(out map[string]any, log *xrayImportLog) (proxy map[string]any, builtin, dialer string, skip xrayText) {
	tag := outboundStringValue(out["tag"])
	protocol := strings.ToLower(outboundStringValue(out["protocol"]))
	settings, stream := mapValue(out["settings"]), mapValue(out["streamSettings"])
	if settings == nil {
		settings = map[string]any{}
	}
	if stream == nil {
		stream = map[string]any{}
	}
	switch protocol {
	case "freedom", "direct":
		var dropped []string
		if strategy := outboundStringValue(settings["domainStrategy"]); strategy != "" && !strings.EqualFold(strategy, "AsIs") {
			dropped = append(dropped, "domainStrategy "+strategy)
		}
		for _, key := range []string{"redirect", "fragment", "noises", "finalRules"} {
			if value := settings[key]; value != nil && outboundStringValue(value) != "" || len(anyList(settings[key])) > 0 || len(mapValue(settings[key])) > 0 {
				dropped = append(dropped, key)
			}
		}
		if len(dropped) > 0 {
			log.info("%s: freedom outbound mapped to DIRECT; %s not supported", tag, strings.Join(dropped, ", "))
		}
		return nil, "DIRECT", "", xrayText{}
	case "blackhole", "block":
		return nil, "REJECT", "", xrayText{}
	case "dns":
		return nil, "", "", xt("DNS outbounds have no Mihomo proxy equivalent")
	case "loopback":
		return nil, "", "", xt("loopback outbounds have no Mihomo proxy equivalent")
	}

	server := map[string]any{}
	user := map[string]any{}
	if vnext := mapList(settings["vnext"]); len(vnext) > 0 {
		server = vnext[0]
		if users := mapList(server["users"]); len(users) > 0 {
			user = users[0]
		}
	} else if servers := mapList(settings["servers"]); len(servers) > 0 {
		server = servers[0]
		if users := mapList(server["users"]); len(users) > 0 {
			user = users[0]
		}
	} else {
		// Xray 26 also accepts the flat form: settings.address / port / id ...
		server, user = settings, settings
	}
	address, port := outboundStringValue(server["address"]), intValue(server["port"])
	proxy = map[string]any{"name": tag, "server": address, "port": port}

	switch protocol {
	case "vless":
		proxy["type"], proxy["uuid"] = "vless", firstString(user, "id", "uuid")
		if flow := outboundStringValue(user["flow"]); flow != "" {
			proxy["flow"] = strings.TrimSuffix(flow, "-udp443")
		}
		if encryption := outboundStringValue(user["encryption"]); encryption != "" && encryption != "none" {
			proxy["encryption"] = encryption
		}
		proxy["udp"] = true
	case "vmess":
		proxy["type"], proxy["uuid"] = "vmess", firstString(user, "id", "uuid")
		proxy["alterId"] = intValue(user["alterId"])
		proxy["cipher"] = valueOr(outboundStringValue(user["security"]), "auto")
		proxy["udp"] = true
	case "trojan":
		proxy["type"], proxy["password"] = "trojan", outboundStringValue(server["password"])
		proxy["udp"] = true
	case "shadowsocks":
		proxy["type"], proxy["cipher"], proxy["password"] = "ss", strings.ToLower(outboundStringValue(server["method"])), outboundStringValue(server["password"])
		proxy["udp"] = true
		if boolValue(server["uot"]) {
			proxy["udp-over-tcp"] = true
		}
	case "socks":
		proxy["type"] = "socks5"
		if name := outboundStringValue(user["user"]); name != "" {
			proxy["username"], proxy["password"] = name, outboundStringValue(user["pass"])
		}
		proxy["udp"] = true
	case "http":
		proxy["type"] = "http"
		if name := outboundStringValue(user["user"]); name != "" {
			proxy["username"], proxy["password"] = name, outboundStringValue(user["pass"])
		}
	case "hysteria":
		proxy["type"] = "hysteria2"
		hysteria := mapValue(stream["hysteriaSettings"])
		proxy["password"] = valueOr(outboundStringValue(hysteria["auth"]), outboundStringValue(settings["auth"]))
		if password := xraySalamanderPassword(stream); password != "" {
			proxy["obfs"], proxy["obfs-password"] = "salamander", password
		}
		if up := outboundStringValue(hysteria["up"]); up != "" && up != "0" {
			proxy["up"] = up
		}
		if down := outboundStringValue(hysteria["down"]); down != "" && down != "0" {
			proxy["down"] = down
		}
	case "wireguard":
		return xrayWireGuardOutbound(tag, settings, log)
	default:
		return nil, "", "", xt("unknown outbound protocol %q", protocol)
	}
	if address == "" || port <= 0 {
		return nil, "", "", xt("the outbound has no server address or port")
	}

	if protocol != "hysteria" {
		switch network := xrayNetwork(stream); network {
		case "tcp":
			if header := mapValue(mapValue(firstNonNil(stream["rawSettings"], stream["tcpSettings"]))["header"]); strings.EqualFold(outboundStringValue(header["type"]), "http") {
				return nil, "", "", xt("TCP with HTTP header obfuscation is not supported by Mihomo")
			}
		case "ws", "httpupgrade":
			ws := mapValue(stream["wsSettings"])
			if network == "httpupgrade" {
				ws = mapValue(stream["httpupgradeSettings"])
			}
			opts := map[string]any{"path": valueOr(outboundStringValue(ws["path"]), "/")}
			if host := valueOr(outboundStringValue(ws["host"]), outboundStringValue(mapValue(ws["headers"])["Host"])); host != "" {
				opts["headers"] = map[string]any{"Host": host}
			}
			if network == "httpupgrade" {
				opts["v2ray-http-upgrade"] = true
			}
			proxy["network"], proxy["ws-opts"] = "ws", opts
		case "grpc":
			proxy["network"] = "grpc"
			proxy["grpc-opts"] = map[string]any{"grpc-service-name": outboundStringValue(mapValue(stream["grpcSettings"])["serviceName"])}
		case "xhttp":
			xhttp := firstMap(stream["xhttpSettings"], stream["splithttpSettings"])
			opts := map[string]any{"path": valueOr(outboundStringValue(xhttp["path"]), "/")}
			if host := outboundStringValue(xhttp["host"]); host != "" {
				opts["host"] = host
			}
			if mode := outboundStringValue(xhttp["mode"]); mode != "" {
				opts["mode"] = mode
			}
			proxy["network"], proxy["xhttp-opts"] = "xhttp", opts
		default:
			return nil, "", "", xt("transport %q is not supported for Mihomo proxies", network)
		}
	}

	switch security := strings.ToLower(outboundStringValue(stream["security"])); security {
	case "", "none":
		if protocol == "trojan" || protocol == "hysteria" {
			proxy["tls"] = true
		}
	case "tls":
		tls := mapValue(stream["tlsSettings"])
		proxy["tls"] = true
		if name := outboundStringValue(tls["serverName"]); name != "" {
			proxy["servername"], proxy["sni"] = name, name
		}
		if alpn := stringList(tls["alpn"]); len(alpn) > 0 {
			proxy["alpn"] = alpn
		}
		if fingerprint := outboundStringValue(tls["fingerprint"]); fingerprint != "" {
			proxy["client-fingerprint"] = fingerprint
		}
		if boolValue(tls["allowInsecure"]) {
			proxy["skip-cert-verify"] = true
		}
	case "reality":
		reality := mapValue(stream["realitySettings"])
		proxy["tls"] = true
		if name := outboundStringValue(reality["serverName"]); name != "" {
			proxy["servername"], proxy["sni"] = name, name
		}
		proxy["reality-opts"] = map[string]any{
			"public-key": valueOr(outboundStringValue(reality["publicKey"]), outboundStringValue(reality["password"])),
			"short-id":   outboundStringValue(reality["shortId"]),
		}
		proxy["client-fingerprint"] = valueOr(outboundStringValue(reality["fingerprint"]), "chrome")
	default:
		return nil, "", "", xt("security %q is not supported", security)
	}
	// SNI only makes sense for the protocol the proxy type reads it from.
	if proxy["type"] == "vless" || proxy["type"] == "vmess" {
		delete(proxy, "sni")
	} else {
		delete(proxy, "servername")
	}
	if proxy["type"] == "http" || proxy["type"] == "socks5" || proxy["type"] == "ss" {
		delete(proxy, "client-fingerprint")
	}

	dialer = valueOr(outboundStringValue(mapValue(out["proxySettings"])["tag"]), outboundStringValue(mapValue(stream["sockopt"])["dialerProxy"]))
	if mux := mapValue(out["mux"]); boolValue(mux["enabled"]) {
		log.info("%s: Xray mux was not imported", tag)
	}
	return proxy, "", dialer, xrayText{}
}

func xrayWireGuardOutbound(tag string, settings map[string]any, log *xrayImportLog) (map[string]any, string, string, xrayText) {
	peers := mapList(settings["peers"])
	if len(peers) == 0 {
		return nil, "", "", xt("WireGuard outbound without peers")
	}
	peer := peers[0]
	host, portText, err := net.SplitHostPort(outboundStringValue(peer["endpoint"]))
	port, _ := strconv.Atoi(portText)
	if err != nil || port <= 0 {
		return nil, "", "", xt("WireGuard peer endpoint is not host:port")
	}
	proxy := map[string]any{
		"name": tag, "type": "wireguard", "server": host, "port": port,
		"private-key": outboundStringValue(settings["secretKey"]),
		"public-key":  outboundStringValue(peer["publicKey"]),
		"udp":         true,
	}
	if psk := outboundStringValue(peer["preSharedKey"]); psk != "" {
		proxy["pre-shared-key"] = psk
	}
	for _, address := range stringList(settings["address"]) {
		ip := strings.SplitN(address, "/", 2)[0]
		if parsed := net.ParseIP(ip); parsed != nil {
			if parsed.To4() != nil {
				if proxy["ip"] == nil {
					proxy["ip"] = ip
				}
			} else if proxy["ipv6"] == nil {
				proxy["ipv6"] = ip
			}
		}
	}
	if allowed := stringList(peer["allowedIPs"]); len(allowed) > 0 {
		proxy["allowed-ips"] = allowed
	}
	if reserved := intList(settings["reserved"]); len(reserved) == 3 {
		proxy["reserved"] = reserved
	}
	if mtu := intValue(settings["mtu"]); mtu > 0 {
		proxy["mtu"] = mtu
	}
	if len(peers) > 1 {
		log.info("%s: only the first of %d WireGuard peers was imported", tag, len(peers))
	}
	return proxy, "", "", xrayText{}
}

// ---- routing rules ----

type xrayRuleContext struct {
	inbound   func(tag string) (string, bool)
	target    func(tag string) (string, bool)
	balancer  func(tag string) (string, bool)
	usernames func(email string) []string
	noResolve bool
}

type xrayRuleLeaf struct {
	text string // "(TYPE,payload)" or a logical "(NOT,(...))"
	rule MihomoRoutingRule
}

func simpleLeaf(kind, payload string, noResolve bool) xrayRuleLeaf {
	suffix := ""
	if noResolve {
		suffix = ",no-resolve"
	}
	return xrayRuleLeaf{text: "(" + kind + "," + payload + suffix + ")", rule: MihomoRoutingRule{Type: kind, Payload: payload, NoResolve: noResolve}}
}

// xrayRule converts one Xray routing rule. A rule whose conditions span several
// fields becomes one AND rule; several values of one field are alternatives.
func xrayRule(raw map[string]any, ctx xrayRuleContext) ([]MihomoRoutingRule, xrayText) {
	var target string
	if tag := outboundStringValue(raw["outboundTag"]); tag != "" {
		name, ok := ctx.target(tag)
		if !ok {
			return nil, xt("its outbound %q was not imported", tag)
		}
		target = name
	} else if tag := outboundStringValue(raw["balancerTag"]); tag != "" {
		name, ok := ctx.balancer(tag)
		if !ok {
			return nil, xt("its balancer %q was not imported", tag)
		}
		target = name
	} else {
		return nil, xt("it has no outboundTag or balancerTag")
	}
	for _, key := range []string{"protocol", "attrs", "localIP", "vlessRoute", "process", "webhook"} {
		if value, ok := raw[key]; ok && value != nil && (outboundStringValue(value) != "" || len(anyList(value)) > 0 || len(mapValue(value)) > 0) {
			detail := strings.Join(stringListRaw(value), ",")
			if detail == "" {
				detail = outboundStringValue(value)
			}
			return nil, xt("Mihomo cannot match %q (%s)", key, detail)
		}
	}

	var fields [][]xrayRuleLeaf
	var dropped xrayList
	add := func(leaves []xrayRuleLeaf) {
		if len(leaves) > 0 {
			fields = append(fields, leaves)
		}
	}

	var domainLeaves []xrayRuleLeaf
	for _, value := range append(stringListRaw(raw["domain"]), stringListRaw(raw["domains"])...) {
		leaf, why := xrayDomainLeaf(value)
		if why.set() {
			dropped = append(dropped, xt("%s (%s)", value, why))
			continue
		}
		domainLeaves = append(domainLeaves, leaf)
	}
	if (len(anyList(raw["domain"])) > 0 || len(anyList(raw["domains"])) > 0) && len(domainLeaves) == 0 {
		return nil, xt("none of its domains can be expressed: %s", dropped)
	}
	add(domainLeaves)

	ipLeaves, ipDropped := xrayIPLeaves(stringListRaw(raw["ip"]), false, ctx.noResolve)
	dropped = append(dropped, ipDropped...)
	if len(anyList(raw["ip"])) > 0 && len(ipLeaves) == 0 {
		return nil, xt("none of its IPs can be expressed: %s", ipDropped)
	}
	add(ipLeaves)

	sources := append(stringListRaw(raw["source"]), stringListRaw(raw["sourceIP"])...)
	srcLeaves, srcDropped := xrayIPLeaves(sources, true, false)
	dropped = append(dropped, srcDropped...)
	if len(sources) > 0 && len(srcLeaves) == 0 {
		return nil, xt("none of its source IPs can be expressed")
	}
	add(srcLeaves)

	for field, kind := range map[string]string{"port": "DST-PORT", "sourcePort": "SRC-PORT", "localPort": "IN-PORT"} {
		if ports := xrayPorts(raw[field]); ports != "" {
			add([]xrayRuleLeaf{simpleLeaf(kind, ports, false)})
		}
	}

	if network := strings.ToLower(strings.ReplaceAll(outboundStringValue(raw["network"]), " ", "")); network != "" {
		parts := strings.Split(network, ",")
		if len(parts) == 1 && (parts[0] == "tcp" || parts[0] == "udp") {
			add([]xrayRuleLeaf{simpleLeaf("NETWORK", parts[0], false)})
		}
	}

	if tags := stringListRaw(raw["inboundTag"]); len(tags) > 0 {
		var names []string
		for _, tag := range tags {
			if name, ok := ctx.inbound(tag); ok {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			return nil, xt("none of its inbounds were imported")
		}
		add([]xrayRuleLeaf{simpleLeaf("IN-NAME", strings.Join(names, "/"), false)})
	}

	if emails := stringListRaw(raw["user"]); len(emails) > 0 {
		seen := map[string]bool{}
		var users []string
		for _, email := range emails {
			for _, username := range ctx.usernames(email) {
				if !seen[username] {
					seen[username] = true
					users = append(users, username)
				}
			}
		}
		if len(users) == 0 {
			return nil, xt("none of its users were imported")
		}
		add([]xrayRuleLeaf{simpleLeaf("IN-USER", strings.Join(users, "/"), false)})
	}

	if len(fields) == 0 {
		return nil, xt("it has no condition Mihomo can match")
	}
	var rules []MihomoRoutingRule
	if len(fields) == 1 {
		for _, leaf := range fields[0] {
			rule := leaf.rule
			if rule.Type == "" {
				// a NOT leaf: Mihomo needs it wrapped in a logical rule
				rule = MihomoRoutingRule{Type: "AND", Payload: "(" + leaf.text + ")"}
			}
			rule.Target = target
			rules = append(rules, rule)
		}
	} else {
		parts := make([]string, 0, len(fields))
		for _, leaves := range fields {
			if len(leaves) == 1 {
				parts = append(parts, leaves[0].text)
				continue
			}
			texts := make([]string, len(leaves))
			for index, leaf := range leaves {
				texts[index] = leaf.text
			}
			parts = append(parts, "(OR,("+strings.Join(texts, ",")+"))")
		}
		rules = append(rules, MihomoRoutingRule{Type: "AND", Payload: "(" + strings.Join(parts, ",") + ")", Target: target})
	}
	if len(dropped) > 0 {
		return rules, xt("partly imported; dropped %s", dropped)
	}
	return rules, xrayText{}
}

func xrayDomainLeaf(value string) (xrayRuleLeaf, xrayText) {
	value = strings.TrimSpace(value)
	prefix, payload := "", value
	if colon := strings.Index(value, ":"); colon > 0 {
		prefix, payload = strings.ToLower(value[:colon]), value[colon+1:]
	}
	if payload == "" {
		return xrayRuleLeaf{}, xt("empty")
	}
	switch prefix {
	case "geosite":
		if strings.Contains(payload, "@") {
			return xrayRuleLeaf{}, xt("geosite attributes are not supported")
		}
		return simpleLeaf("GEOSITE", payload, false), xrayText{}
	case "domain":
		return simpleLeaf("DOMAIN-SUFFIX", payload, false), xrayText{}
	case "full":
		return simpleLeaf("DOMAIN", payload, false), xrayText{}
	case "keyword":
		return simpleLeaf("DOMAIN-KEYWORD", payload, false), xrayText{}
	case "regexp":
		if strings.ContainsAny(payload, ",()") {
			return xrayRuleLeaf{}, xt("the regular expression contains a comma or a parenthesis")
		}
		return simpleLeaf("DOMAIN-REGEX", payload, false), xrayText{}
	case "ext", "dotless":
		return xrayRuleLeaf{}, xt("%s: matchers are not supported", prefix)
	case "":
		// A plain string is a substring match in Xray.
		return simpleLeaf("DOMAIN-KEYWORD", payload, false), xrayText{}
	default:
		return simpleLeaf("DOMAIN-KEYWORD", value, false), xrayText{}
	}
}

func xrayIPLeaves(values []string, source bool, noResolve bool) ([]xrayRuleLeaf, xrayList) {
	var leaves []xrayRuleLeaf
	var dropped xrayList
	geoKind, cidrKind := "GEOIP", "IP-CIDR"
	if source {
		geoKind, cidrKind = "SRC-GEOIP", "SRC-IP-CIDR"
		noResolve = false
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		lower := strings.ToLower(value)
		switch {
		case strings.HasPrefix(lower, "geoip:"):
			code := value[len("geoip:"):]
			negate := strings.HasPrefix(code, "!")
			code = strings.TrimPrefix(code, "!")
			if strings.EqualFold(code, "private") {
				code = "LAN"
			}
			leaf := simpleLeaf(geoKind, strings.ToUpper(code), noResolve)
			if negate {
				leaf = xrayRuleLeaf{text: "(NOT,(" + leaf.text + "))"}
			}
			leaves = append(leaves, leaf)
		case strings.HasPrefix(lower, "ext:") || strings.HasPrefix(lower, "ext-ip:"):
			dropped = append(dropped, xt("%s (external IP files are not supported)", value))
		default:
			cidr := value
			if !strings.Contains(cidr, "/") {
				if ip := net.ParseIP(cidr); ip != nil {
					if ip.To4() != nil {
						cidr += "/32"
					} else {
						cidr += "/128"
					}
				}
			}
			ip, _, err := net.ParseCIDR(cidr)
			if err != nil {
				dropped = append(dropped, xt("%s (not an IP or CIDR)", value))
				continue
			}
			kind := cidrKind
			if !source && ip.To4() == nil {
				kind = "IP-CIDR6"
			}
			leaves = append(leaves, simpleLeaf(kind, cidr, noResolve))
		}
	}
	return leaves, dropped
}

// xrayPorts turns "53,443,1000-2000" (or a number) into Mihomo's "53/443/1000-2000".
func xrayPorts(value any) string {
	text := strings.TrimSpace(outboundStringValue(value))
	if text == "" {
		return ""
	}
	var parts []string
	for _, part := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' }) {
		if err := validateMihomoPortSegment(part); err == nil {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "/")
}

// xrayBalancerGroup turns a balancer into a Mihomo group over the outbounds whose
// tag starts with one of its selector prefixes.
func xrayBalancerGroup(balancer map[string]any, tags []string, nameOf func(string) (string, bool)) (map[string]any, xrayText) {
	tag := outboundStringValue(balancer["tag"])
	var members []string
	seen := map[string]bool{}
	for _, prefix := range stringListRaw(balancer["selector"]) {
		for _, candidate := range tags {
			if strings.HasPrefix(candidate, prefix) {
				if name, ok := nameOf(candidate); ok && !seen[name] {
					seen[name] = true
					members = append(members, name)
				}
			}
		}
	}
	sort.Strings(members)
	if len(members) == 0 {
		return nil, xt("no imported outbound matches its selector")
	}
	// []any like decoded YAML/JSON: the outbound parser reads lists of that shape.
	proxies := make([]any, len(members))
	for index, member := range members {
		proxies[index] = member
	}
	group := map[string]any{"name": tag, "proxies": proxies}
	switch strings.ToLower(outboundStringValue(mapValue(balancer["strategy"])["type"])) {
	case "leastping", "leastload":
		group["type"], group["url"], group["interval"] = "url-test", "https://www.gstatic.com/generate_204", 300
	case "roundrobin":
		group["type"], group["strategy"] = "load-balance", "round-robin"
	default:
		group["type"], group["strategy"] = "load-balance", "consistent-hashing"
	}
	return group, xrayText{}
}

// xrayFlowAllowed mirrors 3x-ui's inboundCanEnableTlsFlow plus the 3.7+
// disable_flow switch: a client's flow only reaches Xray on a VLESS inbound
// over TCP with TLS or Reality, or with VLESS encryption.
func xrayFlowAllowed(in xrayImportInbound) bool {
	if in.Protocol != "vless" || in.DisableFlow {
		return false
	}
	security := strings.ToLower(outboundStringValue(in.Stream["security"]))
	if xrayNetwork(in.Stream) == "tcp" && (security == "tls" || security == "reality") {
		return true
	}
	decryption := outboundStringValue(in.Settings["decryption"])
	return decryption != "" && decryption != "none"
}
