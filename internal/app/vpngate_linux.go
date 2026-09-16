//go:build linux

package app

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// vpnGateLinuxLink drives SoftEther's VPN Client for one slot and owns the
// policy routing that keeps that slot's traffic on its own interface.
//
// Everything it touches is namespaced by slot: the adapter vpnN, the interface
// vpn_vpnN, the account mui-vpngate-N, the routing table 100+N and the DHCP
// files under <dataDir>/vpngate/run/N. It never edits the main routing table,
// the default route or /etc/resolv.conf, and it never flushes a table or drops
// rules it did not create.
type vpnGateLinuxLink struct {
	manager *CoreManager
}

func newVPNGateLink(manager *CoreManager) vpnGateLink {
	return vpnGateLinuxLink{manager: manager}
}

const (
	vpnGateCommandTimeout = 30 * time.Second
	vpnGateSessionTimeout = 25 * time.Second
	vpnGateProbeURL       = "https://www.gstatic.com/generate_204"
	// Rules carry a priority derived from the slot so they can be found and
	// removed one by one instead of by blanket table deletion.
	vpnGateRulePriority = 20000
)

func (l vpnGateLinuxLink) runDir(slot int) string {
	return filepath.Join(l.manager.vpnGateHome(), "run", strconv.Itoa(slot))
}

func (l vpnGateLinuxLink) slotOwnerPath(slot int) string {
	return filepath.Join(l.runDir(slot), "owner")
}

func (l vpnGateLinuxLink) sourceAddressPath(slot int) string {
	return filepath.Join(l.runDir(slot), "source-address")
}

func (l vpnGateLinuxLink) slotOwned(slot int) bool {
	raw, err := os.ReadFile(l.slotOwnerPath(slot))
	return err == nil && strings.TrimSpace(string(raw)) == fmt.Sprintf("m-ui-vpngate-slot:%d", slot)
}

func (l vpnGateLinuxLink) recordSlotOwner(slot int) error {
	if err := os.MkdirAll(l.runDir(slot), 0700); err != nil {
		return err
	}
	temporary := l.slotOwnerPath(slot) + ".tmp"
	if err := os.WriteFile(temporary, []byte(fmt.Sprintf("m-ui-vpngate-slot:%d\n", slot)), 0600); err != nil {
		return err
	}
	return os.Rename(temporary, l.slotOwnerPath(slot))
}

func (l vpnGateLinuxLink) controlSecretPath() string {
	return filepath.Join(l.manager.vpnGateHome(), "control.secret")
}

func (l vpnGateLinuxLink) servicePIDPath() string {
	return filepath.Join(l.manager.vpnGateHome(), "service.pid")
}

// vpncmd runs one VPN Client console command. The arguments are always built
// here from validated values, never assembled from user text through a shell.
func (l vpnGateLinuxLink) vpncmd(ctx context.Context, arguments ...string) (string, error) {
	output, err := l.vpncmdWithPassword(ctx, "", arguments...)
	if err == nil {
		return output, nil
	}
	// Compatibility for a service secured by an earlier m-ui build which
	// required its password even from localhost. secureControl migrates it to
	// remote-only authentication after the next successful connection.
	if raw, readErr := os.ReadFile(l.controlSecretPath()); readErr == nil {
		if password := strings.TrimSpace(string(raw)); password != "" {
			return l.vpncmdWithPassword(ctx, password, arguments...)
		}
	}
	return output, err
}

func (l vpnGateLinuxLink) vpncmdWithPassword(ctx context.Context, password string, arguments ...string) (string, error) {
	binary := l.manager.vpnGateBinary("vpncmd")
	if _, err := os.Stat(binary); err != nil {
		return "", fmt.Errorf("VPNGate 客户端尚未安装")
	}
	commandCtx, cancel := context.WithTimeout(ctx, vpnGateCommandTimeout)
	defer cancel()
	full := []string{"localhost", "/CLIENT"}
	if password != "" {
		full = append(full, "/PASSWORD:"+password)
	}
	full = append(full, "/CMD")
	full = append(full, arguments...)
	command := exec.CommandContext(commandCtx, binary, full...)
	command.Dir = l.manager.vpnGateHome()
	command.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")
	output, err := command.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("vpncmd %s 失败：%s", arguments[0], vpnGateTailLines(string(output), 4))
	}
	return string(output), nil
}

func (l vpnGateLinuxLink) ip(ctx context.Context, arguments ...string) (string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, vpnGateCommandTimeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, "ip", arguments...)
	command.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")
	output, err := command.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("ip %s 失败：%s", strings.Join(arguments, " "), vpnGateTailLines(string(output), 3))
	}
	return string(output), nil
}

// ensureService starts the VPN Client engine in its foreground execsvc mode as
// a child of the panel. An engine without our validated PID ownership record is
// reported as a conflict and is never adopted or stopped.
func (l vpnGateLinuxLink) ensureService(ctx context.Context) error {
	if _, err := l.vpncmd(ctx, "AccountList"); err == nil {
		owned, ownerErr := l.serviceOwned()
		if ownerErr != nil {
			return ownerErr
		}
		if !owned {
			return fmt.Errorf("检测到非 m-ui 管理的 VPN Client 服务，拒绝接管")
		}
		return l.secureControl(ctx)
	}
	if owned, ownerErr := l.serviceOwned(); ownerErr != nil {
		return ownerErr
	} else if owned {
		// A restored configuration may have a stale secret file while the owned
		// service currently has no password. Only the validated owned process is
		// eligible for this recovery attempt.
		if _, err := l.vpncmdWithPassword(ctx, "", "AccountList"); err == nil {
			return l.secureControl(ctx)
		}
		return fmt.Errorf("m-ui 管理的 VPN Client 服务存在但控制端未就绪")
	}
	binary := l.manager.vpnGateBinary("vpnclient")
	if _, err := os.Stat(binary); err != nil {
		return fmt.Errorf("VPNGate 客户端尚未安装")
	}
	l.manager.vpngateServiceMu.Lock()
	defer l.manager.vpngateServiceMu.Unlock()
	if l.manager.vpngateServiceCmd != nil {
		return fmt.Errorf("VPNGate 客户端服务正在启动")
	}
	logFile, err := os.OpenFile(filepath.Join(l.manager.vpnGateHome(), "service.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	command := exec.Command(binary, "execsvc")
	command.Dir = l.manager.vpnGateHome()
	command.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("启动 VPNGate 客户端服务失败：%w", err)
	}
	pid := strconv.Itoa(command.Process.Pid)
	if err := os.WriteFile(l.servicePIDPath()+".tmp", []byte(pid+"\n"), 0600); err != nil {
		_ = command.Process.Kill()
		_ = logFile.Close()
		return err
	}
	if err := os.Rename(l.servicePIDPath()+".tmp", l.servicePIDPath()); err != nil {
		_ = command.Process.Kill()
		_ = logFile.Close()
		return err
	}
	l.manager.vpngateServiceCmd = command
	go func() {
		_ = command.Wait()
		_ = logFile.Close()
		l.manager.vpngateServiceMu.Lock()
		if l.manager.vpngateServiceCmd == command {
			l.manager.vpngateServiceCmd = nil
			_ = os.Remove(l.servicePIDPath())
		}
		l.manager.vpngateServiceMu.Unlock()
	}()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := l.vpncmdWithPassword(ctx, "", "AccountList"); err == nil {
			if err := l.secureControl(ctx); err != nil {
				return err
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("VPNGate 客户端服务未能就绪")
}

func (l vpnGateLinuxLink) secureControl(ctx context.Context) error {
	secret := ""
	if raw, err := os.ReadFile(l.controlSecretPath()); err == nil {
		secret = strings.TrimSpace(string(raw))
	}
	if secret == "" {
		if err := randomToken(&secret); err != nil {
			return err
		}
		temporary := l.controlSecretPath() + ".tmp"
		if err := os.WriteFile(temporary, []byte(secret+"\n"), 0600); err != nil {
			return err
		}
		if _, err := l.vpncmdWithPassword(ctx, "", "PasswordSet", secret, "/REMOTEONLY:yes"); err != nil {
			_ = os.Remove(temporary)
			return err
		}
		if err := os.Rename(temporary, l.controlSecretPath()); err != nil {
			return fmt.Errorf("保存 VPN Client 控制凭据失败：%w", err)
		}
	} else {
		// Migrate any earlier local-password setting. Passing the secret is only
		// needed for this one transition; routine commands then stay out of argv.
		if _, err := l.vpncmdWithPassword(ctx, secret, "PasswordSet", secret, "/REMOTEONLY:yes"); err != nil {
			return fmt.Errorf("更新 VPN Client 控制凭据失败：%w", err)
		}
	}
	if _, err := l.vpncmdWithPassword(ctx, "", "RemoteDisable"); err != nil {
		return fmt.Errorf("关闭 VPN Client 远程管理失败：%w", err)
	}
	return nil
}

func (l vpnGateLinuxLink) serviceOwned() (bool, error) {
	raw, err := os.ReadFile(l.servicePIDPath())
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 1 {
		return false, fmt.Errorf("VPN Client 服务所有权记录无效")
	}
	executable, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if os.IsNotExist(err) {
		_ = os.Remove(l.servicePIDPath())
		return false, nil
	}
	if err != nil {
		return false, err
	}
	want, err := filepath.EvalSymlinks(l.manager.vpnGateBinary("vpnclient"))
	if err != nil {
		return false, err
	}
	got, err := filepath.EvalSymlinks(executable)
	if err != nil || got != want {
		return false, fmt.Errorf("VPN Client PID 所有权冲突，拒绝操作进程 %d", pid)
	}
	cmdline, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return false, err
	}
	if !strings.Contains(strings.ReplaceAll(string(cmdline), "\x00", " "), "execsvc") {
		return false, fmt.Errorf("VPN Client PID %d 不是 m-ui 前台服务", pid)
	}
	return true, nil
}

func (m *CoreManager) stopVPNGateService() {
	link := vpnGateLinuxLink{manager: m}
	owned, err := link.serviceOwned()
	if err != nil {
		m.addLog("VPNGate 服务停止失败: " + err.Error())
		return
	}
	if !owned {
		return
	}
	raw, err := os.ReadFile(link.servicePIDPath())
	if err != nil {
		return
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	process, err := os.FindProcess(pid)
	if err == nil {
		_ = process.Signal(syscall.SIGTERM)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat("/proc/" + strconv.Itoa(pid)); os.IsNotExist(err) {
			_ = os.Remove(link.servicePIDPath())
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	if process != nil {
		_ = process.Kill()
	}
	_ = os.Remove(link.servicePIDPath())
}

// Connect creates or reuses the slot's adapter and account, dials the node and
// installs the slot's own routing.
func (l vpnGateLinuxLink) Connect(ctx context.Context, slot int, node VPNGateNode) error {
	if err := l.ensureService(ctx); err != nil {
		return err
	}
	adapter := vpnGateAdapterName(slot)
	account := vpnGateAccountName(slot)
	iface := vpnGateInterfaceName(slot)

	// Install the fwmark guard before inspecting or creating the interface.
	// Even an interface-name collision therefore cannot let the managed DIRECT
	// outbound fall through to the host's main default route.
	if err := l.ensureFailClosed(ctx, slot, iface); err != nil {
		return err
	}
	if err := l.ensureAdapter(ctx, slot, adapter, iface); err != nil {
		return err
	}
	if err := l.ensureAccount(ctx, account, adapter, node); err != nil {
		return err
	}
	// A stale session on this account is closed first; other accounts are never
	// touched, so other slots and any hand-made VPN stay up.
	_, _ = l.vpncmd(ctx, "AccountDisconnect", account)
	if _, err := l.vpncmd(ctx, "AccountConnect", account); err != nil {
		return err
	}
	if err := l.waitForAccountConnected(ctx, account); err != nil {
		return err
	}
	if err := l.waitForInterface(ctx, iface); err != nil {
		return err
	}
	address, gateway, err := l.leaseAddress(ctx, slot, iface)
	if err != nil {
		return err
	}
	return l.installRouting(ctx, slot, iface, address, gateway)
}

func (l vpnGateLinuxLink) waitForAccountConnected(ctx context.Context, account string) error {
	deadline := time.Now().Add(vpnGateSessionTimeout)
	lastStatus := ""
	for time.Now().Before(deadline) {
		output, err := l.vpncmd(ctx, "AccountStatusGet", account)
		if err == nil && vpnGateAccountConnected(output) {
			return nil
		}
		if tail := vpnGateTailLines(output, 4); tail != "" {
			lastStatus = tail
		} else if err != nil {
			lastStatus = err.Error()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if lastStatus != "" {
		return fmt.Errorf("SoftEther 账户 %s 未能建立连接：%s", account, lastStatus)
	}
	return fmt.Errorf("SoftEther 账户 %s 未能建立连接", account)
}

// ensureAdapter creates the adapter only when neither it nor its interface
// already exists. An interface that exists without our adapter belongs to
// somebody else and is refused rather than hijacked.
func (l vpnGateLinuxLink) ensureAdapter(ctx context.Context, slot int, adapter, iface string) error {
	list, err := l.vpncmd(ctx, "NicList")
	if err != nil {
		return err
	}
	if vpnGateOutputMentions(list, adapter) {
		if !l.slotOwned(slot) {
			return fmt.Errorf("SoftEther 网卡 %s 没有 m-ui 所有权记录，拒绝接管", adapter)
		}
		_, _ = l.vpncmd(ctx, "NicEnable", adapter)
		return nil
	}
	if _, err := net.InterfaceByName(iface); err == nil {
		return fmt.Errorf("网卡 %s 已被其他程序占用，请换一个网卡序号", iface)
	}
	accounts, err := l.vpncmd(ctx, "AccountList")
	if err != nil {
		return err
	}
	if vpnGateOutputMentions(accounts, vpnGateAccountName(slot)) {
		return fmt.Errorf("SoftEther 账户 %s 已存在但网卡所有权记录缺失，拒绝接管", vpnGateAccountName(slot))
	}
	if _, err := l.vpncmd(ctx, "NicCreate", adapter); err != nil {
		return err
	}
	if err := l.recordSlotOwner(slot); err != nil {
		_, _ = l.vpncmd(ctx, "NicDelete", adapter)
		return err
	}
	return nil
}

// ensureAccount points the slot's account at the chosen node, recreating it
// when the endpoint changed.
func (l vpnGateLinuxLink) ensureAccount(ctx context.Context, account, adapter string, node VPNGateNode) error {
	existing, err := l.vpncmd(ctx, "AccountList")
	if err != nil {
		return err
	}
	if vpnGateOutputMentions(existing, account) {
		slot, parseErr := strconv.Atoi(strings.TrimPrefix(account, "mui-vpngate-"))
		if parseErr != nil || !l.slotOwned(slot) {
			return fmt.Errorf("SoftEther 账户 %s 没有 m-ui 所有权记录，拒绝接管", account)
		}
		if _, err := l.vpncmd(ctx, "AccountDisconnect", account); err == nil {
			time.Sleep(time.Second)
		}
		if _, err := l.vpncmd(ctx, "AccountDelete", account); err != nil {
			return err
		}
	}
	if _, err := l.vpncmd(ctx, "AccountCreate", account,
		"/SERVER:"+node.Endpoint(), "/HUB:vpngate", "/USERNAME:vpn", "/NICNAME:"+adapter); err != nil {
		return err
	}
	if _, err := l.vpncmd(ctx, "AccountPasswordSet", account, "/PASSWORD:vpn", "/TYPE:standard"); err != nil {
		return err
	}
	return nil
}

func vpnGateOutputMentions(output, needle string) bool {
	for _, line := range strings.Split(output, "\n") {
		for _, field := range strings.Split(line, "|") {
			if strings.EqualFold(strings.TrimSpace(field), needle) {
				return true
			}
		}
	}
	return false
}

func (l vpnGateLinuxLink) waitForInterface(ctx context.Context, iface string) error {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := net.InterfaceByName(iface); err == nil {
			_, _ = l.ip(ctx, "link", "set", iface, "up")
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("虚拟网卡 %s 没有出现", iface)
}

// leaseAddress runs a dhclient dedicated to this interface, with its own
// config, lease and pid files, and a hook that is forbidden from touching
// anything global.
func (l vpnGateLinuxLink) leaseAddress(ctx context.Context, slot int, iface string) (string, string, error) {
	directory := l.runDir(slot)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", "", err
	}
	config := filepath.Join(directory, "dhclient.conf")
	lease := filepath.Join(directory, "dhclient.leases")
	pid := filepath.Join(directory, "dhclient.pid")
	script := filepath.Join(directory, "dhclient-hook.sh")
	if err := os.WriteFile(config, []byte(vpnGateDHCPConfig), 0600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(script, []byte(vpnGateDHCPHook), 0700); err != nil {
		return "", "", err
	}
	if err := l.ensureDHCPAppArmor(ctx); err != nil {
		return "", "", fmt.Errorf("VPNGate AppArmor 配置失败：%w", err)
	}

	// Only this slot's own client is stopped, by pid; `dhclient -r` without an
	// interface would tear down the server's real network.
	l.releaseLease(ctx, slot, iface)
	_ = os.Remove(lease)
	_ = os.Remove(pid)
	if _, err := l.ip(ctx, "-4", "addr", "flush", "dev", iface); err != nil {
		return "", "", err
	}

	commandCtx, cancel := context.WithTimeout(ctx, vpnGateCommandTimeout)
	defer cancel()
	command := vpnGateDHCPCommand(commandCtx, slot, iface, config, lease, pid, script)
	output, err := command.CombinedOutput()
	if err != nil {
		detail := vpnGateTailLines(string(output), 6)
		if detail == "" {
			detail = err.Error()
		}
		return "", "", fmt.Errorf("为 %s 申请地址失败：%s", iface, detail)
	}
	address, err := l.waitForInterfaceAddress(ctx, iface, 3*time.Second)
	if err != nil {
		// dhclient can exit successfully even when its hook did not configure
		// the address. Preserve its diagnostics instead of hiding that failure
		// behind the generic "no IPv4 address" message.
		if detail := vpnGateTailLines(string(output), 6); detail != "" && ctx.Err() == nil {
			return "", "", fmt.Errorf("DHCP 已结束，但网卡 %s 仍没有 IPv4 地址：%s", iface, detail)
		}
		return "", "", err
	}
	gateway, err := vpnGateLeaseGateway(lease, iface)
	if err != nil {
		return "", "", err
	}
	return address, gateway, nil
}

func (l vpnGateLinuxLink) ensureDHCPAppArmor(ctx context.Context) error {
	// AppArmor matches canonical paths, including installations reached through
	// a symlink or started with a relative --data-dir.
	runRoot, err := filepath.Abs(filepath.Join(l.manager.vpnGateHome(), "run"))
	if err != nil {
		return err
	}
	runRoot, err = filepath.EvalSymlinks(runRoot)
	if err != nil {
		return err
	}
	return l.manager.vpngateAppArmor.ensure(ctx, runRoot,
		"/sys/kernel/security/apparmor/profiles", "/etc/apparmor.d", reloadVPNGateAppArmor)
}

func reloadVPNGateAppArmor(ctx context.Context, profile string) error {
	parser, err := exec.LookPath("apparmor_parser")
	if err != nil {
		return fmt.Errorf("apparmor_parser is required for VPNGate DHCP; install the apparmor package: %w", err)
	}
	commandCtx, cancel := context.WithTimeout(ctx, vpnGateCommandTimeout)
	defer cancel()
	// Ignore an older binary cache so the new local include takes effect.
	command := exec.CommandContext(commandCtx, parser, "-r", "-T", profile)
	command.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("apparmor_parser: %w: %s", err, vpnGateTailLines(string(output), 6))
	}
	return nil
}

func vpnGateDHCPCommand(ctx context.Context, slot int, iface, config, lease, pid, script string) *exec.Cmd {
	// ISC dhclient builds a fresh environment for its hook. Parent-process
	// environment variables are not inherited; -e must be used for both the
	// initial lease and subsequent renewals to receive the slot's routing data.
	command := exec.CommandContext(ctx, "dhclient",
		"-4", "-1", "-v", "-cf", config, "-lf", lease, "-pf", pid, "-sf", script,
		"-e", "MUI_VPNGATE_TABLE="+strconv.Itoa(vpnGateRouteTable(slot)),
		"-e", "MUI_VPNGATE_MARK="+strconv.Itoa(vpnGateRoutingMark(slot)),
		"-e", "MUI_VPNGATE_MARK_PRIORITY="+strconv.Itoa(vpnGateRulePriority+slot),
		"-e", "MUI_VPNGATE_SOURCE_PRIORITY="+strconv.Itoa(vpnGateRulePriority+100+slot), iface)
	command.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")
	return command
}

func (l vpnGateLinuxLink) waitForInterfaceAddress(ctx context.Context, iface string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		address, err := l.interfaceAddress(iface)
		if err == nil {
			return address, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("网卡 %s 还没有拿到 IPv4 地址", iface)
}

func (l vpnGateLinuxLink) interfaceAddress(iface string) (string, error) {
	device, err := net.InterfaceByName(iface)
	if err != nil {
		return "", err
	}
	addresses, err := device.Addrs()
	if err != nil {
		return "", err
	}
	for _, entry := range addresses {
		network, ok := entry.(*net.IPNet)
		if !ok || network.IP.To4() == nil {
			continue
		}
		return network.IP.String(), nil
	}
	return "", fmt.Errorf("网卡 %s 还没有拿到 IPv4 地址", iface)
}

// vpnGateLeaseGateway reads the router the DHCP server actually offered. The
// reference scripts hardcoded 10.211.254.254; that only happens to be right on
// some VPNGate hubs, so it is read from the lease instead.
func vpnGateLeaseGateway(leasePath, iface string) (string, error) {
	file, err := os.Open(leasePath)
	if err != nil {
		return "", fmt.Errorf("读取 %s 的租约失败：%w", iface, err)
	}
	defer file.Close()
	gateway := ""
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "option routers ") {
			continue
		}
		value := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(line, "option routers ")), ";")
		for _, candidate := range strings.Split(value, ",") {
			parsed := net.ParseIP(strings.TrimSpace(candidate))
			if parsed != nil && validVPNGateGateway(parsed.To4()) {
				// Later leases win, so the newest router is used.
				gateway = parsed.String()
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if gateway == "" {
		return "", fmt.Errorf("%s 的租约里没有网关", iface)
	}
	return gateway, nil
}

func validVPNGateGateway(address net.IP) bool {
	if address == nil || address.To4() == nil {
		return false
	}
	address = address.To4()
	return !address.IsUnspecified() && !address.IsLoopback() && !address.IsMulticast() && !address.IsLinkLocalUnicast()
}

// installRouting builds the slot's private table. The table gets the VPN's
// default route plus an unreachable catch-all, so a dead line fails closed
// instead of leaking back out of the server's real egress.
func (l vpnGateLinuxLink) installRouting(ctx context.Context, slot int, iface, address, gateway string) error {
	table := strconv.Itoa(vpnGateRouteTable(slot))
	if err := l.ensureFailClosed(ctx, slot, iface); err != nil {
		return err
	}
	if _, err := l.ip(ctx, "route", "replace", "default", "via", gateway, "dev", iface, "onlink", "table", table, "proto", vpnGateRouteProtocol, "metric", vpnGateActiveMetric); err != nil {
		return err
	}
	// The source-address rule keeps replies to the leased address on this table
	// even when two slots were handed the same private IP by different hubs.
	sourcePriority := strconv.Itoa(vpnGateRulePriority + 100 + slot)
	if err := l.prepareOwnedRule(ctx, sourcePriority, "from "+address, "lookup "+table); err != nil {
		return err
	}
	if _, err := l.ip(ctx, "rule", "add", "from", address, "table", table, "priority", sourcePriority); err != nil {
		return err
	}
	if err := os.WriteFile(l.sourceAddressPath(slot), []byte(address+"\n"), 0600); err != nil {
		l.removeRule(ctx, sourcePriority, "from "+address, "lookup "+table)
		return err
	}
	// rp_filter is relaxed for this interface only, never system-wide.
	_ = os.WriteFile("/proc/sys/net/ipv4/conf/"+iface+"/rp_filter", []byte("2\n"), 0644)
	_, _ = l.ip(ctx, "route", "flush", "cache")
	return nil
}

func (l vpnGateLinuxLink) showRouteTable(ctx context.Context, table string) (string, error) {
	return l.ip(ctx, "route", "show", "table", table)
}

// ensureFailClosed validates the table and then installs the unreachable guard
// before the fwmark rule. A missing VPN default therefore terminates lookup in
// this table instead of continuing at the main-table rule.
func (l vpnGateLinuxLink) ensureFailClosed(ctx context.Context, slot int, iface string) error {
	table := strconv.Itoa(vpnGateRouteTable(slot))
	output, err := l.showRouteTable(ctx, table)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		ownedGuard, ownedDefault := vpnGateManagedRoute(line, iface)
		if !ownedGuard && !ownedDefault {
			return fmt.Errorf("路由表 %s 已被其他配置占用：%s", table, line)
		}
	}
	if _, err := l.ip(ctx, "route", "replace", "unreachable", "default", "table", table, "proto", vpnGateRouteProtocol, "metric", vpnGateGuardMetric); err != nil {
		return err
	}
	priority := strconv.Itoa(vpnGateRulePriority + slot)
	if err := l.prepareOwnedRule(ctx, priority, "fwmark ", "lookup "+table); err != nil {
		return err
	}
	if _, err := l.ip(ctx, "rule", "add", "fwmark", strconv.Itoa(vpnGateRoutingMark(slot)), "table", table, "priority", priority); err != nil {
		return err
	}
	return nil
}

func (l vpnGateLinuxLink) removeOwnedRoutes(ctx context.Context, slot int, iface string, includeGuard bool) error {
	table := strconv.Itoa(vpnGateRouteTable(slot))
	output, err := l.showRouteTable(ctx, table)
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		guard, active := vpnGateManagedRoute(line, iface)
		if !active && !(includeGuard && guard) {
			continue
		}
		arguments := append([]string{"route", "del"}, strings.Fields(line)...)
		arguments = append(arguments, "table", table)
		_, _ = l.ip(ctx, arguments...)
	}
	return nil
}

func (l vpnGateLinuxLink) prepareOwnedRule(ctx context.Context, priority string, expected ...string) error {
	output, err := l.ip(ctx, "rule", "show")
	if err != nil {
		return err
	}
	prefix := priority + ":"
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		for _, fragment := range expected {
			if !strings.Contains(line, fragment) {
				return fmt.Errorf("策略路由优先级 %s 已被其他配置占用：%s", priority, line)
			}
		}
		for attempt := 0; attempt < 4; attempt++ {
			if _, err := l.ip(ctx, "rule", "del", "priority", priority); err != nil {
				break
			}
		}
		break
	}
	return nil
}

func (l vpnGateLinuxLink) removeRule(ctx context.Context, priority string, expected ...string) {
	output, err := l.ip(ctx, "rule", "show")
	if err != nil {
		return
	}
	prefix := priority + ":"
	owned := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		matches := true
		for _, fragment := range expected {
			if !strings.Contains(line, fragment) {
				matches = false
				break
			}
		}
		if matches {
			owned = true
			break
		}
	}
	if !owned {
		return
	}
	for attempt := 0; attempt < 4; attempt++ {
		if _, err := l.ip(ctx, "rule", "del", "priority", priority); err != nil {
			return
		}
	}
}

// Verify checks real reachability through the slot's interface and mark, never
// through the system default route or any configured proxy.
func (l vpnGateLinuxLink) Verify(ctx context.Context, slot int) error {
	iface := vpnGateInterfaceName(slot)
	address, err := l.interfaceAddress(iface)
	if err != nil {
		return err
	}
	mark := vpnGateRoutingMark(slot)
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		LocalAddr: &net.TCPAddr{IP: net.ParseIP(address)},
		Control: func(network, addressText string, connection syscall.RawConn) error {
			var controlErr error
			err := connection.Control(func(handle uintptr) {
				if bindErr := syscall.SetsockoptString(int(handle), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface); bindErr != nil {
					controlErr = bindErr
					return
				}
				controlErr = syscall.SetsockoptInt(int(handle), syscall.SOL_SOCKET, syscall.SO_MARK, mark)
			})
			if err != nil {
				return err
			}
			return controlErr
		},
	}
	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			DisableKeepAlives:     true,
			ResponseHeaderTimeout: 10 * time.Second,
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, vpnGateProbeURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "m-ui/"+appVersion)
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("%s 无法访问外网：%w", iface, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s 的连通性探测返回状态 %d", iface, response.StatusCode)
	}
	return nil
}

// Disconnect releases everything this slot owns and nothing else. The adapter
// itself is kept so a reconnect does not have to recreate it.
func (l vpnGateLinuxLink) Disconnect(ctx context.Context, slot int) error {
	iface := vpnGateInterfaceName(slot)
	l.releaseLease(ctx, slot, iface)
	_ = l.removeOwnedRoutes(ctx, slot, iface, false)
	// Keep the mark rule and unreachable guard during retries. Only the
	// lease-specific source rule is removed.
	if raw, err := os.ReadFile(l.sourceAddressPath(slot)); err == nil {
		l.removeRule(ctx, strconv.Itoa(vpnGateRulePriority+100+slot), "from "+strings.TrimSpace(string(raw)), "lookup "+strconv.Itoa(vpnGateRouteTable(slot)))
	}
	if _, err := l.vpncmd(ctx, "AccountDisconnect", vpnGateAccountName(slot)); err != nil {
		// A session that was already down is not a failure.
		return nil
	}
	return nil
}

// Cleanup is only called after the saved outbound disappears (or the panel is
// shutting down). It removes the exact account, adapter, rules and protocol-
// tagged routes owned by this slot.
func (l vpnGateLinuxLink) Cleanup(ctx context.Context, slot int) error {
	iface := vpnGateInterfaceName(slot)
	if !l.slotOwned(slot) {
		_ = l.removeOwnedRoutes(ctx, slot, iface, true)
		return nil
	}
	if err := l.ensureService(ctx); err != nil {
		return err
	}
	_ = l.Disconnect(ctx, slot)
	account := vpnGateAccountName(slot)
	list, err := l.vpncmd(ctx, "AccountList")
	if err != nil {
		return err
	}
	if vpnGateOutputMentions(list, account) {
		if _, err := l.vpncmd(ctx, "AccountDelete", account); err != nil {
			return err
		}
	}
	adapter := vpnGateAdapterName(slot)
	list, err = l.vpncmd(ctx, "NicList")
	if err != nil {
		return err
	}
	if vpnGateOutputMentions(list, adapter) {
		if _, err := l.vpncmd(ctx, "NicDisable", adapter); err != nil {
			l.manager.addLog(fmt.Sprintf("VPNGate 槽位 %d 禁用网卡 %s 失败，继续尝试删除: %v", slot, adapter, err))
		}
		if _, err := l.vpncmd(ctx, "NicDelete", adapter); err != nil {
			return err
		}
	}
	l.removeRule(ctx, strconv.Itoa(vpnGateRulePriority+slot), "fwmark ", "lookup "+strconv.Itoa(vpnGateRouteTable(slot)))
	if raw, err := os.ReadFile(l.sourceAddressPath(slot)); err == nil {
		l.removeRule(ctx, strconv.Itoa(vpnGateRulePriority+100+slot), "from "+strings.TrimSpace(string(raw)), "lookup "+strconv.Itoa(vpnGateRouteTable(slot)))
	}
	_ = l.removeOwnedRoutes(ctx, slot, iface, true)
	_ = os.Remove(l.slotOwnerPath(slot))
	_ = os.Remove(l.sourceAddressPath(slot))
	_ = os.RemoveAll(l.runDir(slot))
	return nil
}

// releaseLease stops this slot's dhclient by its recorded pid, after checking
// the process really is a dhclient for this interface.
func (l vpnGateLinuxLink) releaseLease(ctx context.Context, slot int, iface string) {
	pidPath := filepath.Join(l.runDir(slot), "dhclient.pid")
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 1 {
		return
	}
	cmdline, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		_ = os.Remove(pidPath)
		return
	}
	arguments := strings.ReplaceAll(string(cmdline), "\x00", " ")
	if !strings.Contains(arguments, "dhclient") || !strings.Contains(arguments, iface) {
		// The pid was recycled by an unrelated process.
		_ = os.Remove(pidPath)
		return
	}
	process, err := os.FindProcess(pid)
	if err == nil {
		_ = process.Signal(syscall.SIGTERM)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat("/proc/" + strconv.Itoa(pid)); err != nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := os.Stat("/proc/" + strconv.Itoa(pid)); err == nil && process != nil {
		_ = process.Kill()
	}
	_ = os.Remove(pidPath)
	_ = ctx.Err()
}

// vpnGateDHCPConfig asks for the bare minimum. Notably it does not request
// domain-name-servers, because this feature must not rewrite the server's DNS.
const vpnGateDHCPConfig = `# Managed by m-ui. Do not edit: rewritten on every VPNGate connect.
request subnet-mask, broadcast-address, interface-mtu, routers;
timeout 20;
retry 30;
`

// vpnGateDHCPHook replaces dhclient's distribution script for this interface.
// It only configures the interface it was called for and deliberately does
// nothing that could reach the main table, the default route or resolv.conf.
const vpnGateDHCPHook = `#!/bin/sh
# Managed by m-ui. Applies a VPNGate lease to one interface and nothing else.
set -u
[ -n "${interface:-}" ] || exit 0
case "$interface" in
    vpn_vpn[0-9]) ;;
    *) exit 0 ;;
esac
fail() {
    printf 'm-ui VPNGate DHCP hook: %s\n' "$*" >&2
    exit 1
}
[ -n "${MUI_VPNGATE_TABLE:-}" ] &&
[ -n "${MUI_VPNGATE_MARK:-}" ] &&
[ -n "${MUI_VPNGATE_MARK_PRIORITY:-}" ] &&
[ -n "${MUI_VPNGATE_SOURCE_PRIORITY:-}" ] || fail "missing slot routing parameters for $interface"
case "$MUI_VPNGATE_TABLE:$MUI_VPNGATE_MARK:$MUI_VPNGATE_MARK_PRIORITY:$MUI_VPNGATE_SOURCE_PRIORITY" in
    *[!0-9:]*) fail "invalid slot routing parameters for $interface" ;;
esac

valid_ipv4() {
    awk -v ip="$1" 'BEGIN{
        n=split(ip, p, "."); if (n != 4) exit 1;
        for (i=1; i<=4; i++) if (p[i] !~ /^[0-9]+$/ || p[i]+0 < 0 || p[i]+0 > 255) exit 1;
        if (p[1]+0 == 0 || p[1]+0 == 127 || p[1]+0 >= 224 || (p[1]+0 == 169 && p[2]+0 == 254)) exit 1;
        exit 0
    }'
}

apply_address() {
    [ -n "${new_ip_address:-}" ] || return 1
    [ -n "${new_subnet_mask:-}" ] || return 1
    gateway=$(printf '%s\n' "${new_routers:-}" | awk '{print $1}')
    [ -n "$gateway" ] || return 1
    valid_ipv4 "$new_ip_address" || return 1
    valid_ipv4 "$gateway" || return 1
    prefix=$(awk -v mask="$new_subnet_mask" 'BEGIN{
        n=split(mask, p, "."); if (n != 4) exit 1; bits=0; zero=0;
        for (i=1; i<=4; i++) {
            v=p[i]+0; if (p[i] !~ /^[0-9]+$/ || v < 0 || v > 255) exit 1;
            for (b=7; b>=0; b--) { bit=int(v/(2^b))%2; if (zero && bit) exit 1; if (bit) bits++; else zero=1; }
        }
        if (bits < 1 || bits > 32) exit 1; print bits
    }') || return 1
    ip -4 addr flush dev "$interface" 2>/dev/null
    ip -4 addr add "$new_ip_address/$prefix" dev "$interface" noprefixroute 2>/dev/null || return 1
    ip link set dev "$interface" up 2>/dev/null
    if [ -n "${new_interface_mtu:-}" ]; then
        case "$new_interface_mtu" in *[!0-9]*) return 1;; esac
        [ "$new_interface_mtu" -ge 576 ] 2>/dev/null && [ "$new_interface_mtu" -le 9000 ] 2>/dev/null || return 1
        ip link set dev "$interface" mtu "$new_interface_mtu" 2>/dev/null || return 1
    fi
    ip route replace unreachable default table "$MUI_VPNGATE_TABLE" proto 242 metric 42760 2>/dev/null || return 1
    mark_hex=$(printf '0x%x' "$MUI_VPNGATE_MARK") || return 1
    mark_rule=$(ip rule show | awk -v p="$MUI_VPNGATE_MARK_PRIORITY:" '$1 == p {print}')
    if [ -n "$mark_rule" ]; then
        case "$mark_rule" in *"fwmark $mark_hex"*"lookup $MUI_VPNGATE_TABLE"*) ip rule del priority "$MUI_VPNGATE_MARK_PRIORITY" 2>/dev/null || return 1;; *) return 1;; esac
    fi
    ip rule add fwmark "$MUI_VPNGATE_MARK" table "$MUI_VPNGATE_TABLE" priority "$MUI_VPNGATE_MARK_PRIORITY" 2>/dev/null || return 1
    source_rule=$(ip rule show | awk -v p="$MUI_VPNGATE_SOURCE_PRIORITY:" '$1 == p {print}')
    if [ -n "$source_rule" ]; then
        case "$source_rule" in *"from $new_ip_address"*"lookup $MUI_VPNGATE_TABLE"*) ip rule del priority "$MUI_VPNGATE_SOURCE_PRIORITY" 2>/dev/null || return 1;; *) return 1;; esac
    fi
    ip rule add from "$new_ip_address" table "$MUI_VPNGATE_TABLE" priority "$MUI_VPNGATE_SOURCE_PRIORITY" 2>/dev/null || return 1
    ip route replace default via "$gateway" dev "$interface" onlink table "$MUI_VPNGATE_TABLE" proto 242 metric 10 2>/dev/null || return 1
}

case "$reason" in
    BOUND|RENEW|REBIND|REBOOT)
        apply_address || fail "could not apply $reason lease to $interface"
        ;;
    EXPIRE|FAIL|RELEASE|STOP)
        # Only this interface is cleared; the panel reinstalls its routes on the
        # next connect, and the slot fails closed until then.
        ip -4 addr flush dev "$interface" 2>/dev/null
        source_rule=$(ip rule show | awk -v p="$MUI_VPNGATE_SOURCE_PRIORITY:" '$1 == p {print}')
        case "$source_rule" in *"from "*"lookup $MUI_VPNGATE_TABLE"*) ip rule del priority "$MUI_VPNGATE_SOURCE_PRIORITY" 2>/dev/null;; esac
        ip route del default dev "$interface" table "$MUI_VPNGATE_TABLE" proto 242 2>/dev/null
        ;;
esac
exit 0
`
