package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type vpnGateAppArmorFixture struct {
	profiles, policyDir, profile, local string
}

func newVPNGateAppArmorFixture(t *testing.T, profileName, include string) vpnGateAppArmorFixture {
	t.Helper()
	dir := t.TempDir()
	f := vpnGateAppArmorFixture{profiles: filepath.Join(dir, "profiles"), policyDir: filepath.Join(dir, "apparmor.d")}
	f.profile = filepath.Join(f.policyDir, profileName)
	if err := os.MkdirAll(filepath.Join(f.policyDir, "local"), 0755); err != nil {
		t.Fatal(err)
	}
	vpnGateAppArmorTestWrite(t, f.profiles, "/{,usr/}sbin/dhclient (enforce)\n")
	vpnGateAppArmorTestWrite(t, f.profile, "/{,usr/}sbin/dhclient {\n  "+include+"\n}\n")
	return f
}

func vpnGateAppArmorTestWrite(t *testing.T, filename, content string) {
	t.Helper()
	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func vpnGateAppArmorTestRead(t *testing.T, filename string) string {
	t.Helper()
	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestVPNGateAppArmorPreparesLocalPolicy(t *testing.T) {
	for _, test := range []struct{ profile, local, include string }{
		{"sbin.dhclient", "sbin.dhclient", "#include <local/sbin.dhclient>"},
		{"usr.sbin.dhclient", "usr.sbin.dhclient", "include if exists <local/usr.sbin.dhclient>"},
		{"usr.sbin.dhclient", "sbin.dhclient", "#include <local/sbin.dhclient>"},
		{"sbin.dhclient", "sbin.dhclient", `#include "local/sbin.dhclient"`},
	} {
		t.Run(test.profile+"/"+test.include, func(t *testing.T) {
			f := newVPNGateAppArmorFixture(t, test.profile, test.include)
			f.local = filepath.Join(f.policyDir, "local", test.local)
			// Include the user's earlier manual repair: it must remain intact.
			original := "# administrator rules\n/etc/custom-dhcp.conf r,\n# m-ui VPNGate DHCP paths\n/usr/local/m-ui/data/vpngate/run/[0-9]/dhclient-hook.sh Uxr,\n"
			vpnGateAppArmorTestWrite(t, f.local, original)
			profileBefore := vpnGateAppArmorTestRead(t, f.profile)
			var manager vpnGateAppArmor
			calls := 0
			reload := func(_ context.Context, profile string) error {
				calls++
				if profile != f.profile {
					t.Fatalf("reloading unrelated profile %s", profile)
				}
				actual := vpnGateAppArmorTestRead(t, f.local)
				if !strings.HasPrefix(actual, original) {
					t.Fatal("administrator's rules were changed")
				}
				for _, rule := range []string{
					`"/srv/my panel/vpngate/run/[0-9]/dhclient.conf" r,`,
					`"/srv/my panel/vpngate/run/[0-9]/dhclient.leases" lrw,`,
					`"/srv/my panel/vpngate/run/[0-9]/dhclient.leases~" lrw,`,
					`"/srv/my panel/vpngate/run/[0-9]/dhclient.pid" rw,`,
					`"/srv/my panel/vpngate/run/[0-9]/dhclient-hook.sh" Uxr,`,
				} {
					if !strings.Contains(actual, rule) {
						t.Fatalf("required DHCP permission missing: %s\n%s", rule, actual)
					}
				}
				return nil
			}
			ensure := func(a *vpnGateAppArmor) {
				t.Helper()
				if err := a.ensure(context.Background(), "/srv/my panel/vpngate/run", f.profiles, f.policyDir, reload); err != nil {
					t.Fatal(err)
				}
			}
			ensure(&manager)
			first := vpnGateAppArmorTestRead(t, f.local)
			backup := filepath.Join(filepath.Dir(f.local), "."+test.local+".m-ui-vpngate.bak")
			if vpnGateAppArmorTestRead(t, backup) != original {
				t.Fatal("original rules were not backed up")
			}
			ensure(&manager)
			if calls != 1 || vpnGateAppArmorTestRead(t, f.local) != first {
				t.Fatal("reconnecting duplicated rules or unnecessarily reloaded policy")
			}
			var restarted vpnGateAppArmor
			ensure(&restarted)
			if calls != 2 || vpnGateAppArmorTestRead(t, f.local) != first {
				t.Fatal("restart must verify policy is loaded without duplicating rules")
			}
			if vpnGateAppArmorTestRead(t, f.profile) != profileBefore {
				t.Fatal("distribution profile was overwritten")
			}
		})
	}
}

func TestVPNGateAppArmorSkipsUnconfinedDHCP(t *testing.T) {
	for name, profiles := range map[string]string{
		"unavailable": "", "unloaded": "other-service (enforce)\n", "complain": "/{,usr/}sbin/dhclient (complain)\n",
		"child only": "/{,usr/}sbin/dhclient//child (enforce)\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := newVPNGateAppArmorFixture(t, "sbin.dhclient", "#include <local/sbin.dhclient>")
			if name == "unavailable" {
				if err := os.Remove(f.profiles); err != nil {
					t.Fatal(err)
				}
			} else {
				vpnGateAppArmorTestWrite(t, f.profiles, profiles)
			}
			var manager vpnGateAppArmor
			err := manager.ensure(context.Background(), "/srv/m-ui/vpngate/run", f.profiles, f.policyDir, func(context.Context, string) error {
				t.Fatal("must not enable an inactive dhclient profile")
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(filepath.Join(f.policyDir, "local"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("unconfined DHCP caused filesystem changes: %v, %v", entries, err)
			}
		})
	}
}

func TestVPNGateAppArmorRollback(t *testing.T) {
	for _, existed := range []bool{false, true} {
		t.Run(fmt.Sprint("existing=", existed), func(t *testing.T) {
			f := newVPNGateAppArmorFixture(t, "sbin.dhclient", "#include if exists <local/sbin.dhclient>")
			f.local = filepath.Join(f.policyDir, "local", "sbin.dhclient")
			original := "# preserve these rules exactly\n/etc/custom.conf r,\n"
			if existed {
				vpnGateAppArmorTestWrite(t, f.local, original)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var manager vpnGateAppArmor
			calls := 0
			err := manager.ensure(ctx, "/srv/m-ui/vpngate/run", f.profiles, f.policyDir, func(ctx context.Context, _ string) error {
				calls++
				if calls == 1 {
					cancel()
					return errors.New("parser rejected the new policy")
				}
				if ctx.Err() != nil {
					t.Fatal("rollback inherited the canceled connection context")
				}
				if existed {
					if vpnGateAppArmorTestRead(t, f.local) != original {
						t.Fatal("old file was not restored before policy reload")
					}
				} else if _, err := os.Stat(f.local); !os.IsNotExist(err) {
					t.Fatal("new local rules file was left behind")
				}
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), "parser rejected") || calls != 2 {
				t.Fatalf("failed policy update was not rolled back: calls=%d, error=%v", calls, err)
			}
			if err := manager.ensure(context.Background(), "/srv/m-ui/vpngate/run", f.profiles, f.policyDir, func(context.Context, string) error { calls++; return nil }); err != nil {
				t.Fatal(err)
			}
			if calls != 3 {
				t.Fatal("failed update was cached as a success")
			}
		})
	}
}

func TestVPNGateAppArmorConcurrentSlots(t *testing.T) {
	f := newVPNGateAppArmorFixture(t, "sbin.dhclient", "#include <local/sbin.dhclient>")
	var manager vpnGateAppArmor
	var wait sync.WaitGroup
	calls := 0
	for slot := 0; slot <= vpnGateMaxSlot; slot++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			err := manager.ensure(context.Background(), "/srv/m-ui/vpngate/run", f.profiles, f.policyDir, func(context.Context, string) error { calls++; return nil })
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	if calls != 1 {
		t.Fatalf("concurrent slots reloaded the same policy %d times", calls)
	}
}

func TestVPNGateAppArmorRetainsConcurrentAdministratorEdit(t *testing.T) {
	f := newVPNGateAppArmorFixture(t, "sbin.dhclient", "#include <local/sbin.dhclient>")
	f.local = filepath.Join(f.policyDir, "local", "sbin.dhclient")
	original := "# original admin rules\n"
	vpnGateAppArmorTestWrite(t, f.local, original)
	var manager vpnGateAppArmor
	updatedByAdmin := "# changed by admin during reload\n"
	err := manager.ensure(context.Background(), "/srv/m-ui/vpngate/run", f.profiles, f.policyDir, func(context.Context, string) error {
		vpnGateAppArmorTestWrite(t, f.local, updatedByAdmin)
		return errors.New("reload failed")
	})
	if err == nil || !strings.Contains(err.Error(), "local rules changed during reload") || vpnGateAppArmorTestRead(t, f.local) != updatedByAdmin {
		t.Fatalf("rollback overwrote concurrent administrator changes: %v", err)
	}
}

func TestVPNGateAppArmorRefreshesChangedProfile(t *testing.T) {
	f := newVPNGateAppArmorFixture(t, "sbin.dhclient", "#include <local/sbin.dhclient>")
	f.local = filepath.Join(f.policyDir, "local", "sbin.dhclient")
	var manager vpnGateAppArmor
	calls := 0
	ensure := func() {
		t.Helper()
		err := manager.ensure(context.Background(), "/srv/m-ui/vpngate/run", f.profiles, f.policyDir, func(context.Context, string) error { calls++; return nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	ensure()
	ensure()
	if calls != 1 {
		t.Fatal("unchanged policy should reuse successful preparation")
	}
	vpnGateAppArmorTestWrite(t, f.profile, vpnGateAppArmorTestRead(t, f.profile)+"# package update\n")
	ensure()
	if calls != 2 {
		t.Fatal("updated distribution profile was not reloaded")
	}
	vpnGateAppArmorTestWrite(t, f.local, vpnGateAppArmorTestRead(t, f.local)+"# administrator change\n")
	ensure()
	if calls != 3 {
		t.Fatal("updated local rules were not reloaded")
	}
}

func TestVPNGateAppArmorRejectsUnsupportedPolicyAndPaths(t *testing.T) {
	f := newVPNGateAppArmorFixture(t, "sbin.dhclient", "# no local customization point")
	var manager vpnGateAppArmor
	err := manager.ensure(context.Background(), "/srv/m-ui/vpngate/run", f.profiles, f.policyDir, func(context.Context, string) error {
		t.Fatal("unsupported profile must not be reloaded")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "no supported local include") {
		t.Fatalf("missing local include was not diagnosed: %v", err)
	}
	for _, root := range []string{"relative/run", "/", "/srv/*/run", "/srv/@{HOME}/run", "/srv/[abc]/run", "/srv/\"/run", "/srv/\n/run", "/srv/\\/run"} {
		if _, _, err := vpnGateAppArmorRules(root); err == nil {
			t.Errorf("unsafe AppArmor path was accepted: %q", root)
		}
	}
}

func TestVPNGateAppArmorMergePreservesOtherInstallations(t *testing.T) {
	begin, rules, err := vpnGateAppArmorRules("/srv/first/vpngate/run")
	if err != nil {
		t.Fatal(err)
	}
	otherBegin, otherRules, err := vpnGateAppArmorRules("/srv/second/vpngate/run")
	if err != nil {
		t.Fatal(err)
	}
	old := "# administrator prefix\n" + strings.ReplaceAll(rules, "lrw,", "rw,") + otherRules + "# administrator suffix\n"
	merged, err := vpnGateAppArmorMerge([]byte(old), begin, rules)
	if err != nil || !strings.Contains(string(merged), rules+otherRules) || !strings.HasSuffix(string(merged), "# administrator suffix\n") {
		t.Fatalf("managed update damaged other rules: %v\n%s", err, merged)
	}
	if begin == otherBegin {
		t.Fatal("different installations must have different managed blocks")
	}
	for _, corrupt := range []string{begin + "\n", rules + rules, strings.Replace(rules, "# END", "# LOST", 1)} {
		if _, err := vpnGateAppArmorMerge([]byte(corrupt), begin, rules); err == nil {
			t.Fatalf("damaged block was silently replaced: %q", corrupt)
		}
	}
}

func TestVPNGateAppArmorPolicyParses(t *testing.T) {
	parser, err := exec.LookPath("apparmor_parser")
	if err != nil {
		t.Skip("AppArmor parser is not installed on this host")
	}
	_, rules, err := vpnGateAppArmorRules("/srv/my panel/vpngate/run")
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(t.TempDir(), "test-profile")
	vpnGateAppArmorTestWrite(t, profile, "profile mui-vpngate-test /nonexistent/mui-vpngate-test {\n"+rules+"}\n")
	// Compile the real policy grammar without loading anything into the kernel.
	if output, err := exec.Command(parser, "-Q", "-T", profile).CombinedOutput(); err != nil {
		t.Fatalf("generated AppArmor policy does not compile: %v\n%s", err, output)
	}
}
