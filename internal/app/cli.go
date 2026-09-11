package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const maxSetupSecretBytes = 4096

// runCLI handles commands used by the Linux installer and the management
// script. The normal no-argument process remains the web panel server.
func runCLI(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Println(appVersion)
		return true, nil
	case "configure":
		return true, runConfigureCLI(args[1:])
	case "settings":
		return true, runSettingsCLI(args[1:])
	case "help", "--help", "-h":
		printCLIUsage(os.Stdout)
		return true, nil
	default:
		return true, fmt.Errorf("unknown command %q; run m-ui help", args[0])
	}
}

func printCLIUsage(w io.Writer) {
	fmt.Fprintln(w, "m-ui commands:")
	fmt.Fprintln(w, "  m-ui version")
	fmt.Fprintln(w, "  m-ui settings [--data-dir PATH]")
	fmt.Fprintln(w, "  m-ui configure [--data-dir PATH] [--listen-ip IP] [--port PORT] [--path PATH]")
	fmt.Fprintln(w, "                 [--username NAME] [--password VALUE | --password-stdin] [--core-path PATH]")
	fmt.Fprintln(w, "                 [--domain DOMAIN] [--cert CERT_FILE --key KEY_FILE]")
}

func defaultCLIDataDir() (string, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, defaultConfigDir), nil
}

func cliManager(dataDir string) (*CoreManager, error) {
	if strings.TrimSpace(dataDir) == "" {
		var err error
		dataDir, err = defaultCLIDataDir()
		if err != nil {
			return nil, err
		}
	}
	dataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	m := &CoreManager{dataDir: dataDir, connectionStats: map[string]connectionSample{}, enforcedClients: map[string]bool{}}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func runConfigureCLI(args []string) error {
	fs := flag.NewFlagSet("configure", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dataDir := fs.String("data-dir", "", "data directory")
	listenIP := fs.String("listen-ip", "", "panel listen IP")
	port := fs.Int("port", 0, "panel port")
	path := fs.String("path", "", "panel URI path")
	username := fs.String("username", "", "panel username")
	password := fs.String("password", "", "panel password")
	passwordStdin := fs.Bool("password-stdin", false, "read password from stdin")
	corePath := fs.String("core-path", "", "Mihomo executable path")
	panelDomain := fs.String("domain", "", "public panel domain")
	certPath := fs.String("cert", "", "panel TLS certificate path")
	keyPath := fs.String("key", "", "panel TLS private key path")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("invalid configure arguments: %w", err)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected configure argument %q", fs.Arg(0))
	}
	provided := map[string]bool{}
	fs.Visit(func(option *flag.Flag) { provided[option.Name] = true })
	if provided["password"] && *passwordStdin {
		return fmt.Errorf("use only one of --password and --password-stdin")
	}
	if *passwordStdin {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, maxSetupSecretBytes+1))
		if err != nil {
			return fmt.Errorf("read password: %w", err)
		}
		if len(data) > maxSetupSecretBytes {
			return fmt.Errorf("password is too long")
		}
		*password = strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
		provided["password"] = true
	}
	m, err := cliManager(*dataDir)
	if err != nil {
		return err
	}
	m.mu.Lock()
	settings := m.state.Settings
	m.mu.Unlock()
	if provided["listen-ip"] || provided["port"] {
		host, currentPort := hostPortFromAddress(settings.PanelListen)
		if provided["listen-ip"] {
			host = strings.TrimSpace(*listenIP)
		}
		if provided["port"] {
			currentPort = *port
		}
		if currentPort < 1 || currentPort > 65535 {
			return fmt.Errorf("panel port must be between 1 and 65535")
		}
		settings.PanelListen = net.JoinHostPort(host, strconv.Itoa(currentPort))
	}
	if provided["path"] {
		settings.PanelPath = normalizePanelPath(*path)
	}
	if provided["username"] {
		settings.Username = strings.TrimSpace(*username)
		if settings.Username == "" || strings.ContainsAny(settings.Username, "\r\n") {
			return fmt.Errorf("username cannot be empty or contain a newline")
		}
	}
	if provided["password"] {
		if *password == "" || strings.ContainsAny(*password, "\r\n") {
			return fmt.Errorf("password cannot be empty or contain a newline")
		}
		settings.Password = *password
	} else {
		// updateSettings treats the current hash as a new clear-text password.
		// Empty means preserve the stored hash instead.
		settings.Password = ""
	}
	if provided["core-path"] {
		settings.CorePath = strings.TrimSpace(*corePath)
		if settings.CorePath == "" {
			return fmt.Errorf("Mihomo core path cannot be empty")
		}
	}
	if provided["domain"] {
		settings.PanelDomain = strings.TrimSpace(*panelDomain)
	}
	if provided["cert"] {
		settings.PanelCertFile = strings.TrimSpace(*certPath)
	}
	if provided["key"] {
		settings.PanelKeyFile = strings.TrimSpace(*keyPath)
	}
	if err := m.updateSettings(settings); err != nil {
		return err
	}
	if err := m.writeConfig(); err != nil {
		return err
	}
	printSettings(m)
	return nil
}

func runSettingsCLI(args []string) error {
	fs := flag.NewFlagSet("settings", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dataDir := fs.String("data-dir", "", "data directory")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("invalid settings arguments: %w", err)
	}
	if fs.NArg() != 0 {
		return errors.New("settings does not accept positional arguments")
	}
	m, err := cliManager(*dataDir)
	if err != nil {
		return err
	}
	printSettings(m)
	return nil
}

func printSettings(m *CoreManager) {
	m.mu.Lock()
	settings := m.state.Settings
	m.mu.Unlock()
	host, port := hostPortFromAddress(settings.PanelListen)
	fmt.Printf("data-dir: %s\n", m.dataDir)
	fmt.Printf("listen-ip: %s\n", host)
	fmt.Printf("port: %d\n", port)
	fmt.Printf("path: %s\n", normalizePanelPath(settings.PanelPath))
	fmt.Printf("username: %s\n", settings.Username)
	fmt.Printf("core-path: %s\n", settings.CorePath)
	fmt.Printf("domain: %s\n", strings.TrimSpace(settings.PanelDomain))
	_, tlsValid := panelTLSStatus(settings)
	if tlsValid {
		fmt.Println("tls: enabled")
	} else {
		fmt.Println("tls: disabled")
	}
}
