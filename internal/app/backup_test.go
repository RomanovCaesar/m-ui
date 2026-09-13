package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

// backupFile 把内存里的 ZIP 包装成 multipart.File，省得为每个用例落一次盘。
type backupFile struct{ *bytes.Reader }

func (backupFile) Close() error { return nil }

func newBackupFile(data []byte) backupFile { return backupFile{bytes.NewReader(data)} }

// backupPanel 造一台带 peers 与 cross 缓存的面板。
func backupPanel(t *testing.T, name string) (*App, *meshTestPanel) {
	t.Helper()
	app, panel := newSyncPanel(t, name)
	manager, err := newCrossSubscriptionManager(panel.dir)
	if err != nil {
		t.Fatal(err)
	}
	app.cross = manager
	return app, panel
}

func exportBackupBytes(t *testing.T, a *App) []byte {
	t.Helper()
	recorder := httptest.NewRecorder()
	a.exportBackup(recorder)
	if recorder.Code != 200 {
		t.Fatalf("export status %d: %s", recorder.Code, recorder.Body.String())
	}
	return recorder.Body.Bytes()
}

func backupEntries(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{}
	for _, file := range reader.File {
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(stream)
		_ = stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		entries[file.Name] = data
	}
	return entries
}

func buildBackup(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	buffer := &bytes.Buffer{}
	archive := zip.NewWriter(buffer)
	for name, data := range entries {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = writer.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func backupManifest(t *testing.T, format int) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"format": format, "app": "m-ui"})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// 备份要真的带上 state 之外的东西：多控身份和跨面板缓存也得进包、也得能读回来。
// 这条覆盖的是整条往返链路，漏打包和漏解包在这里都会露出来。
func TestBackupRoundTripCarriesPeersAndCrossCache(t *testing.T) {
	source, sourcePanel := backupPanel(t, "Panel Source")
	remote, remotePanel := backupPanel(t, "Panel Remote")
	connectMeshPanels(t, sourcePanel, remotePanel)
	remoteNode := remote.peers.view().Self
	if err := source.cross.update(remoteNode, []Inbound{syncTestInbound("Cached Remote", 23111)}, map[string]string{"alice": "abc123def4567890"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := source.manager.saveInbound(syncTestInbound("Local Alpha", 23112)); err != nil {
		t.Fatal(err)
	}
	source.manager.state.Settings.APISecret = "backup-round-trip"

	archive := exportBackupBytes(t, source)
	entries := backupEntries(t, archive)
	for _, name := range []string{backupStateFile, "config.yaml", backupPeerFile, backupCrossFile, "manifest.json"} {
		if len(entries[name]) == 0 {
			t.Fatalf("backup is missing %s, got %v", name, entries)
		}
	}
	var manifest struct {
		Format   int      `json:"format"`
		Contents []string `json:"contents"`
	}
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Format != backupFormatVersion {
		t.Fatalf("manifest format = %d, want %d", manifest.Format, backupFormatVersion)
	}
	if len(manifest.Contents) != 4 {
		t.Fatalf("manifest contents = %v", manifest.Contents)
	}

	target, _ := backupPanel(t, "Panel Target")
	outcome, err := target.importBackup(newBackupFile(archive), true)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Inbounds != 1 || !outcome.IdentityReplaced {
		t.Fatalf("outcome = %+v", outcome)
	}
	if target.manager.state.Settings.APISecret != "backup-round-trip" {
		t.Fatalf("state was not restored: %q", target.manager.state.Settings.APISecret)
	}
	restoredPeers := target.peers.exportDisk()
	sourcePeers := source.peers.exportDisk()
	if restoredPeers.Self.ID != sourcePeers.Self.ID || restoredPeers.PrivateKey != sourcePeers.PrivateKey {
		t.Fatalf("identity not restored: %s vs %s", restoredPeers.Self.ID, sourcePeers.Self.ID)
	}
	if len(restoredPeers.Peers) != len(sourcePeers.Peers) {
		t.Fatalf("peers = %d, want %d", len(restoredPeers.Peers), len(sourcePeers.Peers))
	}
	sources := target.cross.snapshot()
	cached, ok := sources[remoteNode.ID]
	if !ok || len(cached.Inbounds) != 1 || cached.Tokens["alice"] != "abc123def4567890" {
		t.Fatalf("cross cache not restored: %+v", sources)
	}
	// 恢复后的缓存必须真的落了盘，重开一遍管理器还在。
	reopened, err := newCrossSubscriptionManager(target.manager.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.snapshot()[remoteNode.ID]; !ok {
		t.Fatal("cross cache was not persisted")
	}
}

// 不勾"恢复身份"时本机 peerID 必须原地不动：同一份备份在两台机器上还原，两台面板
// 顶着同一个 peerID 上网就会在 mesh 里互相顶掉。备份里的对端要并进来，但等于本机
// ID 的那条得跳过——否则面板会把自己当成邻居。
func TestBackupRestoreKeepsLocalIdentityWhenNotReplacing(t *testing.T) {
	source, sourcePanel := backupPanel(t, "Panel Source")
	remote, remotePanel := backupPanel(t, "Panel Remote")
	connectMeshPanels(t, sourcePanel, remotePanel)
	_ = remote

	target, _ := backupPanel(t, "Panel Target")
	targetSelf := target.peers.exportDisk().Self

	archive := exportBackupBytes(t, source)
	entries := backupEntries(t, archive)
	var disk peerDisk
	if err := json.Unmarshal(entries[backupPeerFile], &disk); err != nil {
		t.Fatal(err)
	}
	disk.Peers[targetSelf.ID] = targetSelf
	rewritten, err := json.Marshal(disk)
	if err != nil {
		t.Fatal(err)
	}
	entries[backupPeerFile] = rewritten

	outcome, err := target.importBackup(newBackupFile(buildBackup(t, entries)), false)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.IdentityReplaced {
		t.Fatal("identity was replaced without being asked")
	}
	restored := target.peers.exportDisk()
	if restored.Self.ID != targetSelf.ID {
		t.Fatalf("local peerID changed: %s -> %s", targetSelf.ID, restored.Self.ID)
	}
	if _, exists := restored.Peers[targetSelf.ID]; exists {
		t.Fatal("the panel added itself as a peer")
	}
	if _, exists := restored.Peers[remote.peers.exportDisk().Self.ID]; !exists {
		t.Fatalf("peer list was not merged: %v", restored.Peers)
	}
	if outcome.Peers != len(restored.Peers) {
		t.Fatalf("outcome peers = %d, disk has %d", outcome.Peers, len(restored.Peers))
	}
}

// format 1 的老包只有 state.json，升级后必须还能导入，不能因为找不到新文件就报错。
// 版本比本机新的包要明确拒绝，而不是把不认识的字段静默丢掉。
func TestBackupFormatCompatibility(t *testing.T) {
	source, _ := backupPanel(t, "Panel Source")
	source.manager.state.Settings.APISecret = "legacy-archive"
	stateData, err := json.Marshal(source.manager.state)
	if err != nil {
		t.Fatal(err)
	}

	target, _ := backupPanel(t, "Panel Target")
	legacy := buildBackup(t, map[string][]byte{
		backupStateFile: stateData,
		"config.yaml":   []byte("# legacy\n"),
		"manifest.json": backupManifest(t, 1),
	})
	if _, err := target.importBackup(newBackupFile(legacy), false); err != nil {
		t.Fatalf("format 1 archive rejected: %v", err)
	}
	if target.manager.state.Settings.APISecret != "legacy-archive" {
		t.Fatal("format 1 archive did not restore state")
	}

	future := buildBackup(t, map[string][]byte{
		backupStateFile: stateData,
		"manifest.json": backupManifest(t, backupFormatVersion+1),
	})
	_, err = target.importBackup(newBackupFile(future), false)
	if err == nil || !strings.Contains(err.Error(), "备份版本过新") {
		t.Fatalf("future format error = %v", err)
	}
}

// 最要紧的一条：多控数据校验不过时，state 也不能已经落了盘。顺序写反了，用户会拿到
// 一台设置被换掉、身份却没换的面板，而且报的是失败——比整体拒绝还难收拾。
func TestBackupRejectsForgedIdentityBeforeTouchingState(t *testing.T) {
	source, _ := backupPanel(t, "Panel Source")
	source.manager.state.Settings.APISecret = "should-not-land"
	archive := exportBackupBytes(t, source)
	entries := backupEntries(t, archive)
	var disk peerDisk
	if err := json.Unmarshal(entries[backupPeerFile], &disk); err != nil {
		t.Fatal(err)
	}
	disk.Self.PublicKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	forged, err := json.Marshal(disk)
	if err != nil {
		t.Fatal(err)
	}
	entries[backupPeerFile] = forged

	target, _ := backupPanel(t, "Panel Target")
	before := target.manager.state.Settings.APISecret
	beforeIdentity := target.peers.exportDisk().Self.ID
	if _, err := target.importBackup(newBackupFile(buildBackup(t, entries)), true); err == nil {
		t.Fatal("a forged identity was accepted")
	}
	if target.manager.state.Settings.APISecret != before {
		t.Fatalf("state was written despite the rejection: %q", target.manager.state.Settings.APISecret)
	}
	if target.peers.exportDisk().Self.ID != beforeIdentity {
		t.Fatal("identity changed despite the rejection")
	}
}

// 证书内容不进备份，所以还原后路径可能指向不存在的文件。面板会静默回落 HTTP，
// 必须有一条提示，否则用户以为 HTTPS 还在。
func TestBackupWarnsAboutMissingTLSCertificate(t *testing.T) {
	source, _ := backupPanel(t, "Panel Source")
	source.manager.state.Settings.PanelCertFile = "/nonexistent/m-ui/fullchain.pem"
	source.manager.state.Settings.PanelKeyFile = "/nonexistent/m-ui/privkey.pem"
	archive := exportBackupBytes(t, source)

	target, _ := backupPanel(t, "Panel Target")
	outcome, err := target.importBackup(newBackupFile(archive), false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, warning := range outcome.Warnings {
		if strings.Contains(warning, "TLS") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no TLS warning: %+v", outcome.Warnings)
	}
	// 提示必须能翻成英文，否则英文面板上会弹一句中文。
	for _, warning := range outcome.Warnings {
		if translated := translateMessage(warning, "en"); hasCJK(translated) {
			t.Fatalf("warning %q has no English translation", warning)
		}
	}
}

// 跨面板缓存里的节点签名同样要校验，否则一份伪造备份就能往缓存里塞任意来源。
func TestBackupRejectsForgedCrossSubscriptionSource(t *testing.T) {
	source, sourcePanel := backupPanel(t, "Panel Source")
	remote, remotePanel := backupPanel(t, "Panel Remote")
	connectMeshPanels(t, sourcePanel, remotePanel)
	if err := source.cross.update(remote.peers.view().Self, []Inbound{syncTestInbound("Cached Remote", 23121)}, nil, nil); err != nil {
		t.Fatal(err)
	}
	entries := backupEntries(t, exportBackupBytes(t, source))
	var disk crossSubscriptionCacheDisk
	if err := json.Unmarshal(entries[backupCrossFile], &disk); err != nil {
		t.Fatal(err)
	}
	for id, cached := range disk.Sources {
		cached.Node.Name = "Renamed After Signing"
		disk.Sources[id] = cached
	}
	rewritten, err := json.Marshal(disk)
	if err != nil {
		t.Fatal(err)
	}
	entries[backupCrossFile] = rewritten

	target, _ := backupPanel(t, "Panel Target")
	if _, err := target.importBackup(newBackupFile(buildBackup(t, entries)), false); err == nil {
		t.Fatal("a forged cross-panel cache entry was accepted")
	}
}
