package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"sort"
	"strings"
	"time"
)

type connectionSample struct {
	Upload      uint64
	Download    uint64
	InboundName string
	InboundUser string
}

// externalInboundTraffic 对应 3x-ui 的 xray.Traffic。那个结构体没写 json tag，
// 所以线上格式就是大写字段名，这里照抄，为 3x-ui 写的接收端可以直接复用。
type externalInboundTraffic struct {
	IsInbound  bool
	IsOutbound bool
	Tag        string
	Up         int64
	Down       int64
}

// externalClientTraffic 对应 3x-ui 的 xray.ClientTraffic。m-ui 的主键是字符串、
// 客户端也没有 email 概念，所以 id/inboundId 用字符串，email 位置放客户端名。
type externalClientTraffic struct {
	ID         string `json:"id"`
	InboundID  string `json:"inboundId"`
	Enable     bool   `json:"enable"`
	Email      string `json:"email"`
	UUID       string `json:"uuid,omitempty"`
	Up         int64  `json:"up"`
	Down       int64  `json:"down"`
	AllTime    int64  `json:"allTime"`
	ExpiryTime int64  `json:"expiryTime"`
	Total      int64  `json:"total"`
	LastOnline int64  `json:"lastOnline"`
}

// 3x-ui 的流量任务每 10 秒跑一次并顺带上报，m-ui 每秒轮询内核，所以把增量攒起来
// 按同样的节奏发出去，接收端看到的频率和 3x-ui 一致。
const externalTrafficInformPeriod = 10 * time.Second

type clientRequest struct {
	InboundID string `json:"inboundId"`
	Client    Client `json:"client"`
}

func (m *CoreManager) monitorTraffic() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	interval := time.Second
	for range ticker.C {
		m.applyScheduledTrafficResets()
		m.mu.Lock()
		nextInterval := time.Duration(effectiveMihomoBasics(m.state).TrafficSampleSeconds) * time.Second
		if nextInterval != interval {
			ticker.Reset(nextInterval)
			interval = nextInterval
		}
		running := m.runningLocked()
		if !running && (len(m.connectionStats) > 0 || m.coreUploadSample > 0 || m.coreDownloadSample > 0) {
			m.connectionStats = map[string]connectionSample{}
			m.coreUploadSample = 0
			m.coreDownloadSample = 0
		}
		m.mu.Unlock()
		if !running {
			continue
		}
		snapshot, err := m.coreGet("/connections")
		if err != nil {
			continue
		}
		m.applyTrafficSnapshot(snapshot)
		m.enforceClientLimits()
	}
}

func (m *CoreManager) applyScheduledTrafficResets() {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 周期重置要按设置里的时区判断"跨天/跨周/跨月"，否则服务器跑 UTC 而用户在东八区时，
	// 每日重置会拖到当地早上八点才发生。
	location, locationErr := resolveTimeLocation(m.state.Settings.TimeLocation)
	if locationErr != nil {
		location = time.Local
	}
	now := time.Now().In(location)
	changed := false
	for index := range m.state.Inbounds {
		inbound := &m.state.Inbounds[index]
		if inbound.TrafficReset == "" || inbound.TrafficReset == "never" {
			continue
		}
		last := now
		if inbound.LastTrafficReset != "" {
			if parsed, err := time.Parse(time.RFC3339, inbound.LastTrafficReset); err == nil {
				last = parsed.In(location)
			}
		} else if parsed, err := time.Parse(time.RFC3339, inbound.CreatedAt); err == nil {
			last = parsed.In(location)
		}
		if !trafficResetDue(inbound.TrafficReset, last, now) {
			continue
		}
		inbound.Traffic.Up, inbound.Traffic.Down = 0, 0
		for clientIndex := range inbound.Clients {
			inbound.Clients[clientIndex].Traffic.Up, inbound.Clients[clientIndex].Traffic.Down = 0, 0
		}
		inbound.LastTrafficReset = now.Format(time.RFC3339)
		m.addLogLocked(fmt.Sprintf("Inbound %s 已执行 %s 周期流量重置", inbound.Name, inbound.TrafficReset))
		changed = true
	}
	if changed {
		_ = m.saveLocked()
	}
}

func trafficResetDue(period string, last, now time.Time) bool {
	switch period {
	case "hourly":
		return now.Sub(last) >= time.Hour
	case "daily":
		return now.YearDay() != last.YearDay() || now.Year() != last.Year()
	case "weekly":
		nowYear, nowWeek := now.ISOWeek()
		lastYear, lastWeek := last.ISOWeek()
		return nowYear != lastYear || nowWeek != lastWeek
	case "monthly":
		return now.Year() != last.Year() || now.Month() != last.Month()
	default:
		return false
	}
}

func (m *CoreManager) enforceClientLimits() {
	m.mu.Lock()
	now := time.Now().UnixMilli()
	newlyDepleted := false
	activeKeys := map[string]bool{}
	for _, inbound := range m.state.Inbounds {
		for _, client := range inbound.Clients {
			key := inbound.ID + ":" + client.ID
			if clientDepleted(client, now) {
				activeKeys[key] = true
				if client.Enabled && !m.enforcedClients[key] {
					m.enforcedClients[key] = true
					newlyDepleted = true
					m.addLogLocked(fmt.Sprintf("客户端 %s/%s 已到期或耗尽，正在应用认证变更", inbound.Name, client.Name))
				}
			}
		}
	}
	for key := range m.enforcedClients {
		if !activeKeys[key] {
			delete(m.enforcedClients, key)
		}
	}
	running := m.runningLocked()
	m.mu.Unlock()
	if newlyDepleted && running {
		go func() {
			if err := m.restartCore(); err != nil {
				m.mu.Lock()
				m.addLogLocked("应用客户端配额失败: " + err.Error())
				m.mu.Unlock()
			}
		}()
	}
}

func (m *CoreManager) applyTrafficSnapshot(snapshot any) {
	root, ok := snapshot.(map[string]any)
	if !ok {
		return
	}
	connections, _ := root["connections"].([]any)
	coreUpload := uintValue(root["uploadTotal"])
	coreDownload := uintValue(root["downloadTotal"])
	current := make(map[string]connectionSample, len(connections))
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	changed := false
	basics := effectiveMihomoBasics(m.state)
	var outboundTraffic map[string]TrafficStats
	if basics.OutboundUploadStatistics || basics.OutboundDownloadStatistics {
		outboundTraffic = maps.Clone(m.state.OutboundTraffic)
		if outboundTraffic == nil {
			outboundTraffic = map[string]TrafficStats{}
		}
	}
	globalUploadDelta := counterDelta(coreUpload, m.coreUploadSample)
	globalDownloadDelta := counterDelta(coreDownload, m.coreDownloadSample)
	m.coreUploadSample, m.coreDownloadSample = coreUpload, coreDownload
	if globalUploadDelta > 0 || globalDownloadDelta > 0 {
		m.state.Traffic.Up += globalUploadDelta
		m.state.Traffic.Down += globalDownloadDelta
		m.state.Traffic.AllTimeUp += globalUploadDelta
		m.state.Traffic.AllTimeDown += globalDownloadDelta
		m.state.Traffic.UpdatedAt = now.Format(time.RFC3339)
		changed = true
	}
	for _, value := range connections {
		connection, ok := value.(map[string]any)
		if !ok {
			continue
		}
		id := stringValue(connection["id"])
		if id == "" {
			continue
		}
		metadata, _ := connection["metadata"].(map[string]any)
		sample := connectionSample{
			Upload:      uintValue(connection["upload"]),
			Download:    uintValue(connection["download"]),
			InboundName: stringValue(metadata["inboundName"]),
			InboundUser: stringValue(metadata["inboundUser"]),
		}
		current[id] = sample
		previous := m.connectionStats[id]
		uploadDelta := counterDelta(sample.Upload, previous.Upload)
		downloadDelta := counterDelta(sample.Download, previous.Download)
		if uploadDelta == 0 && downloadDelta == 0 {
			continue
		}
		if outboundTraffic != nil {
			up, down := uploadDelta, downloadDelta
			if !basics.OutboundUploadStatistics {
				up = 0
			}
			if !basics.OutboundDownloadStatistics {
				down = 0
			}
			chains, _ := connection["chains"].([]any)
			seen := map[string]bool{}
			for _, value := range chains {
				name := stringValue(value)
				if name == "" || seen[name] {
					continue
				}
				seen[name] = true
				stats := outboundTraffic[name]
				addTraffic(&stats, up, down)
				outboundTraffic[name] = stats
			}
		}
		for inboundIndex := range m.state.Inbounds {
			inbound := &m.state.Inbounds[inboundIndex]
			if inbound.Name != sample.InboundName {
				continue
			}
			addTraffic(&inbound.Traffic, uploadDelta, downloadDelta)
			m.recordExternalInboundDeltaLocked(inbound.ID, inbound.Name, uploadDelta, downloadDelta)
			for clientIndex := range inbound.Clients {
				client := &inbound.Clients[clientIndex]
				if sample.InboundUser == "" || !clientMatchesUser(*client, sample.InboundUser) {
					continue
				}
				addTraffic(&client.Traffic, uploadDelta, downloadDelta)
				if client.FirstOnline == "" {
					client.FirstOnline = now.Format(time.RFC3339)
					if client.ExpireAfterDays > 0 && client.ExpiryTime == 0 {
						client.ExpiryTime = now.Add(time.Duration(client.ExpireAfterDays) * 24 * time.Hour).UnixMilli()
					}
				}
				client.LastOnline = now.Format(time.RFC3339)
				m.recordExternalClientDeltaLocked(inbound.ID, *client, uploadDelta, downloadDelta)
				break
			}
			break
		}
		changed = true
	}
	m.connectionStats = current
	if outboundTraffic != nil {
		m.state.OutboundTraffic = outboundTraffic
	}
	if changed && (m.lastTrafficSave.IsZero() || now.Sub(m.lastTrafficSave) >= time.Duration(basics.TrafficSaveSeconds)*time.Second) {
		if err := m.saveLocked(); err == nil {
			m.lastTrafficSave = now
		}
	}
	m.flushExternalTrafficLocked(now)
}

func (m *CoreManager) recordExternalInboundDeltaLocked(id, tag string, upload, download uint64) {
	if !m.state.Settings.ExternalTrafficInformEnable {
		return
	}
	if m.externalTrafficInbounds == nil {
		m.externalTrafficInbounds = map[string]externalInboundTraffic{}
	}
	entry := m.externalTrafficInbounds[id]
	entry.IsInbound, entry.Tag = true, tag
	entry.Up += int64(upload)
	entry.Down += int64(download)
	m.externalTrafficInbounds[id] = entry
}

func (m *CoreManager) recordExternalClientDeltaLocked(inboundID string, client Client, upload, download uint64) {
	if !m.state.Settings.ExternalTrafficInformEnable {
		return
	}
	if m.externalTrafficClients == nil {
		m.externalTrafficClients = map[string]externalClientTraffic{}
	}
	key := inboundID + ":" + client.ID
	entry := m.externalTrafficClients[key]
	entry.ID, entry.InboundID = client.ID, inboundID
	entry.Enable, entry.Email, entry.UUID = client.Enabled, client.Name, client.UUID
	entry.ExpiryTime, entry.Total = client.ExpiryTime, int64(client.Total)
	entry.AllTime = int64(client.Traffic.AllTimeUp + client.Traffic.AllTimeDown)
	entry.LastOnline = parseTimeMillis(client.LastOnline)
	entry.Up += int64(upload)
	entry.Down += int64(download)
	m.externalTrafficClients[key] = entry
}

// flushExternalTrafficLocked 按 3x-ui 的 10 秒节奏把攒下的增量发出去。调用方必须持有 m.mu。
func (m *CoreManager) flushExternalTrafficLocked(now time.Time) {
	if !m.state.Settings.ExternalTrafficInformEnable {
		// 开关关掉之后别把老数据留着，下次打开时一股脑上报。
		m.externalTrafficInbounds, m.externalTrafficClients = nil, nil
		return
	}
	if m.lastTrafficInform.IsZero() {
		m.lastTrafficInform = now
		return
	}
	if now.Sub(m.lastTrafficInform) < externalTrafficInformPeriod {
		return
	}
	m.lastTrafficInform = now
	if len(m.externalTrafficInbounds) == 0 && len(m.externalTrafficClients) == 0 {
		return
	}
	inbounds := make([]externalInboundTraffic, 0, len(m.externalTrafficInbounds))
	for _, entry := range m.externalTrafficInbounds {
		inbounds = append(inbounds, entry)
	}
	clients := make([]externalClientTraffic, 0, len(m.externalTrafficClients))
	for _, entry := range m.externalTrafficClients {
		clients = append(clients, entry)
	}
	sort.Slice(inbounds, func(i, j int) bool { return inbounds[i].Tag < inbounds[j].Tag })
	sort.Slice(clients, func(i, j int) bool {
		if clients[i].InboundID != clients[j].InboundID {
			return clients[i].InboundID < clients[j].InboundID
		}
		return clients[i].Email < clients[j].Email
	})
	m.externalTrafficInbounds, m.externalTrafficClients = nil, nil
	go m.informExternalTraffic(m.state.Settings.ExternalTrafficInformURI, inbounds, clients)
}

// informExternalTraffic 把本周期的流量增量 POST 到用户配置的地址，body 结构照 3x-ui：
// {"clientTraffics": [...], "inboundTraffics": [...]}。
func (m *CoreManager) informExternalTraffic(uri string, inbounds []externalInboundTraffic, clients []externalClientTraffic) {
	body, err := json.Marshal(map[string]any{"clientTraffics": clients, "inboundTraffics": inbounds})
	if err != nil {
		return
	}
	request, err := http.NewRequest(http.MethodPost, uri, bytes.NewReader(body))
	if err != nil {
		m.logExternalTrafficFailure(err.Error())
		return
	}
	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		m.logExternalTrafficFailure(err.Error())
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode >= 400 {
		m.logExternalTrafficFailure(response.Status)
	}
}

// logExternalTrafficFailure 每分钟最多记一条，避免上报地址一直失败时把日志刷满。
func (m *CoreManager) logExternalTrafficFailure(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.lastTrafficInformLog.IsZero() && time.Since(m.lastTrafficInformLog) < time.Minute {
		return
	}
	m.lastTrafficInformLog = time.Now()
	m.addLogLocked("外部流量上报失败: " + reason)
}

// parseTimeMillis 把 RFC3339 时间转成毫秒时间戳，3x-ui 的上报字段用的是这个格式。
func parseTimeMillis(value string) int64 {
	if value == "" {
		return 0
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return 0
	}
	return parsed.UnixMilli()
}

func counterDelta(current, previous uint64) uint64 {
	if current >= previous {
		return current - previous
	}
	return current
}

func addTraffic(stats *TrafficStats, upload, download uint64) {
	stats.Up += upload
	stats.Down += download
	stats.AllTimeUp += upload
	stats.AllTimeDown += download
}

func clientMatchesUser(client Client, user string) bool {
	return strings.EqualFold(client.Username, user) || strings.EqualFold(client.Name, user) || strings.EqualFold(client.UUID, user)
}

func uintValue(value any) uint64 {
	switch typed := value.(type) {
	case float64:
		if typed > 0 {
			return uint64(typed)
		}
	case int64:
		if typed > 0 {
			return uint64(typed)
		}
	case int:
		if typed > 0 {
			return uint64(typed)
		}
	case uint64:
		return typed
	}
	return 0
}

func (a *App) handleClients(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		inboundID := r.URL.Query().Get("inboundId")
		a.manager.mu.Lock()
		for _, inbound := range a.manager.state.Inbounds {
			if inbound.ID == inboundID {
				clients := append([]Client(nil), inbound.Clients...)
				a.manager.mu.Unlock()
				writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: clients})
				return
			}
		}
		a.manager.mu.Unlock()
		writeJSON(w, http.StatusNotFound, apiResponse{Message: a.tr("Inbound 不存在")})
	case http.MethodPost:
		var input clientRequest
		if err := decodeJSON(r, &input); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr("客户端数据无效")})
			return
		}
		if err := a.manager.saveClient(input.InboundID, input.Client); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Message: a.tr("客户端已保存")})
	case http.MethodDelete:
		if err := a.manager.deleteClient(r.URL.Query().Get("inboundId"), r.URL.Query().Get("clientId")); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Message: a.tr("客户端已删除")})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
	}
}

func (m *CoreManager) saveClient(inboundID string, client Client) error {
	if strings.TrimSpace(client.Name) == "" {
		return fmt.Errorf("客户端名称不能为空")
	}
	if client.ID == "" {
		if err := randomToken(&client.ID); err != nil {
			return err
		}
		client.ID = client.ID[:12]
	}
	if client.CreatedAt == "" {
		client.CreatedAt = time.Now().Format(time.RFC3339)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for inboundIndex := range m.state.Inbounds {
		inbound := &m.state.Inbounds[inboundIndex]
		if inbound.ID != inboundID {
			continue
		}
		if singleSecretType(inbound.Type) && len(inbound.Clients) > 0 {
			if len(inbound.Clients) > 1 {
				inbound.Clients = inbound.Clients[:1]
			}
			existingSame := false
			for _, existing := range inbound.Clients {
				if existing.ID == client.ID {
					existingSame = true
				}
			}
			if !existingSame {
				return fmt.Errorf("Mihomo %s listener 仅支持一个 %s 客户端", inbound.Type, singleSecretLabel(inbound.Type))
			}
		}
		if singleSecretType(inbound.Type) {
			// The listener secret is owned by the sole client, never by the inbound.
			inbound.Username = ""
			inbound.Password = ""
			inbound.UUID = ""
		}
		if (inbound.Type == "vmess" || inbound.Type == "vless" || inbound.Type == "tuic") && client.UUID == "" {
			return fmt.Errorf("此协议的客户端必须填写 UUID")
		}
		for clientIndex := range inbound.Clients {
			if inbound.Clients[clientIndex].ID == client.ID {
				client.Traffic = inbound.Clients[clientIndex].Traffic
				client.LastOnline = inbound.Clients[clientIndex].LastOnline
				replaced := append([]Client(nil), inbound.Clients...)
				replaced[clientIndex] = client
				if err := validateClientIdentities(Inbound{Type: inbound.Type, Clients: replaced}); err != nil {
					return err
				}
				inbound.Clients[clientIndex] = client
				if err := m.ensureSubscriptionTokensLocked([]Client{client}); err != nil {
					return err
				}
				m.pruneSubscriptionTokensLocked(m.state.Inbounds)
				return m.saveLocked()
			}
		}
		if err := validateClientIdentities(Inbound{Type: inbound.Type, Clients: append(append([]Client(nil), inbound.Clients...), client)}); err != nil {
			return err
		}
		inbound.Clients = append(inbound.Clients, client)
		if err := m.ensureSubscriptionTokensLocked([]Client{client}); err != nil {
			return err
		}
		m.pruneSubscriptionTokensLocked(m.state.Inbounds)
		return m.saveLocked()
	}
	return fmt.Errorf("Inbound 不存在")
}

func (m *CoreManager) deleteClient(inboundID, clientID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for inboundIndex := range m.state.Inbounds {
		inbound := &m.state.Inbounds[inboundIndex]
		if inbound.ID != inboundID {
			continue
		}
		if singleSecretType(inbound.Type) && len(inbound.Clients) == 1 {
			return fmt.Errorf("%s 必须保留一个 %s 客户端，请停用或修改它", inbound.Type, singleSecretLabel(inbound.Type))
		}
		for clientIndex := range inbound.Clients {
			if inbound.Clients[clientIndex].ID == clientID {
				inbound.Clients = append(inbound.Clients[:clientIndex], inbound.Clients[clientIndex+1:]...)
				m.pruneSubscriptionTokensLocked(m.state.Inbounds)
				return m.saveLocked()
			}
		}
	}
	return fmt.Errorf("客户端不存在")
}

func (a *App) handleTrafficReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	var input struct {
		InboundID    string `json:"inboundId"`
		ClientID     string `json:"clientId"`
		All          bool   `json:"all"`
		InboundsOnly bool   `json:"inboundsOnly"`
		ClientsOnly  bool   `json:"clientsOnly"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr("请求无效")})
		return
	}
	a.manager.mu.Lock()
	if input.All || input.InboundsOnly || input.ClientsOnly {
		if input.All || input.InboundsOnly {
			a.manager.state.Traffic.Up, a.manager.state.Traffic.Down = 0, 0
		}
		for inboundIndex := range a.manager.state.Inbounds {
			inbound := &a.manager.state.Inbounds[inboundIndex]
			if input.All || input.InboundsOnly {
				inbound.Traffic.Up, inbound.Traffic.Down = 0, 0
			}
			if input.All || input.ClientsOnly {
				for clientIndex := range inbound.Clients {
					inbound.Clients[clientIndex].Traffic.Up, inbound.Clients[clientIndex].Traffic.Down = 0, 0
				}
			}
		}
	} else {
		for inboundIndex := range a.manager.state.Inbounds {
			inbound := &a.manager.state.Inbounds[inboundIndex]
			if inbound.ID != input.InboundID {
				continue
			}
			if input.ClientID == "" {
				inbound.Traffic.Up, inbound.Traffic.Down = 0, 0
			} else {
				for clientIndex := range inbound.Clients {
					if inbound.Clients[clientIndex].ID == input.ClientID {
						inbound.Clients[clientIndex].Traffic.Up, inbound.Clients[clientIndex].Traffic.Down = 0, 0
					}
				}
			}
		}
	}
	err := a.manager.saveLocked()
	a.manager.mu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{Message: a.tr(err.Error())})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Message: a.tr("流量统计已重置")})
}
