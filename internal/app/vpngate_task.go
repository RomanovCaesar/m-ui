package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	vpnGateHealthInterval = 60 * time.Second
	vpnGateDialTimeout    = 90 * time.Second
	vpnGateRetryBase      = 15 * time.Second
	vpnGateRetryMax       = 10 * time.Minute
	vpnGateTargetRetry    = 30 * time.Minute
)

type VPNGateStatus struct {
	Slot      int    `json:"slot"`
	Outbound  string `json:"outbound,omitempty"`
	Country   string `json:"country,omitempty"`
	Target    string `json:"target,omitempty"`
	Connected bool   `json:"connected"`
	State     string `json:"state,omitempty"`
	Node      string `json:"node,omitempty"`
	ASN       string `json:"asn,omitempty"`
	ISP       string `json:"isp,omitempty"`
	Fallback  bool   `json:"fallback"`
	Interface string `json:"interface,omitempty"`
	Table     int    `json:"table,omitempty"`
	Since     string `json:"since,omitempty"`
	LastError string `json:"lastError,omitempty"`
}

type vpnGateLink interface {
	Connect(context.Context, int, VPNGateNode) error
	Verify(context.Context, int) error
	Disconnect(context.Context, int) error
	Cleanup(context.Context, int) error
}

type vpnGateTask struct {
	manager        *CoreManager
	slot           int
	cancel         context.CancelFunc
	done           chan struct{}
	mu             sync.Mutex
	config         VPNGateConfig
	outbound       string
	status         VPNGateStatus
	reload         chan struct{}
	attemptCancel  context.CancelFunc
	link           vpnGateLink
	fetch          func(context.Context) ([]VPNGateNode, error)
	lookup         func(context.Context, string) (VPNGateASN, error)
	probe          func(context.Context, VPNGateNode) error
	failed         map[string]time.Time
	healthInterval time.Duration
	retryBase      time.Duration
}

func (t *vpnGateTask) snapshot() VPNGateStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

func (t *vpnGateTask) setStatus(mutate func(*VPNGateStatus)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	mutate(&t.status)
}

func (t *vpnGateTask) settings() (VPNGateConfig, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.config, t.outbound
}

func (t *vpnGateTask) update(config VPNGateConfig, outbound string) {
	t.mu.Lock()
	changed := t.config != config
	t.config, t.outbound = config, outbound
	t.status.Outbound, t.status.Country, t.status.Target = outbound, config.Country, vpnGateISPLabel(config)
	t.mu.Unlock()
	if changed {
		t.requestReload()
	}
}

func (t *vpnGateTask) requestReload() {
	t.mu.Lock()
	defer t.mu.Unlock()
	select {
	case t.reload <- struct{}{}:
	default:
	}
	if t.attemptCancel != nil {
		t.attemptCancel()
	}
}

// Reconcile reads the latest saved state under a separate lifecycle lock. Older
// HTTP requests cannot apply stale snapshots or start a task after shutdown.
func (m *CoreManager) syncVPNGateTasks() {
	m.vpngateSyncMu.Lock()
	defer m.vpngateSyncMu.Unlock()
	if m.vpngateStopped {
		return
	}
	wanted := make(map[int]MihomoOutbound)
	if supported, _ := vpnGatePlatformSupported(); supported && m.vpnGateInstalled() {
		for _, item := range findVPNGateOutbounds(m.outboundsSnapshot()) {
			wanted[item.VPNGate.Slot] = item
		}
	}
	m.vpngateMu.Lock()
	if m.vpngateTasks == nil {
		m.vpngateTasks = make(map[int]*vpnGateTask)
	}
	var stopping []*vpnGateTask
	for slot, task := range m.vpngateTasks {
		if _, keep := wanted[slot]; !keep {
			delete(m.vpngateTasks, slot)
			stopping = append(stopping, task)
		}
	}
	m.vpngateMu.Unlock()
	for _, task := range stopping {
		task.stop()
	}
	m.vpngateMu.Lock()
	defer m.vpngateMu.Unlock()
	for slot, item := range wanted {
		if task := m.vpngateTasks[slot]; task != nil {
			task.update(*item.VPNGate, item.Name)
			continue
		}
		task := &vpnGateTask{
			manager: m, slot: slot, done: make(chan struct{}), reload: make(chan struct{}, 1),
			config: *item.VPNGate, outbound: item.Name, link: newVPNGateLink(m),
			fetch:  func(ctx context.Context) ([]VPNGateNode, error) { return vpnGateNodes.fetch(ctx, time.Now) },
			lookup: func(ctx context.Context, ip string) (VPNGateASN, error) { return m.vpnGateResolver().Lookup(ctx, ip) },
			probe:  probeVPNGateEndpoint,
			failed: make(map[string]time.Time), healthInterval: vpnGateHealthInterval, retryBase: vpnGateRetryBase,
			status: VPNGateStatus{Slot: slot, Outbound: item.Name, Country: item.VPNGate.Country, Target: vpnGateISPLabel(*item.VPNGate), State: "starting", Interface: vpnGateInterfaceName(slot), Table: vpnGateRouteTable(slot)},
		}
		task.start()
		m.vpngateTasks[slot] = task
	}
}

func (m *CoreManager) stopVPNGateTasks() {
	m.vpngateInstaller.stop()
	m.vpngateSyncMu.Lock()
	defer m.vpngateSyncMu.Unlock()
	m.vpngateStopped = true
	m.vpngateMu.Lock()
	var tasks []*vpnGateTask
	for slot, task := range m.vpngateTasks {
		task.cancel()
		tasks = append(tasks, task)
		delete(m.vpngateTasks, slot)
	}
	m.vpngateMu.Unlock()
	for _, task := range tasks {
		task.stop()
	}
	m.stopVPNGateService()
}

// restartVPNGateTasks is used by backup restore. Unlike stopVPNGateTasks it
// does not permanently close the manager: all old slot resources are cleaned
// first, then the restored saved state is reconciled normally.
func (m *CoreManager) restartVPNGateTasks() {
	m.vpngateSyncMu.Lock()
	m.vpngateMu.Lock()
	var tasks []*vpnGateTask
	for slot, task := range m.vpngateTasks {
		tasks = append(tasks, task)
		delete(m.vpngateTasks, slot)
	}
	m.vpngateMu.Unlock()
	for _, task := range tasks {
		task.stop()
	}
	m.vpngateSyncMu.Unlock()
	m.syncVPNGateTasks()
}

func (m *CoreManager) vpnGateStatuses() []VPNGateStatus {
	m.vpngateMu.Lock()
	defer m.vpngateMu.Unlock()
	statuses := make([]VPNGateStatus, 0, len(m.vpngateTasks))
	for _, task := range m.vpngateTasks {
		statuses = append(statuses, task.snapshot())
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Slot < statuses[j].Slot })
	return statuses
}

func (t *vpnGateTask) start() {
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	go t.run(ctx)
}

func (t *vpnGateTask) stop() {
	t.cancel()
	<-t.done
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := t.link.Cleanup(ctx, t.slot); err != nil {
		t.manager.addLog(fmt.Sprintf("VPNGate 槽位 %d 清理失败: %v", t.slot, err))
	}
}

func (t *vpnGateTask) disconnect() {
	t.setStatus(func(s *VPNGateStatus) { s.Connected = false; s.Since = "" })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := t.link.Disconnect(ctx, t.slot); err != nil {
		t.manager.addLog(fmt.Sprintf("VPNGate 槽位 %d: %v", t.slot, err))
	}
}

func (t *vpnGateTask) run(ctx context.Context) {
	defer close(t.done)
	defer t.disconnect()
	backoff := t.retryBase
	for ctx.Err() == nil {
		select {
		case <-t.reload:
			t.failed = make(map[string]time.Time)
		default:
		}
		attempt, cancel := context.WithCancel(ctx)
		t.mu.Lock()
		t.attemptCancel = cancel
		t.mu.Unlock()
		node, asn, fallback, err := t.pick(attempt)
		switchTarget := false
		if err == nil {
			err = t.connect(attempt, node, asn, fallback)
		}
		if err == nil {
			backoff = t.retryBase
			switchTarget, err = t.hold(attempt, node, asn, fallback)
		}
		interrupted := attempt.Err() != nil
		cancel()
		t.mu.Lock()
		t.attemptCancel = nil
		t.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		if interrupted {
			t.disconnect()
			continue
		}
		if switchTarget {
			t.disconnect()
			continue
		}
		if node.IP != "" {
			t.failed[node.Endpoint()] = time.Now().Add(vpnGateRetryMax)
		}
		if err != nil {
			t.fail(err)
		}
		if !t.wait(ctx, backoff) {
			return
		}
		backoff = nextVPNGateBackoff(backoff)
	}
}

func nextVPNGateBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > vpnGateRetryMax {
		return vpnGateRetryMax
	}
	return next
}

func (t *vpnGateTask) wait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.reload:
		t.failed = make(map[string]time.Time)
		return true
	case <-timer.C:
		return true
	}
}

func (t *vpnGateTask) fail(err error) {
	t.setStatus(func(s *VPNGateStatus) { s.Connected = false; s.State = "error"; s.LastError = err.Error() })
	t.manager.addLog(fmt.Sprintf("VPNGate 槽位 %d: %v", t.slot, err))
}

func (t *vpnGateTask) pick(ctx context.Context) (VPNGateNode, VPNGateASN, bool, error) {
	config, _ := t.settings()
	t.setStatus(func(s *VPNGateStatus) { s.State = "searching"; s.Connected = false })
	nodes, err := t.fetch(ctx)
	if err != nil {
		return VPNGateNode{}, VPNGateASN{}, false, err
	}
	candidates := vpnGateCandidates(nodes, config)
	if len(candidates) == 0 {
		return VPNGateNode{}, VPNGateASN{}, false, fmt.Errorf("没有找到 %s 的 VPNGate 节点", config.Country)
	}
	var spare VPNGateNode
	var spareASN VPNGateASN
	var target VPNGateNode
	var targetASN VPNGateASN
	var lookupErr error
	for _, node := range candidates {
		if ctx.Err() != nil {
			return VPNGateNode{}, VPNGateASN{}, false, ctx.Err()
		}
		if time.Now().Before(t.failed[node.Endpoint()]) {
			continue
		}
		if t.probe != nil {
			probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := t.probe(probeCtx, node)
			cancel()
			if err != nil {
				t.failed[node.Endpoint()] = time.Now().Add(vpnGateRetryMax)
				continue
			}
		}
		asn, err := t.lookup(ctx, node.IP)
		if err != nil {
			lookupErr = err
			continue
		}
		if asn.Excluded() {
			continue
		}
		if vpnGateASNMatches(config, asn.Name) || vpnGateASNMatches(config, asn.ASN) {
			if config.ISP != "softbank" || strings.Contains(strings.ToLower(asn.Region), "tokyo") {
				return node, asn, false, nil
			}
			if target.IP == "" {
				target, targetASN = node, asn
			}
			continue
		}
		if spare.IP == "" {
			spare, spareASN = node, asn
		}
	}
	if target.IP != "" {
		return target, targetASN, false, nil
	}
	if spare.IP != "" {
		return spare, spareASN, true, nil
	}
	if lookupErr != nil {
		return VPNGateNode{}, VPNGateASN{}, false, lookupErr
	}
	return VPNGateNode{}, VPNGateASN{}, false, fmt.Errorf("没有找到 %s 的可用节点", vpnGateISPLabel(config))
}

func (t *vpnGateTask) connect(ctx context.Context, node VPNGateNode, asn VPNGateASN, fallback bool) error {
	t.setStatus(func(s *VPNGateStatus) {
		s.State = "connecting"
		s.Connected = false
		s.Node = node.Endpoint()
		s.ASN = asn.ASN
		s.ISP = asn.Name
		s.Fallback = fallback
		s.Since = ""
	})
	dial, cancel := context.WithTimeout(ctx, vpnGateDialTimeout)
	defer cancel()
	if err := t.link.Connect(dial, t.slot, node); err != nil {
		t.disconnect()
		return err
	}
	if err := t.link.Verify(dial, t.slot); err != nil {
		t.disconnect()
		return err
	}
	t.setStatus(func(s *VPNGateStatus) {
		s.State = "connected"
		s.Connected = true
		s.Since = time.Now().Format(time.RFC3339)
		s.LastError = ""
	})
	label := asn.Label()
	if fallback {
		label += " (fallback)"
	}
	t.manager.addLog(fmt.Sprintf("VPNGate 槽位 %d 已连接 %s [%s]", t.slot, node.Endpoint(), label))
	return nil
}

func (t *vpnGateTask) hold(ctx context.Context, node VPNGateNode, asn VPNGateASN, fallback bool) (bool, error) {
	ticker := time.NewTicker(t.healthInterval)
	defer ticker.Stop()
	connectedAt := time.Now()
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-t.reload:
			t.disconnect()
			return false, context.Canceled
		case <-ticker.C:
			check, cancel := context.WithTimeout(ctx, 15*time.Second)
			err := t.link.Verify(check, t.slot)
			cancel()
			if err != nil {
				// First re-dial the current volunteer. Only blacklist it and move
				// to another node when that bounded soft reconnect also fails.
				t.setStatus(func(s *VPNGateStatus) {
					s.State = "reconnecting"
					s.Connected = false
					s.LastError = err.Error()
				})
				t.disconnect()
				if reconnectErr := t.connect(ctx, node, asn, fallback); reconnectErr != nil {
					return false, reconnectErr
				}
				connectedAt = time.Now()
				continue
			}
			if fallback && time.Since(connectedAt) > vpnGateTargetRetry {
				// Do not tear down a healthy fallback unless a target ISP has reappeared.
				node, _, stillFallback, err := t.pick(ctx)
				if err == nil && !stillFallback && node.IP != "" {
					return true, nil
				}
				t.setStatus(func(s *VPNGateStatus) { s.State = "connected"; s.Connected = true })
				connectedAt = time.Now()
			}
		}
	}
}

func (m *CoreManager) vpnGateResolver() *vpnGateASNResolver {
	m.vpngateMu.Lock()
	defer m.vpngateMu.Unlock()
	if m.vpngateASN == nil {
		m.vpngateASN = newVPNGateASNResolver(func() string { m.mu.Lock(); defer m.mu.Unlock(); return m.state.Settings.IPInfoToken })
	}
	return m.vpngateASN
}
