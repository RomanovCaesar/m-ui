package app

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// The SoftEther release is pinned on purpose: the download URL, the archive
// layout and the build command are all decided here, never by the caller.
const (
	vpnGateReleaseTag  = "v4.44-9807-rtm"
	vpnGateReleaseDate = "2025.04.16"
	vpnGateReleaseBase = "https://github.com/SoftEtherVPN/SoftEtherVPN_Stable/releases/download/"

	vpnGateArchiveLimit    = 80 << 20
	vpnGateExtractLimit    = 400 << 20
	vpnGateExtractMaxFiles = 4000
	vpnGateBuildTimeout    = 20 * time.Minute
	vpnGateDownloadTimeout = 20 * time.Minute
)

// vpnGateBinaries are the only files copied out of the build tree.
var vpnGateBinaries = []struct {
	Name string
	Mode os.FileMode
}{
	{Name: "vpnclient", Mode: 0700},
	{Name: "vpncmd", Mode: 0700},
	{Name: "hamcore.se2", Mode: 0600},
}

// The upstream licence travels with the installed binaries. It is not needed
// for the readiness check, but must remain available to the administrator.
var vpnGateDocumentation = []struct {
	Name string
	Mode os.FileMode
}{
	{Name: "ReadMeFirst_License.txt", Mode: 0600},
	{Name: "ReadMeFirst_Important_Notices_en.txt", Mode: 0600},
}

// vpnGateArchiveName maps a Go architecture to the official asset. Only the two
// architectures the first release supports are listed; anything else is refused
// instead of guessed.
func vpnGateArchiveName(goarch string) (string, bool) {
	switch goarch {
	case "amd64":
		return fmt.Sprintf("softether-vpnclient-%s-%s-linux-x64-64bit.tar.gz", vpnGateReleaseTag, vpnGateReleaseDate), true
	case "arm64":
		return fmt.Sprintf("softether-vpnclient-%s-%s-linux-arm64-64bit.tar.gz", vpnGateReleaseTag, vpnGateReleaseDate), true
	default:
		return "", false
	}
}

// VPNGateInstallState is what the panel shows while an install runs.
type VPNGateInstallState struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Stage     string `json:"stage,omitempty"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
	Version   string `json:"version,omitempty"`
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
}

type vpnGateInstaller struct {
	mu      sync.Mutex
	running bool
	stage   string
	message string
	failure string
	cancel  context.CancelFunc
}

func (i *vpnGateInstaller) snapshot() (bool, string, string, string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.running, i.stage, i.message, i.failure
}

func (i *vpnGateInstaller) begin() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.running {
		return fmt.Errorf("VPNGate 安装正在进行中")
	}
	i.running, i.stage, i.message, i.failure, i.cancel = true, "prepare", "", "", nil
	return nil
}

func (i *vpnGateInstaller) setCancel(cancel context.CancelFunc) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.running {
		i.cancel = cancel
	}
}

func (i *vpnGateInstaller) stop() {
	i.mu.Lock()
	cancel := i.cancel
	i.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (i *vpnGateInstaller) step(stage, message string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.stage, i.message = stage, message
}

// finish always releases the lock flag, so a failed attempt can simply be
// retried without restarting the panel.
func (i *vpnGateInstaller) finish(err error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.running = false
	i.cancel = nil
	if err != nil {
		i.stage, i.failure = "failed", err.Error()
		return
	}
	i.stage, i.failure = "done", ""
}

// vpnGateHome is the private directory holding the binaries, the per-slot
// runtime files and the scratch space used while installing.
func (m *CoreManager) vpnGateHome() string {
	return filepath.Join(m.dataDir, "vpngate")
}

func (m *CoreManager) vpnGateBinary(name string) string {
	return filepath.Join(m.vpnGateHome(), name)
}

// vpnGateInstalled only reports true when every file is present, so a half
// finished install never looks like a working one.
func (m *CoreManager) vpnGateInstalled() bool {
	for _, binary := range vpnGateBinaries {
		info, err := os.Stat(m.vpnGateBinary(binary.Name))
		if err != nil || info.IsDir() || info.Size() == 0 {
			return false
		}
	}
	return true
}

func vpnGatePlatformSupported() (bool, string) {
	if runtime.GOOS != "linux" {
		return false, "VPNGate 只支持 Linux 服务器"
	}
	if _, ok := vpnGateArchiveName(runtime.GOARCH); !ok {
		return false, fmt.Sprintf("VPNGate 暂不支持 %s 架构", runtime.GOARCH)
	}
	return true, ""
}

func (m *CoreManager) vpnGateInstallState() VPNGateInstallState {
	supported, reason := vpnGatePlatformSupported()
	running, stage, message, failure := m.vpngateInstaller.snapshot()
	state := VPNGateInstallState{
		Installed: m.vpnGateInstalled(),
		Running:   running,
		Stage:     stage,
		Message:   message,
		Error:     failure,
		Supported: supported,
		Reason:    reason,
	}
	if state.Installed {
		state.Version = vpnGateReleaseTag
	}
	return state
}

// vpnGateRequiredTools are checked before downloading anything. The panel
// reports what is missing and lets the administrator install it; it never runs
// a package manager on its own.
var vpnGateRequiredTools = []struct {
	Binary  string
	Package string
}{
	{Binary: "make", Package: "make"},
	{Binary: "gcc", Package: "gcc"},
	{Binary: "ranlib", Package: "binutils"},
	{Binary: "ip", Package: "iproute2"},
	{Binary: "dhclient", Package: "isc-dhcp-client / dhclient"},
}

func vpnGatePreflight() error {
	if supported, reason := vpnGatePlatformSupported(); !supported {
		return fmt.Errorf("%s", reason)
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("VPNGate 需要以 root 运行面板")
	}
	if _, err := os.Stat("/dev/net/tun"); err != nil {
		return fmt.Errorf("缺少 /dev/net/tun，请先为服务器启用 TUN/TAP")
	}
	var missing []string
	for _, tool := range vpnGateRequiredTools {
		if _, err := exec.LookPath(tool.Binary); err != nil {
			missing = append(missing, tool.Binary+"("+tool.Package+")")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("缺少以下依赖，请先自行安装：%s", strings.Join(missing, "、"))
	}
	return nil
}

// prepareVPNGateHome creates the private directory and refuses to work through
// a symlink someone else may control.
func (m *CoreManager) prepareVPNGateHome() (string, error) {
	home := m.vpnGateHome()
	if err := os.MkdirAll(home, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(home)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("VPNGate 目录不能是符号链接")
	}
	if !info.IsDir() {
		return "", fmt.Errorf("VPNGate 目录被同名文件占用")
	}
	if err := os.Chmod(home, 0700); err != nil {
		return "", err
	}
	return home, nil
}

// installVPNGate downloads, unpacks, builds and installs the official client.
// It never holds m.mu: the whole flow is network and disk bound.
func (m *CoreManager) installVPNGate(ctx context.Context) error {
	if err := m.vpngateInstaller.begin(); err != nil {
		return err
	}
	installCtx, cancel := context.WithCancel(ctx)
	m.vpngateInstaller.setCancel(cancel)
	defer cancel()
	err := m.runVPNGateInstall(installCtx)
	m.vpngateInstaller.finish(err)
	return err
}

func (m *CoreManager) runVPNGateInstall(ctx context.Context) error {
	if m.vpnGateInstalled() {
		return fmt.Errorf("VPNGate 已经安装")
	}
	if err := vpnGatePreflight(); err != nil {
		return err
	}
	archive, ok := vpnGateArchiveName(runtime.GOARCH)
	if !ok {
		return fmt.Errorf("VPNGate 暂不支持 %s 架构", runtime.GOARCH)
	}
	home, err := m.prepareVPNGateHome()
	if err != nil {
		return err
	}
	workspace, err := os.MkdirTemp(home, "install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workspace)
	if err := os.Chmod(workspace, 0700); err != nil {
		return err
	}

	m.vpngateInstaller.step("download", archive)
	m.addLog("VPNGate: 开始下载 " + archive)
	tarball := filepath.Join(workspace, "vpnclient.tar.gz")
	downloadCtx, cancelDownload := context.WithTimeout(ctx, vpnGateDownloadTimeout)
	defer cancelDownload()
	if err := downloadOfficialFile(downloadCtx, vpnGateReleaseBase+vpnGateReleaseTag+"/"+archive, tarball, vpnGateArchiveLimit); err != nil {
		return fmt.Errorf("下载 VPNGate 客户端失败：%w", err)
	}

	m.vpngateInstaller.step("extract", "")
	unpacked := filepath.Join(workspace, "tree")
	if err := os.MkdirAll(unpacked, 0700); err != nil {
		return err
	}
	if err := extractVPNGateArchive(tarball, unpacked); err != nil {
		return fmt.Errorf("解压 VPNGate 客户端失败：%w", err)
	}
	source, err := findVPNGateSourceDir(unpacked)
	if err != nil {
		return err
	}

	m.vpngateInstaller.step("build", "")
	m.addLog("VPNGate: 开始编译客户端")
	if err := m.buildVPNGate(ctx, source); err != nil {
		return err
	}

	m.vpngateInstaller.step("install", "")
	if err := m.placeVPNGateBinaries(source, home); err != nil {
		return err
	}
	m.addLog("VPNGate: 客户端安装完成 " + vpnGateReleaseTag)
	return nil
}

// buildVPNGate runs the vendor Makefile's `main` target, which skips the
// interactive licence prompt the default target shows.
func (m *CoreManager) buildVPNGate(ctx context.Context, source string) error {
	buildCtx, cancel := context.WithTimeout(ctx, vpnGateBuildTimeout)
	defer cancel()
	command := exec.CommandContext(buildCtx, "make", "main")
	command.Dir = source
	command.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("编译 VPNGate 客户端失败：%s", vpnGateTailLines(string(output), 10))
	}
	return nil
}

// placeVPNGateBinaries copies the three files into place only after all of them
// were built, so a failed attempt can never damage a working install.
func (m *CoreManager) placeVPNGateBinaries(source, home string) error {
	files := append(append([]struct {
		Name string
		Mode os.FileMode
	}{}, vpnGateBinaries...), vpnGateDocumentation...)
	staged := make([]string, 0, len(files))
	for _, binary := range files {
		origin := filepath.Join(source, binary.Name)
		info, err := os.Stat(origin)
		if err != nil || info.IsDir() || info.Size() == 0 {
			return fmt.Errorf("编译结果缺少 %s", binary.Name)
		}
		if info.Size() > vpnGateExtractLimit {
			return fmt.Errorf("编译结果 %s 超过大小限制", binary.Name)
		}
		temporary := filepath.Join(home, "."+binary.Name+".new")
		if err := copyVPNGateFile(origin, temporary, binary.Mode); err != nil {
			for _, path := range staged {
				_ = os.Remove(path)
			}
			return err
		}
		staged = append(staged, temporary)
	}
	backups := make([]string, len(files))
	installed := 0
	rollback := func() {
		for index := installed - 1; index >= 0; index-- {
			target := filepath.Join(home, files[index].Name)
			_ = os.Remove(target)
			if backups[index] != "" {
				_ = os.Rename(backups[index], target)
			}
		}
		for _, path := range staged[installed:] {
			_ = os.Remove(path)
		}
	}
	for index, binary := range files {
		target := filepath.Join(home, binary.Name)
		backup := filepath.Join(home, "."+binary.Name+".old")
		_ = os.Remove(backup)
		if _, err := os.Stat(target); err == nil {
			if err := os.Rename(target, backup); err != nil {
				rollback()
				return err
			}
			backups[index] = backup
		} else if !os.IsNotExist(err) {
			rollback()
			return err
		}
		if err := os.Rename(staged[index], target); err != nil {
			if backups[index] != "" {
				_ = os.Rename(backups[index], target)
			}
			rollback()
			return err
		}
		installed++
	}
	for _, backup := range backups {
		if backup != "" {
			_ = os.Remove(backup)
		}
	}
	return nil
}

func copyVPNGateFile(origin, destination string, mode os.FileMode) error {
	input, err := os.Open(origin)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, io.LimitReader(input, vpnGateExtractLimit)); err != nil {
		output.Close()
		_ = os.Remove(destination)
		return err
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(destination)
		return err
	}
	return os.Chmod(destination, mode)
}

// findVPNGateSourceDir locates the vpnclient directory the archive unpacks to
// without trusting the archive to have used that exact name.
func findVPNGateSourceDir(root string) (string, error) {
	if _, err := os.Stat(filepath.Join(root, "Makefile")); err == nil {
		return root, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(root, entry.Name())
		if _, err := os.Stat(filepath.Join(candidate, "Makefile")); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("VPNGate 压缩包里没有找到 Makefile")
}

// extractVPNGateArchive unpacks a gzip tar with hard limits. Anything unusual —
// a path leaving the destination, a link, a device node, a setuid bit — aborts
// the whole extraction instead of being skipped quietly.
func extractVPNGateArchive(archive, destination string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	root, err := filepath.Abs(destination)
	if err != nil {
		return err
	}

	reader := tar.NewReader(gzipReader)
	var files, total int64
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		files++
		if files > vpnGateExtractMaxFiles {
			return fmt.Errorf("压缩包文件数量超过上限")
		}
		target, err := safeVPNGatePath(root, header.Name)
		if err != nil {
			return err
		}
		if header.Mode&07000 != 0 {
			return fmt.Errorf("压缩包内存在异常权限：%s", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
		case tar.TypeReg:
			if header.Size < 0 || header.Size > vpnGateExtractLimit {
				return fmt.Errorf("压缩包内文件过大：%s", header.Name)
			}
			total += header.Size
			if total > vpnGateExtractLimit {
				return fmt.Errorf("压缩包解压后体积超过上限")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			mode := os.FileMode(0600)
			if header.Mode&0111 != 0 {
				mode = 0700
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			written, copyErr := io.Copy(out, io.LimitReader(reader, header.Size))
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if written != header.Size {
				return fmt.Errorf("压缩包内文件不完整：%s", header.Name)
			}
		default:
			// Symlinks, hard links, devices, fifos and sockets have no place in
			// this archive, so their presence means the download is not what we
			// expect.
			return fmt.Errorf("压缩包内存在不允许的条目类型：%s", header.Name)
		}
	}
	if files == 0 {
		return fmt.Errorf("压缩包是空的")
	}
	return nil
}

func safeVPNGatePath(root, name string) (string, error) {
	cleaned := filepath.Clean(strings.ReplaceAll(name, "\\", "/"))
	if cleaned == "." || cleaned == string(filepath.Separator) {
		return root, nil
	}
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("压缩包内路径越界：%s", name)
	}
	target := filepath.Join(root, cleaned)
	if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
		return "", fmt.Errorf("压缩包内路径越界：%s", name)
	}
	return target, nil
}

func vpnGateTailLines(text string, count int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.TrimSpace(strings.Join(lines, " / "))
}
