package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// marshalYAML emits a deterministic YAML document for the subset of values
// used by Mihomo configuration. Strings are double quoted through the JSON
// encoder; JSON string escaping is valid YAML 1.2 string escaping.
func marshalYAML(value any) ([]byte, error) {
	var builder strings.Builder
	if err := writeYAML(&builder, value, 0, false); err != nil {
		return nil, err
	}
	if !strings.HasSuffix(builder.String(), "\n") {
		builder.WriteByte('\n')
	}
	return []byte(builder.String()), nil
}

func writeYAML(builder *strings.Builder, value any, indent int, listItem bool) error {
	prefix := strings.Repeat("  ", indent)
	switch typed := value.(type) {
	case map[string]any:
		keys := orderedYAMLKeys(typed)
		for _, key := range keys {
			child := typed[key]
			builder.WriteString(prefix)
			builder.WriteString(yamlKey(key))
			builder.WriteByte(':')
			if isYAMLScalar(child) {
				builder.WriteByte(' ')
				builder.WriteString(yamlScalar(child))
				builder.WriteByte('\n')
			} else {
				builder.WriteByte('\n')
				if err := writeYAML(builder, child, indent+1, false); err != nil {
					return err
				}
			}
		}
	case []any:
		if len(typed) == 0 {
			if listItem {
				builder.WriteString("[]\n")
			} else {
				builder.WriteString(prefix + "[]\n")
			}
			return nil
		}
		for _, item := range typed {
			builder.WriteString(prefix + "-")
			if isYAMLScalar(item) {
				builder.WriteByte(' ')
				builder.WriteString(yamlScalar(item))
				builder.WriteByte('\n')
				continue
			}
			if mapping, ok := item.(map[string]any); ok {
				keys := orderedYAMLKeys(mapping)
				if len(keys) == 0 {
					builder.WriteString(" {}\n")
					continue
				}
				first := keys[0]
				builder.WriteByte(' ')
				builder.WriteString(yamlKey(first) + ":")
				if isYAMLScalar(mapping[first]) {
					builder.WriteByte(' ')
					builder.WriteString(yamlScalar(mapping[first]))
					builder.WriteByte('\n')
				} else {
					builder.WriteByte('\n')
					if err := writeYAML(builder, mapping[first], indent+2, false); err != nil {
						return err
					}
				}
				for _, key := range keys[1:] {
					builder.WriteString(strings.Repeat("  ", indent+1))
					builder.WriteString(yamlKey(key) + ":")
					if isYAMLScalar(mapping[key]) {
						builder.WriteByte(' ')
						builder.WriteString(yamlScalar(mapping[key]))
						builder.WriteByte('\n')
					} else {
						builder.WriteByte('\n')
						if err := writeYAML(builder, mapping[key], indent+2, false); err != nil {
							return err
						}
					}
				}
				continue
			}
			builder.WriteByte('\n')
			if err := writeYAML(builder, item, indent+1, true); err != nil {
				return err
			}
		}
	case []string:
		items := make([]any, len(typed))
		for index := range typed {
			items[index] = typed[index]
		}
		return writeYAML(builder, items, indent, listItem)
	case []map[string]any:
		items := make([]any, len(typed))
		for index := range typed {
			items[index] = typed[index]
		}
		return writeYAML(builder, items, indent, listItem)
	default:
		if !isYAMLScalar(value) {
			return fmt.Errorf("unsupported YAML type %T", value)
		}
		if !listItem {
			builder.WriteString(prefix)
		}
		builder.WriteString(yamlScalar(value))
		builder.WriteByte('\n')
	}
	return nil
}

func isYAMLScalar(value any) bool {
	switch typed := value.(type) {
	case nil, string, bool, int, int32, int64, uint, uint32, uint64, float32, float64:
		return true
	case []any:
		return len(typed) == 0
	case []string:
		return len(typed) == 0
	case []map[string]any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

func yamlScalar(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		encoded, _ := json.Marshal(typed)
		return string(encoded)
	case bool:
		return strconv.FormatBool(typed)
	case int:
		return strconv.Itoa(typed)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case uint:
		return strconv.FormatUint(uint64(typed), 10)
	case uint32:
		return strconv.FormatUint(uint64(typed), 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case []any, []string, []map[string]any:
		return "[]"
	case map[string]any:
		return "{}"
	default:
		return "null"
	}
}

func yamlKey(key string) string {
	for _, char := range key {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			continue
		}
		encoded, _ := json.Marshal(key)
		return string(encoded)
	}
	return key
}

var yamlKeyOrder = func() map[string]int {
	ordered := []string{
		// Top-level Mihomo configuration.
		"allow-lan", "bind-address", "mode", "log-level", "ipv6", "tcp-concurrent", "unified-delay", "external-controller", "secret", "external-controller-cors", "profile", "listeners", "proxies", "proxy-groups", "rules",
		// Listener identity and authentication.
		"name", "type", "listen", "port", "transport", "users", "username", "uuid", "password", "psk", "key", "flow", "alterId", "cipher", "udp", "decryption",
		// Listener transport and routing.
		"ws-path", "grpc-service-name", "xhttp-config", "mkcp-config", "mekya-config", "kcp-tun", "rule", "proxy", "routing-mark", "mux-option", "obfs-opts", "simple-obfs",
		// Mieru and Sudoku listener options.
		"traffic-pattern", "user-hint-is-mandatory", "aead-method", "table-type", "custom-table", "custom-tables", "padding-min", "padding-max", "handshake-timeout", "enable-pure-downlink", "httpmask", "fallback",
		// Hysteria2 realm rendezvous API options.
		"token", "max-realms", "max-realms-per-ip", "trusted-proxy-header", "realm-name-pattern", "realm-opts",
		// ShadowQuic and TrustTunnel listener options.
		"jls-upstream", "quic-versions", "zero-rtt", "max-datagram-frame-size", "recv-window-conn", "recv-window", "disable-mtu-discovery", "network",
		// TLS and alternative security wrappers.
		"certificate", "client-auth-type", "client-auth-cert", "ech-key", "allow-insecure", "reality-config", "tlsmirror-config", "shadow-tls", "res-tls", "jls-config", "ss-option",
		// Reality configuration.
		"dest", "private-key", "short-id", "server-names", "max-time-difference", "limit-fallback-upload", "limit-fallback-download",
		// XHTTP listener options.
		"x-padding-bytes", "x-padding-obfs-mode", "x-padding-key", "x-padding-header", "x-padding-placement", "x-padding-method", "uplink-http-method", "session-placement", "session-key", "seq-placement", "seq-key", "uplink-data-placement", "uplink-data-key", "uplink-chunk-size", "no-sse-header", "sc-stream-up-server-secs", "sc-max-buffered-posts", "sc-max-each-post-bytes", "mtu", "tti", "uplink-capacity", "downlink-capacity", "congestion", "write-buffer", "read-buffer", "seed", "header", "url", "h2-pool-size", "max-write-delay", "max-request-size", "polling-interval-initial", "max-write-size", "max-write-duration-ms", "max-simultaneous-write-connection", "packet-writing-buffer", "kcp",
		// Shadowsocks kcp-tun options that no other block uses.
		"crypt", "sndwnd", "rcvwnd", "datashard", "parityshard", "dscp", "ratelimit", "nocomp", "acknodelay", "nodelay", "interval", "resend", "nc", "sockbuf", "smuxver", "smuxbuf", "streambuf", "framesize", "keepalive",
		// Frequently nested switches and values.
		"enable", "enabled", "disable", "version", "handshake", "padding", "brutal", "up", "down", "host", "path", "path-root",
	}
	result := make(map[string]int, len(ordered))
	for index, key := range ordered {
		result[key] = index
	}
	return result
}()

// listenerYAMLOrder lays out one listener entry: identity, then the secret and
// its protocol options, then the transport wrappers, then the per-protocol
// tuning blocks, and finally the routing tail. It is built from an ordered slice
// so a new key can be slotted into its group without renumbering the rest.
var listenerYAMLOrder = func() map[string]int {
	ordered := []string{
		// Identity and the shared secret.
		"name", "type", "listen", "port", "transport", "network", "users", "psk", "key", "token", "version", "udp", "cipher", "decryption", "encryption",
		// Mieru.
		"traffic-pattern", "user-hint-is-mandatory",
		// Sudoku.
		"aead-method", "table-type", "custom-table", "custom-tables", "padding-min", "padding-max", "handshake-timeout", "enable-pure-downlink", "httpmask", "fallback",
		// Hysteria2 realm rendezvous API.
		"max-realms", "max-realms-per-ip", "trusted-proxy-header", "realm-name-pattern",
		// Stream transports.
		"ws-path", "grpc-service-name", "xhttp-config", "mkcp-config", "mekya-config", "kcp-tun",
		// TLS and the alternative security wrappers, which are mutually exclusive.
		"certificate", "private-key", "client-auth-type", "client-auth-cert", "ech-key", "allow-insecure", "reality-config", "tlsmirror-config", "shadow-tls", "res-tls", "jls-config", "jls-upstream", "ss-option",
		// Multiplexing and obfuscation.
		"mux-option", "obfs-opts", "simple-obfs",
		// Hysteria2.
		"up", "down", "ignore-client-bandwidth", "obfs", "obfs-password", "obfs-min-packet-size", "obfs-max-packet-size", "masquerade", "alpn", "udp-mtu", "realm-opts",
		// QUIC tuning shared by hysteria2, tuic, shadowquic and trusttunnel.
		"quic-versions", "zero-rtt", "congestion-controller", "cwnd", "bbr-profile", "initial-stream-receive-window", "max-stream-receive-window", "initial-connection-receive-window", "max-connection-receive-window", "recv-window-conn", "recv-window", "max-idle-time", "authentication-timeout", "max-udp-relay-packet-size", "max-datagram-frame-size", "disable-mtu-discovery", "padding-scheme",
		// Routing tail.
		"rule", "proxy", "routing-mark",
	}
	result := make(map[string]int, len(ordered))
	for index, key := range ordered {
		result[key] = index
	}
	return result
}()

var proxyYAMLOrder = func() map[string]int {
	ordered := []string{
		"name", "type", "server", "port", "username", "uuid", "password", "token", "alterId", "cipher", "flow", "encryption",
		"udp", "network", "ws-opts", "grpc-opts", "tls", "servername", "sni", "alpn", "skip-cert-verify", "client-fingerprint",
		"up", "down", "obfs", "obfs-password", "congestion-controller", "udp-relay-mode",
		"ip-version", "dialer-proxy", "interface-name", "routing-mark", "tfo", "mptcp",
		"ip", "ipv6", "private-key", "public-key", "pre-shared-key", "reserved", "persistent-keepalive", "workers", "mtu",
		"proto", "dev", "data-ciphers", "data-ciphers-fallback", "auth", "comp-lzo", "ca", "cert", "key", "tls-auth", "key-direction", "tls-crypt", "tls-crypt-v2", "ping", "ping-restart", "handshake-timeout", "remote-dns-resolve", "dns", "refresh-server-ip-interval",
	}
	result := make(map[string]int, len(ordered))
	for index, key := range ordered {
		result[key] = index
	}
	return result
}()

var proxyGroupYAMLOrder = func() map[string]int {
	ordered := []string{
		"name", "type", "proxies", "default-selected", "url", "interval", "lazy", "timeout", "tolerance", "strategy", "disable-udp",
	}
	result := make(map[string]int, len(ordered))
	for index, key := range ordered {
		result[key] = index
	}
	return result
}()

// tlsMirrorYAMLOrder lays out the vmess tlsmirror-config block. `primary-key` is
// the discriminator in orderedYAMLKeys — it is the only key of that name in the
// whole document and tlsMirrorConfigMap always writes it, because an empty key is
// how the kernel decides tlsmirror is off. Mihomo's own documentation lists `dest`
// first; the secret leads here to match every other block in this file.
//
// The nested defer-instance-derived-write-time map needs no entry of its own: its
// two keys are unknown to yamlKeyOrder and sort alphabetically into
// base-nanoseconds before uniform-random-multiplier-nanoseconds.
var tlsMirrorYAMLOrder = map[string]int{
	"primary-key": 0, "dest": 1, "proxy": 2, "explicit-nonce-ciphersuites": 3,
	"defer-instance-derived-write-time": 4, "transport-layer-padding": 5, "connection-enrolment": 6, "sequence-watermarking-enabled": 7,
}

// realmOptsYAMLOrder lays out the hysteria2 realm-opts block in the order
// listener/inbound/hysteria2.go declares it: the switch, the rendezvous identity,
// then the TLS settings that apply to server-url. `server-url` is the
// discriminator in orderedYAMLKeys and hysteriaRealmConfigMap always writes it.
var realmOptsYAMLOrder = map[string]int{
	"enable": 0, "server-url": 1, "token": 2, "realm-id": 3, "stun-servers": 4,
	"sni": 5, "skip-cert-verify": 6, "name-cert-verify": 7, "fingerprint": 8, "certificate": 9, "private-key": 10, "alpn": 11, "proxy": 12,
}

// jlsUpstreamYAMLOrder lays out the shadowquic jls-upstream block. `addr` is the
// discriminator in orderedYAMLKeys: it is the only nested map in the whole
// document that carries that key, and applyShadowQuicConfig always writes it
// because listener/shadowquic/server.go refuses to start without one.
var jlsUpstreamYAMLOrder = map[string]int{
	"addr": 0, "sni": 1, "proxy": 2, "rate-limit": 3,
}

// kcpTunYAMLOrder lays out the shadowsocks kcp-tun block: the shared secret and
// its cipher first, then the KCP tuning, then the smux session parameters. The
// `crypt` key doubles as the discriminator in orderedYAMLKeys because kcp-tun is
// the only block that carries it — which is also why kcpTunConfigMap always
// writes it out.
var kcpTunYAMLOrder = map[string]int{
	"enable": 0, "key": 1, "crypt": 2, "mode": 3,
	"mtu": 4, "sndwnd": 5, "rcvwnd": 6, "datashard": 7, "parityshard": 8, "dscp": 9, "ratelimit": 10,
	"nocomp": 11, "acknodelay": 12, "nodelay": 13, "interval": 14, "resend": 15, "nc": 16,
	"sockbuf": 17, "smuxver": 18, "smuxbuf": 19, "streambuf": 20, "framesize": 21, "keepalive": 22,
}

func orderedYAMLKeys(mapping map[string]any) []string {
	keys := make([]string, 0, len(mapping))
	for key := range mapping {
		keys = append(keys, key)
	}
	localOrder := yamlKeyOrder
	if _, reality := mapping["dest"]; reality {
		if _, hasPrivateKey := mapping["private-key"]; hasPrivateKey {
			localOrder = map[string]int{"dest": 0, "server-names": 1, "private-key": 2, "short-id": 3, "max-time-difference": 4, "limit-fallback-upload": 5, "limit-fallback-download": 6, "proxy": 7}
		}
	}
	if _, listener := mapping["name"]; listener {
		if _, hasType := mapping["type"]; hasType {
			if _, isProxy := mapping["server"]; isProxy {
				localOrder = proxyYAMLOrder
			} else if _, isProxyGroup := mapping["proxies"]; isProxyGroup {
				localOrder = proxyGroupYAMLOrder
			} else {
				localOrder = listenerYAMLOrder
			}
		}
	}
	// The sudoku httpmask object is the only nested map carrying these keys, so
	// they double as its discriminator.
	_, hasMaskDisable := mapping["disable"]
	_, hasMaskPathRoot := mapping["path-root"]
	if hasMaskDisable || hasMaskPathRoot {
		localOrder = map[string]int{"disable": 0, "mode": 1, "path-root": 2}
	}
	if _, kcpTun := mapping["crypt"]; kcpTun {
		localOrder = kcpTunYAMLOrder
	}
	if _, jlsUpstream := mapping["addr"]; jlsUpstream {
		localOrder = jlsUpstreamYAMLOrder
	}
	if _, tlsMirror := mapping["primary-key"]; tlsMirror {
		localOrder = tlsMirrorYAMLOrder
	}
	if _, realmOpts := mapping["server-url"]; realmOpts {
		localOrder = realmOptsYAMLOrder
	}
	sort.Slice(keys, func(i, j int) bool {
		left, leftKnown := localOrder[keys[i]]
		right, rightKnown := localOrder[keys[j]]
		if leftKnown != rightKnown {
			return leftKnown
		}
		if leftKnown && left != right {
			return left < right
		}
		return keys[i] < keys[j]
	})
	return keys
}
