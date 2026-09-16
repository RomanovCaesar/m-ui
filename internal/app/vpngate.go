package app

import (
	"fmt"
	"strings"
)

// VPNGate outbounds are managed `direct` proxies pinned to a SoftEther VPN
// Client adapter. Each saved outbound owns one slot: slot N always maps to the
// internal adapter name vpnN, the Linux interface vpn_vpnN and the policy
// routing table 100+N, so two outbounds can never share an interface.
const (
	vpnGateMaxSlot             = 9
	vpnGateTableBase           = 100
	vpnGateOutboundType        = "direct"
	vpnGateRouteProtocol       = "242"
	vpnGateLegacyRouteProtocol = "186"
	vpnGateGuardMetric         = "42760"
	vpnGateActiveMetric        = "10"
)

// VPNGateConfig is the managed part of a VPNGate outbound. It travels with the
// outbound through the settings draft, state.json and panel backups.
type VPNGateConfig struct {
	Country    string `json:"country"`
	ISP        string `json:"isp,omitempty"`
	ISPKeyword string `json:"ispKeyword,omitempty"`
	Slot       int    `json:"slot"`
}

type vpnGateISPPreset struct {
	ID      string
	Label   string
	Aliases []string
}

// Preset ISPs carry the ASN name fragments actually seen in IPinfo/ip-api
// answers, because the marketing name and the registered ASN name differ for
// most of these carriers.
var vpnGatePresets = map[string][]vpnGateISPPreset{
	"JP": {
		{ID: "kddi", Label: "KDDI", Aliases: []string{"kddi", "au one net", "au-net"}},
		{ID: "softbank", Label: "SoftBank", Aliases: []string{"softbank", "bbtec", "bb technology", "yahoo!bb"}},
		{ID: "sony", Label: "Sony", Aliases: []string{"sony network", "so-net", "sony corporation"}},
		{ID: "docomo", Label: "Docomo", Aliases: []string{"docomo", "ntt communications", "ntt docomo"}},
		{ID: "asahi", Label: "Asahi", Aliases: []string{"asahi net", "asahi-net", "asahinet"}},
		{ID: "chubu", Label: "Chubu", Aliases: []string{"chubu telecommunications", "ctc", "commufa"}},
	},
	"KR": {
		{ID: "kt", Label: "KT", Aliases: []string{"kixs", "korea telecom", "kt corporation"}},
		{ID: "sk", Label: "SK", Aliases: []string{"sk broadband", "sk telecom", "skb", "sktelecom"}},
		{ID: "lg", Label: "LG", Aliases: []string{"lg dacom", "lg powercomm", "lguplus", "lg u+", "lg hellovision"}},
	},
}

// Korea only ever offers the three presets; Japan and every other country also
// accept a free ASN keyword.
var vpnGateKeywordCountries = map[string]bool{"KR": false}

func vpnGateCountryAllowsKeyword(country string) bool {
	allowed, known := vpnGateKeywordCountries[country]
	if known {
		return allowed
	}
	return true
}

func findVPNGatePreset(country, id string) (vpnGateISPPreset, bool) {
	for _, preset := range vpnGatePresets[country] {
		if preset.ID == id {
			return preset, true
		}
	}
	return vpnGateISPPreset{}, false
}

// vpnGateAdapterName is the name handed to vpncmd's NicCreate.
func vpnGateAdapterName(slot int) string { return fmt.Sprintf("vpn%d", slot) }

// vpnGateInterfaceName is the Linux interface SoftEther creates for that adapter.
func vpnGateInterfaceName(slot int) string { return fmt.Sprintf("vpn_vpn%d", slot) }

// vpnGateRouteTable and vpnGateRoutingMark stay identical on purpose so a packet
// marked for a slot is always looked up in that slot's own table.
func vpnGateRouteTable(slot int) int  { return vpnGateTableBase + slot }
func vpnGateRoutingMark(slot int) int { return vpnGateTableBase + slot }

func vpnGateAccountName(slot int) string { return fmt.Sprintf("mui-vpngate-%d", slot) }

func vpnGateRouteField(line, key string) string {
	fields := strings.Fields(line)
	for index := 0; index+1 < len(fields); index++ {
		if fields[index] == key {
			return fields[index+1]
		}
	}
	return ""
}

// iproute2 prints protocol 186 as the symbolic name "bgp" on common Linux
// distributions. Accept that legacy spelling so routes created by v0.1.4-rc.1
// are recognised and migrated to the private protocol number used now.
func vpnGateOwnedRouteProtocol(line string) bool {
	value := strings.ToLower(vpnGateRouteField(line, "proto"))
	return value == vpnGateRouteProtocol || value == vpnGateLegacyRouteProtocol || value == "bgp"
}

func vpnGateManagedRoute(line, iface string) (guard, active bool) {
	line = strings.TrimSpace(line)
	if line == "" || !vpnGateOwnedRouteProtocol(line) {
		return false, false
	}
	// `ip -N route` renders RTN_UNREACHABLE as its numeric value 7 on some
	// iproute2 releases. Normal route reads below request symbolic output, but
	// accepting both forms also lets rc.2-created guards recover safely.
	guard = (strings.HasPrefix(line, "unreachable default") || strings.HasPrefix(line, "7 default")) && vpnGateRouteField(line, "metric") == vpnGateGuardMetric
	active = strings.HasPrefix(line, "default via ") && vpnGateRouteField(line, "dev") == iface && vpnGateRouteField(line, "metric") == vpnGateActiveMetric
	return guard, active
}

func vpnGateAccountConnected(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 || !strings.Contains(strings.ToLower(strings.TrimSpace(parts[0])), "status") {
			continue
		}
		value := strings.ToLower(strings.Join(strings.Fields(parts[1]), " "))
		if strings.Contains(value, "not connected") || strings.Contains(value, "disconnected") || strings.Contains(value, "offline") {
			continue
		}
		if value == "connected" || value == "online" || strings.Contains(value, "connection completed") || strings.Contains(value, "connection established") || strings.Contains(value, "session established") {
			return true
		}
	}
	return false
}

func isVPNGateCountryCode(value string) bool {
	if len(value) != 2 {
		return false
	}
	for index := 0; index < 2; index++ {
		if value[index] < 'A' || value[index] > 'Z' {
			return false
		}
	}
	return true
}

// normalizeVPNGateConfig validates the user's choices and rewrites them into
// their canonical form. It never trusts the keyword as anything but literal
// text: matching is a case-insensitive substring test, never a regexp or a
// shell fragment.
func normalizeVPNGateConfig(config *VPNGateConfig) error {
	config.Country = strings.ToUpper(strings.TrimSpace(config.Country))
	config.ISP = strings.ToLower(strings.TrimSpace(config.ISP))
	config.ISPKeyword = strings.TrimSpace(config.ISPKeyword)
	if !isVPNGateCountryCode(config.Country) {
		return fmt.Errorf("VPNGate 国家必须是两位大写字母代码")
	}
	if config.Slot < 0 || config.Slot > vpnGateMaxSlot {
		return fmt.Errorf("VPNGate 网卡序号必须是 0-%d", vpnGateMaxSlot)
	}
	if config.ISP != "" {
		if _, ok := findVPNGatePreset(config.Country, config.ISP); !ok {
			return fmt.Errorf("VPNGate 运营商 %q 不是 %s 的可选项", config.ISP, config.Country)
		}
		config.ISPKeyword = ""
		return nil
	}
	if config.ISPKeyword == "" {
		return fmt.Errorf("请选择 VPNGate 运营商或填写 ASN 名称关键词")
	}
	if !vpnGateCountryAllowsKeyword(config.Country) {
		return fmt.Errorf("%s 只能选择预设运营商", config.Country)
	}
	if len(config.ISPKeyword) > 64 || strings.ContainsAny(config.ISPKeyword, "\r\n") {
		return fmt.Errorf("VPNGate 运营商关键词不能包含换行，且不能超过 64 个字符")
	}
	return nil
}

// vpnGateMatchers returns the lowercase ASN name fragments that satisfy the
// configured operator.
func vpnGateMatchers(config VPNGateConfig) []string {
	if config.ISP != "" {
		if preset, ok := findVPNGatePreset(config.Country, config.ISP); ok {
			matchers := append([]string{preset.ID}, preset.Aliases...)
			matchers = append(matchers, strings.ToLower(preset.Label))
			return matchers
		}
		return nil
	}
	return []string{strings.ToLower(config.ISPKeyword)}
}

func vpnGateASNMatches(config VPNGateConfig, asnName string) bool {
	asnName = strings.ToLower(asnName)
	if asnName == "" {
		return false
	}
	for _, matcher := range vpnGateMatchers(config) {
		if matcher != "" && strings.Contains(asnName, matcher) {
			return true
		}
	}
	return false
}

// vpnGateISPLabel is what the UI shows for the configured operator.
func vpnGateISPLabel(config VPNGateConfig) string {
	if preset, ok := findVPNGatePreset(config.Country, config.ISP); ok {
		return preset.Label
	}
	return config.ISPKeyword
}

// applyVPNGateOutbound forces the managed shape of a VPNGate outbound. The
// outbound is a plain Mihomo `direct` proxy bound to the slot's interface and
// mark; Mihomo itself knows nothing about VPNGate.
func applyVPNGateOutbound(item *MihomoOutbound) error {
	if item.VPNGate == nil {
		return nil
	}
	if err := normalizeVPNGateConfig(item.VPNGate); err != nil {
		return err
	}
	if item.Kind != "group" {
		item.Kind = "proxy"
	}
	if item.Kind != "proxy" {
		return fmt.Errorf("VPNGate 出站不能是策略组")
	}
	// A YAML edit must not be able to drop the interface pinning and quietly
	// turn the outbound into an unbound DIRECT.
	item.Native = nil
	item.Type = vpnGateOutboundType
	item.Server, item.Port = "", 0
	item.DialerProxy = ""
	item.InterfaceName = vpnGateInterfaceName(item.VPNGate.Slot)
	item.RoutingMark = vpnGateRoutingMark(item.VPNGate.Slot)
	item.IPVersion = "ipv4"
	return nil
}

func findVPNGateOutbounds(items []MihomoOutbound) []MihomoOutbound {
	var result []MihomoOutbound
	for _, item := range items {
		if item.VPNGate != nil {
			result = append(result, item)
		}
	}
	return result
}
