package app

import (
	"os"
	"os/exec"
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
		`read -rp "Choose [0-7]: " ssl_choice`,
		`local menu_choice=""`,
		`read -rp "Select: " menu_choice`,
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
