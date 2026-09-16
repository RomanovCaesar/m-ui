//go:build linux

package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Model ISC dhclient's script_go environment: only -e values and DHCP lease
// fields reach the hook, not arbitrary variables in the parent process.
func vpnGateTestDHCPEnvironment(t *testing.T, slot int) []string {
	t.Helper()
	command := vpnGateDHCPCommand(context.Background(), slot, vpnGateInterfaceName(slot),
		"/private slot/dhclient.conf", "/private slot/dhclient.leases", "/private slot/dhclient.pid", "/private slot/hook.sh")
	environment := []string{"PATH=" + os.Getenv("PATH")}
	for i := 1; i < len(command.Args); i++ {
		if command.Args[i] == "-e" {
			i++
			if i == len(command.Args) {
				t.Fatal("dhclient -e is missing a value")
			}
			environment = append(environment, command.Args[i])
		}
	}
	return environment
}

func vpnGateTestDHCPHook(t *testing.T, environment []string) (string, string, error) {
	t.Helper()
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal("DHCP hook tests require a POSIX shell: ", err)
	}
	directory := t.TempDir()
	command := exec.Command(shell)
	command.Dir = directory
	command.Env = environment
	// Intercept all ip operations: these tests must never configure a real
	// interface, policy rule, or route, even when run as root on Linux.
	command.Stdin = strings.NewReader(`ip() { printf '%s\n' "$*" >> ip.log; }
` + vpnGateDHCPHook)
	output, runErr := command.CombinedOutput()
	operations, err := os.ReadFile(filepath.Join(directory, "ip.log"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(operations), string(output), runErr
}

func TestVPNGateDHCPHookAppliesLeaseWithSanitizedEnvironment(t *testing.T) {
	// Poison the parent environment to catch any reliance on inherited slot
	// parameters. The explicit -e values must select the correct slot instead.
	t.Setenv("MUI_VPNGATE_TABLE", "999")
	t.Setenv("MUI_VPNGATE_MARK", "999")
	for _, slot := range []int{0, 9} {
		for _, reason := range []string{"BOUND", "RENEW", "REBIND", "REBOOT"} {
			t.Run(fmt.Sprintf("slot%d/%s", slot, reason), func(t *testing.T) {
				environment := append(vpnGateTestDHCPEnvironment(t, slot),
					"interface="+vpnGateInterfaceName(slot), "reason="+reason,
					"new_ip_address=10.211.1.42", "new_subnet_mask=255.255.0.0",
					"new_routers=10.211.254.254", "new_interface_mtu=1500")
				operations, output, err := vpnGateTestDHCPHook(t, environment)
				if err != nil {
					t.Fatalf("DHCP lease was not applied: %v\n%s", err, output)
				}
				// Check the full set of effects, including the absence of main
				// table routes and operations on any other slot's interface.
				want := fmt.Sprintf(`-4 addr flush dev vpn_vpn%d
-4 addr add 10.211.1.42/16 dev vpn_vpn%d noprefixroute
link set dev vpn_vpn%d up
link set dev vpn_vpn%d mtu 1500
route replace unreachable default table %d proto 242 metric 42760
rule show
rule add fwmark %d table %d priority %d
rule show
rule add from 10.211.1.42 table %d priority %d
route replace default via 10.211.254.254 dev vpn_vpn%d onlink table %d proto 242 metric 10
`, slot, slot, slot, slot, 100+slot, 100+slot, 100+slot, 20000+slot, 100+slot, 20100+slot, slot, 100+slot)
				if operations != want {
					t.Fatalf("unexpected DHCP network operations:\n%s\nwant:\n%s", operations, want)
				}
			})
		}
	}
}

func TestVPNGateDHCPHookRejectsMissingRoutingParameters(t *testing.T) {
	for _, missing := range []string{"MUI_VPNGATE_TABLE", "MUI_VPNGATE_MARK", "MUI_VPNGATE_MARK_PRIORITY", "MUI_VPNGATE_SOURCE_PRIORITY"} {
		t.Run(missing, func(t *testing.T) {
			var environment []string
			for _, entry := range vpnGateTestDHCPEnvironment(t, 0) {
				if !strings.HasPrefix(entry, missing+"=") {
					environment = append(environment, entry)
				}
			}
			environment = append(environment, "interface=vpn_vpn0", "reason=BOUND",
				"new_ip_address=10.211.1.42", "new_subnet_mask=255.255.0.0", "new_routers=10.211.254.254")
			operations, output, err := vpnGateTestDHCPHook(t, environment)
			if err == nil || !strings.Contains(output, "missing slot routing parameters for vpn_vpn0") {
				t.Fatalf("missing context was not diagnosed: error %v, output %q", err, output)
			}
			if operations != "" {
				t.Fatalf("hook changed the network without slot context: %s", operations)
			}
		})
	}
}

func TestVPNGateDHCPHookRejectsMissingAddress(t *testing.T) {
	environment := append(vpnGateTestDHCPEnvironment(t, 0), "interface=vpn_vpn0", "reason=BOUND",
		"new_subnet_mask=255.255.0.0", "new_routers=10.211.254.254")
	operations, output, err := vpnGateTestDHCPHook(t, environment)
	if err == nil || !strings.Contains(output, "could not apply BOUND lease to vpn_vpn0") || operations != "" {
		t.Fatalf("incomplete lease was not rejected: error %v, output %q, operations %q", err, output, operations)
	}
}
