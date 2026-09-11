package app

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const subscriptionTokenAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

const subscriptionPathPrefix = "/sub"

type subscriptionNode struct {
	Name string
	URL  string
}

type subscriptionPageData struct {
	Token           string
	Username        string
	SubscriptionURL string
	ClashURL        string
	ClashEnabled    bool
	ClientToggles   string
	StaticURL       string
	DefaultLanguage string
	Status          string
	Downloaded      string
	Uploaded        string
	Usage           string
	Total           string
	LastOnline      string
	Expiry          string
	Nodes           []subscriptionNode
}

type subscriptionSnapshot struct {
	Settings Settings
	Inbounds []Inbound
	Username string
	Token    string
	Clients  []subscriptionClient
}

type subscriptionClient struct {
	Inbound Inbound
	Client  Client
	Server  string
}

func allStateClients(inbounds []Inbound) []Client {
	clients := make([]Client, 0)
	for _, inbound := range inbounds {
		clients = append(clients, inbound.Clients...)
	}
	return clients
}

func subscriptionUsername(client Client) string {
	return strings.TrimSpace(valueOr(client.Username, client.Name))
}

func randomSubscriptionToken() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	for index := range bytes {
		bytes[index] = subscriptionTokenAlphabet[int(bytes[index])%len(subscriptionTokenAlphabet)]
	}
	return string(bytes), nil
}

func validSubscriptionToken(token string) bool {
	if len(token) != 16 {
		return false
	}
	for _, letter := range token {
		if !strings.ContainsRune(subscriptionTokenAlphabet, letter) {
			return false
		}
	}
	return true
}

func randomSubscriptionPath() (string, error) {
	for {
		token, err := randomSubscriptionToken()
		if err != nil {
			return "", err
		}
		hasLetter := false
		hasDigit := false
		for _, letter := range token {
			hasLetter = hasLetter || letter >= 'a' && letter <= 'z'
			hasDigit = hasDigit || letter >= '0' && letter <= '9'
		}
		if hasLetter && hasDigit {
			return subscriptionPathPrefix + token, nil
		}
	}
}

func validGeneratedSubscriptionPath(path string) bool {
	if !strings.HasPrefix(path, subscriptionPathPrefix) {
		return false
	}
	token := strings.TrimPrefix(path, subscriptionPathPrefix)
	if !validSubscriptionToken(token) {
		return false
	}
	hasLetter := false
	hasDigit := false
	for _, letter := range token {
		hasLetter = hasLetter || letter >= 'a' && letter <= 'z'
		hasDigit = hasDigit || letter >= '0' && letter <= '9'
	}
	return hasLetter && hasDigit
}

func (m *CoreManager) ensureSubscriptionTokensLocked(clients []Client) error {
	if m.state.SubscriptionTokens == nil {
		m.state.SubscriptionTokens = map[string]string{}
	}
	used := map[string]bool{}
	for _, token := range m.state.SubscriptionTokens {
		if validSubscriptionToken(token) {
			used[token] = true
		}
	}
	for _, client := range clients {
		username := subscriptionUsername(client)
		if username == "" || validSubscriptionToken(m.state.SubscriptionTokens[username]) {
			continue
		}
		for {
			token, err := randomSubscriptionToken()
			if err != nil {
				return err
			}
			if !used[token] {
				m.state.SubscriptionTokens[username] = token
				used[token] = true
				break
			}
		}
	}
	return nil
}

func (m *CoreManager) pruneSubscriptionTokensLocked(inbounds []Inbound) {
	if len(m.state.SubscriptionTokens) == 0 {
		return
	}
	active := map[string]bool{}
	for _, inbound := range inbounds {
		for _, client := range inbound.Clients {
			if username := subscriptionUsername(client); username != "" {
				active[username] = true
			}
		}
	}
	for username := range m.state.SubscriptionTokens {
		if !active[username] {
			delete(m.state.SubscriptionTokens, username)
		}
	}
}

func (a *App) handleSubscriptionToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	var input struct {
		Username string `json:"username"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr("请求无效")})
		return
	}
	username := strings.TrimSpace(input.Username)
	if username == "" {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr("Username 不能为空")})
		return
	}
	a.manager.mu.Lock()
	defer a.manager.mu.Unlock()
	found := false
	for _, client := range allStateClients(a.manager.state.Inbounds) {
		if subscriptionUsername(client) == username {
			found = true
			break
		}
	}
	if !found {
		writeJSON(w, http.StatusNotFound, apiResponse{Message: a.tr("Username 不存在")})
		return
	}
	if a.manager.state.SubscriptionTokens == nil {
		a.manager.state.SubscriptionTokens = map[string]string{}
	}
	used := map[string]bool{}
	for name, token := range a.manager.state.SubscriptionTokens {
		if name != username {
			used[token] = true
		}
	}
	var token string
	for {
		generated, err := randomSubscriptionToken()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, apiResponse{Message: a.tr(err.Error())})
			return
		}
		if !used[generated] {
			token = generated
			break
		}
	}
	a.manager.state.SubscriptionTokens[username] = token
	if err := a.manager.saveLocked(); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{Message: a.tr(err.Error())})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: map[string]string{"username": username, "token": token}})
}

func (a *App) handleNewSubscriptionPath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	path, err := randomSubscriptionPath()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{Message: a.tr(err.Error())})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: map[string]string{"path": path}})
}

func subscriptionRelativePath(settings Settings, token string) string {
	base := normalizeSubscriptionPath(settings.SubscriptionPath)
	if base == "" {
		return "/" + token
	}
	return base + "/" + token
}

func subscriptionPublicPath(settings Settings, token string) string {
	return subscriptionRelativePath(settings, token)
}

func subscriptionClashPublicPath(settings Settings, token string) string {
	return subscriptionPublicPath(settings, token) + normalizeClashPath(settings.ClashPath)
}

func subscriptionAssetPublicPath(settings Settings) string {
	prefix := normalizeSubscriptionPath(settings.SubscriptionPath)
	if prefix == "" {
		return "/_subscription-assets/qrious2.min.js"
	}
	return prefix + "/_assets/qrious2.min.js"
}

// subscriptionFormat selects what a subscription request renders. The bare token
// path is subscriptionFormatPage (HTML info page, or the base64 share-link list
// when the client does not accept HTML). A per-client suffix pins one concrete
// format so each 3x-ui client can fetch exactly what it consumes.
type subscriptionFormat int

const (
	subscriptionFormatPage      subscriptionFormat = iota // bare token: HTML page or base64 list
	subscriptionFormatBase64                              // client suffix consuming a base64 share-link list
	subscriptionFormatClash                               // mihomo / Clash YAML
	subscriptionFormatSingbox                             // sing-box JSON
	subscriptionFormatSurge                               // Surge ini lines
	subscriptionFormatSurgeMac                            // Surge ini lines + mihomo external fallback
	subscriptionFormatSurfboard                           // Surfboard (Android) ini lines
	subscriptionFormatLoon                                // Loon ini lines
	subscriptionFormatQX                                  // Quantumult X ini lines
	subscriptionFormatStash                               // Stash clash-style YAML
	subscriptionFormatEgern                               // Egern nested YAML
)

// subscriptionClientSuffixes maps a fixed per-client path suffix to its format.
// Clash is handled separately because its suffix (ClashPath) is configurable. The
// base64 clients are universal share-link importers, so they all receive the same
// list as the bare URL, just under a client-labelled path (per the user's spec:
// mimic /clash by appending each client's name as the suffix).
var subscriptionClientSuffixes = map[string]subscriptionFormat{
	"/sing-box":     subscriptionFormatSingbox,
	"/v2box":        subscriptionFormatBase64,
	"/v2rayng":      subscriptionFormatBase64,
	"/v2raytun":     subscriptionFormatBase64,
	"/npvtunnel":    subscriptionFormatBase64,
	"/happ":         subscriptionFormatBase64,
	"/shadowrocket": subscriptionFormatBase64,
	"/streisand":    subscriptionFormatBase64,
	// Sub-Store targets that 3x-ui does not offer; each gets its own producer
	// format (see client_subscriptions.go), mirroring sub-store's producers.
	"/surge":     subscriptionFormatSurge,
	"/surgemac":  subscriptionFormatSurgeMac,
	"/surfboard": subscriptionFormatSurfboard,
	"/loon":      subscriptionFormatLoon,
	"/qx":        subscriptionFormatQX,
	"/stash":     subscriptionFormatStash,
	"/egern":     subscriptionFormatEgern,
}

// subscriptionClientKeys lists every client subscription conversion the panel
// can serve, in the order the settings page shows them. The bare subscription
// page and its base64 list are not conversions, so they carry no toggle.
var subscriptionClientKeys = []string{
	"clash", "sing-box", "v2box", "v2rayng", "v2raytun", "npvtunnel", "happ",
	"shadowrocket", "streisand", "surge", "surgemac", "surfboard", "loon", "qx", "stash", "egern",
}

// subscriptionClientDefaultEnabled is the shipped default per client: the
// widely deployed clients are on, the long tail stays off until the operator
// opts in from the panel settings.
var subscriptionClientDefaultEnabled = map[string]bool{
	"clash": true, "sing-box": true, "v2box": true, "v2rayng": true,
	"shadowrocket": true, "surge": true, "surgemac": true, "surfboard": true,
	"loon": true, "qx": true, "stash": true,
}

// subscriptionClientEnabled reports whether a client conversion is exposed,
// falling back to the shipped default for keys the state does not store yet.
func subscriptionClientEnabled(settings Settings, key string) bool {
	if enabled, ok := settings.SubscriptionClients[key]; ok {
		return enabled
	}
	return subscriptionClientDefaultEnabled[key]
}

// normalizeSubscriptionClientToggles expands the stored toggle map to every
// known client (dropping unknown keys) so the settings page always shows the
// effective state. It reports whether anything changed.
func normalizeSubscriptionClientToggles(settings *Settings) bool {
	normalized := make(map[string]bool, len(subscriptionClientKeys))
	for _, key := range subscriptionClientKeys {
		normalized[key] = subscriptionClientEnabled(*settings, key)
	}
	if len(normalized) == len(settings.SubscriptionClients) {
		same := true
		for key, enabled := range normalized {
			if settings.SubscriptionClients[key] != enabled {
				same = false
				break
			}
		}
		if same {
			return false
		}
	}
	settings.SubscriptionClients = normalized
	return true
}

// subscriptionClientSuffixKey maps a fixed client suffix to its toggle key.
func subscriptionClientSuffixKey(suffix string) string {
	return strings.TrimPrefix(suffix, "/")
}

// subscriptionClientTogglesJSON renders the effective per-client on/off state
// for the subscription page, which hides menu entries for disabled clients.
func subscriptionClientTogglesJSON(settings Settings) string {
	toggles := make(map[string]bool, len(subscriptionClientKeys))
	for _, key := range subscriptionClientKeys {
		toggles[key] = subscriptionClientEnabled(settings, key)
	}
	data, err := json.Marshal(toggles)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// detectSubscriptionFormat strips a trailing client/clash suffix from path (which
// has already had its subscription prefix removed) and reports the format. The
// returned path still carries the token for the caller to validate. Disabled
// conversions behave as if their suffix did not exist, so the request falls
// through to token validation and 404s.
func detectSubscriptionFormat(settings Settings, path string) (string, subscriptionFormat) {
	if clashPath := normalizeClashPath(settings.ClashPath); clashPath != "" && strings.HasSuffix(path, clashPath) && subscriptionClientEnabled(settings, "clash") {
		return strings.TrimSuffix(path, clashPath), subscriptionFormatClash
	}
	for suffix, format := range subscriptionClientSuffixes {
		if strings.HasSuffix(path, suffix) && subscriptionClientEnabled(settings, subscriptionClientSuffixKey(suffix)) {
			return strings.TrimSuffix(path, suffix), format
		}
	}
	return path, subscriptionFormatPage
}

func matchSubscriptionPath(settings Settings, requestPath string) (token string, format subscriptionFormat, ok bool) {
	prefix := normalizeSubscriptionPath(settings.SubscriptionPath)
	path := requestPath
	if prefix != "" {
		if !strings.HasPrefix(path, prefix+"/") {
			return "", subscriptionFormatPage, false
		}
		path = strings.TrimPrefix(path, prefix)
	}
	remaining, format := detectSubscriptionFormat(settings, path)
	token = strings.Trim(remaining, "/")
	return token, format, validSubscriptionToken(token)
}

func (a *App) subscriptionSnapshot(path string) (subscriptionSnapshot, subscriptionFormat, bool) {
	a.manager.mu.Lock()
	defer a.manager.mu.Unlock()
	settings := a.manager.state.Settings
	token, format, ok := matchSubscriptionPath(settings, path)
	if !ok {
		return subscriptionSnapshot{}, subscriptionFormatPage, false
	}
	username := ""
	for name, candidate := range a.manager.state.SubscriptionTokens {
		if candidate == token {
			username = name
			break
		}
	}
	if username == "" {
		return subscriptionSnapshot{}, subscriptionFormatPage, false
	}
	snapshot := subscriptionSnapshot{Settings: settings, Inbounds: append([]Inbound(nil), a.manager.state.Inbounds...), Username: username, Token: token}
	for _, inbound := range snapshot.Inbounds {
		for _, client := range inbound.Clients {
			if subscriptionUsername(client) == username {
				snapshot.Clients = append(snapshot.Clients, subscriptionClient{Inbound: inbound, Client: client})
			}
		}
	}
	return snapshot, format, len(snapshot.Clients) > 0
}

func (a *App) handleSubscriptionRequest(w http.ResponseWriter, r *http.Request) bool {
	a.manager.mu.Lock()
	assetPath := subscriptionAssetPublicPath(a.manager.state.Settings)
	a.manager.mu.Unlock()
	if r.URL.Path == assetPath {
		a.serveEmbedded(w, "web/qrious2.min.js", "application/javascript; charset=utf-8")
		return true
	}
	if snapshot, format, ok := a.crossSubscriptionSnapshot(r.URL.Path); ok {
		server := subscriptionServerHost(r)
		a.respondSubscription(w, r, snapshot, format, server,
			func() []subscriptionNode { return crossSubscriptionLinks(snapshot, server) },
			func(nodes []subscriptionNode) error {
				crossBase := subscriptionScheme(r) + "://" + subscriptionPublicHost(snapshot.Settings, r)
				crossURL := crossBase + crossPanelSubscriptionPublicPath(snapshot.Settings, snapshot.Token)
				crossClashURL := crossBase + crossPanelSubscriptionClashPublicPath(snapshot.Settings, snapshot.Token)
				return renderSubscriptionPageAt(w, r, snapshot, nodes, crossURL, crossClashURL)
			})
		return true
	}
	if snapshot, format, ok := a.subscriptionSnapshot(r.URL.Path); ok {
		server := subscriptionServerHost(r)
		a.respondSubscription(w, r, snapshot, format, server,
			func() []subscriptionNode { return subscriptionLinks(snapshot, server) },
			func(nodes []subscriptionNode) error { return renderSubscriptionPage(w, r, snapshot, nodes) })
		return true
	}
	return false
}

// respondSubscription writes the response for one matched subscription according
// to format. Clash and sing-box build straight from the snapshot; the base64 and
// page formats share the link list (page adds the HTML view for browsers). The
// linksFn / renderPage closures let local and cross-panel callers plug in their
// own link source and page URLs while sharing this branching logic.
func (a *App) respondSubscription(w http.ResponseWriter, r *http.Request, snapshot subscriptionSnapshot, format subscriptionFormat, server string, linksFn func() []subscriptionNode, renderPage func([]subscriptionNode) error) {
	switch format {
	case subscriptionFormatClash:
		data, err := buildMihomoSubscription(snapshot, server)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		setSubscriptionHeaders(w, snapshot)
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = w.Write(data)
	case subscriptionFormatSingbox:
		data, err := buildSingboxSubscription(snapshot, server)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		setSubscriptionHeaders(w, snapshot)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(data)
	case subscriptionFormatSurge, subscriptionFormatSurgeMac, subscriptionFormatSurfboard, subscriptionFormatLoon, subscriptionFormatQX, subscriptionFormatStash, subscriptionFormatEgern:
		data, contentType, err := buildClientSubscription(snapshot, server, format)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		setSubscriptionHeaders(w, snapshot)
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(data)
	default:
		links := linksFn()
		if len(links) == 0 {
			http.Error(w, "No available nodes", http.StatusBadRequest)
			return
		}
		setSubscriptionHeaders(w, snapshot)
		if format == subscriptionFormatPage && wantsSubscriptionHTML(r) {
			if err := renderPage(links); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(strings.Join(nodeURLs(links), "\n") + "\n"))))
	}
}

func wantsSubscriptionHTML(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html") || r.URL.Query().Get("html") == "1"
}

func setSubscriptionHeaders(w http.ResponseWriter, snapshot subscriptionSnapshot) {
	up, down, total, expiry, _ := aggregateSubscriptionStats(snapshot.Clients)
	w.Header().Set("Subscription-Userinfo", fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d", up, down, total, expiry/1000))
	w.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(snapshot.Username)))
	w.Header().Set("Profile-Update-Interval", "24")
	w.Header().Set("Cache-Control", "no-store")
}

func subscriptionServerHost(r *http.Request) string {
	host := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0])
	if host == "" {
		host = r.Host
	}
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		return parsed
	}
	return strings.Trim(host, "[]")
}

func subscriptionPublicHost(settings Settings, r *http.Request) string {
	domain := strings.TrimSpace(settings.PanelDomain)
	if domain == "" {
		if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0]); forwarded != "" {
			return forwarded
		}
		return r.Host
	}
	if _, port, err := net.SplitHostPort(r.Host); err == nil && port != "" && !strings.Contains(domain, ":") {
		return net.JoinHostPort(domain, port)
	}
	return domain
}

func subscriptionScheme(r *http.Request) string {
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); forwarded == "http" || forwarded == "https" {
		return forwarded
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func renderSubscriptionPage(w http.ResponseWriter, r *http.Request, snapshot subscriptionSnapshot, nodes []subscriptionNode) error {
	base := subscriptionScheme(r) + "://" + subscriptionPublicHost(snapshot.Settings, r)
	return renderSubscriptionPageAt(w, r, snapshot, nodes, base+subscriptionPublicPath(snapshot.Settings, snapshot.Token), base+subscriptionClashPublicPath(snapshot.Settings, snapshot.Token))
}

func renderSubscriptionPageAt(w http.ResponseWriter, r *http.Request, snapshot subscriptionSnapshot, nodes []subscriptionNode, subscriptionURL, clashURL string) error {
	up, down, total, expiry, lastOnline := aggregateSubscriptionStats(snapshot.Clients)
	status := "Unlimited"
	if total > 0 || expiry > 0 {
		status = "Active"
	}
	if expiry > 0 && expiry <= time.Now().UnixMilli() {
		status = "Expired"
	}
	data := subscriptionPageData{
		Token: snapshot.Token, Username: snapshot.Username,
		SubscriptionURL: subscriptionURL,
		ClashURL:        clashURL,
		ClashEnabled:    subscriptionClientEnabled(snapshot.Settings, "clash"),
		ClientToggles:   subscriptionClientTogglesJSON(snapshot.Settings),
		StaticURL:       subscriptionAssetPublicPath(snapshot.Settings),
		DefaultLanguage: settingsLanguage(snapshot.Settings),
		Status:          status, Downloaded: formatSubscriptionBytes(down), Uploaded: formatSubscriptionBytes(up),
		Usage: formatSubscriptionBytes(up + down), Total: "∞", LastOnline: lastOnline, Expiry: "No expiry", Nodes: nodes,
	}
	if !data.ClashEnabled {
		data.ClashURL = ""
	}
	if total > 0 {
		data.Total = formatSubscriptionBytes(total)
	}
	if expiry > 0 {
		data.Expiry = time.UnixMilli(expiry).Format("1/2/2006, 3:04:05 PM")
	}
	page, err := template.ParseFS(webFS, "web/subscription.html")
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return page.Execute(w, data)
}

func aggregateSubscriptionStats(clients []subscriptionClient) (up, down, total uint64, expiry int64, lastOnline string) {
	unlimited := false
	noExpiry := false
	var latest time.Time
	for _, item := range clients {
		client := item.Client
		up += client.Traffic.Up
		down += client.Traffic.Down
		if client.Total == 0 {
			unlimited = true
		} else {
			total += client.Total
		}
		if client.ExpiryTime == 0 {
			noExpiry = true
		} else if expiry == 0 || client.ExpiryTime < expiry {
			expiry = client.ExpiryTime
		}
		if parsed, err := time.Parse(time.RFC3339, client.LastOnline); err == nil && parsed.After(latest) {
			latest = parsed
		}
	}
	if unlimited {
		total = 0
	}
	if noExpiry {
		expiry = 0
	}
	if latest.IsZero() {
		lastOnline = "Never"
	} else {
		lastOnline = latest.Local().Format("1/2/2006, 3:04:05 PM")
	}
	return
}

func formatSubscriptionBytes(value uint64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	number := float64(value)
	unit := 0
	for number >= 1024 && unit < len(units)-1 {
		number /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d B", value)
	}
	return fmt.Sprintf("%.2f%s", number, units[unit])
}

func nodeURLs(nodes []subscriptionNode) []string {
	values := make([]string, 0, len(nodes))
	for _, node := range nodes {
		values = append(values, node.URL)
	}
	return values
}

func subscriptionLinks(snapshot subscriptionSnapshot, server string) []subscriptionNode {
	nodes := make([]subscriptionNode, 0, len(snapshot.Clients))
	now := time.Now().UnixMilli()
	for _, item := range snapshot.Clients {
		if !item.Inbound.Enabled || clientDepleted(item.Client, now) {
			continue
		}
		itemServer := server
		if item.Server != "" {
			itemServer = item.Server
		}
		if link := subscriptionShareURL(item.Inbound, item.Client, itemServer); link != "" {
			nodes = append(nodes, subscriptionNode{Name: subscriptionNodeName(item.Inbound, item.Client), URL: link})
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	return nodes
}

func subscriptionNodeName(inbound Inbound, client Client) string {
	return strings.Trim(strings.TrimSpace(valueOr(inbound.Notes, inbound.Name))+"-"+strings.TrimSpace(valueOr(client.Name, client.Username)), "-")
}

func subscriptionUnsupported(inbound Inbound) bool {
	return containsString([]string{"snell", "sudoku", "shadowquic", "trusttunnel", "hysteria2-realm"}, inbound.Type) ||
		(inbound.Type == "shadowsocks" && inbound.KcpTun.Enabled) || (inbound.Type == "vmess" && inbound.TLSMirror.Enabled) ||
		inbound.ShadowTLS.Enabled || inbound.RestTLS.Enabled || inbound.JLS.Enabled || inbound.TrojanSS.Enabled
}

func subscriptionShareURL(inbound Inbound, client Client, server string) string {
	if subscriptionUnsupported(inbound) {
		return ""
	}
	port, name := inbound.Port, url.QueryEscape(subscriptionNodeName(inbound, client))
	password, uuid := client.Password, client.UUID
	switch inbound.Type {
	case "shadowsocks":
		credential := base64.StdEncoding.EncodeToString([]byte(valueOr(inbound.Cipher, "2022-blake3-aes-256-gcm") + ":" + password))
		return fmt.Sprintf("ss://%s@%s:%d#%s", credential, server, port, name)
	case "vless":
		values := url.Values{"type": {valueOr(inbound.Network, "tcp")}, "encryption": {valueOr(inbound.Encryption, "none")}}
		applySubscriptionTransportQuery(values, inbound)
		if inbound.Reality.Enabled {
			values.Set("security", "reality")
			values.Set("pbk", inbound.Reality.PublicKey)
			values.Set("fp", valueOr(inbound.Reality.Fingerprint, "chrome"))
			values.Set("sni", firstListValue(inbound.Reality.ServerNames))
			values.Set("sid", firstListValue(inbound.Reality.ShortIDs))
		} else if inbound.TLS {
			values.Set("security", "tls")
			values.Set("sni", inbound.TLSServerName)
		}
		if client.Flow != "" {
			values.Set("flow", client.Flow)
		}
		return fmt.Sprintf("vless://%s@%s:%d?%s#%s", uuid, server, port, values.Encode(), name)
	case "vmess":
		if inbound.Mekya.Enabled {
			return ""
		}
		config := map[string]any{"v": "2", "ps": subscriptionNodeName(inbound, client), "add": server, "port": fmt.Sprint(port), "id": uuid, "aid": fmt.Sprint(client.AlterID), "scy": "auto", "net": valueOr(inbound.Network, "tcp"), "type": "none", "host": inbound.Host, "path": inbound.WSPath, "tls": "", "sni": ""}
		if inbound.GRPCServiceName != "" {
			config["net"], config["path"], config["type"] = "grpc", inbound.GRPCServiceName, "gun"
		}
		if inbound.MKCP.Enabled {
			config["net"], config["path"], config["type"] = "kcp", inbound.MKCP.Seed, valueOr(inbound.MKCP.Header, "none")
		}
		if inbound.TLS {
			config["tls"], config["sni"] = "tls", inbound.TLSServerName
		}
		data, _ := json.Marshal(config)
		return "vmess://" + base64.StdEncoding.EncodeToString(data)
	case "trojan":
		values := url.Values{}
		applySubscriptionTransportQuery(values, inbound)
		if inbound.TLS {
			values.Set("security", "tls")
			values.Set("sni", inbound.TLSServerName)
		}
		return fmt.Sprintf("trojan://%s@%s:%d?%s#%s", url.QueryEscape(password), server, port, values.Encode(), name)
	case "hysteria2":
		values := url.Values{}
		if inbound.TLSServerName != "" {
			values.Set("sni", inbound.TLSServerName)
		}
		if inbound.AllowInsecure {
			values.Set("insecure", "1")
		}
		if inbound.Hysteria.Obfs != "" && inbound.Hysteria.ObfsPassword != "" {
			values.Set("obfs", inbound.Hysteria.Obfs)
			values.Set("obfs-password", inbound.Hysteria.ObfsPassword)
		}
		return fmt.Sprintf("hysteria2://%s@%s:%d/?%s#%s", url.QueryEscape(password), server, port, values.Encode(), name)
	case "tuic":
		values := url.Values{}
		if inbound.TLSServerName != "" {
			values.Set("sni", inbound.TLSServerName)
		}
		if inbound.AllowInsecure {
			values.Set("allow_insecure", "1")
		}
		return fmt.Sprintf("tuic://%s:%s@%s:%d?%s#%s", url.QueryEscape(uuid), url.QueryEscape(password), server, port, values.Encode(), name)
	case "anytls":
		values := url.Values{}
		if inbound.TLSServerName != "" {
			values.Set("sni", inbound.TLSServerName)
		}
		if inbound.AllowInsecure {
			values.Set("insecure", "1")
		}
		return fmt.Sprintf("anytls://%s@%s:%d?%s#%s", url.QueryEscape(password), server, port, values.Encode(), name)
	case "socks", "mixed":
		return fmt.Sprintf("socks5://%s:%s@%s:%d#%s", url.QueryEscape(subscriptionUsername(client)), url.QueryEscape(password), server, port, name)
	case "http":
		return fmt.Sprintf("http://%s:%s@%s:%d#%s", url.QueryEscape(subscriptionUsername(client)), url.QueryEscape(password), server, port, name)
	case "mieru":
		values := url.Values{"port": {fmt.Sprint(port)}, "protocol": {strings.ToUpper(valueOr(inbound.Mieru.Transport, "TCP"))}, "profile": {subscriptionNodeName(inbound, client)}}
		return fmt.Sprintf("mierus://%s:%s@%s?%s#%s", url.QueryEscape(subscriptionUsername(client)), url.QueryEscape(password), server, values.Encode(), name)
	}
	return ""
}

func applySubscriptionTransportQuery(values url.Values, inbound Inbound) {
	if inbound.WSPath != "" {
		values.Set("type", "ws")
		values.Set("path", inbound.WSPath)
		if inbound.Host != "" {
			values.Set("host", inbound.Host)
		}
	} else if inbound.GRPCServiceName != "" {
		values.Set("type", "grpc")
		values.Set("serviceName", inbound.GRPCServiceName)
	}
}

func firstListValue(value string) string {
	values := splitList(value)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// subscriptionProxies renders every usable client as a clash/mihomo proxy map
// with deduplicated names. All client-specific producers (clash YAML, sing-box
// JSON, Surge/Loon/QX ini lines, Stash/Egern YAML) start from this shared list
// so protocol support and field extraction stay in subscriptionProxy.
func subscriptionProxies(snapshot subscriptionSnapshot, server string) []map[string]any {
	proxies := make([]map[string]any, 0)
	usedNames := map[string]int{}
	now := time.Now().UnixMilli()
	for _, item := range snapshot.Clients {
		if !item.Inbound.Enabled || clientDepleted(item.Client, now) {
			continue
		}
		itemServer := server
		if item.Server != "" {
			itemServer = item.Server
		}
		proxy := subscriptionProxy(item.Inbound, item.Client, itemServer)
		if proxy == nil {
			continue
		}
		name := proxy["name"].(string)
		usedNames[name]++
		if usedNames[name] > 1 {
			name = fmt.Sprintf("%s %d", name, usedNames[name])
			proxy["name"] = name
		}
		proxies = append(proxies, proxy)
	}
	return proxies
}

func buildMihomoSubscription(snapshot subscriptionSnapshot, server string) ([]byte, error) {
	proxies := subscriptionProxies(snapshot, server)
	if len(proxies) == 0 {
		return nil, fmt.Errorf("此用户没有 Mihomo 可用节点")
	}
	names := make([]any, 0, len(proxies)+1)
	for _, proxy := range proxies {
		names = append(names, proxy["name"].(string))
	}
	names = append(names, "DIRECT")
	return marshalYAML(map[string]any{"proxies": proxies, "proxy-groups": []any{map[string]any{"name": "PROXY", "type": "select", "proxies": names}}, "rules": []any{"MATCH,PROXY"}})
}

func subscriptionProxy(inbound Inbound, client Client, server string) map[string]any {
	if subscriptionUnsupported(inbound) {
		return nil
	}
	proxy := map[string]any{"name": subscriptionNodeName(inbound, client), "server": server, "port": inbound.Port}
	switch inbound.Type {
	case "shadowsocks":
		proxy["type"], proxy["cipher"], proxy["password"], proxy["udp"] = "ss", valueOr(inbound.Cipher, "2022-blake3-aes-256-gcm"), client.Password, inbound.UDP
		if inbound.SimpleObfs.Enabled {
			proxy["plugin"] = "obfs"
			proxy["plugin-opts"] = map[string]any{"mode": valueOr(inbound.SimpleObfs.Mode, "http"), "host": inbound.Host}
		}
	case "vless":
		proxy["type"], proxy["uuid"], proxy["udp"] = "vless", client.UUID, true
		if inbound.Encryption != "" && inbound.Encryption != "none" {
			proxy["encryption"] = inbound.Encryption
		}
		applySubscriptionProxyTransport(proxy, inbound)
		if client.Flow != "" {
			proxy["flow"] = client.Flow
		}
		applySubscriptionProxySecurity(proxy, inbound)
	case "vmess":
		proxy["type"], proxy["uuid"], proxy["alterId"], proxy["cipher"], proxy["udp"] = "vmess", client.UUID, client.AlterID, "auto", true
		applySubscriptionProxyTransport(proxy, inbound)
		applySubscriptionProxySecurity(proxy, inbound)
	case "trojan":
		proxy["type"], proxy["password"], proxy["udp"] = "trojan", client.Password, true
		applySubscriptionProxyTransport(proxy, inbound)
		applySubscriptionProxySecurity(proxy, inbound)
	case "hysteria2":
		proxy["type"], proxy["password"] = "hysteria2", client.Password
		if inbound.TLSServerName != "" {
			proxy["sni"] = inbound.TLSServerName
		}
		if inbound.AllowInsecure {
			proxy["skip-cert-verify"] = true
		}
		if inbound.Hysteria.Obfs != "" {
			proxy["obfs"] = inbound.Hysteria.Obfs
			proxy["obfs-password"] = inbound.Hysteria.ObfsPassword
		}
		if inbound.Hysteria.Up != "" {
			proxy["up"] = inbound.Hysteria.Up
		}
		if inbound.Hysteria.Down != "" {
			proxy["down"] = inbound.Hysteria.Down
		}
	case "tuic":
		proxy["type"], proxy["uuid"], proxy["password"], proxy["udp-relay-mode"] = "tuic", client.UUID, client.Password, "native"
		if inbound.TLSServerName != "" {
			proxy["sni"] = inbound.TLSServerName
		}
		if inbound.AllowInsecure {
			proxy["skip-cert-verify"] = true
		}
		if inbound.TUIC.CongestionController != "" {
			proxy["congestion-controller"] = inbound.TUIC.CongestionController
		}
	case "anytls":
		proxy["type"], proxy["password"] = "anytls", client.Password
		if inbound.TLSServerName != "" {
			proxy["sni"] = inbound.TLSServerName
		}
		if inbound.AllowInsecure {
			proxy["skip-cert-verify"] = true
		}
	case "socks", "mixed":
		proxy["type"], proxy["username"], proxy["password"], proxy["udp"] = "socks5", subscriptionUsername(client), client.Password, inbound.UDP
	case "http":
		proxy["type"], proxy["username"], proxy["password"] = "http", subscriptionUsername(client), client.Password
	case "mieru":
		proxy["type"], proxy["username"], proxy["password"], proxy["transport"] = "mieru", subscriptionUsername(client), client.Password, strings.ToUpper(valueOr(inbound.Mieru.Transport, "TCP"))
	default:
		return nil
	}
	return proxy
}

func applySubscriptionProxyTransport(proxy map[string]any, inbound Inbound) {
	if inbound.XHTTP.Enabled {
		proxy["network"] = "xhttp"
		opts := map[string]any{"path": valueOr(inbound.XHTTP.Path, "/"), "host": inbound.XHTTP.Host, "mode": valueOr(inbound.XHTTP.Mode, "auto")}
		if inbound.XHTTP.XPaddingBytes != "" {
			opts["x-padding-bytes"] = inbound.XHTTP.XPaddingBytes
		}
		proxy["xhttp-opts"] = opts
	} else if inbound.Mekya.Enabled {
		proxy["network"] = "mekya"
		proxy["mekya-opts"] = map[string]any{"url": inbound.Mekya.URL, "h2-pool-size": inbound.Mekya.H2PoolSize, "max-write-delay": inbound.Mekya.MaxWriteDelay, "max-request-size": inbound.Mekya.MaxRequestSize, "polling-interval-initial": inbound.Mekya.PollingIntervalInitial}
	} else if inbound.MKCP.Enabled {
		proxy["network"] = "mkcp"
		proxy["mkcp-opts"] = map[string]any{"mtu": inbound.MKCP.MTU, "tti": inbound.MKCP.TTI, "uplink-capacity": inbound.MKCP.UplinkCapacity, "downlink-capacity": inbound.MKCP.DownlinkCapacity, "congestion": inbound.MKCP.Congestion, "write-buffer-size": inbound.MKCP.WriteBuffer, "read-buffer-size": inbound.MKCP.ReadBuffer, "seed": inbound.MKCP.Seed, "header": valueOr(inbound.MKCP.Header, "none")}
	} else if inbound.WSPath != "" {
		proxy["network"] = "ws"
		opts := map[string]any{"path": inbound.WSPath}
		if inbound.Host != "" {
			opts["headers"] = map[string]any{"Host": inbound.Host}
		}
		proxy["ws-opts"] = opts
	} else if inbound.GRPCServiceName != "" {
		proxy["network"] = "grpc"
		proxy["grpc-opts"] = map[string]any{"grpc-service-name": inbound.GRPCServiceName}
	}
}

func applySubscriptionProxySecurity(proxy map[string]any, inbound Inbound) {
	if inbound.Reality.Enabled {
		proxy["tls"] = true
		proxy["servername"] = firstListValue(inbound.Reality.ServerNames)
		proxy["client-fingerprint"] = valueOr(inbound.Reality.Fingerprint, "chrome")
		proxy["reality-opts"] = map[string]any{"public-key": inbound.Reality.PublicKey, "short-id": firstListValue(inbound.Reality.ShortIDs)}
	} else if inbound.TLS {
		proxy["tls"] = true
		if inbound.TLSServerName != "" {
			proxy["servername"] = inbound.TLSServerName
		}
		if inbound.AllowInsecure {
			proxy["skip-cert-verify"] = true
		}
	}
}
