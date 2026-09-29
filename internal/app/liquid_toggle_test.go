package app

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubscriptionToggleAssetsOnBothListeners(t *testing.T) {
	for _, prefix := range []string{"/sub", "", "/custom-sub"} {
		app := &App{manager: &CoreManager{state: State{Settings: Settings{PanelPath: "/private-panel/", SubscriptionPath: prefix}}}}
		base := strings.TrimSuffix(subscriptionAssetPublicPath(app.manager.state.Settings), "qrious2.min.js")
		for _, handler := range []struct {
			name  string
			serve func(*httptest.ResponseRecorder, string)
		}{
			{"panel", func(w *httptest.ResponseRecorder, path string) {
				app.routes().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			}},
			{"subscription", func(w *httptest.ResponseRecorder, path string) {
				app.subscriptionRoutes().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			}},
		} {
			for name, want := range map[string][2]string{
				"liquid-toggle.css": {"text/css", "lg-toggle"},
				"liquid-toggle.js":  {"text/javascript", "lg-toggle"},
				"liquid-button.css": {"text/css", "--lb-fill"},
				"liquid-button.js":  {"text/javascript", "MUILiquidButton"},
			} {
				mime := want[0]
				w := httptest.NewRecorder()
				handler.serve(w, base+name)
				if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), mime) || !strings.Contains(w.Body.String(), want[1]) {
					t.Fatalf("%s %s: %d %s", handler.name, base+name, w.Code, w.Header().Get("Content-Type"))
				}
			}
		}
		w := httptest.NewRecorder()
		app.subscriptionRoutes().ServeHTTP(w, httptest.NewRequest("GET", base+"index.html", nil))
		if w.Code == 200 {
			t.Fatal("Subscription assets must not expose the panel")
		}
	}
}
