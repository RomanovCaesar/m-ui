package app

import (
	"encoding/json"
	"fmt"
	"time"
)

// buildSingboxSubscription renders a complete, directly-importable sing-box
// configuration (targets sing-box >= 1.11) for a subscription user. It mirrors
// buildMihomoSubscription: every supported node becomes an outbound, a "PROXY"
// selector fans out to all of them, and the default route sends traffic through
// that selector. Protocols sing-box cannot express are skipped, just like
// buildMihomoSubscription drops the ones subscriptionProxy returns nil for.
func buildSingboxSubscription(snapshot subscriptionSnapshot, server string) ([]byte, error) {
	outbounds := make([]any, 0)
	tags := make([]string, 0)
	usedTags := map[string]int{}
	now := time.Now().UnixMilli()
	for _, item := range snapshot.Clients {
		if !item.Inbound.Enabled || clientDepleted(item.Client, now) {
			continue
		}
		itemServer := server
		if item.Server != "" {
			itemServer = item.Server
		}
		outbound := singboxOutbound(item.Inbound, item.Client, itemServer)
		if outbound == nil {
			continue
		}
		tag, _ := outbound["tag"].(string)
		usedTags[tag]++
		if usedTags[tag] > 1 {
			tag = fmt.Sprintf("%s %d", tag, usedTags[tag])
			outbound["tag"] = tag
		}
		outbounds = append(outbounds, outbound)
		tags = append(tags, tag)
	}
	if len(outbounds) == 0 {
		return nil, fmt.Errorf("此用户没有 sing-box 可用节点")
	}
	selectorTags := make([]any, 0, len(tags)+1)
	for _, tag := range tags {
		selectorTags = append(selectorTags, tag)
	}
	selectorTags = append(selectorTags, "direct")
	outbounds = append(outbounds,
		map[string]any{"type": "selector", "tag": "PROXY", "outbounds": selectorTags, "default": tags[0]},
		map[string]any{"type": "direct", "tag": "direct"},
	)
	config := map[string]any{
		"log": map[string]any{"level": "info"},
		"inbounds": []any{
			map[string]any{"type": "tun", "tag": "tun-in", "address": []any{"172.19.0.1/30"}, "auto_route": true, "strict_route": true, "stack": "mixed"},
			map[string]any{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 2080},
		},
		"outbounds": outbounds,
		"route": map[string]any{
			"rules": []any{
				map[string]any{"action": "sniff"},
				map[string]any{"protocol": "dns", "action": "hijack-dns"},
			},
			"final":                 "PROXY",
			"auto_detect_interface": true,
		},
	}
	return json.MarshalIndent(config, "", "  ")
}

// singboxOutbound translates one inbound+client pair into a sing-box outbound, or
// nil when sing-box cannot express the protocol/transport. Field names follow
// sub-store's sing-box producer; the unsupported cases mirror
// subscriptionUnsupported plus the mihomo-only transports sing-box lacks.
func singboxOutbound(inbound Inbound, client Client, server string) map[string]any {
	if subscriptionUnsupported(inbound) {
		return nil
	}
	// sing-box has no equivalent for these mihomo-only transports.
	if inbound.XHTTP.Enabled || inbound.Mekya.Enabled || inbound.MKCP.Enabled {
		return nil
	}
	out := map[string]any{"tag": subscriptionNodeName(inbound, client), "server": server, "server_port": inbound.Port}
	switch inbound.Type {
	case "shadowsocks":
		out["type"] = "shadowsocks"
		out["method"] = valueOr(inbound.Cipher, "2022-blake3-aes-256-gcm")
		out["password"] = client.Password
		if inbound.SimpleObfs.Enabled {
			opts := "obfs=" + valueOr(inbound.SimpleObfs.Mode, "http")
			if inbound.Host != "" {
				opts += ";obfs-host=" + inbound.Host
			}
			out["plugin"] = "obfs-local"
			out["plugin_opts"] = opts
		}
	case "vless":
		if inbound.Encryption != "" && inbound.Encryption != "none" {
			return nil // sing-box has no VLESS encryption layer
		}
		out["type"] = "vless"
		out["uuid"] = client.UUID
		tls := singboxTLSBlock(inbound, server, false)
		if tls != nil {
			out["tls"] = tls
		}
		if client.Flow != "" {
			if client.Flow != "xtls-rprx-vision" || tls == nil {
				return nil // sing-box only supports vision flow over TLS/REALITY
			}
			out["flow"] = client.Flow
		}
		singboxApplyTransport(out, inbound)
	case "vmess":
		out["type"] = "vmess"
		out["uuid"] = client.UUID
		out["security"] = "auto"
		out["alter_id"] = client.AlterID
		if tls := singboxTLSBlock(inbound, server, false); tls != nil {
			out["tls"] = tls
		}
		singboxApplyTransport(out, inbound)
	case "trojan":
		out["type"] = "trojan"
		out["password"] = client.Password
		out["tls"] = singboxTLSBlock(inbound, server, true)
		singboxApplyTransport(out, inbound)
	case "hysteria2":
		out["type"] = "hysteria2"
		out["password"] = client.Password
		if inbound.Hysteria.Obfs != "" {
			obfs := map[string]any{"type": inbound.Hysteria.Obfs}
			if inbound.Hysteria.ObfsPassword != "" {
				obfs["password"] = inbound.Hysteria.ObfsPassword
			}
			out["obfs"] = obfs
		}
		out["tls"] = singboxTLSBlock(inbound, server, true)
	case "tuic":
		out["type"] = "tuic"
		out["uuid"] = client.UUID
		out["password"] = client.Password
		out["udp_relay_mode"] = "native"
		if inbound.TUIC.CongestionController != "" && inbound.TUIC.CongestionController != "cubic" {
			out["congestion_control"] = inbound.TUIC.CongestionController
		}
		out["tls"] = singboxTLSBlock(inbound, server, true)
	case "anytls":
		out["type"] = "anytls"
		out["password"] = client.Password
		out["tls"] = singboxTLSBlock(inbound, server, true)
	case "socks", "mixed":
		out["type"] = "socks"
		out["version"] = "5"
		if user := subscriptionUsername(client); user != "" {
			out["username"] = user
			out["password"] = client.Password
		}
	case "http":
		out["type"] = "http"
		if user := subscriptionUsername(client); user != "" {
			out["username"] = user
			out["password"] = client.Password
		}
	default:
		return nil // mieru and anything else has no sing-box outbound
	}
	return out
}

// singboxTLSBlock builds a sing-box tls object. force is set for protocols that
// are always TLS (trojan/hysteria2/tuic/anytls); otherwise the block is emitted
// only when the inbound actually enables TLS or REALITY.
func singboxTLSBlock(inbound Inbound, server string, force bool) map[string]any {
	if !force && !inbound.TLS && !inbound.Reality.Enabled {
		return nil
	}
	tls := map[string]any{"enabled": true}
	sni := inbound.TLSServerName
	if inbound.Reality.Enabled {
		sni = firstListValue(inbound.Reality.ServerNames)
	}
	if sni == "" {
		sni = server
	}
	tls["server_name"] = sni
	if inbound.AllowInsecure {
		tls["insecure"] = true
	}
	if inbound.Reality.Enabled {
		reality := map[string]any{"enabled": true}
		if inbound.Reality.PublicKey != "" {
			reality["public_key"] = inbound.Reality.PublicKey
		}
		if sid := firstListValue(inbound.Reality.ShortIDs); sid != "" {
			reality["short_id"] = sid
		}
		tls["reality"] = reality
		tls["utls"] = map[string]any{"enabled": true, "fingerprint": valueOr(inbound.Reality.Fingerprint, "chrome")}
	}
	return tls
}

// singboxApplyTransport maps ws/grpc transports. XHTTP/Mekya/MKCP are filtered
// out before this is called, and plain TCP needs no transport object.
func singboxApplyTransport(out map[string]any, inbound Inbound) {
	switch {
	case inbound.WSPath != "":
		transport := map[string]any{"type": "ws", "path": inbound.WSPath}
		if inbound.Host != "" {
			transport["headers"] = map[string]any{"Host": inbound.Host}
		}
		out["transport"] = transport
	case inbound.GRPCServiceName != "":
		out["transport"] = map[string]any{"type": "grpc", "service_name": inbound.GRPCServiceName}
	}
}
