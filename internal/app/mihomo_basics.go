package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

const basicsDirectName = "MUI-DIRECT"
const basicsIPv4Name = "MUI-IPV4"

type MihomoBasics struct {
	Mode                       string   `json:"mode"`
	DirectIPVersion            string   `json:"directIpVersion"`
	IPv6                       bool     `json:"ipv6"`
	TCPConcurrent              bool     `json:"tcpConcurrent"`
	UnifiedDelay               bool     `json:"unifiedDelay"`
	OutboundTestURL            string   `json:"outboundTestUrl"`
	TrafficSampleSeconds       int      `json:"trafficSampleSeconds"`
	TrafficSaveSeconds         int      `json:"trafficSaveSeconds"`
	OutboundUploadStatistics   bool     `json:"outboundUploadStatistics"`
	OutboundDownloadStatistics bool     `json:"outboundDownloadStatistics"`
	LogLevel                   string   `json:"logLevel"`
	LogBufferSize              int      `json:"logBufferSize"`
	MaskLogAddress             bool     `json:"maskLogAddress"`
	BlockIPs                   []string `json:"blockIps"`
	BlockDomains               []string `json:"blockDomains"`
	IPv4Domains                []string `json:"ipv4Domains"`
	WarpDomains                []string `json:"warpDomains"`
}

func defaultMihomoBasics() MihomoBasics {
	return MihomoBasics{Mode: "rule", DirectIPVersion: "dual", IPv6: true, OutboundTestURL: defaultMihomoTestURL,
		TrafficSampleSeconds: 1, TrafficSaveSeconds: 5, LogLevel: "info", LogBufferSize: 300,
		BlockIPs: []string{}, BlockDomains: []string{}, IPv4Domains: []string{}, WarpDomains: []string{}}
}

func effectiveMihomoBasics(state State) MihomoBasics {
	basics := defaultMihomoBasics()
	if state.MihomoBasics != nil {
		basics = *state.MihomoBasics
	}
	basics.DirectIPVersion = valueOr(basics.DirectIPVersion, "dual")
	basics.OutboundTestURL = valueOr(basics.OutboundTestURL, defaultMihomoTestURL)
	if basics.TrafficSampleSeconds <= 0 {
		basics.TrafficSampleSeconds = 1
	}
	if basics.TrafficSaveSeconds <= 0 {
		basics.TrafficSaveSeconds = 5
	}
	if basics.LogBufferSize <= 0 {
		basics.LogBufferSize = 300
	}
	// Keep the existing Panel Settings fields as the single authority for these
	// shared values so old clients and backups retain their behavior.
	basics.Mode = valueOr(state.Settings.Mode, "rule")
	basics.LogLevel = valueOr(state.Settings.LogLevel, "info")
	return basics
}

func normalizeMihomoBasics(input MihomoBasics) (MihomoBasics, error) {
	input.Mode = strings.ToLower(strings.TrimSpace(input.Mode))
	if !containsString([]string{"rule", "global", "direct"}, input.Mode) {
		return input, fmt.Errorf("Routing Mode 无效")
	}
	input.DirectIPVersion = strings.ToLower(strings.TrimSpace(input.DirectIPVersion))
	if !containsString([]string{"dual", "ipv4", "ipv6", "ipv4-prefer", "ipv6-prefer"}, input.DirectIPVersion) {
		return input, fmt.Errorf("Direct IP Version 无效")
	}
	input.OutboundTestURL = strings.TrimSpace(input.OutboundTestURL)
	if input.OutboundTestURL == "" {
		input.OutboundTestURL = defaultMihomoTestURL
	}
	parsed, err := url.Parse(input.OutboundTestURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || !containsString([]string{"http", "https"}, parsed.Scheme) {
		return input, fmt.Errorf("Outbound Test URL 必须是完整的 HTTP / HTTPS 地址")
	}
	if input.TrafficSampleSeconds < 1 || input.TrafficSampleSeconds > 10 {
		return input, fmt.Errorf("Traffic Sampling Interval 必须在 1-10 秒之间")
	}
	if input.TrafficSaveSeconds < 1 || input.TrafficSaveSeconds > 300 {
		return input, fmt.Errorf("Traffic Save Interval 必须在 1-300 秒之间")
	}
	input.LogLevel = strings.ToLower(strings.TrimSpace(input.LogLevel))
	if !containsString([]string{"silent", "error", "warning", "info", "debug"}, input.LogLevel) {
		return input, fmt.Errorf("Log Level 无效")
	}
	if input.LogBufferSize < 100 || input.LogBufferSize > 10000 {
		return input, fmt.Errorf("Log Buffer Size 必须在 100-10000 行之间")
	}
	input.BlockIPs, err = normalizeBasicsList(input.BlockIPs, true)
	if err != nil {
		return input, fmt.Errorf("Block IPs: %w", err)
	}
	input.BlockDomains, err = normalizeBasicsList(input.BlockDomains, false)
	if err != nil {
		return input, fmt.Errorf("Block Domains: %w", err)
	}
	input.IPv4Domains, err = normalizeBasicsList(input.IPv4Domains, false)
	if err != nil {
		return input, fmt.Errorf("IPv4 Routing: %w", err)
	}
	input.WarpDomains, err = normalizeBasicsList(input.WarpDomains, false)
	if err != nil {
		return input, fmt.Errorf("WARP Routing: %w", err)
	}
	return input, nil
}

func normalizeBasicsList(input []string, ips bool) ([]string, error) {
	if len(input) > 500 {
		return nil, fmt.Errorf("最多支持 500 项")
	}
	result := make([]string, 0, len(input))
	for _, value := range input {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(value) > 253 || strings.ContainsAny(value, ",\r\n\t ()") {
			return nil, fmt.Errorf("无效匹配项 %q", value)
		}
		if ips {
			if strings.EqualFold(value, "private") || strings.EqualFold(value, "geoip:private") || strings.EqualFold(value, "geoip:lan") {
				value = "private"
			} else if strings.HasPrefix(strings.ToLower(value), "geoip:") {
				code := strings.TrimPrefix(strings.ToLower(value), "geoip:")
				if len(code) != 2 || code[0] < 'a' || code[0] > 'z' || code[1] < 'a' || code[1] > 'z' {
					return nil, fmt.Errorf("GEOIP 需使用两位国家代码")
				}
				value = "geoip:" + strings.ToUpper(code)
			} else if ip := net.ParseIP(value); ip != nil {
				if ip.To4() != nil {
					value = ip.String() + "/32"
				} else {
					value = ip.String() + "/128"
				}
			} else if _, network, err := net.ParseCIDR(value); err == nil {
				value = network.String()
			} else {
				return nil, fmt.Errorf("%q 不是 IP、CIDR、private 或 geoip:国家代码", value)
			}
		} else {
			prefix, payload, hasPrefix := strings.Cut(value, ":")
			if hasPrefix {
				prefix = strings.ToLower(prefix)
				if !containsString([]string{"geosite", "domain", "full", "keyword"}, prefix) || payload == "" {
					return nil, fmt.Errorf("%q 不是域名或 geosite/domain/full/keyword 匹配项", value)
				}
				if prefix == "geosite" {
					if !regexp.MustCompile(`^[A-Za-z0-9_@!.-]+$`).MatchString(payload) {
						return nil, fmt.Errorf("Geosite 分类无效")
					}
				}
				if prefix == "domain" || prefix == "full" {
					if err := validatePanelDomain(payload); err != nil {
						return nil, err
					}
				}
				value = prefix + ":" + strings.ToLower(payload)
			} else {
				if err := validatePanelDomain(value); err != nil {
					return nil, err
				}
				value = strings.ToLower(value)
			}
		}
		if !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	return result, nil
}

func basicsDomainRule(value, target string) string {
	prefix, payload, ok := strings.Cut(value, ":")
	if !ok {
		return "DOMAIN-SUFFIX," + value + "," + target
	}
	types := map[string]string{"domain": "DOMAIN-SUFFIX", "full": "DOMAIN", "keyword": "DOMAIN-KEYWORD", "geosite": "GEOSITE"}
	return types[prefix] + "," + payload + "," + target
}

func compileMihomoBasics(basics MihomoBasics, outbounds []MihomoOutbound, rules []MihomoRoutingRule) ([]map[string]any, []map[string]any, []string, error) {
	if len(rules) == 0 {
		rules = defaultMihomoRoutingRules()
	}
	proxies, groups := mihomoOutboundConfig(outbounds)
	needsDirect := basics.DirectIPVersion != "dual"
	for _, item := range outbounds {
		if needsDirect && item.Name == basicsDirectName || len(basics.IPv4Domains) > 0 && item.Name == basicsIPv4Name {
			return nil, nil, nil, fmt.Errorf("名称 %s 已被 Basics 自动策略使用，请修改 Outbound 名称", item.Name)
		}
	}
	if needsDirect {
		proxies = append(proxies, map[string]any{"name": basicsDirectName, "type": "direct", "ip-version": basics.DirectIPVersion})
		for i, proxy := range proxies {
			if proxy["dialer-proxy"] == "DIRECT" {
				copy := maps.Clone(proxy)
				copy["dialer-proxy"] = basicsDirectName
				proxies[i] = copy
			}
		}
		for i, group := range groups {
			copy := maps.Clone(group)
			if values, ok := group["proxies"].([]any); ok {
				next := slices.Clone(values)
				for j, value := range next {
					if value == "DIRECT" {
						next[j] = basicsDirectName
					}
				}
				copy["proxies"] = next
			}
			if values, ok := group["proxies"].([]string); ok {
				next := slices.Clone(values)
				for j, value := range next {
					if value == "DIRECT" {
						next[j] = basicsDirectName
					}
				}
				copy["proxies"] = next
			}
			if copy["default-selected"] == "DIRECT" {
				copy["default-selected"] = basicsDirectName
			}
			groups[i] = copy
		}
		rules = slices.Clone(rules)
		for i := range rules {
			if rules[i].Target == "DIRECT" {
				rules[i].Target = basicsDirectName
			}
		}
	}
	managed := []string{}
	for _, value := range basics.BlockIPs {
		switch {
		case value == "private":
			managed = append(managed, "GEOIP,LAN,REJECT")
		case strings.HasPrefix(value, "geoip:"):
			managed = append(managed, "GEOIP,"+strings.TrimPrefix(value, "geoip:")+",REJECT")
		default:
			managed = append(managed, "IP-CIDR,"+value+",REJECT")
		}
	}
	for _, value := range basics.BlockDomains {
		managed = append(managed, basicsDomainRule(value, "REJECT"))
	}
	if len(basics.IPv4Domains) > 0 {
		proxies = append(proxies, map[string]any{"name": basicsIPv4Name, "type": "direct", "ip-version": "ipv4"})
		for _, value := range basics.IPv4Domains {
			managed = append(managed, basicsDomainRule(value, basicsIPv4Name))
		}
	}
	if len(basics.WarpDomains) > 0 {
		warp := findWarpOutbound(outbounds)
		if warp == nil {
			return nil, nil, nil, fmt.Errorf("WARP Routing 需要有效的 WARP WireGuard Outbound，请先添加或清空 WARP Routing")
		}
		for _, value := range basics.WarpDomains {
			managed = append(managed, basicsDomainRule(value, warp.Name))
		}
	}
	return proxies, groups, append(managed, mihomoRoutingConfig(rules)...), nil
}

var logIPCandidate = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b|(?:[0-9a-fA-F]{0,4}:){2,}[0-9a-fA-F:.]*`)

func maskLogAddresses(line string) string {
	return logIPCandidate.ReplaceAllStringFunc(line, func(value string) string {
		if net.ParseIP(value) != nil {
			return "[masked-ip]"
		}
		return value
	})
}

func (a *App) handleMihomoOutboundDelay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: "invalid outbound"})
		return
	}
	a.manager.mu.Lock()
	state := a.manager.state
	basics := effectiveMihomoBasics(state)
	running := a.manager.runningLocked()
	a.manager.mu.Unlock()
	valid := input.Name == "DIRECT"
	for _, item := range state.Outbounds {
		if item.Name == input.Name {
			valid = true
		}
	}
	if !valid {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr("Outbound 不存在或不支持测试")})
		return
	}
	if !running {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr("请先启动 Mihomo，并应用已保存的配置")})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 7*time.Second)
	defer cancel()
	query := url.Values{"url": {basics.OutboundTestURL}, "timeout": {"5000"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+state.Settings.APIAddress+"/proxies/"+url.PathEscape(input.Name)+"/delay?"+query.Encode(), nil)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
		return
	}
	request.Header.Set("Authorization", "Bearer "+state.Settings.APISecret)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, apiResponse{Message: a.tr("Outbound 延迟测试失败: " + err.Error())})
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusBadGateway, apiResponse{Message: a.tr("Outbound 不可用，或尚未应用到运行中的 Mihomo")})
		return
	}
	var result struct {
		Delay int `json:"delay"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result); err != nil {
		writeJSON(w, http.StatusBadGateway, apiResponse{Message: a.tr("Mihomo 返回了无效的延迟结果")})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: result})
}
