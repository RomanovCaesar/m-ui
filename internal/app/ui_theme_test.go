package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestNormalizeUITheme(t *testing.T) {
	for input, want := range map[string]string{"": "shadcn", " md3 ": "md3", "liquid": "liquid", "shadcn": "shadcn", "Liquid": "shadcn", "glass": "shadcn"} {
		if got := normalizeUITheme(input); got != want {
			t.Fatalf("normalizeUITheme(%q) = %q, want %q", input, got, want)
		}
	}
}

// Every page tags its theme-only assets, and themedPage keeps only the ones of
// the chosen theme, so a theme never loads another theme's styles or scripts.
func TestThemedPageKeepsOnlyTheChosenThemeAssets(t *testing.T) {
	onlyTag := regexp.MustCompile(`data-ui-only="([a-z0-9]+)"`)
	for _, name := range []string{"web/index.html", "web/login.html"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		page := string(raw)
		if !strings.Contains(page, `data-ui-theme="shadcn"`) {
			t.Fatalf("%s has no default data-ui-theme", name)
		}
		for _, theme := range []string{"shadcn", "md3", "liquid"} {
			out := themedPage(page, theme)
			if !strings.Contains(out, `data-ui-theme="`+theme+`"`) {
				t.Fatalf("%s/%s: data-ui-theme not set", name, theme)
			}
			kept := onlyTag.FindAllStringSubmatch(out, -1)
			if len(kept) == 0 {
				t.Fatalf("%s/%s: no theme assets left", name, theme)
			}
			for _, match := range kept {
				if match[1] != theme {
					t.Fatalf("%s/%s: kept an asset of %s", name, theme, match[1])
				}
			}
			has := func(asset string) bool { return strings.Contains(out, `static/`+asset) }
			switch theme {
			case "liquid":
				if !has("liquid-optics.css") || !has("liquid-toggle.js") || has("theme-shadcn.css") || has("fonts.css") {
					t.Fatalf("%s/liquid: wrong assets", name)
				}
			case "shadcn":
				if !has("theme-shadcn.css") || !has("fonts.css") || !has("icons-lucide.js") || has("liquid-optics.css") || has("liquid-glass-gl.js") || has("icons-material.js") {
					t.Fatalf("%s/shadcn: wrong assets", name)
				}
			case "md3":
				if !has("theme-md3.css") || !has("fonts.css") || !has("icons-material.js") || has("liquid-optics.css") || has("theme-shadcn.css") {
					t.Fatalf("%s/md3: wrong assets", name)
				}
			}
		}
		// An unknown theme falls back to the default instead of dropping every theme asset.
		if out := themedPage(page, "bogus"); !strings.Contains(out, "static/theme-shadcn.css") {
			t.Fatalf("%s: unknown theme did not fall back to shadcn", name)
		}
	}
}

// The panel and the login page get the theme from the settings when served.
func TestServedPagesUseTheSettingsTheme(t *testing.T) {
	app := i18nTestApp(t)
	app.manager.state.Settings.UITheme = "md3"
	for _, page := range []string{"web/index.html", "web/login.html"} {
		recorder := httptest.NewRecorder()
		app.serveLocalizedPage(recorder, page)
		body := recorder.Body.String()
		if !strings.Contains(body, `data-ui-theme="md3"`) || !strings.Contains(body, "static/theme-md3.css") || strings.Contains(body, "static/liquid-optics.css") {
			t.Fatalf("%s is not served with the md3 theme", page)
		}
	}
}

func TestUIThemeSettingValidationAndMigration(t *testing.T) {
	m := &CoreManager{dataDir: t.TempDir()}
	if err := m.load(); err != nil {
		t.Fatal(err)
	}
	if m.state.Settings.UITheme != "shadcn" {
		t.Fatalf("new install theme = %q, want shadcn", m.state.Settings.UITheme)
	}
	settings := m.state.Settings
	settings.Password = ""
	settings.UITheme = "liquid"
	if err := m.updateSettings(settings); err != nil {
		t.Fatal(err)
	}
	settings = m.state.Settings
	settings.Password = ""
	settings.UITheme = "glass"
	if err := m.updateSettings(settings); err == nil {
		t.Fatal("an unknown theme was accepted")
	}
	if m.state.Settings.UITheme != "liquid" {
		t.Fatalf("a rejected save changed the theme to %q", m.state.Settings.UITheme)
	}
	settings.UITheme = ""
	if err := m.updateSettings(settings); err != nil {
		t.Fatal(err)
	}
	if m.state.Settings.UITheme != "shadcn" {
		t.Fatalf("an empty theme saved as %q, want shadcn", m.state.Settings.UITheme)
	}

	// Installs from before the option have no theme stored and move to shadcn.
	m.state.Settings.UITheme = ""
	if err := m.saveLocked(); err != nil {
		t.Fatal(err)
	}
	reloaded := &CoreManager{dataDir: m.dataDir}
	if err := reloaded.load(); err != nil {
		t.Fatal(err)
	}
	if reloaded.state.Settings.UITheme != "shadcn" {
		t.Fatalf("old install theme = %q, want shadcn", reloaded.state.Settings.UITheme)
	}
}

func TestSubscriptionPageFollowsTheTheme(t *testing.T) {
	settings := Settings{PanelPath: "/panel/", SubscriptionPath: "/sub", ClashPath: "/clash"}
	render := func(theme string) string {
		settings.UITheme = theme
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/sub/abc123def4567890", nil)
		snapshot := subscriptionSnapshot{Settings: settings, Token: "abc123def4567890", Username: "alice"}
		if err := renderSubscriptionPage(recorder, request, snapshot, nil); err != nil {
			t.Fatal(err)
		}
		return recorder.Body.String()
	}
	for _, theme := range []string{"shadcn", "md3"} {
		page := render(theme)
		if !strings.Contains(page, `data-ui-theme="`+theme+`"`) || !strings.Contains(page, `href="/sub/_assets/subscription-themes.css"`) || !strings.Contains(page, `href="/sub/_assets/fonts.css"`) || strings.Contains(page, "liquid-toggle") {
			t.Fatalf("subscription page is not themed for %s", theme)
		}
	}
	page := render("liquid")
	if !strings.Contains(page, `data-ui-theme="liquid"`) || !strings.Contains(page, "/sub/_assets/liquid-toggle.css") || !strings.Contains(page, "/sub/_assets/liquid-button.js") || strings.Contains(page, "subscription-themes.css") {
		t.Fatal("subscription page lost the Liquid Glass assets")
	}
}

// The subscription port serves the theme files and fonts, and nothing else.
func TestSubscriptionThemeAssetsArePublic(t *testing.T) {
	app := &App{manager: &CoreManager{dataDir: t.TempDir(), state: State{Settings: Settings{PanelPath: "/panel/", SubscriptionPath: "/sub", ClashPath: "/clash"}}}, session: "test-session"}
	get := func(path string) (*httptest.ResponseRecorder, bool) {
		recorder := httptest.NewRecorder()
		handled := app.handleSubscriptionRequest(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		return recorder, handled
	}
	for path, contentType := range map[string]string{
		"/sub/_assets/subscription-themes.css":                 "text/css",
		"/sub/_assets/fonts.css":                               "text/css",
		"/sub/_assets/fonts/geist-latin-wght-normal.woff2":     "font/woff2",
		"/sub/_assets/fonts/roboto-cyrillic-wght-normal.woff2": "font/woff2",
	} {
		recorder, handled := get(path)
		if !handled || recorder.Code != http.StatusOK || !strings.HasPrefix(recorder.Header().Get("Content-Type"), contentType) || recorder.Body.Len() == 0 {
			t.Fatalf("%s: handled=%v code=%d type=%q", path, handled, recorder.Code, recorder.Header().Get("Content-Type"))
		}
	}
	for _, path := range []string{"/sub/_assets/fonts/LICENSE-Geist.txt", "/sub/_assets/fonts/../theme-md3.css", "/sub/_assets/fonts/x.woff2/../../main.go", "/sub/_assets/theme-md3.css", "/sub/_assets/icons-lucide.js"} {
		if recorder, _ := get(path); recorder.Code == http.StatusOK && recorder.Body.Len() > 0 {
			t.Fatalf("%s is public", path)
		}
	}
}

// The shadcn and md3 icon sets replace the sprite of sf-symbols.js, so they
// must define every icon id it has, or icons go blank in those themes.
func TestThemeIconSetsCoverEverySymbol(t *testing.T) {
	symbolID := regexp.MustCompile(`<symbol id=\\"(sf-[a-z0-9-]+)\\"`)
	ids := func(name string) map[string]bool {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]bool{}
		for _, match := range symbolID.FindAllStringSubmatch(string(raw), -1) {
			found[match[1]] = true
		}
		return found
	}
	base := ids("web/sf-symbols.js")
	if len(base) < 50 {
		t.Fatalf("found only %d symbols in sf-symbols.js", len(base))
	}
	for _, set := range []string{"web/icons-lucide.js", "web/icons-material.js"} {
		themed := ids(set)
		for id := range base {
			if !themed[id] {
				t.Fatalf("%s has no %s", set, id)
			}
		}
	}
}

// The inbound drawer adds ShadowTLS, RestTLS, TLSMirror and JLS to the
// security segmented control inside <span id="security-extra-toggles">
// (display: contents), so ".segmented > button" alone misses them and they
// render as unstyled pills.
func TestThemeSegmentedRulesCoverAddedSecurityButtons(t *testing.T) {
	index, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "extras.id='security-extra-toggles'") || !strings.Contains(string(index), "#security-extra-toggles { display: contents; }") {
		t.Skip("the security buttons are no longer added through #security-extra-toggles")
	}
	for _, name := range []string{"web/theme-shadcn.css", "web/theme-md3.css"} {
		css, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(css)
		if !strings.Contains(text, ".segmented, .liquid-tabbar, #security-extra-toggles) > button {") {
			t.Fatalf("%s does not style the added security buttons", name)
		}
		if !regexp.MustCompile(`#security-extra-toggles\) > (button\.active|:is\(button\.active)`).MatchString(text) {
			t.Fatalf("%s does not mark the selected added security button", name)
		}
	}
}
