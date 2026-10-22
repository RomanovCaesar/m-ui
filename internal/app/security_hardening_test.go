package app

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Every install used to run the Mihomo controller with the same secret and a CORS
// policy that let any web page reach it; on a desktop that meant any site the
// browser opened could drive the local core.

func TestControllerSecretIsRandomPerInstall(t *testing.T) {
	first, second := defaultState().Settings.APISecret, defaultState().Settings.APISecret
	if first == legacyAPISecret || first == "" || first == second {
		t.Fatalf("default secrets %q and %q must be random and distinct", first, second)
	}
	if !regexp.MustCompile(`^[0-9a-f]{36}$`).MatchString(first) {
		t.Fatalf("unexpected secret format %q", first)
	}
}

func TestLoadReplacesLegacyControllerSecret(t *testing.T) {
	for name, tc := range map[string]struct {
		stored  string
		replace bool
	}{
		"legacy":   {legacyAPISecret, true},
		"custom":   {"my-own-secret", false},
		"disabled": {"", false},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			m := newStateTestManager(dir)
			m.state = defaultState()
			m.state.Settings.SubscriptionPath = "/sub"
			m.state.Settings.APISecret = tc.stored
			if err := m.saveLocked(); err != nil {
				t.Fatal(err)
			}
			loaded := newStateTestManager(dir)
			if err := loaded.load(); err != nil {
				t.Fatal(err)
			}
			got := loaded.state.Settings.APISecret
			if tc.replace {
				if got == legacyAPISecret || got == "" {
					t.Fatalf("legacy secret was kept: %q", got)
				}
				data, _ := os.ReadFile(filepath.Join(dir, stateFileName))
				if strings.Contains(string(data), legacyAPISecret) {
					t.Fatal("the new secret was not saved")
				}
			} else if got != tc.stored {
				t.Fatalf("secret %q was changed to %q", tc.stored, got)
			}
		})
	}
}

func TestControllerCORSAllowsNoWebPage(t *testing.T) {
	state := defaultState()
	state.Settings.SubscriptionPath = "/sub"
	config, err := renderStateConfig(state)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		CORS struct {
			AllowOrigins        []string `yaml:"allow-origins"`
			AllowPrivateNetwork bool     `yaml:"allow-private-network"`
		} `yaml:"external-controller-cors"`
	}
	if err := yaml.Unmarshal(config, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.CORS.AllowOrigins) != 1 || parsed.CORS.AllowOrigins[0] != controllerCORSOrigin || parsed.CORS.AllowPrivateNetwork {
		t.Fatalf("controller CORS is not locked down: %+v", parsed.CORS)
	}
	text := string(config)
	if strings.Contains(text, legacyAPISecret) {
		t.Fatal("rendered config still uses the shared secret")
	}
}

func TestServersBoundSlowClients(t *testing.T) {
	panel := newPanelServer(":0", nil)
	if panel.ReadHeaderTimeout <= 0 || panel.ReadTimeout <= 0 || panel.IdleTimeout <= 0 {
		t.Fatalf("panel server lacks timeouts: %+v", panel)
	}
	if panel.WriteTimeout != 0 {
		t.Fatal("the panel must not have a WriteTimeout: core updates run inside one request")
	}
	sub := newSubscriptionServer(":0", nil)
	if sub.ReadHeaderTimeout <= 0 || sub.ReadTimeout <= 0 || sub.WriteTimeout <= 0 || sub.IdleTimeout <= 0 {
		t.Fatalf("subscription server lacks timeouts: %+v", sub)
	}
}

func TestCoreBackupsArePruned(t *testing.T) {
	dir := t.TempDir()
	core := filepath.Join(dir, "mihomo")
	names := []string{"20260913-111844", "20261008-090000", "20261022-045231", "20261023-101010"}
	for _, stamp := range names {
		if err := os.WriteFile(core+".backup-"+stamp, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(core, []byte("current"), 0700)
	pruneCoreBackups(core, coreBackupsKept)
	left, _ := filepath.Glob(core + ".backup-*")
	if len(left) != 2 || !strings.HasSuffix(left[0], names[2]) || !strings.HasSuffix(left[1], names[3]) {
		t.Fatalf("kept %v, want the two newest", left)
	}
	if _, err := os.Stat(core); err != nil {
		t.Fatal("the current core must never be touched")
	}
}

func TestLoginUpgradesWeakPasswordHash(t *testing.T) {
	app := newLoginTestApp(t)
	weak := func(password string) string {
		salt := []byte("0123456789abcdef")
		key := pbkdf2SHA256([]byte(password), salt, 120000, 32)
		return "pbkdf2_sha256$120000$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key)
	}
	app.manager.state.Settings.Password = weak("right-password")
	if !passwordNeedsRehash(app.manager.state.Settings.Password) {
		t.Fatal("a 120000-iteration hash must need a rehash")
	}
	if code := loginRequest(app, "203.0.113.30", "admin", "right-password"); code != http.StatusOK {
		t.Fatalf("login with the old hash: %d", code)
	}
	stored := app.manager.state.Settings.Password
	if !strings.HasPrefix(stored, fmt.Sprintf("pbkdf2_sha256$%d$", passwordHashIterations)) || !verifyPassword(stored, "right-password") {
		t.Fatalf("hash was not upgraded: %s", stored)
	}
	if passwordNeedsRehash(stored) {
		t.Fatal("an upgraded hash must not need another rehash")
	}
	// A failed login never touches the stored hash.
	app.manager.state.Settings.Password = weak("right-password")
	before := app.manager.state.Settings.Password
	loginRequest(app, "203.0.113.31", "admin", "wrong")
	if app.manager.state.Settings.Password != before {
		t.Fatal("a failed login changed the stored hash")
	}
}
