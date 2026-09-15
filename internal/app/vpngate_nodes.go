package app

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// VPNGate publishes its volunteer list over plain HTTP only; the payload is
	// treated as untrusted data and only IP/port/country are ever used.
	vpnGateListURL     = "http://www.vpngate.net/api/iphone/"
	vpnGateListTTL     = 10 * time.Minute
	vpnGateListLimit   = 16 << 20
	vpnGateListTimeout = 45 * time.Second

	// AS36599 is excluded because those nodes are datacentre relays, not the
	// home broadband lines this feature exists for.
	vpnGateExcludedASN = "AS36599"

	vpnGateASNTTL       = 12 * time.Hour
	vpnGateASNTimeout   = 12 * time.Second
	vpnGateASNCooldown  = 30 * time.Minute
	vpnGateMinASNGap    = 250 * time.Millisecond
	vpnGateMaxCandidate = 40
)

// VPNGateNode is one volunteer endpoint. Nothing from the OpenVPN payload other
// than the remote port is kept, and no directive from it is ever executed.
type VPNGateNode struct {
	IP      string
	Port    int
	Country string
	Region  string
	Score   int64
}

func (n VPNGateNode) Endpoint() string {
	return net.JoinHostPort(n.IP, strconv.Itoa(n.Port))
}

func probeVPNGateEndpoint(ctx context.Context, node VPNGateNode) error {
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp4", node.Endpoint())
	if err != nil {
		return err
	}
	return connection.Close()
}

type vpnGateNodeCache struct {
	mu      sync.Mutex
	nodes   []VPNGateNode
	fetched time.Time
	inWork  bool
	wait    chan struct{}
}

var vpnGateNodes vpnGateNodeCache

// fetchVPNGateNodes returns the cached list when it is fresh and coalesces
// concurrent refreshes so ten slots never pull the CSV ten times.
func (c *vpnGateNodeCache) fetch(ctx context.Context, now func() time.Time) ([]VPNGateNode, error) {
	for {
		c.mu.Lock()
		if len(c.nodes) > 0 && now().Sub(c.fetched) < vpnGateListTTL {
			nodes := c.nodes
			c.mu.Unlock()
			return nodes, nil
		}
		if c.inWork {
			wait := c.wait
			c.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		c.inWork, c.wait = true, make(chan struct{})
		wait := c.wait
		c.mu.Unlock()

		nodes, err := downloadVPNGateList(ctx)
		c.mu.Lock()
		if err == nil {
			c.nodes, c.fetched = nodes, now()
		}
		c.inWork = false
		close(wait)
		cached := c.nodes
		c.mu.Unlock()
		if err != nil {
			if len(cached) > 0 {
				// A stale list beats no list at all while the mirror is down.
				return cached, nil
			}
			return nil, err
		}
		return nodes, nil
	}
}

func downloadVPNGateList(ctx context.Context) ([]VPNGateNode, error) {
	requestCtx, cancel := context.WithTimeout(ctx, vpnGateListTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, vpnGateListURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "m-ui/"+appVersion)
	response, err := (&http.Client{Timeout: vpnGateListTimeout}).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("VPNGate 节点列表返回状态 %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, vpnGateListLimit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > vpnGateListLimit {
		return nil, fmt.Errorf("VPNGate 节点列表超过大小限制")
	}
	nodes := parseVPNGateList(string(body))
	if len(nodes) == 0 {
		return nil, fmt.Errorf("VPNGate 节点列表为空")
	}
	return nodes, nil
}

// parseVPNGateList reads the iPhone CSV: two header lines, then one node per
// line. Field 1 is the IP, field 6 the two-letter country, field 14 the base64
// OpenVPN profile we only mine for the remote port.
func parseVPNGateList(body string) []VPNGateNode {
	var nodes []VPNGateNode
	seen := make(map[string]bool)
	reader := csv.NewReader(strings.NewReader(body))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	for index := 0; ; index++ {
		fields, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		if index < 2 || len(fields) == 0 || strings.HasPrefix(strings.TrimSpace(fields[0]), "*") || strings.HasPrefix(strings.TrimSpace(fields[0]), "#") {
			continue
		}
		if len(fields) < 15 {
			continue
		}
		ip := strings.TrimSpace(fields[1])
		if !isPublicIPv4(ip) {
			continue
		}
		country := strings.ToUpper(strings.TrimSpace(fields[6]))
		if !isVPNGateCountryCode(country) {
			continue
		}
		port := vpnGateRemotePort(fields[14])
		if port <= 0 || port > 65535 {
			continue
		}
		endpoint := net.JoinHostPort(ip, strconv.Itoa(port))
		if seen[endpoint] {
			continue
		}
		score, _ := strconv.ParseInt(strings.TrimSpace(fields[2]), 10, 64)
		seen[endpoint] = true
		nodes = append(nodes, VPNGateNode{IP: ip, Port: port, Country: country, Score: score})
	}
	return nodes
}

// vpnGateRemotePort pulls the port out of the OpenVPN config's `remote` line.
// The rest of the profile is discarded: m-ui dials with SoftEther, never with
// OpenVPN, so no directive from a volunteer can turn into a local command.
func vpnGateRemotePort(encoded string) int {
	value := strings.TrimSpace(encoded)
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(value, "="))
	}
	if err != nil {
		return 443
	}
	if len(raw) > 1<<20 {
		raw = raw[:1<<20]
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 3 || !strings.EqualFold(fields[0], "remote") {
			continue
		}
		port, err := strconv.Atoi(fields[2])
		if err == nil && port > 0 && port <= 65535 {
			return port
		}
	}
	return 443
}

func isPublicIPv4(value string) bool {
	address := net.ParseIP(strings.TrimSpace(value))
	if address == nil {
		return false
	}
	address = address.To4()
	if address == nil {
		return false
	}
	if address.IsLoopback() || address.IsPrivate() || address.IsUnspecified() || address.IsMulticast() || address.IsLinkLocalUnicast() {
		return false
	}
	// 100.64.0.0/10 and 0.0.0.0/8 are not covered by IsPrivate.
	if address[0] == 0 || address[0] >= 224 ||
		(address[0] == 100 && address[1] >= 64 && address[1] <= 127) ||
		(address[0] == 192 && address[1] == 0 && (address[2] == 0 || address[2] == 2)) ||
		(address[0] == 198 && (address[1] == 18 || address[1] == 19 || address[1] == 51 && address[2] == 100)) ||
		(address[0] == 203 && address[1] == 0 && address[2] == 113) {
		return false
	}
	return true
}

// VPNGateASN is one answer from an ASN lookup service.
type VPNGateASN struct {
	ASN    string
	Name   string
	Region string
}

func (a VPNGateASN) Excluded() bool {
	return strings.EqualFold(strings.TrimSpace(a.ASN), vpnGateExcludedASN)
}

func (a VPNGateASN) Label() string {
	switch {
	case a.ASN != "" && a.Name != "":
		return a.ASN + " " + a.Name
	case a.Name != "":
		return a.Name
	default:
		return a.ASN
	}
}

type vpnGateASNEntry struct {
	value   VPNGateASN
	fetched time.Time
}

// vpnGateASNResolver caches lookups, rate-limits itself and remembers when a
// provider told it to back off.
type vpnGateASNResolver struct {
	mu       sync.Mutex
	entries  map[string]vpnGateASNEntry
	pending  map[string]chan struct{}
	lastCall time.Time
	// ipinfoUntil / ipapiUntil hold the moment each provider may be used again
	// after a quota or Retry-After response.
	ipinfoUntil time.Time
	ipapiUntil  time.Time

	now    func() time.Time
	sleep  func(time.Duration)
	client *http.Client
	// token is only ever sent to IPinfo. ip-api is plain HTTP, so handing it a
	// secret would leak it on the wire.
	token func() string
}

func newVPNGateASNResolver(token func() string) *vpnGateASNResolver {
	return &vpnGateASNResolver{
		entries: make(map[string]vpnGateASNEntry),
		pending: make(map[string]chan struct{}),
		now:     time.Now,
		sleep:   time.Sleep,
		client:  &http.Client{Timeout: vpnGateASNTimeout},
		token:   token,
	}
}

func (r *vpnGateASNResolver) cached(ip string) (VPNGateASN, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entries[ip]
	if !ok || r.now().Sub(entry.fetched) > vpnGateASNTTL {
		return VPNGateASN{}, false
	}
	return entry.value, true
}

// Lookup resolves one address, coalescing duplicate in-flight requests.
func (r *vpnGateASNResolver) Lookup(ctx context.Context, ip string) (VPNGateASN, error) {
	if !isPublicIPv4(ip) {
		return VPNGateASN{}, fmt.Errorf("不是可查询的公网 IPv4 地址")
	}
	for {
		if value, ok := r.cached(ip); ok {
			return value, nil
		}
		r.mu.Lock()
		if wait, busy := r.pending[ip]; busy {
			r.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return VPNGateASN{}, ctx.Err()
			}
		}
		wait := make(chan struct{})
		r.pending[ip] = wait
		r.mu.Unlock()

		value, err := r.resolve(ctx, ip)
		r.mu.Lock()
		if err == nil {
			r.entries[ip] = vpnGateASNEntry{value: value, fetched: r.now()}
		}
		delete(r.pending, ip)
		close(wait)
		r.mu.Unlock()
		return value, err
	}
}

// throttle keeps a minimum gap between outbound lookups so a ten-slot panel
// cannot hammer a free service.
func (r *vpnGateASNResolver) throttle(ctx context.Context) error {
	r.mu.Lock()
	gap := vpnGateMinASNGap - r.now().Sub(r.lastCall)
	r.lastCall = r.now()
	if gap > 0 {
		r.lastCall = r.lastCall.Add(gap)
	}
	r.mu.Unlock()
	if gap <= 0 {
		return nil
	}
	if r.sleep != nil {
		r.sleep(gap)
		return ctx.Err()
	}
	select {
	case <-time.After(gap):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *vpnGateASNResolver) available(until time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.now().Before(until)
}

func (r *vpnGateASNResolver) penalise(provider string, retryAfter time.Duration) {
	if retryAfter <= 0 {
		retryAfter = vpnGateASNCooldown
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if provider == "ipinfo" {
		r.ipinfoUntil = r.now().Add(retryAfter)
		return
	}
	r.ipapiUntil = r.now().Add(retryAfter)
}

// resolve tries IPinfo first and only falls back to ip-api when IPinfo says it
// is out of quota. With a user token the free fallback is skipped entirely: the
// token is the whole point of the setting, and ip-api never sees it.
func (r *vpnGateASNResolver) resolve(ctx context.Context, ip string) (VPNGateASN, error) {
	token := ""
	if r.token != nil {
		token = strings.TrimSpace(r.token())
	}
	r.mu.Lock()
	ipinfoUntil, ipapiUntil := r.ipinfoUntil, r.ipapiUntil
	r.mu.Unlock()

	var firstErr error
	useFallback := false
	if r.available(ipinfoUntil) {
		if err := r.throttle(ctx); err != nil {
			return VPNGateASN{}, err
		}
		value, quota, retryAfter, err := r.lookupIPInfo(ctx, ip, token)
		switch {
		case err == nil:
			return value, nil
		case quota:
			r.penalise("ipinfo", retryAfter)
			firstErr = err
			useFallback = true
		default:
			firstErr = err
		}
	} else {
		firstErr = fmt.Errorf("IPinfo 处于冷却中")
		useFallback = true
	}
	if token != "" {
		// A configured token must not silently degrade to a service the user
		// did not choose and that would see their traffic in clear text.
		return VPNGateASN{}, firstErr
	}
	if !useFallback {
		return VPNGateASN{}, firstErr
	}
	if !r.available(ipapiUntil) {
		return VPNGateASN{}, firstErr
	}
	if err := r.throttle(ctx); err != nil {
		return VPNGateASN{}, err
	}
	value, retryAfter, err := r.lookupIPAPI(ctx, ip)
	if err != nil {
		if retryAfter > 0 {
			r.penalise("ipapi", retryAfter)
		}
		return VPNGateASN{}, err
	}
	return value, nil
}

type ipinfoResponse struct {
	ASN *struct {
		ASN  string `json:"asn"`
		Name string `json:"name"`
	} `json:"asn"`
	Org    string `json:"org"`
	Region string `json:"region"`
	Error  *struct {
		Title   string `json:"title"`
		Message string `json:"message"`
	} `json:"error"`
}

// lookupIPInfo returns (value, quotaExhausted, error). Both response shapes are
// accepted: the paid plan returns an `asn` object, the free one folds the same
// data into `org` as "AS1234 Example Inc".
func (r *vpnGateASNResolver) lookupIPInfo(ctx context.Context, ip, token string) (VPNGateASN, bool, time.Duration, error) {
	endpoint := "https://ipinfo.io/" + url.PathEscape(ip) + "/json"
	requestCtx, cancel := context.WithTimeout(ctx, vpnGateASNTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return VPNGateASN{}, false, 0, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "m-ui/"+appVersion)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := r.client.Do(request)
	if err != nil {
		return VPNGateASN{}, false, 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return VPNGateASN{}, false, 0, err
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusPaymentRequired {
		return VPNGateASN{}, true, retryAfterDuration(response.Header, r.now()), fmt.Errorf("IPinfo 查询额度已用尽")
	}
	if response.StatusCode != http.StatusOK {
		return VPNGateASN{}, false, 0, fmt.Errorf("IPinfo 返回状态 %d", response.StatusCode)
	}
	var parsed ipinfoResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		// An HTML quota or error page is not an ISP name.
		return VPNGateASN{}, false, 0, fmt.Errorf("IPinfo 返回了无法解析的内容")
	}
	if parsed.Error != nil {
		quota := strings.Contains(strings.ToLower(parsed.Error.Title+parsed.Error.Message), "limit")
		return VPNGateASN{}, quota, retryAfterDuration(response.Header, r.now()), fmt.Errorf("IPinfo: %s", strings.TrimSpace(parsed.Error.Title+" "+parsed.Error.Message))
	}
	if parsed.ASN != nil && strings.TrimSpace(parsed.ASN.Name) != "" {
		return VPNGateASN{ASN: strings.TrimSpace(parsed.ASN.ASN), Name: strings.TrimSpace(parsed.ASN.Name), Region: strings.TrimSpace(parsed.Region)}, false, 0, nil
	}
	if value, ok := splitOrgField(parsed.Org); ok {
		value.Region = strings.TrimSpace(parsed.Region)
		return value, false, 0, nil
	}
	return VPNGateASN{}, false, 0, fmt.Errorf("IPinfo 没有返回 ASN 信息")
}

// splitOrgField turns "AS2516 KDDI CORPORATION" into its two halves.
func splitOrgField(org string) (VPNGateASN, bool) {
	org = strings.TrimSpace(org)
	if org == "" {
		return VPNGateASN{}, false
	}
	fields := strings.SplitN(org, " ", 2)
	if len(fields) == 2 && strings.HasPrefix(strings.ToUpper(fields[0]), "AS") {
		return VPNGateASN{ASN: strings.ToUpper(strings.TrimSpace(fields[0])), Name: strings.TrimSpace(fields[1])}, true
	}
	return VPNGateASN{Name: org}, true
}

type ipapiResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	AS      string `json:"as"`
	ASName  string `json:"asname"`
	ISP     string `json:"isp"`
	Org     string `json:"org"`
	Region  string `json:"regionName"`
}

// lookupIPAPI is the free fallback. It is HTTP-only, so it is used without any
// credential and only when the user has not configured a token.
func (r *vpnGateASNResolver) lookupIPAPI(ctx context.Context, ip string) (VPNGateASN, time.Duration, error) {
	endpoint := "http://ip-api.com/json/" + url.PathEscape(ip) + "?fields=status,message,as,asname,isp,org,regionName"
	requestCtx, cancel := context.WithTimeout(ctx, vpnGateASNTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return VPNGateASN{}, 0, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "m-ui/"+appVersion)
	response, err := r.client.Do(request)
	if err != nil {
		return VPNGateASN{}, 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return VPNGateASN{}, 0, err
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return VPNGateASN{}, ipapiCooldown(response.Header), fmt.Errorf("ip-api 查询频率超限")
	}
	if response.StatusCode != http.StatusOK {
		return VPNGateASN{}, 0, fmt.Errorf("ip-api 返回状态 %d", response.StatusCode)
	}
	var parsed ipapiResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return VPNGateASN{}, 0, fmt.Errorf("ip-api 返回了无法解析的内容")
	}
	if !strings.EqualFold(parsed.Status, "success") {
		return VPNGateASN{}, 0, fmt.Errorf("ip-api: %s", strings.TrimSpace(parsed.Message))
	}
	value := VPNGateASN{}
	if asn, ok := splitOrgField(parsed.AS); ok {
		value = asn
	}
	for _, name := range []string{parsed.ASName, parsed.ISP, parsed.Org} {
		if value.Name == "" && strings.TrimSpace(name) != "" {
			value.Name = strings.TrimSpace(name)
		}
	}
	value.Region = strings.TrimSpace(parsed.Region)
	if value.Name == "" && value.ASN == "" {
		return VPNGateASN{}, 0, fmt.Errorf("ip-api 没有返回 ASN 信息")
	}
	// Remaining-quota headers let us stop before the service starts refusing.
	if remaining, err := strconv.Atoi(strings.TrimSpace(response.Header.Get("X-Rl"))); err == nil && remaining <= 0 {
		r.penalise("ipapi", ipapiCooldown(response.Header))
	}
	return value, 0, nil
}

func ipapiCooldown(header http.Header) time.Duration {
	if delay := retryAfterDuration(header, time.Now()); delay > 0 {
		return delay
	}
	for _, name := range []string{"Retry-After", "X-Ttl"} {
		if seconds, err := strconv.Atoi(strings.TrimSpace(header.Get(name))); err == nil && seconds > 0 {
			if seconds > int(vpnGateASNCooldown/time.Second) {
				seconds = int(vpnGateASNCooldown / time.Second)
			}
			return time.Duration(seconds) * time.Second
		}
	}
	return time.Minute
}

func retryAfterDuration(header http.Header, now time.Time) time.Duration {
	raw := strings.TrimSpace(header.Get("Retry-After"))
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if deadline, err := http.ParseTime(raw); err == nil && deadline.After(now) {
		return deadline.Sub(now)
	}
	return 0
}

// vpnGateCandidates orders the country's nodes so the ones most likely to match
// are probed first. SoftBank lines are noticeably better from Tokyo, so when
// region data exists that preference is kept.
func vpnGateCandidates(nodes []VPNGateNode, config VPNGateConfig) []VPNGateNode {
	var candidates []VPNGateNode
	for _, node := range nodes {
		if node.Country == config.Country {
			candidates = append(candidates, node)
		}
	}
	preferTokyo := config.ISP == "softbank"
	sortVPNGateNodes(candidates, preferTokyo)
	if len(candidates) > vpnGateMaxCandidate {
		candidates = candidates[:vpnGateMaxCandidate]
	}
	return candidates
}

func sortVPNGateNodes(nodes []VPNGateNode, preferTokyo bool) {
	weight := func(node VPNGateNode) int64 {
		score := node.Score
		if preferTokyo && strings.Contains(strings.ToLower(node.Region), "tokyo") {
			score += 1 << 40
		}
		return score
	}
	for i := 1; i < len(nodes); i++ {
		current := nodes[i]
		j := i - 1
		for j >= 0 && weight(nodes[j]) < weight(current) {
			nodes[j+1] = nodes[j]
			j--
		}
		nodes[j+1] = current
	}
}
