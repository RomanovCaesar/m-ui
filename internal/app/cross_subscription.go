package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const crossSubscriptionCacheVersion = 1
const crossSubscriptionCacheFile = "cross-panel-subscriptions.json"
const maxCrossPullJobs = 20

type crossSubscriptionCacheSource struct {
	Node        PeerNode          `json:"node"`
	RetrievedAt time.Time         `json:"retrievedAt"`
	Inbounds    []Inbound         `json:"inbounds"`
	Tokens      map[string]string `json:"tokens,omitempty"`
	LastError   string            `json:"lastError,omitempty"`
}
type crossSubscriptionCacheDisk struct {
	Version int                                     `json:"version"`
	Sources map[string]crossSubscriptionCacheSource `json:"sources"`
}
type CrossSubscriptionManager struct {
	mu      sync.Mutex
	path    string
	version int
	sources map[string]crossSubscriptionCacheSource
	jobs    map[string]*crossSubscriptionJob
	active  string
}
type crossSubscriptionSourceView struct {
	Node         PeerNode  `json:"node"`
	RetrievedAt  time.Time `json:"retrievedAt"`
	InboundCount int       `json:"inboundCount"`
	ClientCount  int       `json:"clientCount"`
	LastError    string    `json:"lastError,omitempty"`
}
type crossSubscriptionUser struct {
	Username string `json:"username"`
	Token    string `json:"token"`
	URL      string `json:"url"`
}
type crossSubscriptionView struct {
	Peers     []peerRow                     `json:"peers"`
	Users     []crossSubscriptionUser       `json:"users"`
	Sources   []crossSubscriptionSourceView `json:"sources"`
	ActiveJob string                        `json:"activeJob,omitempty"`
}
type crossPullTarget struct {
	ID       string `json:"id"`
	Endpoint string `json:"endpoint"`
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
	Inbounds int    `json:"inbounds"`
	Clients  int    `json:"clients"`
}
type crossSubscriptionJob struct {
	ID        string            `json:"id"`
	Started   time.Time         `json:"started"`
	Done      bool              `json:"done"`
	Completed int               `json:"completed"`
	Targets   []crossPullTarget `json:"targets"`
}
type crossPanelPullPayload struct {
	Inbounds []Inbound         `json:"inbounds"`
	Tokens   map[string]string `json:"tokens,omitempty"`
}

func newCrossSubscriptionManager(dataDir string) (*CrossSubscriptionManager, error) {
	m := &CrossSubscriptionManager{path: filepath.Join(dataDir, crossSubscriptionCacheFile), version: crossSubscriptionCacheVersion, sources: map[string]crossSubscriptionCacheSource{}, jobs: map[string]*crossSubscriptionJob{}}
	data, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	var disk crossSubscriptionCacheDisk
	if err := json.Unmarshal(data, &disk); err != nil {
		return nil, fmt.Errorf("跨面板订阅缓存无效: %w", err)
	}
	if disk.Version != 0 && disk.Version != crossSubscriptionCacheVersion {
		return nil, fmt.Errorf("不支持的跨面板订阅缓存版本")
	}
	for id, source := range disk.Sources {
		if id != source.Node.ID || !peerIDPattern.MatchString(id) {
			return nil, fmt.Errorf("跨面板订阅缓存节点身份无效")
		}
		if err := verifyPeerNode(source.Node); err != nil {
			return nil, fmt.Errorf("跨面板订阅缓存节点无效: %w", err)
		}
		if len(source.Inbounds) > maxSyncInbounds {
			return nil, fmt.Errorf("跨面板订阅缓存入站数量超过限制")
		}
		for name, token := range source.Tokens {
			if strings.TrimSpace(name) == "" || !validSubscriptionToken(token) {
				return nil, fmt.Errorf("跨面板订阅缓存 token 无效")
			}
		}
		m.sources[id] = source
	}
	return m, nil
}

func cloneInboundList(inbounds []Inbound) []Inbound {
	if len(inbounds) == 0 {
		return nil
	}
	data, _ := json.Marshal(inbounds)
	var copy []Inbound
	_ = json.Unmarshal(data, &copy)
	return copy
}

func (m *CrossSubscriptionManager) persistLocked() error {
	disk := crossSubscriptionCacheDisk{Version: crossSubscriptionCacheVersion, Sources: map[string]crossSubscriptionCacheSource{}}
	for id, source := range m.sources {
		source.Inbounds = cloneInboundList(source.Inbounds)
		disk.Sources[id] = source
	}
	data, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(m.path), ".cross-panel-subscriptions-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), m.path)
}

func (m *CrossSubscriptionManager) snapshot() map[string]crossSubscriptionCacheSource {
	m.mu.Lock()
	defer m.mu.Unlock()
	copy := map[string]crossSubscriptionCacheSource{}
	for id, source := range m.sources {
		source.Inbounds = cloneInboundList(source.Inbounds)
		source.Tokens = cloneStringMap(source.Tokens)
		copy[id] = source
	}
	return copy
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	copy := make(map[string]string, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func orderedCrossSources(values map[string]crossSubscriptionCacheSource) []crossSubscriptionCacheSource {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]crossSubscriptionCacheSource, 0, len(ids))
	for _, id := range ids {
		result = append(result, values[id])
	}
	return result
}

func aggregateCrossTokens(local map[string]string, sources map[string]crossSubscriptionCacheSource) map[string]string {
	result, owners := map[string]string{}, map[string]string{}
	names := make([]string, 0, len(local))
	for name := range local {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		token := local[name]
		if !validSubscriptionToken(token) {
			continue
		}
		if owner, exists := owners[token]; exists && owner != name {
			continue
		}
		result[name], owners[token] = token, name
	}
	for _, source := range orderedCrossSources(sources) {
		names = names[:0]
		for name := range source.Tokens {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			token := source.Tokens[name]
			if _, exists := result[name]; exists || !validSubscriptionToken(token) {
				continue
			}
			if owner, exists := owners[token]; exists && owner != name {
				continue
			}
			result[name], owners[token] = token, name
		}
	}
	return result
}
func (m *CrossSubscriptionManager) update(node PeerNode, inbounds []Inbound, tokens map[string]string, pullErr error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous, existed := m.sources[node.ID]
	source := m.sources[node.ID]
	source.Node = node
	if pullErr == nil {
		source.Inbounds = cloneInboundList(inbounds)
		source.Tokens = map[string]string{}
		for name, token := range tokens {
			if strings.TrimSpace(name) != "" && validSubscriptionToken(token) {
				source.Tokens[name] = token
			}
		}
		source.RetrievedAt = time.Now()
		source.LastError = ""
	} else {
		source.LastError = pullErr.Error()
	}
	m.sources[node.ID] = source
	if err := m.persistLocked(); err != nil {
		if existed {
			m.sources[node.ID] = previous
		} else {
			delete(m.sources, node.ID)
		}
		return err
	}
	return nil
}
func (m *CrossSubscriptionManager) clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := m.sources
	m.sources = map[string]crossSubscriptionCacheSource{}
	if err := m.persistLocked(); err != nil {
		m.sources = previous
		return err
	}
	return nil
}

func crossSubscriptionHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func matchCrossPanelSubscriptionPath(settings Settings, requestPath string) (token string, format subscriptionFormat, ok bool) {
	prefix := normalizeSubscriptionPath(settings.CrossPanelSubscriptionPath)
	if prefix == "" || !strings.HasPrefix(requestPath, prefix+"/") {
		return "", subscriptionFormatPage, false
	}
	remaining, format := detectSubscriptionFormat(settings, strings.TrimPrefix(requestPath, prefix))
	token = strings.Trim(remaining, "/")
	return token, format, validSubscriptionToken(token)
}

func (a *App) crossSubscriptionSnapshot(requestPath string) (subscriptionSnapshot, subscriptionFormat, bool) {
	if a.cross == nil {
		return subscriptionSnapshot{}, subscriptionFormatPage, false
	}
	a.manager.mu.Lock()
	settings := a.manager.state.Settings
	token, format, ok := matchCrossPanelSubscriptionPath(settings, requestPath)
	if !ok {
		a.manager.mu.Unlock()
		return subscriptionSnapshot{}, subscriptionFormatPage, false
	}
	username := ""
	for name, candidate := range a.manager.state.SubscriptionTokens {
		if candidate == token {
			username = name
			break
		}
	}
	local := cloneInboundList(a.manager.state.Inbounds)
	a.manager.mu.Unlock()
	sources := a.cross.snapshot()
	if username == "" {
		for name, candidate := range aggregateCrossTokens(nil, sources) {
			if candidate == token {
				username = name
				break
			}
		}
	}
	if username == "" {
		return subscriptionSnapshot{}, subscriptionFormatPage, false
	}
	snapshot := subscriptionSnapshot{Settings: settings, Username: username, Token: token}
	for _, inbound := range local {
		for _, client := range inbound.Clients {
			if subscriptionUsername(client) == username {
				snapshot.Clients = append(snapshot.Clients, subscriptionClient{Inbound: inbound, Client: client})
			}
		}
	}
	for _, source := range orderedCrossSources(sources) {
		server := crossSubscriptionHost(source.Node.Endpoint)
		if server == "" {
			continue
		}
		for _, inbound := range source.Inbounds {
			for _, client := range inbound.Clients {
				if subscriptionUsername(client) == username {
					snapshot.Clients = append(snapshot.Clients, subscriptionClient{Inbound: inbound, Client: client, Server: server})
				}
			}
		}
	}
	sort.SliceStable(snapshot.Clients, func(i, j int) bool {
		if snapshot.Clients[i].Inbound.Name != snapshot.Clients[j].Inbound.Name {
			return snapshot.Clients[i].Inbound.Name < snapshot.Clients[j].Inbound.Name
		}
		if snapshot.Clients[i].Server != snapshot.Clients[j].Server {
			return snapshot.Clients[i].Server < snapshot.Clients[j].Server
		}
		return subscriptionUsername(snapshot.Clients[i].Client) < subscriptionUsername(snapshot.Clients[j].Client)
	})
	return snapshot, format, len(snapshot.Clients) > 0
}

func crossSubscriptionInbound(in Inbound) Inbound {
	clients := make([]Client, 0, len(in.Clients))
	for _, client := range in.Clients {
		clients = append(clients, Client{Name: client.Name, Username: client.Username, Password: client.Password, UUID: client.UUID, Flow: client.Flow, AlterID: client.AlterID, Enabled: client.Enabled, Total: client.Total, ExpiryTime: client.ExpiryTime, Traffic: client.Traffic, LastOnline: client.LastOnline, FirstOnline: client.FirstOnline, ExpireAfterDays: client.ExpireAfterDays})
	}
	return Inbound{ID: in.ID, Name: in.Name, Type: in.Type, Port: in.Port, Enabled: in.Enabled, UDP: in.UDP, Cipher: in.Cipher, Network: in.Network, WSPath: in.WSPath, Host: in.Host, TLS: in.TLS, Notes: in.Notes, Clients: clients, Traffic: in.Traffic, Total: in.Total, ExpiryTime: in.ExpiryTime, GRPCServiceName: in.GRPCServiceName, Encryption: in.Encryption, TLSServerName: in.TLSServerName, AllowInsecure: in.AllowInsecure,
		Reality:   RealitySettings{Enabled: in.Reality.Enabled, PublicKey: in.Reality.PublicKey, ShortIDs: in.Reality.ShortIDs, ServerNames: in.Reality.ServerNames, Fingerprint: in.Reality.Fingerprint},
		ShadowTLS: ShadowTLSSettings{Enabled: in.ShadowTLS.Enabled}, RestTLS: RestTLSSettings{Enabled: in.RestTLS.Enabled}, JLS: JLSSettings{Enabled: in.JLS.Enabled}, TrojanSS: TrojanSSSettings{Enabled: in.TrojanSS.Enabled}, TLSMirror: TLSMirrorSettings{Enabled: in.TLSMirror.Enabled},
		XHTTP: XHTTPSettings{Enabled: in.XHTTP.Enabled, Path: in.XHTTP.Path, Host: in.XHTTP.Host, Mode: in.XHTTP.Mode, XPaddingBytes: in.XHTTP.XPaddingBytes}, Hysteria: HysteriaSettings{Obfs: in.Hysteria.Obfs, ObfsPassword: in.Hysteria.ObfsPassword, Up: in.Hysteria.Up, Down: in.Hysteria.Down}, TUIC: TUICSettings{CongestionController: in.TUIC.CongestionController}, Mieru: MieruSettings{Transport: in.Mieru.Transport}, SimpleObfs: in.SimpleObfs, KcpTun: KcpTunSettings{Enabled: in.KcpTun.Enabled}, MKCP: in.MKCP, Mekya: MekyaSettings{Enabled: in.Mekya.Enabled, URL: in.Mekya.URL, H2PoolSize: in.Mekya.H2PoolSize, MaxWriteDelay: in.Mekya.MaxWriteDelay, MaxRequestSize: in.Mekya.MaxRequestSize, PollingIntervalInitial: in.Mekya.PollingIntervalInitial}}
}

func (a *App) crossPanelPullInbounds() ([]Inbound, map[string]string, error) {
	a.manager.mu.Lock()
	inbounds := cloneInboundList(a.manager.state.Inbounds)
	tokens := map[string]string{}
	for name, token := range a.manager.state.SubscriptionTokens {
		if validSubscriptionToken(token) {
			tokens[name] = token
		}
	}
	a.manager.mu.Unlock()
	if len(inbounds) > maxSyncInbounds {
		return nil, nil, fmt.Errorf("本机入站数量超过同步上限")
	}
	for i := range inbounds {
		inbounds[i] = crossSubscriptionInbound(inbounds[i])
	}
	return inbounds, tokens, nil
}

func crossSubscriptionLinks(snapshot subscriptionSnapshot, server string) []subscriptionNode {
	items := slices.Clone(snapshot.Clients)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Inbound.Name != items[j].Inbound.Name {
			return items[i].Inbound.Name < items[j].Inbound.Name
		}
		if items[i].Server != items[j].Server {
			return items[i].Server < items[j].Server
		}
		return subscriptionUsername(items[i].Client) < subscriptionUsername(items[j].Client)
	})
	nodes := make([]subscriptionNode, 0, len(items))
	now := time.Now().UnixMilli()
	for _, item := range items {
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
	return nodes
}

func (a *App) crossSubscriptionView(r *http.Request) crossSubscriptionView {
	view := crossSubscriptionView{}
	if a.peers != nil {
		view.Peers = a.trPeerView(a.peers.view()).Peers
	}
	a.manager.mu.Lock()
	settings := a.manager.state.Settings
	tokens := map[string]string{}
	for name, token := range a.manager.state.SubscriptionTokens {
		if validSubscriptionToken(token) {
			tokens[name] = token
		}
	}
	a.manager.mu.Unlock()
	base := crossSubscriptionPublicOrigin(settings, r)
	sources := a.cross.snapshot()
	allTokens := aggregateCrossTokens(tokens, sources)
	for name, token := range allTokens {
		view.Users = append(view.Users, crossSubscriptionUser{Username: name, Token: token, URL: base + crossPanelSubscriptionPublicPath(settings, token)})
	}
	sort.Slice(view.Users, func(i, j int) bool { return view.Users[i].Username < view.Users[j].Username })
	for _, source := range orderedCrossSources(sources) {
		clients := 0
		for _, inbound := range source.Inbounds {
			clients += len(inbound.Clients)
		}
		view.Sources = append(view.Sources, crossSubscriptionSourceView{Node: source.Node, RetrievedAt: source.RetrievedAt, InboundCount: len(source.Inbounds), ClientCount: clients, LastError: a.tr(source.LastError)})
	}
	sort.Slice(view.Sources, func(i, j int) bool { return view.Sources[i].Node.ID < view.Sources[j].Node.ID })
	a.cross.mu.Lock()
	view.ActiveJob = a.cross.active
	a.cross.mu.Unlock()
	return view
}

func crossSubscriptionPublicOrigin(settings Settings, r *http.Request) string {
	host := subscriptionPublicHost(settings, r)
	if settings.SubscriptionPort != 0 {
		name := strings.TrimSpace(settings.PanelDomain)
		if name == "" {
			candidate := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0])
			if candidate == "" {
				candidate = r.Host
			}
			if parsed, _, err := net.SplitHostPort(candidate); err == nil {
				name = parsed
			} else {
				name = strings.Trim(candidate, "[]")
			}
		}
		host = net.JoinHostPort(name, strconv.Itoa(settings.SubscriptionPort))
	}
	return subscriptionScheme(r) + "://" + host
}

func (a *App) handleCrossSubscriptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.cross == nil || a.peers == nil {
		writeJSON(w, 503, apiResponse{Message: a.tr("跨面板订阅未初始化")})
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/cross-subscriptions")
	if action != "" && action != "/" {
		writeJSON(w, 405, apiResponse{Message: "method not allowed"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		if id := r.URL.Query().Get("job"); id != "" {
			a.cross.mu.Lock()
			job := a.cross.jobs[id]
			if job == nil {
				a.cross.mu.Unlock()
				writeJSON(w, 404, apiResponse{Message: a.tr("拉取任务不存在")})
				return
			}
			copy := *job
			copy.Targets = slices.Clone(job.Targets)
			a.cross.mu.Unlock()
			for i := range copy.Targets {
				copy.Targets[i].Message = a.tr(copy.Targets[i].Message)
			}
			writeJSON(w, 200, apiResponse{OK: true, Data: &copy})
			return
		}
		writeJSON(w, 200, apiResponse{OK: true, Data: a.crossSubscriptionView(r)})
		return
	case http.MethodDelete:
		a.cross.mu.Lock()
		active := a.cross.active != ""
		a.cross.mu.Unlock()
		if active {
			writeJSON(w, 409, apiResponse{Message: a.tr("当前正在拉取，完成后再清除缓存")})
			return
		}
		if err := a.cross.clear(); err != nil {
			writeJSON(w, 500, apiResponse{Message: a.tr("清除跨面板订阅缓存失败")})
			return
		}
		writeJSON(w, 200, apiResponse{OK: true, Data: a.crossSubscriptionView(r)})
		return
	case http.MethodPost:
		if action != "" && action != "/" {
			writeJSON(w, 405, apiResponse{Message: "method not allowed"})
			return
		}
		if err := a.startCrossSubscriptionPull(); err != nil {
			writeJSON(w, 409, apiResponse{Message: a.tr(err.Error())})
			return
		}
		writeJSON(w, 202, apiResponse{OK: true, Data: a.crossSubscriptionView(r)})
		return
	default:
		writeJSON(w, 405, apiResponse{Message: "method not allowed"})
		return
	}
}

func (a *App) startCrossSubscriptionPull() error {
	peers := a.peers.view().Peers
	if len(peers) == 0 {
		return fmt.Errorf("暂无已配对面板")
	}
	a.cross.mu.Lock()
	defer a.cross.mu.Unlock()
	if a.cross.active != "" {
		return fmt.Errorf("已有跨面板节点拉取任务正在进行")
	}
	id, err := newPairToken()
	if err != nil {
		return err
	}
	job := &crossSubscriptionJob{ID: id, Started: time.Now(), Targets: []crossPullTarget{}}
	for _, peer := range peers {
		job.Targets = append(job.Targets, crossPullTarget{ID: peer.ID, Endpoint: peer.Endpoint, Status: "pending"})
	}
	if len(a.cross.jobs) >= maxCrossPullJobs {
		var oldest *crossSubscriptionJob
		for _, candidate := range a.cross.jobs {
			if oldest == nil || candidate.Started.Before(oldest.Started) {
				oldest = candidate
			}
		}
		if oldest != nil {
			delete(a.cross.jobs, oldest.ID)
		}
	}
	a.cross.jobs[id] = job
	a.cross.active = id
	go a.runCrossSubscriptionPull(id, peers)
	return nil
}

func (a *App) runCrossSubscriptionPull(id string, peers []peerRow) {
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for index, peer := range peers {
		sem <- struct{}{}
		wg.Add(1)
		go func(index int, peer peerRow) {
			defer wg.Done()
			defer func() { <-sem }()
			a.cross.mu.Lock()
			if job := a.cross.jobs[id]; job != nil {
				job.Targets[index].Status = "running"
			}
			a.cross.mu.Unlock()
			var inbounds []Inbound
			var tokens map[string]string
			var err error
			if !peer.Online {
				err = fmt.Errorf("面板当前离线")
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				inbounds, tokens, err = a.sendInboundPull(ctx, peer.PeerNode)
				cancel()
			}
			if updateErr := a.cross.update(peer.PeerNode, inbounds, tokens, err); updateErr != nil && err == nil {
				err = fmt.Errorf("缓存保存失败")
			}
			a.cross.mu.Lock()
			if job := a.cross.jobs[id]; job != nil {
				target := &job.Targets[index]
				job.Completed++
				target.Status = "success"
				target.Inbounds = len(inbounds)
				for _, in := range inbounds {
					target.Clients += len(in.Clients)
				}
				if err != nil {
					target.Status = "failed"
					target.Message = err.Error()
				}
			}
			a.cross.mu.Unlock()
		}(index, peer)
	}
	wg.Wait()
	a.cross.mu.Lock()
	if job := a.cross.jobs[id]; job != nil {
		job.Done = true
	}
	a.cross.active = ""
	a.cross.mu.Unlock()
}
