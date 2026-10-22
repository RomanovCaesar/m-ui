package app

import (
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
