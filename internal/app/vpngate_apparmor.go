package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

// The file transaction is platform independent so it can be tested without
// loading host security policy. Only the Linux link invokes it in production.
type vpnGateAppArmor struct {
	mu      sync.Mutex
	applied [sha256.Size]byte
}

var vpnGateAppArmorInclude = regexp.MustCompile(`(?m)^[\t ]*(?:#[\t ]*)?include[\t ]+(?:if[\t ]+exists[\t ]+)?(?:<local/((?:usr\.)?sbin\.dhclient)>|"local/((?:usr\.)?sbin\.dhclient)")[\t ]*(?:#.*)?$`)

func vpnGateAppArmorEnforced(profiles string) bool {
	for _, line := range strings.Split(profiles, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || (fields[1] != "(enforce)" && fields[1] != "(kill)") {
			continue
		}
		switch fields[0] {
		case "/{,usr/}sbin/dhclient", "/usr/sbin/dhclient", "/sbin/dhclient":
			return true
		}
	}
	return false
}

func vpnGateAppArmorProfile(policyDir string) (string, string, []byte, error) {
	for _, name := range []string{"usr.sbin.dhclient", "sbin.dhclient"} {
		profile := filepath.Join(policyDir, name)
		content, err := os.ReadFile(profile)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", "", nil, err
		}
		match := vpnGateAppArmorInclude.FindSubmatch(bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n")))
		if match == nil {
			continue
		}
		localName := string(match[1]) + string(match[2])
		return profile, filepath.Join(policyDir, "local", localName), content, nil
	}
	return "", "", nil, fmt.Errorf("dhclient is confined but no supported local include was found in %s/sbin.dhclient or usr.sbin.dhclient", policyDir)
}

func vpnGateAppArmorRules(runRoot string) (string, string, error) {
	// Quote paths with spaces, but refuse policy syntax in configured paths.
	// In particular, quotes alone do not make AppArmor glob characters literal.
	if !strings.HasPrefix(runRoot, "/") || strings.ContainsAny(runRoot, "\\\"*?[]{}^") || strings.IndexFunc(runRoot, unicode.IsControl) >= 0 {
		return "", "", fmt.Errorf("unsupported VPNGate path for AppArmor: %q", runRoot)
	}
	runRoot = path.Clean(runRoot)
	if runRoot == "/" {
		return "", "", fmt.Errorf("refusing to grant AppArmor access to the filesystem root")
	}
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(runRoot)))
	begin := "# BEGIN m-ui VPNGate DHCP " + id
	end := "# END m-ui VPNGate DHCP " + id
	prefix := runRoot + "/[0-9]/"
	// Uxr matches the distribution's dhclient-script transition: only the
	// panel-owned hook runs outside dhclient's profile, with a scrubbed loader
	// environment. dhclient itself remains confined and cannot write the hook.
	rules := begin + "\n" +
		fmt.Sprintf("\"%sdhclient.conf\" r,\n", prefix) +
		fmt.Sprintf("\"%sdhclient.leases\" lrw,\n", prefix) +
		fmt.Sprintf("\"%sdhclient.leases~\" lrw,\n", prefix) +
		fmt.Sprintf("\"%sdhclient.pid\" rw,\n", prefix) +
		fmt.Sprintf("\"%sdhclient-hook.sh\" Uxr,\n", prefix) + end + "\n"
	return begin, rules, nil
}

func vpnGateAppArmorMerge(existing []byte, begin, rules string) ([]byte, error) {
	text := string(existing)
	end := strings.Replace(begin, "# BEGIN ", "# END ", 1)
	start, finish := strings.Index(text, begin), strings.Index(text, end)
	if start < 0 && finish < 0 {
		if len(text) > 0 && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		return []byte(text + rules), nil
	}
	if start < 0 || finish < start || strings.Count(text, begin) != 1 || strings.Count(text, end) != 1 ||
		(start > 0 && text[start-1] != '\n') || (finish > 0 && text[finish-1] != '\n') {
		return nil, fmt.Errorf("incomplete or duplicate m-ui VPNGate AppArmor block; refusing to overwrite local rules")
	}
	finish += len(end)
	if finish < len(text) && text[finish] == '\r' {
		finish++
	}
	if finish < len(text) && text[finish] != '\n' {
		return nil, fmt.Errorf("invalid m-ui VPNGate AppArmor block boundary")
	}
	if finish < len(text) {
		finish++
	}
	return []byte(text[:start] + rules + text[finish:]), nil
}

// ensure serializes the slots' shared policy update. The kernel profile list
// prevents accidentally enabling a disabled or complain-mode profile.
func (a *vpnGateAppArmor) ensure(ctx context.Context, runRoot, profilesPath, policyDir string, reload func(context.Context, string) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	profiles, err := os.ReadFile(profilesPath)
	if os.IsNotExist(err) {
		a.applied = [sha256.Size]byte{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read loaded AppArmor profiles: %w", err)
	}
	if !vpnGateAppArmorEnforced(string(profiles)) {
		a.applied = [sha256.Size]byte{}
		return nil
	}
	profile, local, profileContent, err := vpnGateAppArmorProfile(policyDir)
	if err != nil {
		return err
	}
	begin, rules, err := vpnGateAppArmorRules(runRoot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(local), 0755); err != nil {
		return err
	}
	if info, err := os.Lstat(filepath.Dir(local)); err != nil || !info.IsDir() {
		return fmt.Errorf("AppArmor local rules directory must be a real directory: %s", filepath.Dir(local))
	}
	mode := os.FileMode(0644)
	info, err := os.Lstat(local)
	existed := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var original []byte
	if existed {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("AppArmor local rules must be a regular file: %s", local)
		}
		mode = info.Mode().Perm()
		original, err = os.ReadFile(local)
		if err != nil {
			return err
		}
	}
	updated, err := vpnGateAppArmorMerge(original, begin, rules)
	if err != nil {
		return err
	}
	fingerprint := sha256.Sum256([]byte(profile + "\x00" + string(profileContent) + "\x00" + string(updated)))
	changed := !bytes.Equal(original, updated)
	if !changed && a.applied == fingerprint {
		return nil
	}
	if changed {
		if existed {
			// A hidden backup is not loaded as policy or included by the profile.
			backup := filepath.Join(filepath.Dir(local), "."+filepath.Base(local)+".m-ui-vpngate.bak")
			if err := vpnGateAppArmorWrite(backup, original, mode); err != nil {
				return fmt.Errorf("back up AppArmor local rules: %w", err)
			}
		}
		if err := vpnGateAppArmorWrite(local, updated, mode); err != nil {
			return fmt.Errorf("write AppArmor local rules: %w", err)
		}
	}
	// Also reload once after a panel restart, even if the file is unchanged:
	// an earlier process may have exited between writing and loading the rules.
	if err := reload(ctx, profile); err != nil {
		a.applied = [sha256.Size]byte{}
		if !changed {
			return fmt.Errorf("reload %s: %w", profile, err)
		}
		current, readErr := os.ReadFile(local)
		if readErr != nil || !bytes.Equal(current, updated) {
			return fmt.Errorf("reload %s: %w; local rules changed during reload, automatic rollback was not applied", profile, err)
		}
		var restoreErr error
		if existed {
			restoreErr = vpnGateAppArmorWrite(local, original, mode)
		} else {
			restoreErr = os.Remove(local)
		}
		if restoreErr != nil {
			return fmt.Errorf("reload %s: %w; restoring local rules failed: %v", profile, err, restoreErr)
		}
		// Cancellation of a connection must not prevent policy rollback.
		restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if restoreErr := reload(restoreCtx, profile); restoreErr != nil {
			return fmt.Errorf("reload %s: %w; local rules restored, but reloading the previous policy failed: %v", profile, err, restoreErr)
		}
		return fmt.Errorf("reload %s: %w; previous local rules restored", profile, err)
	}
	a.applied = fingerprint
	return nil
}

func vpnGateAppArmorWrite(filename string, content []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(filename), ".m-ui-apparmor-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	defer file.Close()
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, filename)
}
