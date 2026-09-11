package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func validGeneratedCrossPath(path string) bool {
	return validCrossPanelSubscriptionPath(path)
}

func TestCrossPanelPathInitialLoadAndLegacyMigration(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "first startup", true: "upgrade"}[legacy], func(t *testing.T) {
			dir := t.TempDir()
			if legacy {
				old := defaultState()
				old.Settings.SubscriptionPath = "/custom-local-sub"
				old.SubscriptionTokens = map[string]string{"alice": "abc123def4567890"}
				old.Inbounds = nil
				old.Inbounds = append(old.Inbounds, syncTestInbound("test", 23001))
				data, _ := json.Marshal(old)
				if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			m := &CoreManager{dataDir: dir}
			if err := m.load(); err != nil {
				t.Fatal(err)
			}
			cross := m.state.Settings.CrossPanelSubscriptionPath
			if !validGeneratedCrossPath(cross) {
				t.Fatalf("invalid default: %q", cross)
			}
			if legacy && (m.state.Settings.SubscriptionPath != "/custom-local-sub" || m.state.SubscriptionTokens["alice"] != "abc123def4567890") {
				t.Fatal("upgrade changed local subscriptions")
			}
			reloaded := &CoreManager{dataDir: dir}
			if err := reloaded.load(); err != nil {
				t.Fatal(err)
			}
			if reloaded.state.Settings.CrossPanelSubscriptionPath != cross {
				t.Fatal("path rotated after restart")
			}
		})
	}
}

func TestCrossPanelPathManualSaveValidationAndCompatibility(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir()}
	if err := m.load(); err != nil {
		t.Fatal(err)
	}
	settings := m.state.Settings
	settings.Password = ""
	settings.CrossPanelSubscriptionPath = " /isub4ad5kaf479afnbj2 "
	if err := m.updateSettings(settings); err != nil {
		t.Fatal(err)
	}
	if m.state.Settings.CrossPanelSubscriptionPath != "/isub4ad5kaf479afnbj2" {
		t.Fatal("manual path not normalized")
	}
	reloaded := &CoreManager{dataDir: m.dataDir}
	if err := reloaded.load(); err != nil {
		t.Fatal(err)
	}
	if reloaded.state.Settings.CrossPanelSubscriptionPath != "/isub4ad5kaf479afnbj2" {
		t.Fatal("manual path not persisted")
	}
	settings = m.state.Settings
	settings.Password = ""
	settings.CrossPanelSubscriptionPath = ""
	if err := m.updateSettings(settings); err != nil {
		t.Fatal(err)
	}
	if m.state.Settings.CrossPanelSubscriptionPath != "/isub4ad5kaf479afnbj2" {
		t.Fatal("older client erased path")
	}
	for _, path := range []string{"/", "/api", "/static/abc", "/_m-ui/peer", "/_subscription-assets", "/bad?query", "/bad/../path", "/bad//path", "/isubabc", "/isubabcdefghijklmnop", "/isub1234567890123456", m.state.Settings.SubscriptionPath} {
		settings = m.state.Settings
		settings.Password = ""
		settings.CrossPanelSubscriptionPath = path
		if err := m.updateSettings(settings); err == nil {
			t.Errorf("accepted invalid/conflicting path %q", path)
		}
		if m.state.Settings.CrossPanelSubscriptionPath != "/isub4ad5kaf479afnbj2" {
			t.Fatal("failed save mutated path")
		}
	}
	settings = m.state.Settings
	settings.Password = ""
	settings.SubscriptionPath = settings.CrossPanelSubscriptionPath
	settings.CrossPanelSubscriptionPath = ""
	if err := m.updateSettings(settings); err == nil {
		t.Fatal("older client's local path change collided with cross path")
	}
}

func TestCrossPanelPathGeneratorAndFutureClashURL(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir()}
	if err := m.load(); err != nil {
		t.Fatal(err)
	}
	app := &App{manager: m, session: "test-session"}
	path := "/api/tools/cross-panel-subscription-path"
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
	if w.Code != 401 {
		t.Fatal("generator not protected")
	}
	before := m.state.Settings.CrossPanelSubscriptionPath
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		w = syncAPICall(t, app, http.MethodPost, path, nil)
		var response struct {
			OK   bool `json:"ok"`
			Data struct {
				Path string `json:"path"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		generated := response.Data.Path
		if w.Code != 200 || !response.OK || !validGeneratedCrossPath(generated) || seen[generated] {
			t.Fatalf("invalid generated path: %s", w.Body.String())
		}
		seen[generated] = true
		if m.state.Settings.CrossPanelSubscriptionPath != before {
			t.Fatal("generate persisted without Save")
		}
	}
	w = syncAPICall(t, app, http.MethodGet, path, nil)
	if w.Code != 405 {
		t.Fatal("generator accepted GET")
	}
	settings := m.state.Settings
	settings.PanelPath = "/secret-panel/"
	settings.CrossPanelSubscriptionPath = "/isub4ad5kaf479afnbj2"
	settings.ClashPath = "/clash"
	if got := crossPanelSubscriptionPublicPath(settings, "abc123def4567890"); got != "/isub4ad5kaf479afnbj2/abc123def4567890" {
		t.Fatalf("wrong subscription page path: %q", got)
	}
	if got := crossPanelSubscriptionClashPublicPath(settings, "abc123def4567890"); got != "/isub4ad5kaf479afnbj2/abc123def4567890/clash" {
		t.Fatalf("wrong Clash URL path: %q", got)
	}
	if crossPanelSubscriptionPublicPath(settings, "../invalid") != "" {
		t.Fatal("invalid token accepted in URL")
	}
	if crossPanelSubscriptionClashPublicPath(settings, "../invalid") != "" {
		t.Fatal("invalid token accepted in Clash URL")
	}
}
