package app

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The consumer registration API and version match the bundled 3x-ui service.
const warpAPIBase = "https://api.cloudflareclient.com/v0a2158"
const warpOutboundName = "warp"

type WarpAccount struct {
	AccessToken string     `json:"accessToken"`
	DeviceID    string     `json:"deviceId"`
	LicenseKey  string     `json:"licenseKey"`
	PrivateKey  string     `json:"privateKey"`
	Device      WarpDevice `json:"device"`
}

type WarpDevice struct {
	ID      string           `json:"id"`
	Token   string           `json:"token,omitempty"`
	Name    string           `json:"name"`
	Model   string           `json:"model"`
	Enabled bool             `json:"enabled"`
	Account WarpAccountInfo  `json:"account"`
	Config  WarpTunnelConfig `json:"config"`
}

type WarpAccountInfo struct {
	License     string `json:"license"`
	AccountType string `json:"account_type"`
	Role        string `json:"role"`
	PremiumData uint64 `json:"premium_data"`
	Quota       uint64 `json:"quota"`
	Usage       uint64 `json:"usage"`
}

type WarpTunnelConfig struct {
	ClientID  string `json:"client_id"`
	Interface struct {
		Addresses struct {
			V4 string `json:"v4"`
			V6 string `json:"v6"`
		} `json:"addresses"`
	} `json:"interface"`
	Peers []struct {
		PublicKey string `json:"public_key"`
		Endpoint  struct {
			Host string `json:"host"`
			V4   string `json:"v4"`
			V6   string `json:"v6"`
		} `json:"endpoint"`
	} `json:"peers"`
}

type warpView struct {
	Account     *WarpAccount    `json:"account"`
	Outbound    *MihomoOutbound `json:"outbound,omitempty"`
	ConfigError string          `json:"configError,omitempty"`
}

type warpClient struct {
	baseURL    string
	httpClient *http.Client
}

func (m *CoreManager) cloudflareWarpClient() *warpClient {
	if m.warpClient != nil {
		return m.warpClient
	}
	return &warpClient{baseURL: warpAPIBase, httpClient: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *warpClient) request(ctx context.Context, method, path, token string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("无法构造 WARP 请求")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("CF-Client-Version", "a-7.21-0721")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("无法连接 Cloudflare WARP，请检查服务器网络或稍后重试")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Cloudflare WARP 请求失败 (HTTP %d)", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return fmt.Errorf("Cloudflare WARP 响应读取失败或过大")
	}
	if len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
		return fmt.Errorf("Cloudflare WARP 返回了无效的对象")
	}
	var envelope struct {
		Success *bool             `json:"success"`
		Errors  []json.RawMessage `json:"errors"`
		Result  json.RawMessage   `json:"result"`
	}
	if err = json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("Cloudflare WARP 返回了无效的 JSON")
	}
	if envelope.Success != nil && !*envelope.Success || len(envelope.Errors) > 0 {
		return fmt.Errorf("Cloudflare WARP 拒绝了请求，请检查账户或 License Key")
	}
	if len(envelope.Result) > 0 && string(envelope.Result) != "null" {
		data = envelope.Result
	}
	if output != nil {
		if err = json.Unmarshal(data, output); err != nil {
			return fmt.Errorf("Cloudflare WARP 响应格式不符合预期")
		}
	}
	return nil
}

var warpDeviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,80}$`)
var warpLicensePattern = regexp.MustCompile(`^[A-Za-z0-9]{8}-[A-Za-z0-9]{8}-[A-Za-z0-9]{8}$`)

func validateWarpAccount(account *WarpAccount) error {
	if account == nil {
		return nil
	}
	if !warpDeviceIDPattern.MatchString(account.DeviceID) || account.AccessToken == "" || len(account.AccessToken) > 4096 || strings.ContainsAny(account.AccessToken, "\r\n") {
		return fmt.Errorf("WARP 账户缺少有效的 Device ID / Access Token")
	}
	if _, err := decodeWarpKey(account.PrivateKey); err != nil {
		return fmt.Errorf("WARP Private Key 无效")
	}
	return nil
}

func decodeWarpKey(value string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(data) != 32 {
		return nil, fmt.Errorf("WireGuard 密钥必须是 32 字节 Base64")
	}
	return data, nil
}

func (m *CoreManager) warpSnapshot() *WarpAccount {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.WARP
}

func (m *CoreManager) saveWarpAccount(account *WarpAccount) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := m.state.WARP
	m.state.WARP = account
	if err := m.saveLocked(); err != nil {
		m.state.WARP = previous
		return fmt.Errorf("WARP 账户保存失败: %w", err)
	}
	return nil
}

func (m *CoreManager) createWarpAccount(ctx context.Context) (*WarpAccount, error) {
	m.warpMu.Lock()
	defer m.warpMu.Unlock()
	if existing := m.warpSnapshot(); existing != nil {
		return existing, nil
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	input := map[string]any{"key": base64.StdEncoding.EncodeToString(key.PublicKey().Bytes()), "tos": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), "type": "PC", "model": "m-ui", "name": "m-ui"}
	var device WarpDevice
	if err = m.cloudflareWarpClient().request(ctx, http.MethodPost, "/reg", "", input, &device); err != nil {
		return nil, err
	}
	account := &WarpAccount{DeviceID: device.ID, AccessToken: device.Token, LicenseKey: device.Account.License, PrivateKey: base64.StdEncoding.EncodeToString(key.Bytes()), Device: device}
	account.Device.Token = ""
	if err = validateWarpAccount(account); err != nil {
		return nil, err
	}
	// Keep valid credentials even when CF has not returned a usable tunnel yet;
	// More Information can retry fetching it without registering another device.
	if err = m.saveWarpAccount(account); err != nil {
		return nil, err
	}
	return account, nil
}

func (m *CoreManager) refreshWarpAccount(ctx context.Context) (*WarpAccount, error) {
	m.warpMu.Lock()
	defer m.warpMu.Unlock()
	current := m.warpSnapshot()
	if current == nil {
		return nil, fmt.Errorf("请先创建 WARP 账户")
	}
	if err := validateWarpAccount(current); err != nil {
		return nil, err
	}
	var device WarpDevice
	if err := m.cloudflareWarpClient().request(ctx, http.MethodGet, "/reg/"+url.PathEscape(current.DeviceID), current.AccessToken, nil, &device); err != nil {
		return nil, err
	}
	if device.ID != current.DeviceID {
		return nil, fmt.Errorf("Cloudflare 返回的 Device ID 不一致")
	}
	next := *current
	next.Device = device
	next.Device.Token = ""
	if device.Account.License != "" {
		next.LicenseKey = device.Account.License
	}
	if err := m.saveWarpAccount(&next); err != nil {
		return nil, err
	}
	return &next, nil
}

func (m *CoreManager) updateWarpLicense(ctx context.Context, license string) (*WarpAccount, error) {
	license = strings.TrimSpace(license)
	if !warpLicensePattern.MatchString(license) {
		return nil, fmt.Errorf("License Key 格式应为 XXXXXXXX-XXXXXXXX-XXXXXXXX")
	}
	m.warpMu.Lock()
	defer m.warpMu.Unlock()
	current := m.warpSnapshot()
	if current == nil {
		return nil, fmt.Errorf("请先创建 WARP 账户")
	}
	if err := validateWarpAccount(current); err != nil {
		return nil, err
	}
	var info WarpAccountInfo
	if err := m.cloudflareWarpClient().request(ctx, http.MethodPut, "/reg/"+url.PathEscape(current.DeviceID)+"/account", current.AccessToken, map[string]string{"license": license}, &info); err != nil {
		return nil, err
	}
	next := *current
	next.LicenseKey = license
	if info.AccountType != "" {
		next.Device.Account = info
	}
	next.Device.Account.License = license
	if err := m.saveWarpAccount(&next); err != nil {
		return nil, err
	}
	return &next, nil
}

func (m *CoreManager) deleteWarpAccount() error {
	m.warpMu.Lock()
	defer m.warpMu.Unlock()
	// Match 3x-ui: forget local credentials, never revoke the remote device.
	return m.saveWarpAccount(nil)
}

func warpAddress(value string, ipv4 bool) (string, error) {
	if value == "" {
		return "", nil
	}
	address, err := netip.ParseAddr(value)
	if err != nil {
		if prefix, prefixErr := netip.ParsePrefix(value); prefixErr == nil {
			address = prefix.Addr()
			err = nil
		}
	}
	if err != nil || address.Is4() != ipv4 {
		return "", fmt.Errorf("Cloudflare 返回了无效的隧道 IP 地址")
	}
	return address.String(), nil
}

func warpOutbound(account *WarpAccount) (MihomoOutbound, error) {
	if account == nil {
		return MihomoOutbound{}, fmt.Errorf("请先创建 WARP 账户")
	}
	if err := validateWarpAccount(account); err != nil {
		return MihomoOutbound{}, err
	}
	config := account.Device.Config
	if len(config.Peers) == 0 {
		return MihomoOutbound{}, fmt.Errorf("Cloudflare 尚未返回 WireGuard peer，请点击 More Information 重试")
	}
	peer := config.Peers[0]
	if _, err := decodeWarpKey(peer.PublicKey); err != nil {
		return MihomoOutbound{}, fmt.Errorf("Cloudflare 返回的 WireGuard Public Key 无效")
	}
	endpoint := valueOr(peer.Endpoint.Host, valueOr(peer.Endpoint.V4, peer.Endpoint.V6))
	host, portText, err := net.SplitHostPort(endpoint)
	port, portErr := strconv.Atoi(portText)
	if err != nil || portErr != nil || port < 1 || port > 65535 || host == "" || strings.ContainsAny(host, "/\r\n \t") {
		return MihomoOutbound{}, fmt.Errorf("Cloudflare 返回的 WireGuard Endpoint 无效")
	}
	reserved, err := base64.StdEncoding.DecodeString(config.ClientID)
	if err != nil || len(reserved) != 3 {
		return MihomoOutbound{}, fmt.Errorf("Cloudflare 返回的 client_id 无效，应为 3 字节 Base64")
	}
	v4, err := warpAddress(config.Interface.Addresses.V4, true)
	if err != nil {
		return MihomoOutbound{}, err
	}
	v6, err := warpAddress(config.Interface.Addresses.V6, false)
	if err != nil {
		return MihomoOutbound{}, err
	}
	if v4 == "" && v6 == "" {
		return MihomoOutbound{}, fmt.Errorf("Cloudflare 尚未返回隧道 IP 地址")
	}
	native := map[string]any{"name": warpOutboundName, "type": "wireguard", "server": host, "port": port, "private-key": account.PrivateKey, "public-key": peer.PublicKey, "reserved": []int{int(reserved[0]), int(reserved[1]), int(reserved[2])}, "mtu": 1420, "udp": true}
	if v4 != "" {
		native["ip"] = v4
	}
	if v6 != "" {
		native["ipv6"] = v6
	}
	item, err := nativeOutbound(native)
	if err != nil {
		return item, err
	}
	item.WarpDeviceID = account.DeviceID
	return item, nil
}

func findWarpOutbound(items []MihomoOutbound) *MihomoOutbound {
	for i := range items {
		if items[i].Type == "wireguard" && items[i].WarpDeviceID != "" {
			return &items[i]
		}
	}
	for i := range items {
		if items[i].Type == "wireguard" && items[i].Name == warpOutboundName {
			return &items[i]
		}
	}
	return nil
}

func makeWarpView(account *WarpAccount) warpView {
	view := warpView{Account: account}
	if account != nil {
		item, err := warpOutbound(account)
		if err != nil {
			view.ConfigError = err.Error()
		} else {
			view.Outbound = &item
		}
	}
	return view
}

func (a *App) handleWarp(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	action := strings.TrimPrefix(r.URL.Path, "/api/mihomo/warp")
	var account *WarpAccount
	var err error
	switch {
	case action == "" && r.Method == http.MethodGet:
		account = a.manager.warpSnapshot()
	case action == "/create" && r.Method == http.MethodPost:
		account, err = a.manager.createWarpAccount(r.Context())
	case action == "/refresh" && r.Method == http.MethodPost:
		account, err = a.manager.refreshWarpAccount(r.Context())
	case action == "/license" && r.Method == http.MethodPost:
		var input struct {
			License string `json:"license"`
		}
		if err = decodeJSON(r, &input); err == nil {
			account, err = a.manager.updateWarpLicense(r.Context(), input.License)
		}
	case action == "" && r.Method == http.MethodDelete:
		err = a.manager.deleteWarpAccount()
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: makeWarpView(account)})
}
