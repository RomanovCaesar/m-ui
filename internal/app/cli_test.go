package app

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigureCLIInitializesAndUpdatesCompleteState(t *testing.T) {
	dir := t.TempDir()
	core := filepath.Join(dir, "mihomo")
	if err := runConfigureCLI([]string{"--data-dir", dir, "--listen-ip", "0.0.0.0", "--port", "34567", "--path", "AbC123xYz789Qwer", "--username", "AdminUser12345", "--password", "Password12345678", "--core-path", core}); err != nil {
		t.Fatal(err)
	}
	m := &CoreManager{dataDir: dir}
	if err := m.load(); err != nil {
		t.Fatal(err)
	}
	settings := m.state.Settings
	if settings.PanelListen != "0.0.0.0:34567" || settings.PanelPath != "/AbC123xYz789Qwer/" || settings.Username != "AdminUser12345" || settings.CorePath != core {
		t.Fatalf("unexpected settings: %+v", settings)
	}
	if !verifyPassword(settings.Password, "Password12345678") || settings.Password == "Password12345678" {
		t.Fatal("password was not hashed")
	}
	if settings.APIAddress == "" || settings.ClashPath == "" || settings.SubscriptionPath == "" || settings.CrossPanelSubscriptionPath == "" {
		t.Fatal("configure lost generated defaults")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	oldPassword := settings.Password
	if err := runConfigureCLI([]string{"--data-dir", dir, "--port", "45678"}); err != nil {
		t.Fatal(err)
	}
	reloaded := &CoreManager{dataDir: dir}
	if err := reloaded.load(); err != nil {
		t.Fatal(err)
	}
	if reloaded.state.Settings.PanelListen != "0.0.0.0:45678" || reloaded.state.Settings.Password != oldPassword || reloaded.state.Settings.Username != "AdminUser12345" {
		t.Fatal("partial update did not preserve credentials/listen host")
	}
}

func TestConfigureCLIRejectsUnsafeOrInvalidValues(t *testing.T) {
	for name, args := range map[string][]string{
		"bad port":       {"--port", "99999"},
		"bad path":       {"--path", "bad path"},
		"empty username": {"--username", "   "},
		"empty password": {"--password", ""},
	} {
		t.Run(name, func(t *testing.T) {
			args = append([]string{"--data-dir", t.TempDir()}, args...)
			if err := runConfigureCLI(args); err == nil {
				t.Fatal("invalid value accepted")
			}
		})
	}
}

func TestRunCLICommandSelection(t *testing.T) {
	if handled, err := runCLI(nil); handled || err != nil {
		t.Fatal("empty args must start the server")
	}
	if handled, err := runCLI([]string{"unknown"}); !handled || err == nil {
		t.Fatal("unknown command must fail instead of starting a second server")
	}
}

func TestConfigureCLIPasswordStdin(t *testing.T) {
	dir := t.TempDir()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.WriteString("StdinPassword123\n"); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	previous := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() { os.Stdin = previous; _ = reader.Close() })
	if err := runConfigureCLI([]string{"--data-dir", dir, "--password-stdin"}); err != nil {
		t.Fatal(err)
	}
	m := &CoreManager{dataDir: dir}
	if err := m.load(); err != nil {
		t.Fatal(err)
	}
	if !verifyPassword(m.state.Settings.Password, "StdinPassword123") {
		t.Fatal("stdin password was not saved")
	}
}

func TestConfigureCLIStoresTLSCertificatePaths(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	certPath := filepath.Join(dir, "fullchain.pem")
	keyPath := filepath.Join(dir, "privkey.pem")
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.TLS.Certificates[0].Certificate[0]})
	keyBytes, err := x509.MarshalPKCS8PrivateKey(server.TLS.Certificates[0].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	key := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})
	if err := os.WriteFile(certPath, cert, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runConfigureCLI([]string{"--data-dir", dir, "--cert", certPath, "--key", keyPath, "--domain", "panel.example.com"}); err != nil {
		t.Fatal(err)
	}
	m := &CoreManager{dataDir: dir}
	if err := m.load(); err != nil {
		t.Fatal(err)
	}
	if m.state.Settings.PanelCertFile != certPath || m.state.Settings.PanelKeyFile != keyPath || m.state.Settings.PanelDomain != "panel.example.com" {
		t.Fatalf("TLS settings not stored: %+v", m.state.Settings)
	}
}

// The management script runs `m-ui configure` while the panel is running. The
// panel saves every few seconds and when it stops, and used to write its old
// settings back over the new ones.
func TestRunningPanelKeepsSettingsWrittenByConfigure(t *testing.T) {
	dir := t.TempDir()
	if err := runConfigureCLI([]string{"--data-dir", dir, "--port", "34567", "--path", "OldPath12345678", "--username", "OldUser", "--password", "OldPassword123"}); err != nil {
		t.Fatal(err)
	}
	panel := newStateTestManager(dir)
	if err := panel.load(); err != nil {
		t.Fatal(err)
	}
	panel.state.Inbounds = []Inbound{{ID: "in", Name: "in", Type: "socks", Port: 1080, Enabled: true, Traffic: TrafficStats{Up: 42}}}
	if err := panel.saveLocked(); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	certPath, keyPath := filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "privkey.pem")
	keyBytes, err := x509.MarshalPKCS8PrivateKey(server.TLS.Certificates[0].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.TLS.Certificates[0].Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runConfigureCLI([]string{"--data-dir", dir, "--port", "2005", "--path", "O5Ei8SY1m4X7dDm", "--username", "caesar311", "--password", "NewPassword456", "--cert", certPath, "--key", keyPath, "--domain", "panel.example.com"}); err != nil {
		t.Fatal(err)
	}
	// The panel counts more traffic and saves (periodically, or on stop).
	panel.state.Inbounds[0].Traffic.Up = 99
	if err := panel.saveLocked(); err != nil {
		t.Fatal(err)
	}

	reloaded := newStateTestManager(dir)
	if err := reloaded.load(); err != nil {
		t.Fatal(err)
	}
	settings := reloaded.state.Settings
	if !strings.HasSuffix(settings.PanelListen, ":2005") || settings.PanelPath != "/O5Ei8SY1m4X7dDm/" || settings.Username != "caesar311" || !verifyPassword(settings.Password, "NewPassword456") ||
		settings.PanelCertFile != certPath || settings.PanelKeyFile != keyPath || settings.PanelDomain != "panel.example.com" {
		t.Fatalf("the running panel overwrote the configured settings: %+v", settings)
	}
	if len(reloaded.state.Inbounds) != 1 || reloaded.state.Inbounds[0].Traffic.Up != 99 {
		t.Fatalf("the panel's own data must still be saved: %+v", reloaded.state.Inbounds)
	}
	// The running panel uses the new settings at once (the login checks them).
	if panel.state.Settings.Username != "caesar311" || !verifyPassword(panel.state.Settings.Password, "NewPassword456") {
		t.Fatal("the running panel did not take over the new settings")
	}
}
