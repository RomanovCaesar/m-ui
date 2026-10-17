package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A power loss on XFS left a VPS with a zero-length state.json. The panel used to log the
// error and serve a zero State: listening on :80 with an empty username and password.

func newStateTestManager(dir string) *CoreManager {
	return &CoreManager{dataDir: dir, connectionStats: map[string]connectionSample{}, enforcedClients: map[string]bool{}}
}

func writeValidState(t *testing.T, dir, name, username string) {
	t.Helper()
	m := newStateTestManager(dir)
	m.state = defaultState()
	m.state.Settings.Username = username
	m.state.Settings.SubscriptionPath = "/sub"
	if err := m.saveLocked(); err != nil {
		t.Fatal(err)
	}
	if name != stateFileName {
		if err := os.Rename(filepath.Join(dir, stateFileName), filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadRefusesDamagedStateWithoutBackup(t *testing.T) {
	for name, content := range map[string]string{"empty": "", "truncated": `{"settings":{"username":"al`, "spaces": " \n "} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, stateFileName)
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			err := newStateTestManager(dir).load()
			if err == nil || !strings.Contains(err.Error(), "refusing to start") {
				t.Fatalf("load() = %v, want a refusal", err)
			}
			if data, _ := os.ReadFile(path); string(data) != content {
				t.Fatalf("the damaged file must be left untouched, now %q", data)
			}
		})
	}
}

func TestLoadRecoversDamagedStateFromBackup(t *testing.T) {
	dir := t.TempDir()
	writeValidState(t, dir, stateBackupFileName, "alice")
	path := filepath.Join(dir, stateFileName)
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	m := newStateTestManager(dir)
	if err := m.load(); err != nil {
		t.Fatal(err)
	}
	if m.state.Settings.Username != "alice" {
		t.Fatalf("username = %q, want the backup's alice", m.state.Settings.Username)
	}
	if data, err := os.ReadFile(path); err != nil || !validStateJSON(data) {
		t.Fatalf("state.json was not rewritten from the backup: %v %q", err, data)
	}
	broken, _ := filepath.Glob(filepath.Join(dir, stateFileName+".broken-*"))
	if len(broken) != 1 {
		t.Fatalf("the damaged file must be kept aside, found %v", broken)
	}
}

func TestLoadRecoversMissingStateFromBackup(t *testing.T) {
	dir := t.TempDir()
	writeValidState(t, dir, stateBackupFileName, "bob")
	m := newStateTestManager(dir)
	if err := m.load(); err != nil {
		t.Fatal(err)
	}
	if m.state.Settings.Username != "bob" {
		t.Fatalf("username = %q, want the backup's bob instead of fresh defaults", m.state.Settings.Username)
	}
}

func TestSaveKeepsPreviousStateAsBackup(t *testing.T) {
	dir := t.TempDir()
	writeValidState(t, dir, stateFileName, "first")
	m := newStateTestManager(dir)
	if err := m.load(); err != nil {
		t.Fatal(err)
	}
	m.state.Settings.Username = "second"
	if err := m.saveLocked(); err != nil {
		t.Fatal(err)
	}
	data, ok := readStateBackup(dir)
	if !ok {
		t.Fatal("no usable state.json.bak after a save")
	}
	if !strings.Contains(string(data), `"first"`) {
		t.Fatalf("backup should hold the previous state, got %s", data)
	}
	// Within the interval the backup is not rewritten on every save.
	m.state.Settings.Username = "third"
	if err := m.saveLocked(); err != nil {
		t.Fatal(err)
	}
	if again, _ := readStateBackup(dir); string(again) != string(data) {
		t.Fatal("backup was refreshed before stateBackupInterval elapsed")
	}
	m.lastStateBackup = time.Now().Add(-stateBackupInterval)
	if err := m.saveLocked(); err != nil {
		t.Fatal(err)
	}
	if again, _ := readStateBackup(dir); !strings.Contains(string(again), `"third"`) {
		t.Fatalf("backup was not refreshed after the interval: %s", again)
	}
	if _, err := os.Stat(filepath.Join(dir, stateFileName+".tmp")); !os.IsNotExist(err) {
		t.Fatalf("temporary file left behind: %v", err)
	}
}

func TestEmptyCredentialsNeverLogIn(t *testing.T) {
	if verifyPassword("", "") {
		t.Fatal("verifyPassword accepted an empty password against an empty hash")
	}
	manager := &CoreManager{state: State{}}
	app := &App{manager: manager, session: "s"}
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"","password":""}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("empty credentials on an empty state: status %d, want 401: %s", recorder.Code, recorder.Body.String())
	}
}
