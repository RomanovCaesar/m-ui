package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The browser's own confirm(), alert() and prompt() boxes ignore the panel theme
// and language; web/dialog.js provides muiConfirm and muiPrompt instead.
func TestWebUsesPanelDialogs(t *testing.T) {
	files, err := filepath.Glob("web/*")
	if err != nil {
		t.Fatal(err)
	}
	native := regexp.MustCompile(`(^|[^\w.$])(window\.)?(confirm|alert|prompt)\s*\(`)
	checked := 0
	for _, file := range files {
		if !(strings.HasSuffix(file, ".js") || strings.HasSuffix(file, ".html")) || strings.HasSuffix(file, ".min.js") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for number, line := range strings.Split(string(data), "\n") {
			if match := native.FindString(line); match != "" {
				t.Errorf("%s:%d uses the browser dialog %q; use muiConfirm / muiPrompt", file, number+1, strings.TrimSpace(match))
			}
		}
	}
	if checked < 10 {
		t.Fatalf("checked only %d web files", checked)
	}
	index, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), `<script src="static/dialog.js"></script>`) {
		t.Fatal("index.html does not load dialog.js")
	}
}
