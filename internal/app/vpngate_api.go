package app

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// vpngateView is what the VPNGate modal renders: install progress, the presets
// the UI must offer, and the live state of every saved slot.
type vpngateView struct {
	Install  VPNGateInstallState `json:"install"`
	Presets  []vpngateCountry    `json:"presets"`
	MaxSlot  int                 `json:"maxSlot"`
	Statuses []VPNGateStatus     `json:"statuses"`
	UsedSlot []int               `json:"usedSlots"`
}

type vpngateCountry struct {
	Code    string       `json:"code"`
	ISPs    []vpngateISP `json:"isps"`
	Keyword bool         `json:"keyword"`
}

type vpngateISP struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

func (a *App) trVPNGateView(view vpngateView) vpngateView {
	view.Install.Message = a.tr(view.Install.Message)
	view.Install.Error = a.tr(view.Install.Error)
	view.Install.Reason = a.tr(view.Install.Reason)
	for index := range view.Statuses {
		view.Statuses[index].LastError = a.tr(view.Statuses[index].LastError)
	}
	return view
}

// vpnGatePresetCountries is the fixed list the selector offers. Japan and Korea
// are named because nearly every VPNGate node is in one of them; any other
// country is typed in as a two-letter code.
var vpnGatePresetCountries = []string{"JP", "KR"}

func makeVPNGateView(m *CoreManager) vpngateView {
	view := vpngateView{
		Install: m.vpnGateInstallState(),
		MaxSlot: vpnGateMaxSlot,
	}
	for _, code := range vpnGatePresetCountries {
		country := vpngateCountry{Code: code, Keyword: vpnGateCountryAllowsKeyword(code)}
		for _, preset := range vpnGatePresets[code] {
			country.ISPs = append(country.ISPs, vpngateISP{ID: preset.ID, Label: preset.Label})
		}
		view.Presets = append(view.Presets, country)
	}
	view.Statuses = m.vpnGateStatuses()
	statusSlots := make(map[int]bool, len(view.Statuses))
	for _, status := range view.Statuses {
		statusSlots[status.Slot] = true
	}
	supported, platformReason := vpnGatePlatformSupported()
	installed := m.vpnGateInstalled()

	m.mu.Lock()
	for _, item := range findVPNGateOutbounds(m.state.Outbounds) {
		slot := item.VPNGate.Slot
		view.UsedSlot = append(view.UsedSlot, slot)
		if statusSlots[slot] {
			continue
		}
		status := VPNGateStatus{
			Slot: slot, Outbound: item.Name, Country: item.VPNGate.Country,
			Target: vpnGateISPLabel(*item.VPNGate), Interface: vpnGateInterfaceName(slot),
			Table: vpnGateRouteTable(slot), State: "starting",
		}
		switch {
		case !supported:
			status.State, status.LastError = "error", platformReason
		case !installed:
			status.State, status.LastError = "error", "VPNGate 客户端尚未安装"
		}
		view.Statuses = append(view.Statuses, status)
	}
	m.mu.Unlock()
	sort.Ints(view.UsedSlot)
	sort.Slice(view.Statuses, func(i, j int) bool { return view.Statuses[i].Slot < view.Statuses[j].Slot })
	return view
}

// nextFreeVPNGateSlot suggests the lowest slot no saved outbound claims.
func (m *CoreManager) nextFreeVPNGateSlot() (int, error) {
	m.mu.Lock()
	used := make(map[int]bool)
	for _, item := range findVPNGateOutbounds(m.state.Outbounds) {
		used[item.VPNGate.Slot] = true
	}
	m.mu.Unlock()
	for slot := 0; slot <= vpnGateMaxSlot; slot++ {
		if !used[slot] {
			return slot, nil
		}
	}
	return 0, fmt.Errorf("0-%d 号网卡序号都已被占用", vpnGateMaxSlot)
}

// buildVPNGateOutbound turns the modal's choices into a draft outbound. It does
// not touch the system: the NIC is only created after the page is saved.
func buildVPNGateOutbound(name string, config VPNGateConfig) (MihomoOutbound, error) {
	item := MihomoOutbound{
		Kind:    "proxy",
		Name:    strings.TrimSpace(name),
		Type:    vpnGateOutboundType,
		VPNGate: &config,
	}
	if item.Name == "" {
		item.Name = fmt.Sprintf("vpngate-%s-%d", strings.ToLower(config.Country), config.Slot)
	}
	if err := applyVPNGateOutbound(&item); err != nil {
		return MihomoOutbound{}, err
	}
	return item, nil
}

func (a *App) handleVPNGate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	action := strings.TrimPrefix(r.URL.Path, "/api/mihomo/vpngate")
	switch {
	case action == "" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: a.trVPNGateView(makeVPNGateView(a.manager))})
	case action == "/install" && r.Method == http.MethodPost:
		a.startVPNGateInstall(w)
	case action == "/outbound" && r.Method == http.MethodPost:
		a.buildVPNGateDraft(w, r)
	case action == "/reconnect" && r.Method == http.MethodPost:
		a.reconnectVPNGateSlot(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
	}
}

// startVPNGateInstall kicks the install off in the background so the request
// returns immediately; the modal polls GET for progress.
func (a *App) startVPNGateInstall(w http.ResponseWriter) {
	if supported, reason := vpnGatePlatformSupported(); !supported {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(reason)})
		return
	}
	if a.manager.vpnGateInstalled() {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr("VPNGate 已经安装")})
		return
	}
	if err := vpnGatePreflight(); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
		return
	}
	if err := a.manager.vpngateInstaller.begin(); err != nil {
		writeJSON(w, http.StatusConflict, apiResponse{Message: a.tr(err.Error())})
		return
	}
	installCtx, cancel := context.WithCancel(context.Background())
	a.manager.vpngateInstaller.setCancel(cancel)
	go func() {
		defer cancel()
		err := a.manager.runVPNGateInstall(installCtx)
		a.manager.vpngateInstaller.finish(err)
		if err != nil {
			a.manager.addLog("VPNGate 安装失败: " + err.Error())
			return
		}
		if err := a.manager.writeConfig(); err != nil {
			a.manager.addLog("VPNGate 安装后写入 Mihomo 配置失败: " + err.Error())
			return
		}
		a.manager.syncVPNGateTasks()
	}()
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: a.trVPNGateView(makeVPNGateView(a.manager))})
}

func (a *App) buildVPNGateDraft(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name    string `json:"name"`
		Country string `json:"country"`
		ISP     string `json:"isp"`
		Keyword string `json:"ispKeyword"`
		Slot    *int   `json:"slot"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
		return
	}
	slot := 0
	if input.Slot != nil {
		slot = *input.Slot
	} else {
		suggested, err := a.manager.nextFreeVPNGateSlot()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
			return
		}
		slot = suggested
	}
	item, err := buildVPNGateOutbound(input.Name, VPNGateConfig{
		Country:    input.Country,
		ISP:        input.ISP,
		ISPKeyword: input.Keyword,
		Slot:       slot,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: item})
}

// reconnectVPNGateSlot asks one slot to re-dial. Only that slot is affected:
// other outbounds and the Mihomo core keep running.
func (a *App) reconnectVPNGateSlot(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Slot int `json:"slot"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
		return
	}
	if input.Slot < 0 || input.Slot > vpnGateMaxSlot {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(fmt.Sprintf("VPNGate 网卡序号必须是 0-%d", vpnGateMaxSlot))})
		return
	}
	a.manager.vpngateMu.Lock()
	task := a.manager.vpngateTasks[input.Slot]
	a.manager.vpngateMu.Unlock()
	if task == nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr("该 VPNGate 出站还没有保存")})
		return
	}
	task.requestReload()
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: a.trVPNGateView(makeVPNGateView(a.manager))})
}
