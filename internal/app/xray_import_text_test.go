package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var xrayVerb = regexp.MustCompile(`%(\[\d+\])?[a-zA-Z]`)

// verbs lists the verbs of a format in argument order, so a translation may
// reorder them with explicit indexes but must use the same ones.
func xrayFormatVerbs(format string) []string {
	var verbs []string
	next := 1
	for _, match := range xrayVerb.FindAllStringSubmatch(strings.ReplaceAll(format, "%%", ""), -1) {
		index := next
		if match[1] != "" {
			fmt.Sscanf(match[1], "[%d]", &index)
		}
		verbs = append(verbs, fmt.Sprintf("%d%s", index, match[0][len(match[0])-1:]))
		next = index + 1
	}
	slices.Sort(verbs)
	return verbs
}

func TestXrayImportCatalogCoversEveryMessage(t *testing.T) {
	files, _ := filepath.Glob("internal/app/xray_import*.go")
	literal := regexp.MustCompile(`(?:log\.(?:info|warn|section)|\bxt)\(\s*"((?:[^"\\]|\\.)*)"`)
	letters := regexp.MustCompile(`[A-Za-z]{2,}`)
	used := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") || strings.HasSuffix(file, "_catalog.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range literal.FindAllStringSubmatch(string(data), -1) {
			if letters.MatchString(xrayVerb.ReplaceAllString(match[1], "")) {
				used[match[1]] = true
			}
		}
	}
	if len(used) < 100 {
		t.Fatalf("found only %d messages; is the pattern still right?", len(used))
	}
	for format := range used {
		translations, ok := xrayTextCatalog[format]
		if !ok {
			t.Errorf("no translation for %q", format)
			continue
		}
		want := xrayFormatVerbs(format)
		for language, index := range xrayTextLanguages {
			translated := translations[index]
			if strings.TrimSpace(translated) == "" {
				t.Errorf("%s: empty translation of %q", language, format)
			}
			if got := xrayFormatVerbs(translated); !slices.Equal(got, want) {
				t.Errorf("%s: %q has verbs %v, want %v", language, translated, got, want)
			}
			if language != "zh-CN" && language != "zh-TW" && language != "ja" && hasCJK(translated) {
				t.Errorf("%s: Chinese characters in %q", language, translated)
			}
		}
	}
	for format := range xrayTextCatalog {
		if !used[format] && format != "%s (%s)" && format != "%s: %s" {
			t.Errorf("catalog entry %q is not used any more", format)
		}
	}
}

func TestXrayImportLogFollowsTheLanguage(t *testing.T) {
	for _, language := range []string{"en", "zh-CN", "zh-TW", "ja", "ru", "fa", "vi", "es", "tr", "uk", "pt-BR"} {
		m := newXrayImportManager(t)
		source, err := parseXrayImportSource(xrayTestConfig(t))
		if err != nil {
			t.Fatal(err)
		}
		result, err := m.importXray(source, language)
		if err != nil {
			t.Fatalf("%s: %v", language, err)
		}
		if strings.Contains(result.Log, "%!") {
			t.Fatalf("%s: broken format in the log:\n%s", language, result.Log)
		}
		switch language {
		case "en":
			if !strings.Contains(result.Log, "== Summary") {
				t.Fatalf("en log:\n%s", result.Log)
			}
		case "zh-CN":
			if !strings.Contains(result.Log, "== 汇总") || !strings.Contains(result.Log, "已跳过") {
				t.Fatalf("zh-CN log:\n%s", result.Log)
			}
		case "zh-TW", "ja":
		default:
			for _, line := range strings.Split(result.Log, "\n") {
				if hasCJK(line) {
					t.Errorf("%s: untranslated line %q", language, line)
				}
			}
			if strings.Contains(result.Log, "== Summary") {
				t.Errorf("%s: section titles stayed in English", language)
			}
		}
		if len(result.Highlights) == 0 || strings.HasPrefix(result.Highlights[0], "  ") {
			t.Fatalf("%s: highlights %q", language, result.Highlights)
		}
	}
}
