package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

func testEntry(broker, target string, expiresIn time.Duration) *cacheEntry {
	return &cacheEntry{
		credentialProcessOutput: credentialProcessOutput{
			Version:         1,
			AccessKeyId:     "AKI",
			SecretAccessKey: "SEC",
			SessionToken:    "TOK",
			Expiration:      time.Now().Add(expiresIn).UTC().Format(time.RFC3339),
		},
		Broker:      broker,
		Target:      target,
		EndpointURL: "https://s3.example",
		Region:      "us-east-1",
	}
}

// writeTest writes a cache entry for testBroker.
func writeTest(t *testing.T, target string, expiresIn time.Duration) {
	t.Helper()
	if err := writeCachedCredentials(testEntry(testBroker, target, expiresIn)); err != nil {
		t.Fatal(err)
	}
}

func TestCacheRoundTrip(t *testing.T) {
	isolateCache(t)
	in := testEntry(testBroker, "a:members", time.Hour)
	if err := writeCachedCredentials(in); err != nil {
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
	writeTest(t, "a:members", time.Hour)
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
	writeTest(t, "a:members", time.Hour)
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
	writeTest(t, "a", credCacheGracePeriod-time.Minute)
	if loadCachedCredentials(testBroker, "a") != nil {
		t.Fatal("expected miss for credentials expiring within the grace period")
	}
}

func TestCacheMissWhenExpired(t *testing.T) {
	isolateCache(t)
	writeTest(t, "a", -time.Hour)
	if loadCachedCredentials(testBroker, "a") != nil {
		t.Fatal("expected miss for expired credentials")
	}
}

func TestCacheMissWithoutExpiration(t *testing.T) {
	isolateCache(t)
	c := testEntry(testBroker, "a", time.Hour)
	c.Expiration = ""
	if err := writeCachedCredentials(c); err != nil {
		t.Fatal(err)
	}
	if loadCachedCredentials(testBroker, "a") != nil {
		t.Fatal("expected miss without Expiration")
	}
}

func TestCacheCorruptFileRemoved(t *testing.T) {
	for name, content := range map[string]string{
		"invalid JSON":       "{not json",
		"invalid Expiration": `{"Version":1,"AccessKeyId":"AKI","Expiration":"2026-01-01 00:00:00","broker":"https://broker.example","target":"a"}`,
		"CLI 0.x entry":      `{"Version":1,"AccessKeyId":"AKI","Expiration":"2099-01-01T00:00:00Z"}`,
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
		writeTest(t, p, time.Hour)
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
		writeTest(t, p, time.Hour)
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
	t.Setenv("OIDC_ISSUER", "https://ignored.example") // no fallback to the broker's own setting
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
	writeTest(t, "old", -time.Minute)
	writeTest(t, "valid", time.Hour)
	writeTest(t, "new", time.Hour)

	for p, want := range map[string]bool{"old": false, "valid": true, "new": true} {
		path, _ := credCachePath(testBroker, p)
		_, err := os.Stat(path)
		if got := err == nil; got != want {
			t.Errorf("%s: exists=%v, want %v", p, got, want)
		}
	}
}

func TestCachedCredentialProcessOutputUnchanged(t *testing.T) {
	// The cache file holds broker/target/endpoint/region, but what login prints from it
	// is the credential_process format only.
	isolateCache(t)
	writeTest(t, "a", time.Hour)
	got := loadCachedCredentials(testBroker, "a")
	var buf bytes.Buffer
	if err := printCredentialProcess(&buf, got.credentialProcessOutput); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"broker", "target", "endpoint_url", "region"} {
		if strings.Contains(buf.String(), `"`+s+`"`) {
			t.Fatalf("%s in credential_process output:\n%s", s, buf.String())
		}
	}
}

func TestStatus(t *testing.T) {
	old := defaultBrokerURL
	defer func() { defaultBrokerURL = old }()
	defaultBrokerURL = "https://sram-s3.production.sdp.surf.nl"

	now := time.Date(2026, 11, 27, 13, 45, 0, 0, time.UTC)
	entry := func(broker, target string, exp time.Time) *cacheEntry {
		e := testEntry(broker, target, 0)
		e.Expiration = exp.Format(time.RFC3339)
		e.EndpointURL = "https://objectstore.example.org"
		return e
	}
	entries := []*cacheEntry{
		entry("https://sram-s3.production.sdp.surf.nl", "org1:project1:s3admin", now.Add(47*time.Minute)),
		entry("https://sram-s3.production.sdp.surf.nl", "org1:project1", time.Date(2026, 11, 27, 11, 5, 0, 0, time.UTC)),
		entry("https://sram-s3.playground.sdp.surf.nl", "surf:play_x", now.Add(3*time.Minute)),
	}
	var buf bytes.Buffer
	printStatus(&buf, entries, "", "/home/you/.local/bin/sram-s3", now)
	want := `org1:project1:s3admin   valid until 14:32 (47 min)                          https://sram-s3.production.sdp.surf.nl

[profile org1-project1-s3admin]
credential_process = /home/you/.local/bin/sram-s3 login --credential-process -p org1:project1:s3admin
endpoint_url = https://objectstore.example.org
region = us-east-1

org1:project1           expired 11:05                                       https://sram-s3.production.sdp.surf.nl

[profile org1-project1]
credential_process = /home/you/.local/bin/sram-s3 login --credential-process -p org1:project1
endpoint_url = https://objectstore.example.org
region = us-east-1

surf:play_x             valid until 13:48 (3 min, next use logs in again)   https://sram-s3.playground.sdp.surf.nl

[profile surf-play-x]
credential_process = /home/you/.local/bin/sram-s3 login --credential-process -b https://sram-s3.playground.sdp.surf.nl -p surf:play_x
endpoint_url = https://objectstore.example.org
region = us-east-1
`
	if buf.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", buf.String(), want)
	}

	buf.Reset()
	printStatus(&buf, entries, "org1:project1", "/x/sram-s3", now)
	if !strings.HasPrefix(buf.String(), "org1:project1   expired 11:05") || strings.Contains(buf.String(), "s3admin") {
		t.Fatalf("-p: got:\n%s", buf.String())
	}

	buf.Reset()
	printStatus(&buf, nil, "", "/x/sram-s3", now)
	if !strings.HasPrefix(buf.String(), "No cached credentials. Log in once with") {
		t.Fatalf("empty: got %q", buf.String())
	}
}

func TestCachedEntriesSorted(t *testing.T) {
	isolateCache(t)
	writeTest(t, "b", time.Hour)
	writeTest(t, "a", time.Hour)
	es, err := cachedEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 2 || es[0].Target != "a" || es[1].Target != "b" || es[0].Broker != testBroker {
		t.Fatalf("got %+v", es)
	}
}
