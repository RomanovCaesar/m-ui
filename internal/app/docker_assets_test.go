package app

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDockerAssetsStayWiredForRelease(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	requireText := func(name, content string, values ...string) {
		t.Helper()
		for _, value := range values {
			if !strings.Contains(content, value) {
				t.Fatalf("%s is missing %q", name, value)
			}
		}
	}

	dockerfile := read("Dockerfile")
	requireText("Dockerfile", dockerfile,
		"GOOS=linux GOARCH=amd64",
		"mihomo-linux-amd64-compatible-v${MIHOMO_VERSION}.gz",
		"sha256sum --check --strict",
		"ENTRYPOINT [\"/usr/local/bin/m-ui-entrypoint\"]",
	)

	entrypoint := read("docker/entrypoint.sh")
	if strings.Contains(entrypoint, "\r\n") {
		t.Fatal("docker/entrypoint.sh must use LF line endings for its Linux shebang")
	}
	requireText("docker/entrypoint.sh", entrypoint,
		"--password-stdin",
		"--listen-ip \"$LISTEN_IP\"",
		"exec \"$MUI_BIN\"",
	)

	var compose struct {
		Services map[string]struct {
			Image       string   `yaml:"image"`
			Platform    string   `yaml:"platform"`
			NetworkMode string   `yaml:"network_mode"`
			Volumes     []string `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(read("docker-compose.yml")), &compose); err != nil {
		t.Fatalf("docker-compose.yml is invalid YAML: %v", err)
	}
	service, ok := compose.Services["m-ui"]
	if !ok || service.Image != "ghcr.io/romanovcaesar/m-ui:${MUI_IMAGE_TAG:-latest}" || service.Platform != "linux/amd64" || service.NetworkMode != "host" {
		t.Fatalf("unexpected m-ui Compose service: %#v", service)
	}
	for _, volume := range []string{"m-ui-data:/opt/m-ui/data", "m-ui-core:/opt/m-ui/core"} {
		if !containsString(service.Volumes, volume) {
			t.Fatalf("docker-compose.yml is missing volume %q", volume)
		}
	}

	var workflow struct {
		Jobs map[string]struct {
			Needs       string            `yaml:"needs"`
			Permissions map[string]string `yaml:"permissions"`
			Steps       []struct {
				Uses string         `yaml:"uses"`
				With map[string]any `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(read(".github/workflows/release.yml")), &workflow); err != nil {
		t.Fatalf("release.yml is invalid YAML: %v", err)
	}
	dockerJob, ok := workflow.Jobs["docker"]
	if !ok || dockerJob.Needs != "release" || dockerJob.Permissions["packages"] != "write" {
		t.Fatalf("unexpected Docker release job: %#v", dockerJob)
	}
	buildPushFound := false
	for _, step := range dockerJob.Steps {
		if step.Uses == "docker/build-push-action@v6" {
			buildPushFound = step.With["platforms"] == "linux/amd64" && step.With["push"] == true
		}
	}
	if !buildPushFound {
		t.Fatal("the Docker release job must push a linux/amd64 image")
	}
}
