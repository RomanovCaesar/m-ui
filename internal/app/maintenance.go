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
		a.manager.exportBackup(w)
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
		if err := a.manager.importBackup(file); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{OK: true, Message: a.tr("备份恢复成功，请按需重启核心")})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
	}
}

func (m *CoreManager) exportBackup(w http.ResponseWriter) {
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
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="m-ui-backup-%s.zip"`, time.Now().Format("20060102-150405")))
	archive := zip.NewWriter(w)
	stateFile, _ := archive.Create("state.json")
	_, _ = stateFile.Write(stateData)
	configFile, _ := archive.Create("config.yaml")
	_, _ = configFile.Write(config)
	manifestFile, _ := archive.Create("manifest.json")
	manifest, _ := json.MarshalIndent(map[string]any{"format": 1, "app": "m-ui", "version": appVersion, "createdAt": time.Now().Format(time.RFC3339)}, "", "  ")
	_, _ = manifestFile.Write(manifest)
	_ = archive.Close()
}

func (m *CoreManager) importBackup(source multipart.File) error {
	temporary, err := os.CreateTemp(m.dataDir, "restore-*.zip")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	written, err := io.Copy(temporary, io.LimitReader(source, maximumBackupInput+1))
	closeErr := temporary.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if written > maximumBackupInput {
		return fmt.Errorf("备份文件超过 32 MB 限制")
	}
	reader, err := zip.OpenReader(temporaryPath)
	if err != nil {
		return fmt.Errorf("备份文件不是有效的 ZIP")
	}
	defer reader.Close()
	var stateData []byte
	for _, file := range reader.File {
		if file.Name != "state.json" || file.UncompressedSize64 > 4<<20 {
			continue
		}
		stream, err := file.Open()
		if err != nil {
			return err
		}
		stateData, err = io.ReadAll(io.LimitReader(stream, 4<<20))
		_ = stream.Close()
		if err != nil {
			return err
		}
		break
	}
	if len(stateData) == 0 {
		return fmt.Errorf("备份中缺少 state.json")
	}
	var restored State
	if err := json.Unmarshal(stateData, &restored); err != nil {
		return fmt.Errorf("state.json 无效: %w", err)
	}
	if restored.Settings.Username == "" || restored.Settings.Password == "" || restored.Settings.APIAddress == "" {
		return fmt.Errorf("备份缺少必要的面板设置")
	}
	if restored.Settings.MixedPort < 1 || restored.Settings.MixedPort > 65535 {
		return fmt.Errorf("备份中的默认端口无效")
	}
	if err := validateSubscriptionServicePort(restored.Settings, restored.Inbounds); err != nil {
		return fmt.Errorf("备份中的订阅服务端口无效: %w", err)
	}
	outbounds, err := normalizeMihomoOutbounds(restored.Outbounds)
	if err != nil {
		return fmt.Errorf("备份中的 Mihomo Outbounds 无效: %w", err)
	}
	routingRules, err := normalizeMihomoRoutingRules(restored.RoutingRules, outbounds)
	if err != nil {
		return fmt.Errorf("备份中的 Mihomo Routing Rules 无效: %w", err)
	}
	restored.Outbounds, restored.RoutingRules = outbounds, routingRules
	basics, err := normalizeMihomoBasics(effectiveMihomoBasics(restored))
	if err != nil {
		return fmt.Errorf("备份中的 Mihomo Basics 无效: %w", err)
	}
	if _, _, _, err = compileMihomoBasics(basics, outbounds, routingRules); err != nil {
		return err
	}
	restored.MihomoBasics = &basics
	if err = validateWarpAccount(restored.WARP); err != nil {
		return fmt.Errorf("备份中的 WARP 账户无效: %w", err)
	}
	m.warpMu.Lock()
	defer m.warpMu.Unlock()
	m.mu.Lock()
	m.state = restored
	m.versionCache = ""
	err = m.saveLocked()
	m.mu.Unlock()
	if err == nil {
		m.mu.Lock()
		m.addLogLocked("面板数据已从备份恢复")
		m.mu.Unlock()
	}
	return err
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
			err = writeStreamAtomically(destination, io.LimitReader(stream, maximumDownload+1), 0755)
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
	return writeStreamAtomically(destination, io.LimitReader(stream, maximumDownload+1), 0755)
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
	return writeStreamAtomically(destination, io.LimitReader(response.Body, maximum+1), 0600)
}

func isApprovedDownloadHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "github.com" || host == "api.github.com" || host == "objects.githubusercontent.com" || host == "release-assets.githubusercontent.com" || strings.HasSuffix(host, ".githubusercontent.com") || host == "fastly.jsdelivr.net" || host == "cdn.jsdelivr.net" || strings.HasSuffix(host, ".jsdelivr.net")
}

func writeStreamAtomically(destination string, source io.Reader, mode os.FileMode) error {
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
	if written > maximumDownload {
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
