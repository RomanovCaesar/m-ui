package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestInstallerTLSVariablesAreInitializedBeforeUse(t *testing.T) {
	installer, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(installer)
	for _, unsafe := range []string{
		`local domain="$1" cert_dir="/root/cert/${domain}"`,
		`local cert="$1" key="$2" domain="${3:-}" args=`,
	} {
		if strings.Contains(content, unsafe) {
			t.Fatalf("install.sh still expands a local variable before it is assigned: %s", unsafe)
		}
	}
	for _, required := range []string{
		`domain="${1:-}"`,
		`cert_dir="/root/cert/${domain}"`,
		`args=(configure --data-dir "$DATA_DIR" --cert "$cert" --key "$key" --domain "$domain")`,
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("install.sh is missing the safe TLS initialization %q", required)
		}
	}
}

func TestManagementScriptKeepsMenuChoicesLocal(t *testing.T) {
	management, err := os.ReadFile("scripts/m-ui.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(management)
	for _, required := range []string{
		`local ssl_command="${1:-menu}" ssl_choice=""`,
		`read -rp "$(t p_choose_07)" ssl_choice`,
		`local menu_choice=""`,
		`read -rp "$(t p_select)" menu_choice`,
		`bash -n "$script"`,
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("scripts/m-ui.sh is missing %q", required)
		}
	}
}

func TestAcmeInstallUsesNoEmailBootstrap(t *testing.T) {
	for _, name := range []string{"install.sh", "scripts/m-ui.sh"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		content := string(data)
		if strings.Contains(content, `sh "$installer" email=""`) {
			t.Fatalf("%s still passes an empty email to get.acme.sh", name)
		}
		for _, required := range []string{
			`cd /root && curl -fsSL --retry 3 --connect-timeout 10 https://get.acme.sh | sh`,
			`acme.sh installed successfully`,
		} {
			if !strings.Contains(content, required) {
				t.Fatalf("%s is missing the no-email acme.sh bootstrap %q", name, required)
			}
		}
	}
}

func TestBashScriptsParse(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed on this host")
	}
	command := exec.Command(bash, "-n", "install.sh", "scripts/m-ui.sh", "docker/entrypoint.sh")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("shell syntax check failed: %v\n%s", err, output)
	}
}

// 菜单脚本的语言表要和面板 supportedLanguages 一一对应，每种语言的键和英文完全一致，
// 带 %s 的格式串占位数也要一致，否则 printf 会错位。
func TestManagementScriptTranslationsAreComplete(t *testing.T) {
	data, err := os.ReadFile("scripts/m-ui.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	blocks := regexp.MustCompile(`(?ms)^lang_([a-z]{2}(?:_[A-Z]{2})?)\(\) \{\n(.*?)^\}`).FindAllStringSubmatch(content, -1)
	entry := regexp.MustCompile(`(?m)^\s*M ([a-z0-9_]+) "(.*)"$`)
	tables := map[string]map[string]string{}
	for _, block := range blocks {
		code := strings.ReplaceAll(block[1], "_", "-")
		table := map[string]string{}
		for _, match := range entry.FindAllStringSubmatch(block[2], -1) {
			if _, dup := table[match[1]]; dup {
				t.Fatalf("%s defines %s twice", code, match[1])
			}
			table[match[1]] = match[2]
		}
		tables[code] = table
	}
	if len(tables) != len(supportedLanguages) {
		t.Fatalf("menu script has %d language tables, panel supports %d", len(tables), len(supportedLanguages))
	}
	if !strings.Contains(content, "MENU_LANGS=(en zh-CN zh-TW ja ru fa vi es tr uk pt-BR)") {
		t.Fatal("MENU_LANGS no longer lists the panel languages")
	}
	english := tables["en"]
	for _, language := range supportedLanguages {
		table, ok := tables[language]
		if !ok {
			t.Fatalf("menu script has no lang_%s table", strings.ReplaceAll(language, "-", "_"))
		}
		if !strings.Contains(content, " "+language+") lang_") && language != "en" {
			t.Fatalf("load_lang does not dispatch %s", language)
		}
		for key, value := range english {
			translated, ok := table[key]
			if !ok {
				t.Fatalf("%s is missing %s", language, key)
			}
			if strings.Count(translated, "%") != strings.Count(value, "%") || strings.Count(translated, "%s") != strings.Count(value, "%s") {
				t.Fatalf("%s %s has different format verbs: %q", language, key, translated)
			}
			if strings.ContainsAny(translated, "$`\\") {
				t.Fatalf("%s %s contains shell-expanded characters: %q", language, key, translated)
			}
		}
		if len(table) != len(english) {
			t.Fatalf("%s has %d messages, English has %d", language, len(table), len(english))
		}
	}
	for _, match := range regexp.MustCompile(`\$\(tf? "?([a-z0-9_]+)[")\s]`).FindAllStringSubmatch(content, -1) {
		if _, ok := english[match[1]]; !ok {
			t.Fatalf("script uses undefined message %s", match[1])
		}
	}
}

func TestManagementScriptLanguageOption(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed on this host")
	}
	dir := t.TempDir()
	run := func(args ...string) (string, error) {
		command := exec.Command(bash, append([]string{"scripts/m-ui.sh"}, args...)...)
		command.Env = append(os.Environ(), "MUI_INSTALL_DIR="+filepath.ToSlash(dir))
		output, err := command.CombinedOutput()
		return string(output), err
	}
	if output, err := run("help"); err != nil || !strings.Contains(output, "m-ui management commands:") {
		t.Fatalf("default language is not English: %v\n%s", err, output)
	}
	if output, err := run("--lang", "zh-cn", "help"); err != nil || !strings.Contains(output, "m-ui 管理命令：") {
		t.Fatalf("--lang zh-cn did not switch to Simplified Chinese: %v\n%s", err, output)
	}
	if saved, err := os.ReadFile(filepath.Join(dir, "menu-lang")); err != nil || strings.TrimSpace(string(saved)) != "zh-CN" {
		t.Fatalf("language preference not saved: %q %v", saved, err)
	}
	if output, err := run("help"); err != nil || !strings.Contains(output, "m-ui 管理命令：") {
		t.Fatalf("saved language not reused: %v\n%s", err, output)
	}
	if output, err := run("help", "--lang=PT_br"); err != nil || !strings.Contains(output, "Comandos de gerenciamento do m-ui:") {
		t.Fatalf("--lang after the command was not honoured: %v\n%s", err, output)
	}
	if output, err := run("--lang", "xx", "help"); err == nil || !strings.Contains(output, "xx") {
		t.Fatalf("unsupported language was accepted: %v\n%s", err, output)
	}
}
