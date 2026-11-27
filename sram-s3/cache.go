package main

// The --credential-process cache: one file per broker + target, holding the
// credential_process fields plus the broker, target, endpoint and region in clear (for
// sram-s3 status). Commands: logout and status.

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const credCacheGracePeriod = 5 * time.Minute

// cacheEntry is one cache file. The embedded fields are exactly what login
// --credential-process prints; the rest is for status.
type cacheEntry struct {
	credentialProcessOutput
	Broker      string `json:"broker"`
	Target      string `json:"target"`
	EndpointURL string `json:"endpoint_url"`
	Region      string `json:"region"`
}

func (e *cacheEntry) expiration() (time.Time, error) {
	return time.Parse(time.RFC3339, e.Expiration)
}

func credCacheDir() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, "sram-s3"), nil
}

func credCachePath(broker, target string) (string, error) {
	dir, err := credCacheDir()
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(broker + "\x00" + target))
	return filepath.Join(dir, fmt.Sprintf("%x.json", hash)), nil
}

// readCacheFile parses a cache file; entries without target or a valid expiration
// (unreadable, or written by CLI 0.x) are invalid.
func readCacheFile(path string) (*cacheEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var e cacheEntry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	if e.Target == "" || e.Broker == "" {
		return nil, fmt.Errorf("%s: no broker or target", path)
	}
	if _, err := e.expiration(); err != nil {
		return nil, err
	}
	return &e, nil
}

// loadCachedCredentials returns the cached credentials for broker + target unless they
// expire within the grace period. Invalid files are removed.
func loadCachedCredentials(broker, target string) *cacheEntry {
	path, err := credCachePath(broker, target)
	if err != nil {
		return nil
	}
	e, err := readCacheFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			_ = os.Remove(path)
		}
		return nil
	}
	if exp, _ := e.expiration(); time.Until(exp) < credCacheGracePeriod {
		return nil
	}
	return e
}

func writeCachedCredentials(e *cacheEntry) error {
	path, err := credCachePath(e.Broker, e.Target)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	removeExpiredCache(dir)

	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// removeExpiredCache deletes cache files whose credentials have expired or that cannot be
// read, so old secrets do not pile up on disk.
func removeExpiredCache(dir string) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, f := range files {
		e, err := readCacheFile(f)
		if err != nil {
			_ = os.Remove(f)
			continue
		}
		if exp, _ := e.expiration(); time.Now().After(exp) {
			_ = os.Remove(f)
		}
	}
}

// cachedEntries returns the valid cache entries, sorted by target and broker.
func cachedEntries() ([]*cacheEntry, error) {
	dir, err := credCacheDir()
	if err != nil {
		return nil, err
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	var out []*cacheEntry
	for _, f := range files {
		if e, err := readCacheFile(f); err == nil {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		return out[i].Broker < out[j].Broker
	})
	return out, nil
}

// clearCachedCredentials removes the cache entry for one broker/target pair.
// It reports whether an entry existed.
func clearCachedCredentials(broker, target string) (bool, error) {
	path, err := credCachePath(broker, target)
	if err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// clearAllCachedCredentials removes every cache entry (all brokers and targets)
// and returns the number of entries removed.
func clearAllCachedCredentials() (int, error) {
	dir, err := credCacheDir()
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !(strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".json.tmp")) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return removed, err
		}
		if strings.HasSuffix(name, ".json") {
			removed++
		}
	}
	return removed, nil
}

func runLogout(args []string) int {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var broker, target string
	fs.StringVar(&broker, "b", "", "broker URL")
	fs.StringVar(&broker, "broker", "", "broker URL")
	fs.StringVar(&target, "p", "", "target")
	fs.StringVar(&target, "project", "", "target")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	target = strings.TrimSpace(target)

	const note = "Note: this only removes the local copy; issued credentials stay valid until they expire."

	if target == "" {
		n, err := clearAllCachedCredentials()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 1
		}
		if n == 0 {
			fmt.Fprintln(os.Stderr, "No cached credentials.")
			return 0
		}
		fmt.Fprintf(os.Stderr, "Removed %d cached credential set(s).\n%s\n", n, note)
		return 0
	}

	broker = resolveBroker(broker)
	if broker == "" {
		fmt.Fprintln(os.Stderr, "ERROR: missing broker URL (-b / --broker or SRAM_BROKER_URL)")
		return 1
	}
	removed, err := clearCachedCredentials(broker, target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	if !removed {
		fmt.Fprintf(os.Stderr, "No cached credentials for %s.\n", target)
		return 0
	}
	fmt.Fprintf(os.Stderr, "Removed cached credentials for %s.\n%s\n", target, note)
	return 0
}

func runStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var target string
	fs.StringVar(&target, "p", "", "target")
	fs.StringVar(&target, "project", "", "target")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	entries, err := cachedEntries()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	exe := executablePath()
	printStatus(os.Stdout, entries, strings.TrimSpace(target), exe, time.Now())
	return 0
}

// printStatus writes one line per cached target, each followed by its ~/.aws/config profile.
func printStatus(w io.Writer, entries []*cacheEntry, onlyTarget, exe string, now time.Time) {
	var shown []*cacheEntry
	for _, e := range entries {
		if onlyTarget == "" || e.Target == onlyTarget {
			shown = append(shown, e)
		}
	}
	if len(shown) == 0 {
		if onlyTarget != "" {
			fmt.Fprintf(w, "No cached credentials for %s.\n", onlyTarget)
		} else {
			fmt.Fprintln(w, "No cached credentials. Log in once with `sram-s3 login --credential-process -p <target>`.")
		}
		return
	}
	states := make([]string, len(shown))
	tw, sw := 0, 0
	for i, e := range shown {
		states[i] = validity(e, now)
		tw = max(tw, len(e.Target))
		sw = max(sw, len([]rune(states[i])))
	}
	for i, e := range shown {
		if i > 0 {
			fmt.Fprintln(w)
		}
		pad := sw - len([]rune(states[i]))
		fmt.Fprintf(w, "%-*s   %s%s   %s\n\n", tw, e.Target, states[i], strings.Repeat(" ", pad), e.Broker)
		fmt.Fprint(w, profileBlock(e, exe))
	}
}

// validity describes when the credentials expire, e.g. "valid until 14:32 (47 min)".
func validity(e *cacheEntry, now time.Time) string {
	exp, _ := e.expiration()
	exp = exp.In(now.Location())
	if !now.Before(exp) {
		return "expired " + clockTime(exp, now)
	}
	left := exp.Sub(now)
	s := "valid until " + clockTime(exp, now) + " (" + shortDuration(left)
	if left < credCacheGracePeriod {
		s += ", next use logs in again"
	}
	return s + ")"
}

func clockTime(t, now time.Time) string {
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	if y1 == y2 && m1 == m2 && d1 == d2 {
		return t.Format("15:04")
	}
	return t.Format("2006-01-02 15:04")
}

func shortDuration(d time.Duration) string {
	m := int(d.Round(time.Minute).Minutes())
	if m < 60 {
		return fmt.Sprintf("%d min", m)
	}
	return fmt.Sprintf("%d h %d min", m/60, m%60)
}

// profileName is the target with ':' and '_' replaced by '-'.
func profileName(target string) string {
	return strings.NewReplacer(":", "-", "_", "-").Replace(target)
}

// profileBlock is a ready-to-paste ~/.aws/config profile for a cache entry. The broker is
// named only when it is not the compiled-in default.
func profileBlock(e *cacheEntry, exe string) string {
	if strings.ContainsAny(exe, " \t") {
		exe = `"` + exe + `"`
	}
	cmd := exe + " login --credential-process"
	if e.Broker != strings.TrimRight(defaultBrokerURL, "/") {
		cmd += " -b " + e.Broker
	}
	cmd += " -p " + e.Target
	return fmt.Sprintf("[profile %s]\ncredential_process = %s\nendpoint_url = %s\nregion = %s\n",
		profileName(e.Target), cmd, e.EndpointURL, e.Region)
}

// executablePath is the absolute path of the running binary, symlinks resolved.
func executablePath() string {
	exe, err := os.Executable()
	if err != nil {
		return "sram-s3"
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe
}
