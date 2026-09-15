package app

import (
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	githubAPIBase      = "https://api.github.com/repos/MetaCubeX/mihomo"
	maximumDownload    = 160 << 20
	maximumBackupInput = 32 << 20
	// backupFormatVersion 2 adds multi-control.json and the cross-panel
	// subscription cache. Format 1 archives (state.json only, on restore) are
	// still accepted.
	backupFormatVersion = 2
	backupStateFile     = "state.json"
	backupPeerFile      = "multi-control.json"
	backupCrossFile     = crossSubscriptionCacheFile
)

type githubRelease struct {
	TagName    string        `json:"tag_name"`
	Name       string        `json:"name"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Published  string        `json:"published_at"`
	Assets     []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name       string `json:"name"`
	URL        string `json:"browser_download_url"`
	Size       int64  `json:"size"`
	Digest     string `json:"digest"`
	Downloaded int64  `json:"download_count"`
}

type releaseView struct {
	Tag        string `json:"tag"`
	Name       string `json:"name"`
	Published  string `json:"publishedAt"`
	Prerelease bool   `json:"prerelease"`
	Asset      string `json:"asset"`
	Available  bool   `json:"available"`
}

func (a *App) handleConfigDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	config, err := a.manager.actualConfig()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{Message: a.tr(err.Error())})
		return
	}
	w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="config.yaml"`)
	_, _ = w.Write(config)
}

func (a *App) handleLogsDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	a.manager.mu.Lock()
	logs := append([]string(nil), a.manager.logs...)
	a.manager.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="mihomo.log"`)
	_, _ = io.WriteString(w, strings.Join(logs, "\n")+"\n")
}

func (a *App) handleBackup(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.exportBackup(w)
	case http.MethodPost:
		if err := r.ParseMultipartForm(maximumBackupInput); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr("无法读取备份文件")})
			return
		}
		file, _, err := r.FormFile("backup")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr("请选择 m-ui 备份文件")})
			return
		}
		defer file.Close()
		outcome, err := a.importBackup(file, r.FormValue("replaceIdentity") == "1")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
			return
		}
		for index, warning := range outcome.Warnings {
			outcome.Warnings[index] = a.tr(warning)
		}
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Message: a.tr("备份恢复成功，请按需重启核心"), Data: outcome})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
	}
}

// restoreOutcome reports what a restore actually touched so the panel can show
// more than a generic success message.
type restoreOutcome struct {
	Inbounds         int      `json:"inbounds"`
	Peers            int      `json:"peers"`
	IdentityReplaced bool     `json:"identityReplaced"`
	Warnings         []string `json:"warnings,omitempty"`
}

func (a *App) exportBackup(w http.ResponseWriter) {
	m := a.manager
	m.mu.Lock()
	state := m.state
	m.mu.Unlock()
	stateData, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{Message: m.tr(err.Error())})
		return
	}
	config, err := m.renderConfig()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{Message: m.tr(err.Error())})
		return
	}
	entries := []struct {
		name string
		data []byte
	}{
		{name: backupStateFile, data: stateData},
		{name: "config.yaml", data: config},
	}
	if a.peers != nil {
		if data, err := json.MarshalIndent(a.peers.exportDisk(), "", "  "); err == nil {
			entries = append(entries, struct {
				name string
				data []byte
			}{name: backupPeerFile, data: data})
		}
	}
	if a.cross != nil {
		if data, err := json.MarshalIndent(a.cross.exportDisk(), "", "  "); err == nil {
			entries = append(entries, struct {
				name string
				data []byte
			}{name: backupCrossFile, data: data})
		}
	}
	contents := make([]string, 0, len(entries))
	for _, entry := range entries {
		contents = append(contents, entry.name)
	}
	manifest, err := json.MarshalIndent(map[string]any{
		"format":    backupFormatVersion,
		"app":       "m-ui",
		"version":   appVersion,
		"createdAt": time.Now().Format(time.RFC3339),
		"contents":  contents,
	}, "", "  ")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{Message: m.tr(err.Error())})
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="m-ui-backup-%s.zip"`, time.Now().Format("20060102-150405")))
	archive := zip.NewWriter(w)
	for _, entry := range entries {
		writer, err := archive.Create(entry.name)
		if err != nil {
			_ = archive.Close()
			return
		}
		if _, err = writer.Write(entry.data); err != nil {
			_ = archive.Close()
			return
		}
	}
	manifestFile, err := archive.Create("manifest.json")
	if err != nil {
		_ = archive.Close()
		return
	}
	_, _ = manifestFile.Write(manifest)
	_ = archive.Close()
}

// readBackupEntry returns the named ZIP entry, or nil when it is absent. limit
// caps the declared and the actual size so a compression bomb cannot be read
// into memory.
func readBackupEntry(reader *zip.ReadCloser, name string, limit uint64) ([]byte, error) {
	for _, file := range reader.File {
		if file.Name != name {
			continue
		}
		if file.UncompressedSize64 > limit {
			return nil, fmt.Errorf("备份中的 %s 超过大小限制", name)
		}
		stream, err := file.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(stream, int64(limit)+1))
		_ = stream.Close()
		if err != nil {
			return nil, err
		}
		if uint64(len(data)) > limit {
			return nil, fmt.Errorf("备份中的 %s 超过大小限制", name)
		}
		return data, nil
	}
	return nil, nil
}

// importBackup restores a panel backup. Everything is parsed and validated
// before anything is written, so a forged or corrupt section cannot leave the
// panel with state.json already replaced and the rest rejected.
func (a *App) importBackup(source multipart.File, replaceIdentity bool) (restoreOutcome, error) {
	m := a.manager
	var outcome restoreOutcome
	temporary, err := os.CreateTemp(m.dataDir, "restore-*.zip")
	if err != nil {
		return outcome, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	written, err := io.Copy(temporary, io.LimitReader(source, maximumBackupInput+1))
	closeErr := temporary.Close()
	if err != nil {
		return outcome, err
	}
	if closeErr != nil {
		return outcome, closeErr
	}
	if written > maximumBackupInput {
		return outcome, fmt.Errorf("备份文件超过 32 MB 限制")
	}
	reader, err := zip.OpenReader(temporaryPath)
	if err != nil {
		return outcome, fmt.Errorf("备份文件不是有效的 ZIP")
	}
	defer reader.Close()
	format := 1
	manifestData, err := readBackupEntry(reader, "manifest.json", 64<<10)
	if err != nil {
		return outcome, err
	}
	if len(manifestData) > 0 {
		var manifest struct {
			Format int `json:"format"`
		}
		if err := json.Unmarshal(manifestData, &manifest); err != nil {
			return outcome, fmt.Errorf("manifest.json 无效: %w", err)
		}
		if manifest.Format > 0 {
			format = manifest.Format
		}
	}
	if format > backupFormatVersion {
		return outcome, fmt.Errorf("备份版本过新，请升级 m-ui")
	}
	stateData, err := readBackupEntry(reader, backupStateFile, 4<<20)
	if err != nil {
		return outcome, err
	}
	if len(stateData) == 0 {
		return outcome, fmt.Errorf("备份中缺少 state.json")
	}
	var restored State
	if err := json.Unmarshal(stateData, &restored); err != nil {
		return outcome, fmt.Errorf("state.json 无效: %w", err)
	}
	restored.Settings.IPInfoToken, err = normalizeIPInfoToken(restored.Settings.IPInfoToken)
	if err != nil {
		return outcome, fmt.Errorf("备份中的 IPinfo Token 无效: %w", err)
	}
	restored.Settings.IPInfoTokenSet, restored.Settings.IPInfoTokenClear = false, false
	if restored.Settings.Username == "" || restored.Settings.Password == "" || restored.Settings.APIAddress == "" {
		return outcome, fmt.Errorf("备份缺少必要的面板设置")
	}
	if restored.Settings.MixedPort < 1 || restored.Settings.MixedPort > 65535 {
		return outcome, fmt.Errorf("备份中的默认端口无效")
	}
	if err := validateSubscriptionServicePort(restored.Settings, restored.Inbounds); err != nil {
		return outcome, fmt.Errorf("备份中的订阅服务端口无效: %w", err)
	}
	outbounds, err := normalizeMihomoOutbounds(restored.Outbounds)
	if err != nil {
		return outcome, fmt.Errorf("备份中的 Mihomo Outbounds 无效: %w", err)
	}
	routingRules, err := normalizeMihomoRoutingRules(restored.RoutingRules, outbounds)
	if err != nil {
		return outcome, fmt.Errorf("备份中的 Mihomo Routing Rules 无效: %w", err)
	}
	restored.Outbounds, restored.RoutingRules = outbounds, routingRules
	basics, err := normalizeMihomoBasics(effectiveMihomoBasics(restored))
	if err != nil {
		return outcome, fmt.Errorf("备份中的 Mihomo Basics 无效: %w", err)
	}
	if _, _, _, err = compileMihomoBasics(basics, outbounds, routingRules); err != nil {
		return outcome, err
	}
	restored.MihomoBasics = &basics
	if err = validateWarpAccount(restored.WARP); err != nil {
		return outcome, fmt.Errorf("备份中的 WARP 账户无效: %w", err)
	}
	var peerDiskData *peerDisk
	var crossDiskData *crossSubscriptionCacheDisk
	if format >= 2 {
		data, err := readBackupEntry(reader, backupPeerFile, 1<<20)
		if err != nil {
			return outcome, err
		}
		if len(data) > 0 {
			var incoming peerDisk
			if err := json.Unmarshal(data, &incoming); err != nil {
				return outcome, fmt.Errorf("备份中的 Multi-control 数据无效: %w", err)
			}
			if err := validatePeerDisk(&incoming); err != nil {
				return outcome, err
			}
			peerDiskData = &incoming
		}
		data, err = readBackupEntry(reader, backupCrossFile, 8<<20)
		if err != nil {
			return outcome, err
		}
		if len(data) > 0 {
			var incoming crossSubscriptionCacheDisk
			if err := json.Unmarshal(data, &incoming); err != nil {
				return outcome, fmt.Errorf("备份中的跨面板订阅缓存无效: %w", err)
			}
			if _, err := validateCrossSubscriptionDisk(incoming); err != nil {
				return outcome, err
			}
			crossDiskData = &incoming
		}
	}
	if peerDiskData != nil && a.peers == nil {
		outcome.Warnings = append(outcome.Warnings, "Multi-control 未启用，备份中的联机身份未恢复")
		peerDiskData = nil
	}
	if crossDiskData != nil && a.cross == nil {
		outcome.Warnings = append(outcome.Warnings, "跨面板订阅未启用，备份中的订阅缓存未恢复")
		crossDiskData = nil
	}
	m.warpMu.Lock()
	defer m.warpMu.Unlock()
	m.mu.Lock()
	m.state = restored
	m.versionCache = ""
	err = m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		return outcome, err
	}
	if err := m.writeConfig(); err != nil {
		return outcome, err
	}
	outcome.Inbounds = len(restored.Inbounds)
	if peerDiskData != nil {
		if err := a.peers.restoreDisk(*peerDiskData, replaceIdentity); err != nil {
			outcome.Warnings = append(outcome.Warnings, fmt.Sprintf("Multi-control 恢复失败: %s", err.Error()))
		} else {
			outcome.IdentityReplaced = replaceIdentity
			outcome.Peers = len(a.peers.exportDisk().Peers)
		}
	}
	if crossDiskData != nil {
		if err := a.cross.restoreDisk(*crossDiskData); err != nil {
			outcome.Warnings = append(outcome.Warnings, fmt.Sprintf("跨面板订阅缓存恢复失败: %s", err.Error()))
		}
	}
	if configured, valid := panelTLSStatus(restored.Settings); configured && !valid {
		outcome.Warnings = append(outcome.Warnings, "TLS 证书文件缺失或无效，面板已回落 HTTP")
	}
	// 备份里只有 VPNGate 的逻辑配置，可执行文件、租约和 PID 都不在其中。所以
	// 恢复之后先停掉按旧配置跑的槽位，再按新配置重建；软件本身没装就只提示，
	// 不擅自去下载编译。
	if vpnGateOutbounds := findVPNGateOutbounds(restored.Outbounds); len(vpnGateOutbounds) > 0 {
		if supported, reason := vpnGatePlatformSupported(); !supported {
			outcome.Warnings = append(outcome.Warnings, reason)
		} else if !m.vpnGateInstalled() {
			outcome.Warnings = append(outcome.Warnings, "备份里的 VPNGate 出站需要先在 VPNGate 窗口点击 Install 安装客户端")
		}
	}
	m.restartVPNGateTasks()
	m.mu.Lock()
	m.addLogLocked("面板数据已从备份恢复")
	m.mu.Unlock()
	return outcome, nil
}

func (a *App) handleCoreReleases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	releases, err := fetchReleases(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, apiResponse{Message: a.tr(err.Error())})
		return
	}
	views := make([]releaseView, 0, len(releases))
	for _, release := range releases {
		if release.Draft || release.Prerelease {
			continue
		}
		asset, ok := selectReleaseAsset(release)
		views = append(views, releaseView{Tag: release.TagName, Name: release.Name, Published: release.Published, Prerelease: release.Prerelease, Asset: asset.Name, Available: ok})
		if len(views) >= 15 {
			break
		}
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: views})
}

func (a *App) handleCoreInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	var input struct {
		Tag string `json:"tag"`
	}
	if err := decodeJSON(r, &input); err != nil || input.Tag == "" || strings.ContainsAny(input.Tag, "/\\") {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr("版本标签无效")})
		return
	}
	version, err := a.manager.installCore(r.Context(), input.Tag)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, apiResponse{Message: a.tr(err.Error())})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Message: a.tr("Mihomo 已切换到 " + version)})
}

func (a *App) handleGeofileUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	if err := a.manager.updateGeofiles(r.Context()); err != nil {
		writeJSON(w, http.StatusBadGateway, apiResponse{Message: a.tr(err.Error())})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Message: a.tr("Geofile 更新完成")})
}

func fetchReleases(ctx context.Context) ([]githubRelease, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, githubAPIBase+"/releases?per_page=30", nil)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "m-ui/"+appVersion)
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("无法连接 GitHub: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub 返回状态 %d", response.StatusCode)
	}
	var releases []githubRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&releases); err != nil {
		return nil, err
	}
	return releases, nil
}

func fetchRelease(ctx context.Context, tag string) (githubRelease, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, githubAPIBase+"/releases/tags/"+url.PathEscape(tag), nil)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "m-ui/"+appVersion)
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
	if err != nil {
		return githubRelease{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return githubRelease{}, fmt.Errorf("GitHub 找不到版本 %s", tag)
	}
	var release githubRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&release); err != nil {
		return githubRelease{}, err
	}
	return release, nil
}

func selectReleaseAsset(release githubRelease) (githubAsset, bool) {
	extension := ".gz"
	if runtime.GOOS == "windows" {
		extension = ".zip"
	}
	prefixes := []string{}
	switch runtime.GOARCH {
	case "amd64":
		prefixes = []string{
			fmt.Sprintf("mihomo-%s-amd64-v1-%s%s", runtime.GOOS, release.TagName, extension),
			fmt.Sprintf("mihomo-%s-amd64-compatible-%s%s", runtime.GOOS, release.TagName, extension),
			fmt.Sprintf("mihomo-%s-amd64-%s%s", runtime.GOOS, release.TagName, extension),
		}
	default:
		prefixes = []string{fmt.Sprintf("mihomo-%s-%s-%s%s", runtime.GOOS, runtime.GOARCH, release.TagName, extension)}
	}
	for _, expected := range prefixes {
		for _, asset := range release.Assets {
			if asset.Name == expected && asset.URL != "" && asset.Size > 0 && asset.Size <= maximumDownload {
				return asset, true
			}
		}
	}
	return githubAsset{}, false
}

func (m *CoreManager) installCore(ctx context.Context, tag string) (string, error) {
	release, err := fetchRelease(ctx, tag)
	if err != nil {
		return "", err
	}
	asset, ok := selectReleaseAsset(release)
	if !ok {
		return "", fmt.Errorf("版本 %s 没有适合 %s/%s 的兼容构建", tag, runtime.GOOS, runtime.GOARCH)
	}
	m.mu.Lock()
	wasRunning := m.runningLocked()
	corePath := strings.TrimSpace(m.state.Settings.CorePath)
	m.mu.Unlock()
	if corePath == "" {
		return "", fmt.Errorf("未配置 Mihomo 核心路径")
	}
	if wasRunning {
		if err := m.stopCore(); err != nil {
			return "", err
		}
		time.Sleep(250 * time.Millisecond)
	}
	temporaryDirectory, err := os.MkdirTemp(m.dataDir, "core-update-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temporaryDirectory)
	archivePath := filepath.Join(temporaryDirectory, asset.Name)
	if err := downloadOfficialFile(ctx, asset.URL, archivePath, maximumDownload); err != nil {
		return "", err
	}
	if asset.Digest != "" && strings.HasPrefix(asset.Digest, "sha256:") {
		digest, err := fileSHA256(archivePath)
		if err != nil {
			return "", err
		}
		if !strings.EqualFold(digest, strings.TrimPrefix(asset.Digest, "sha256:")) {
			return "", fmt.Errorf("下载文件 SHA-256 校验失败")
		}
	}
	newCorePath := corePath + ".new"
	if err := unpackCoreArchive(archivePath, newCorePath); err != nil {
		return "", err
	}
	_ = os.Chmod(newCorePath, 0755)
	versionOutput, err := exec.Command(newCorePath, "-v").CombinedOutput()
	if err != nil {
		_ = os.Remove(newCorePath)
		return "", fmt.Errorf("新核心无法运行: %s", strings.TrimSpace(string(versionOutput)))
	}
	version := strings.TrimSpace(string(versionOutput))
	if newline := strings.IndexByte(version, '\n'); newline >= 0 {
		version = strings.TrimSpace(version[:newline])
	}
	backupPath := corePath + ".backup-" + time.Now().Format("20060102-150405")
	if err := os.Rename(corePath, backupPath); err != nil {
		_ = os.Remove(newCorePath)
		return "", fmt.Errorf("备份旧核心失败: %w", err)
	}
	if err := os.Rename(newCorePath, corePath); err != nil {
		_ = os.Rename(backupPath, corePath)
		return "", fmt.Errorf("替换核心失败: %w", err)
	}
	m.mu.Lock()
	m.versionCache = ""
	m.versionCorePath = ""
	m.addLogLocked("Mihomo 核心已切换: " + version)
	m.mu.Unlock()
	if wasRunning {
		if err := m.startCore(); err != nil {
			return version, fmt.Errorf("核心已更新，但重新启动失败: %w", err)
		}
	}
	return version, nil
}

func unpackCoreArchive(archivePath, destination string) error {
	if strings.HasSuffix(strings.ToLower(archivePath), ".zip") {
		archive, err := zip.OpenReader(archivePath)
		if err != nil {
			return err
		}
		defer archive.Close()
		for _, file := range archive.File {
			if file.FileInfo().IsDir() || file.UncompressedSize64 > maximumDownload {
				continue
			}
			stream, err := file.Open()
			if err != nil {
				return err
			}
			err = writeStreamAtomically(destination, io.LimitReader(stream, maximumDownload+1), 0755, maximumDownload)
			_ = stream.Close()
			return err
		}
		return fmt.Errorf("ZIP 中没有 Mihomo 可执行文件")
	}
	archive, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	stream, err := gzip.NewReader(archive)
	if err != nil {
		return err
	}
	defer stream.Close()
	return writeStreamAtomically(destination, io.LimitReader(stream, maximumDownload+1), 0755, maximumDownload)
}

func (m *CoreManager) updateGeofiles(ctx context.Context) error {
	files := []struct {
		Name string
		URLs []string
	}{
		{"GeoIP.dat", []string{"https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.dat", "https://fastly.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/geoip.dat"}},
		{"GeoSite.dat", []string{"https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat", "https://fastly.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/geosite.dat"}},
		{"geoip.metadb", []string{"https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.metadb", "https://fastly.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/geoip.metadb"}},
	}
	m.mu.Lock()
	wasRunning := m.runningLocked()
	m.mu.Unlock()
	if wasRunning {
		if err := m.stopCore(); err != nil {
			return err
		}
		time.Sleep(250 * time.Millisecond)
	}
	for _, file := range files {
		temporary := filepath.Join(m.dataDir, file.Name+".download")
		var downloadErr error
		for index, sourceURL := range file.URLs {
			downloadErr = downloadOfficialFile(ctx, sourceURL, temporary, maximumDownload)
			if downloadErr == nil {
				if index > 0 {
					m.mu.Lock()
					m.addLogLocked(file.Name + " 已通过 jsDelivr 备用线路下载")
					m.mu.Unlock()
				}
				break
			}
			m.mu.Lock()
			m.addLogLocked(fmt.Sprintf("下载 %s 线路 %d 失败: %v", file.Name, index+1, downloadErr))
			m.mu.Unlock()
		}
		if downloadErr != nil {
			_ = os.Remove(temporary)
			if wasRunning {
				_ = m.startCore()
			}
			return fmt.Errorf("下载 %s 失败：GitHub 与 jsDelivr 均不可用，请检查网络或 HTTPS_PROXY", file.Name)
		}
		target := filepath.Join(m.dataDir, file.Name)
		backup := target + ".backup"
		_ = os.Remove(backup)
		if _, err := os.Stat(target); err == nil {
			if err := os.Rename(target, backup); err != nil {
				return err
			}
		}
		if err := os.Rename(temporary, target); err != nil {
			_ = os.Rename(backup, target)
			return err
		}
	}
	m.mu.Lock()
	m.addLogLocked("GeoIP.dat、GeoSite.dat 与 geoip.metadb 已更新")
	m.mu.Unlock()
	if wasRunning {
		return m.startCore()
	}
	return nil
}

func downloadOfficialFile(ctx context.Context, sourceURL, destination string, maximum int64) error {
	parsed, err := url.Parse(sourceURL)
	if err != nil || parsed.Scheme != "https" || !isApprovedDownloadHost(parsed.Hostname()) {
		return fmt.Errorf("拒绝未授权的下载地址")
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	request.Header.Set("User-Agent", "m-ui/"+appVersion)
	client := &http.Client{Timeout: 15 * time.Minute, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) > 8 {
			return errors.New("too many redirects")
		}
		if request.URL.Scheme != "https" {
			return errors.New("insecure redirect")
		}
		if !isApprovedDownloadHost(request.URL.Hostname()) {
			return errors.New("redirected outside approved hosts")
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("下载服务器返回状态 %d", response.StatusCode)
	}
	if response.ContentLength > maximum {
		return fmt.Errorf("下载文件超过大小限制")
	}
	return writeStreamAtomically(destination, io.LimitReader(response.Body, maximum+1), 0600, maximum)
}

func isApprovedDownloadHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "github.com" || host == "api.github.com" || host == "objects.githubusercontent.com" || host == "release-assets.githubusercontent.com" || strings.HasSuffix(host, ".githubusercontent.com") || host == "fastly.jsdelivr.net" || host == "cdn.jsdelivr.net" || strings.HasSuffix(host, ".jsdelivr.net")
}

func writeStreamAtomically(destination string, source io.Reader, mode os.FileMode, maximum int64) error {
	temporary := destination + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, source)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(temporary)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(temporary)
		return closeErr
	}
	if written > maximum {
		_ = os.Remove(temporary)
		return fmt.Errorf("文件超过大小限制")
	}
	_ = os.Remove(destination)
	return os.Rename(temporary, destination)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
