package app

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const maxSyncInbounds = 256
const maxSyncClients = 4096
const maxSyncPayload = 4 << 20

// Origin is assigned by the receiving panel from the authenticated sender.
// It never grants permission to update an unrelated, locally-created inbound.
type InboundSyncOrigin struct {
	NodeID    string `json:"nodeId"`
	InboundID string `json:"inboundId"`
}

type inboundSyncSelection struct {
	InboundID string   `json:"inboundId"`
	Revision  string   `json:"revision"`
	ClientIDs []string `json:"clientIds"`
}
type inboundSyncRequest struct {
	JobID      string                 `json:"jobId"`
	PeerIDs    []string               `json:"peerIds"`
	Selections []inboundSyncSelection `json:"selections"`
}
type inboundSyncResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Clients int    `json:"clients"`
}
type inboundSyncTarget struct {
	ID       string              `json:"id"`
	Endpoint string              `json:"endpoint"`
	Status   string              `json:"status"`
	Message  string              `json:"message,omitempty"`
	Results  []inboundSyncResult `json:"results"`
}
type inboundSyncJob struct {
	ID        string              `json:"id"`
	Done      bool                `json:"done"`
	Completed int                 `json:"completed"`
	Started   time.Time           `json:"started"`
	Targets   []inboundSyncTarget `json:"targets"`
}
type inboundSyncState struct {
	mu       sync.Mutex
	jobs     map[string]*inboundSyncJob
	sessions map[string]inboundSyncSession
	active   string
}
type inboundSyncColumn struct {
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Type     string              `json:"type"`
	Port     int                 `json:"port"`
	Revision string              `json:"revision"`
	Users    map[string][]string `json:"users"`
}

func cloneSyncState(state State) State {
	data, _ := json.Marshal(state)
	var copy State
	_ = json.Unmarshal(data, &copy)
	return copy
}

// Runtime counters do not invalidate a user's selection while the dialog is open.
func clearSyncCounters(inbound *Inbound) {
	inbound.Traffic = TrafficStats{}
	inbound.CreatedAt, inbound.LastTrafficReset = "", ""
	for i := range inbound.Clients {
		c := &inbound.Clients[i]
		c.Traffic = TrafficStats{}
		c.CreatedAt, c.FirstOnline, c.LastOnline = "", "", ""
	}
}
func inboundSyncRevision(inbound Inbound) string {
	inbound.Clients = slices.Clone(inbound.Clients)
	clearSyncCounters(&inbound)
	data, _ := json.Marshal(inbound)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func syncedObjectID(parts ...string) string {
	data, _ := json.Marshal(parts)
	sum := sha256.Sum256(data)
	return "sync-" + hex.EncodeToString(sum[:12])
}

func (a *App) handleInboundSync(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.peers == nil {
		writeJSON(w, 503, apiResponse{Message: a.tr("Multi-control 未初始化")})
		return
	}
	if r.Method == http.MethodGet {
		if id := r.URL.Query().Get("job"); id != "" {
			a.inboundSync.mu.Lock()
			defer a.inboundSync.mu.Unlock()
			job := a.inboundSync.jobs[id]
			if job == nil {
				writeJSON(w, 404, apiResponse{Message: a.tr("同步任务不存在或已过期，请检查目标面板后重新操作")})
				return
			}
			writeJSON(w, 200, apiResponse{OK: true, Data: a.trSyncJob(job)})
			return
		}
		a.manager.mu.Lock()
		columns := []inboundSyncColumn{}
		for _, in := range a.manager.state.Inbounds {
			col := inboundSyncColumn{ID: in.ID, Name: in.Name, Type: in.Type, Port: in.Port, Revision: inboundSyncRevision(in), Users: map[string][]string{}}
			for _, c := range in.Clients {
				if name := subscriptionUsername(c); name != "" && c.ID != "" {
					col.Users[name] = append(col.Users[name], c.ID)
				}
			}
			columns = append(columns, col)
		}
		a.manager.mu.Unlock()
		peers := []peerRow{}
		for _, peer := range a.trPeerView(a.peers.view()).Peers {
			if peer.Online {
				peers = append(peers, peer)
			}
		}
		a.inboundSync.mu.Lock()
		active := a.inboundSync.active
		a.inboundSync.mu.Unlock()
		writeJSON(w, 200, apiResponse{OK: true, Data: map[string]any{"peers": peers, "inbounds": columns, "activeJob": active}})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, 405, apiResponse{Message: "method not allowed"})
		return
	}
	var input inboundSyncRequest
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, 400, apiResponse{Message: a.tr("同步选项无效")})
		return
	}
	if !peerNoncePattern.MatchString(input.JobID) {
		writeJSON(w, 400, apiResponse{Message: a.tr("任务 ID 无效")})
		return
	}
	a.inboundSync.mu.Lock()
	defer a.inboundSync.mu.Unlock()
	if job := a.inboundSync.jobs[input.JobID]; job != nil {
		writeJSON(w, 200, apiResponse{OK: true, Data: a.trSyncJob(job)})
		return
	}
	if a.inboundSync.active != "" {
		writeJSON(w, 409, apiResponse{Message: a.tr("已有同步任务正在进行")})
		return
	}
	if len(input.PeerIDs) == 0 || len(input.PeerIDs) > maxMeshPeers {
		writeJSON(w, 400, apiResponse{Message: a.tr("请选择目标面板")})
		return
	}
	view := a.peers.view()
	available := map[string]peerRow{}
	for _, peer := range view.Peers {
		available[peer.ID] = peer
	}
	nodes := []PeerNode{}
	seen := map[string]bool{}
	for _, id := range input.PeerIDs {
		peer, ok := available[id]
		if !ok || !peer.Online || seen[id] {
			writeJSON(w, 400, apiResponse{Message: a.tr("目标面板已离线、断开或重复，请重新打开选择窗口")})
			return
		}
		seen[id] = true
		nodes = append(nodes, peer.PeerNode)
	}
	a.manager.mu.Lock()
	snapshot := cloneSyncState(a.manager.state)
	a.manager.mu.Unlock()
	items, err := selectSyncInbounds(snapshot.Inbounds, input.Selections)
	if err != nil {
		writeJSON(w, 400, apiResponse{Message: a.tr(err.Error())})
		return
	}
	// Only configured inbound certificates are materialized, never arbitrary paths
	// supplied by a remote peer. Imported certificates are inline PEM on the target.
	for i := range items {
		if err = portableSyncCertificates(&items[i]); err != nil {
			writeJSON(w, 400, apiResponse{Message: a.tr(fmt.Sprintf("入口 %s: %v", items[i].Name, err))})
			return
		}
	}
	payload, _ := json.Marshal(items)
	if len(payload) > maxSyncPayload {
		writeJSON(w, 400, apiResponse{Message: a.tr("同步内容超过 4 MiB，请分批同步")})
		return
	}
	job := &inboundSyncJob{ID: input.JobID, Started: time.Now(), Targets: []inboundSyncTarget{}}
	for _, node := range nodes {
		job.Targets = append(job.Targets, inboundSyncTarget{ID: node.ID, Endpoint: node.Endpoint, Status: "pending", Results: []inboundSyncResult{}})
	}
	if a.inboundSync.jobs == nil {
		a.inboundSync.jobs = map[string]*inboundSyncJob{}
	}
	if len(a.inboundSync.jobs) >= 20 {
		var oldest *inboundSyncJob
		for _, j := range a.inboundSync.jobs {
			if oldest == nil || j.Started.Before(oldest.Started) {
				oldest = j
			}
		}
		delete(a.inboundSync.jobs, oldest.ID)
	}
	a.inboundSync.jobs[job.ID] = job
	a.inboundSync.active = job.ID
	// The task belongs to the server, not to the browser request context.
	go a.runInboundSyncJob(job.ID, nodes, items)
	writeJSON(w, 202, apiResponse{OK: true, Data: job})
}

func selectSyncInbounds(inbounds []Inbound, selections []inboundSyncSelection) ([]Inbound, error) {
	if len(selections) == 0 || len(selections) > maxSyncInbounds {
		return nil, fmt.Errorf("请选择 1-%d 个入站", maxSyncInbounds)
	}
	lookup := map[string]Inbound{}
	for _, in := range inbounds {
		lookup[in.ID] = in
	}
	seen := map[string]bool{}
	result := []Inbound{}
	total := 0
	for _, selection := range selections {
		in, ok := lookup[selection.InboundID]
		if !ok || seen[in.ID] || selection.Revision != inboundSyncRevision(in) {
			return nil, fmt.Errorf("入站已变化或选择重复，请重新打开同步窗口")
		}
		seen[in.ID] = true
		if len(selection.ClientIDs) == 0 {
			return nil, fmt.Errorf("入口 %s 未选择客户端", in.Name)
		}
		clients := map[string]Client{}
		for _, c := range in.Clients {
			clients[c.ID] = c
		}
		in.Clients = []Client{}
		selected := map[string]bool{}
		for _, id := range selection.ClientIDs {
			c, ok := clients[id]
			if !ok || id == "" || selected[id] || subscriptionUsername(c) == "" {
				return nil, fmt.Errorf("入口 %s 的客户端选择无效", in.Name)
			}
			selected[id] = true
			in.Clients = append(in.Clients, c)
			total++
		}
		if total > maxSyncClients {
			return nil, fmt.Errorf("每次最多同步 %d 个客户端，请分批操作", maxSyncClients)
		}
		clearSyncCounters(&in)
		// Legacy fallback credentials must not reintroduce an unselected client.
		in.Username, in.Password, in.UUID = "", "", ""
		in.SyncOrigin = nil
		result = append(result, in)
	}
	return result, nil
}

func portableSyncCertificates(in *Inbound) error {
	for _, field := range []*string{&in.Certificate, &in.PrivateKey, &in.ClientAuthCert} {
		if strings.TrimSpace(*field) == "" || strings.Contains(*field, "-----BEGIN ") {
			continue
		}
		file, err := os.Open(*field)
		if err != nil {
			return fmt.Errorf("无法读取入站配置引用的证书文件")
		}
		data, err := io.ReadAll(io.LimitReader(file, 256<<10+1))
		file.Close()
		if err != nil || len(data) > 256<<10 || !strings.Contains(string(data), "-----BEGIN ") {
			return fmt.Errorf("证书文件无效或超过 256 KiB")
		}
		*field = string(data)
	}
	if in.Certificate != "" || in.PrivateKey != "" {
		in.CertificateMode = "content"
	}
	return validateSyncCertificates(*in)
}
func validateSyncCertificates(in Inbound) error {
	for _, value := range []string{in.Certificate, in.PrivateKey, in.ClientAuthCert} {
		if value != "" && !strings.Contains(value, "-----BEGIN ") {
			return fmt.Errorf("同步证书必须为 PEM 内容，不能引用目标本地路径")
		}
	}
	if in.Certificate != "" || in.PrivateKey != "" {
		if _, err := tls.X509KeyPair([]byte(in.Certificate), []byte(in.PrivateKey)); err != nil {
			return fmt.Errorf("入站证书与私钥无效或不匹配")
		}
	}
	if in.ClientAuthCert != "" && !x509.NewCertPool().AppendCertsFromPEM([]byte(in.ClientAuthCert)) {
		return fmt.Errorf("客户端 CA 证书内容无效")
	}
	return nil
}

func (a *App) runInboundSyncJob(id string, nodes []PeerNode, items []Inbound) {
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for index, node := range nodes {
		sem <- struct{}{}
		wg.Add(1)
		go func(index int, node PeerNode) {
			defer wg.Done()
			defer func() { <-sem }()
			a.inboundSync.mu.Lock()
			a.inboundSync.jobs[id].Targets[index].Status = "running"
			a.inboundSync.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			results, err := a.sendInboundSync(ctx, node, items)
			a.inboundSync.mu.Lock()
			defer a.inboundSync.mu.Unlock()
			job := a.inboundSync.jobs[id]
			target := &job.Targets[index]
			target.Status = "success"
			if err != nil {
				target.Status = "failed"
				target.Message = err.Error()
			} else {
				target.Results = results
				failed := 0
				for _, r := range results {
					if r.Status == "failed" {
						failed++
					}
				}
				if failed == len(results) {
					target.Status = "failed"
				} else if failed > 0 {
					target.Status = "partial"
				}
			}
			job.Completed++
		}(index, node)
	}
	wg.Wait()
	a.inboundSync.mu.Lock()
	a.inboundSync.jobs[id].Done = true
	a.inboundSync.active = ""
	a.inboundSync.mu.Unlock()
}

func (m *CoreManager) receiveSyncedInbounds(sender string, items []Inbound) []inboundSyncResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := cloneSyncState(m.state)
	results := make([]inboundSyncResult, 0, len(items))
	changed := false
	for _, in := range items {
		result := inboundSyncResult{ID: in.ID, Name: in.Name, Status: "failed", Clients: len(in.Clients)}
		candidate := cloneSyncState(next)
		status, err := mergeSyncedInbound(&candidate, sender, in)
		if err == nil {
			_, err = renderStateConfig(candidate)
		}
		if err != nil {
			result.Message = err.Error()
		} else {
			next = candidate
			changed = true
			result.Status = status
			result.Message = "已保存，重启目标 Mihomo 后生效"
		}
		results = append(results, result)
	}
	if !changed {
		return results
	}
	config, err := renderStateConfig(next)
	if err == nil {
		staging := &CoreManager{state: next}
		err = staging.ensureSubscriptionTokensLocked(allStateClients(next.Inbounds))
		next = staging.state
	}
	// Stage the generated YAML before changing authoritative state. A failed
	// write leaves all target inbounds untouched; the old YAML is restored if
	// persisting the new state fails. Startup also regenerates YAML from state.
	path := filepath.Join(m.dataDir, "config.yaml")
	var oldConfig []byte
	var oldErr error
	if err == nil {
		oldConfig, oldErr = os.ReadFile(path)
		if oldErr != nil && !os.IsNotExist(oldErr) {
			err = oldErr
		}
	}
	if err == nil {
		err = writeSyncFile(path, config)
	}
	if err == nil {
		previous := m.state
		m.state = next
		if err = m.saveLocked(); err != nil {
			m.state = previous
			var rollback error
			if oldErr == nil {
				rollback = writeSyncFile(path, oldConfig)
			} else {
				rollback = os.Remove(path)
			}
			if rollback != nil {
				err = fmt.Errorf("保存失败且 YAML 回滚失败，请在目标面板重新生成配置: %w", err)
			}
		}
	}
	if err != nil {
		for i := range results {
			if results[i].Status != "failed" {
				results[i].Status = "failed"
				results[i].Message = "保存目标配置失败: " + err.Error()
			}
		}
	}
	return results
}

func writeSyncFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".sync-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func mergeSyncedInbound(state *State, sender string, in Inbound) (string, error) {
	if in.ID == "" || len(in.ID) > 128 || in.Name == "" || len(in.Name) > 256 || len(in.Clients) == 0 || len(in.Clients) > maxSyncClients {
		return "", fmt.Errorf("入站或客户端信息无效")
	}
	originID := in.ID
	in.ID = syncedObjectID(sender, originID)
	index := -1
	for i, old := range state.Inbounds {
		if old.SyncOrigin != nil && old.SyncOrigin.NodeID == sender && old.SyncOrigin.InboundID == originID {
			index = i
			in.ID = old.ID
			break
		}
		if old.ID == in.ID {
			return "", fmt.Errorf("入站 ID 与目标已有入站冲突")
		}
	}
	in.SyncOrigin = &InboundSyncOrigin{NodeID: sender, InboundID: originID}
	clearSyncCounters(&in)
	in.CreatedAt = time.Now().Format(time.RFC3339)
	clientIDs := map[string]bool{}
	for i := range in.Clients {
		c := &in.Clients[i]
		if c.ID == "" || len(c.ID) > 128 || clientIDs[c.ID] || subscriptionUsername(*c) == "" {
			return "", fmt.Errorf("客户端身份无效")
		}
		clientIDs[c.ID] = true
		c.ID = syncedObjectID(sender, originID, c.ID)
	}
	status := "created"
	if index >= 0 {
		old := state.Inbounds[index]
		if old.Type != in.Type {
			return "", fmt.Errorf("同步来源的协议已变化，请在目标确认后移除旧副本")
		}
		status = "updated"
		in.Traffic = old.Traffic
		in.CreatedAt = old.CreatedAt
		in.LastTrafficReset = old.LastTrafficReset
		incoming := mergeClientCounters(old.Clients, in.Clients)
		merged := slices.Clone(old.Clients)
		for _, c := range incoming {
			found := -1
			for i, existing := range merged {
				if existing.ID == c.ID {
					found = i
					break
				}
			}
			if found < 0 {
				merged = append(merged, c)
			} else {
				merged[found] = c
			}
		}
		in.Clients = merged
	}
	if len(in.Clients) > maxSyncClients {
		return "", fmt.Errorf("目标入站合并后超过 %d 个 Client", maxSyncClients)
	}
	for i := range in.Clients {
		if in.Clients[i].CreatedAt == "" {
			in.Clients[i].CreatedAt = time.Now().Format(time.RFC3339)
		}
	}
	if singleSecretType(in.Type) && len(in.Clients) != 1 {
		return "", fmt.Errorf("%s 仅支持单 Client，目标已存在另一 Client", in.Type)
	}
	if in.Listen == "" {
		in.Listen = "0.0.0.0"
	}
	if ip := net.ParseIP(in.Listen); ip == nil {
		return "", fmt.Errorf("入站监听地址必须是有效 IP")
	}
	if in.Port < 1 || in.Port > 65535 {
		return "", fmt.Errorf("端口必须在 1-65535 之间")
	}
	if in.TrafficReset == "" {
		in.TrafficReset = "never"
	}
	if err := validateSyncCertificates(in); err != nil {
		return "", err
	}
	if err := normalizeInboundSecrets(&in); err != nil {
		return "", err
	}
	if err := validateInbound(in); err != nil {
		return "", err
	}
	if _, err := inboundConfig(in); err != nil {
		return "", err
	}
	for i, existing := range state.Inbounds {
		if i == index {
			continue
		}
		if existing.Name == in.Name {
			return "", fmt.Errorf("名称 %q 已被目标本地入站占用", in.Name)
		}
		if existing.Port == in.Port && listenAddressesOverlap(existing.Listen, in.Listen) {
			return "", fmt.Errorf("端口 %d 已被目标入站 %q 占用", in.Port, existing.Name)
		}
	}
	if conflict := reservedPanelPortConflict(state.Settings, in.Listen, in.Port); conflict != "" {
		return "", fmt.Errorf("端口 %d 与%s冲突", in.Port, conflict)
	}
	// Proxy/rule names reference the target panel's routing configuration.
	for _, target := range []string{in.Proxy, in.Rule, in.Reality.Proxy, in.JLS.Proxy, in.TLSMirror.Proxy} {
		if target == "" || containsString(mihomoBuiltins, target) {
			continue
		}
		if !slices.ContainsFunc(state.Outbounds, func(out MihomoOutbound) bool { return out.Name == target }) {
			return "", fmt.Errorf("目标缺少路由出站 %q，请先在目标配置", target)
		}
	}
	if index < 0 {
		state.Inbounds = append(state.Inbounds, in)
	} else {
		state.Inbounds[index] = in
	}
	return status, nil
}
