package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testBroker = "https://broker.example"

// isolateCache points os.UserCacheDir at a temp dir (HOME on macOS,
// XDG_CACHE_HOME on Linux) and returns the sram-s3 cache directory.
func isolateCache(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tmp, ".cache"))
	dir, err := credCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func testCreds(expiresIn time.Duration) *credentialProcessOutput {
	return &credentialProcessOutput{
		Version:         1,
		AccessKeyId:     "AKI",
		SecretAccessKey: "SEC",
		SessionToken:    "TOK",
		Expiration:      time.Now().Add(expiresIn).UTC().Format(time.RFC3339),
	}
}

func TestCacheRoundTrip(t *testing.T) {
	isolateCache(t)
	in := testCreds(time.Hour)
	if err := writeCachedCredentials(testBroker, "a:members", in); err != nil {
		t.Fatal(err)
	}
	got := loadCachedCredentials(testBroker, "a:members")
	if got == nil {
		t.Fatal("expected cache hit")
	}
	if *got != *in {
		t.Fatalf("got %+v, want %+v", *got, *in)
	}
}

func TestCacheFilePermissions(t *testing.T) {
	isolateCache(t)
	if err := writeCachedCredentials(testBroker, "a:members", testCreds(time.Hour)); err != nil {
		t.Fatal(err)
	}
	path, _ := credCachePath(testBroker, "a:members")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0600 {
		t.Fatalf("file mode %o, want 600", perm)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0700 {
		t.Fatalf("dir mode %o, want 700", perm)
	}
}

func TestCacheKeyedByBrokerAndProject(t *testing.T) {
	isolateCache(t)
	if err := writeCachedCredentials(testBroker, "a:members", testCreds(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if loadCachedCredentials(testBroker, "b:members") != nil {
		t.Fatal("hit for other project")
	}
	if loadCachedCredentials(testBroker, "") != nil {
		t.Fatal("hit for empty project")
	}
	if loadCachedCredentials("https://other.example", "a:members") != nil {
		t.Fatal("hit for other broker")
	}
}

func TestCacheMissWithinGracePeriod(t *testing.T) {
	isolateCache(t)
	if err := writeCachedCredentials(testBroker, "a", testCreds(credCacheGracePeriod-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if loadCachedCredentials(testBroker, "a") != nil {
		t.Fatal("expected miss for credentials expiring within the grace period")
	}
}

func TestCacheMissWhenExpired(t *testing.T) {
	isolateCache(t)
	if err := writeCachedCredentials(testBroker, "a", testCreds(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if loadCachedCredentials(testBroker, "a") != nil {
		t.Fatal("expected miss for expired credentials")
	}
}

func TestCacheMissWithoutExpiration(t *testing.T) {
	isolateCache(t)
	c := testCreds(time.Hour)
	c.Expiration = ""
	if err := writeCachedCredentials(testBroker, "a", c); err != nil {
		t.Fatal(err)
	}
	if loadCachedCredentials(testBroker, "a") != nil {
		t.Fatal("expected miss without Expiration")
	}
}

func TestCacheCorruptFileRemoved(t *testing.T) {
	for name, content := range map[string]string{
		"invalid JSON":       "{not json",
		"invalid Expiration": `{"Version":1,"AccessKeyId":"AKI","Expiration":"2026-01-01 00:00:00"}`,
	} {
		t.Run(name, func(t *testing.T) {
			isolateCache(t)
			path, _ := credCachePath(testBroker, "a")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if loadCachedCredentials(testBroker, "a") != nil {
				t.Fatal("expected miss")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("corrupt cache file not removed (stat err: %v)", err)
			}
		})
	}
}

func TestClearCachedCredentials(t *testing.T) {
	isolateCache(t)
	for _, p := range []string{"a", "b"} {
		if err := writeCachedCredentials(testBroker, p, testCreds(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := clearCachedCredentials(testBroker, "a")
	if err != nil || !removed {
		t.Fatalf("clear a: removed=%v err=%v", removed, err)
	}
	if loadCachedCredentials(testBroker, "a") != nil {
		t.Fatal("a still cached")
	}
	if loadCachedCredentials(testBroker, "b") == nil {
		t.Fatal("b removed too")
	}
	removed, err = clearCachedCredentials(testBroker, "a")
	if err != nil || removed {
		t.Fatalf("second clear a: removed=%v err=%v", removed, err)
	}
}

func TestClearAllCachedCredentials(t *testing.T) {
	dir := isolateCache(t)

	// No cache directory yet.
	if n, err := clearAllCachedCredentials(); err != nil || n != 0 {
		t.Fatalf("empty: n=%d err=%v", n, err)
	}

	for _, p := range []string{"a", "b", ""} {
		if err := writeCachedCredentials(testBroker, p, testCreds(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	// Leftover temp file and an unrelated file.
	if err := os.WriteFile(filepath.Join(dir, "x.json.tmp"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	n, err := clearAllCachedCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("removed %d, want 3", n)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "keep.txt" {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("remaining entries %v, want [keep.txt]", names)
	}
}

func TestResolveBroker(t *testing.T) {
	t.Setenv("SRAM_BROKER_URL", "")
	t.Setenv("OIDC_ISSUER", "")
	old := defaultBrokerURL
	defer func() { defaultBrokerURL = old }()
	defaultBrokerURL = "https://default.example"

	if got := resolveBroker(""); got != "https://default.example" {
		t.Fatalf("default: got %q", got)
	}
	t.Setenv("SRAM_BROKER_URL", "https://env.example/")
	if got := resolveBroker(""); got != "https://env.example" {
		t.Fatalf("env: got %q", got)
	}
	if got := resolveBroker(" https://flag.example/ "); got != "https://flag.example" {
		t.Fatalf("flag: got %q", got)
	}
}

func TestWriteRemovesExpiredEntries(t *testing.T) {
	isolateCache(t)
	_ = writeCachedCredentials(testBroker, "old", testCreds(-time.Minute))
	_ = writeCachedCredentials(testBroker, "valid", testCreds(time.Hour))
	_ = writeCachedCredentials(testBroker, "new", testCreds(time.Hour))

	for p, want := range map[string]bool{"old": false, "valid": true, "new": true} {
		path, _ := credCachePath(testBroker, p)
		_, err := os.Stat(path)
		if got := err == nil; got != want {
			t.Errorf("%s: exists=%v, want %v", p, got, want)
		}
	}
}
