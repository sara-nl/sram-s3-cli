//go:build unix

package main

// Tests for scripts/sram-s3-login.sh against the same fake broker as the Go client, and
// checks that the script prints the same role list, errors and export lines as the binary.

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

const scriptPath = "../scripts/sram-s3-login.sh"

const deviceReply = `{"device_code":"DC","user_code":"ABCD","verification_uri":"https://idp.example/device",
	"verification_uri_complete":"https://idp.example/device?code=ABCD","expires_in":600,"interval":5}`

// runScript runs the bash script against the fake broker without a terminal (own session),
// with no-op open/xdg-open so no browser starts. It returns stdout, stderr and the exit code.
func runScript(t *testing.T, c *BrokerClient, args ...string) (string, string, int) {
	t.Helper()
	for _, tool := range []string{"bash", "curl", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found", tool)
		}
	}
	stubs := t.TempDir()
	for _, name := range []string{"open", "xdg-open"} {
		if err := os.WriteFile(filepath.Join(stubs, name), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("bash", append([]string{scriptPath, "-b", c.BaseURL}, args...)...)
	cmd.Env = []string{"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + t.TempDir()}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // no controlling terminal: no /dev/tty
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), code
}

// binaryRoles is the role list as the binary prints it without a terminal.
func binaryRoles(t *testing.T, reply string) string {
	t.Helper()
	var resp credentialsResponse
	if err := json.Unmarshal([]byte(reply), &resp); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	printRoles(&buf, resp.Targets)
	return "Your roles:\n" + buf.String()
}

// binaryExports is the export block as the binary prints it.
func binaryExports(t *testing.T, reply string) string {
	t.Helper()
	var resp credentialsResponse
	if err := json.Unmarshal([]byte(reply), &resp); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	printExports(&buf, &resp)
	return buf.String()
}

func TestScriptTargetGiven(t *testing.T) {
	fb, c, _ := newFakeBroker(t,
		fakeReply{body: deviceReply},
		fakeReply{body: `{"status":"pending","interval":1}`},
		fakeReply{body: chooseReply},
		fakeReply{body: okReply})
	stdout, stderr, code := runScript(t, c, "-p", "surf:demo:s3admin")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	if want := binaryExports(t, okReply); stdout != want {
		t.Fatalf("stdout:\n%s\nwant (as the binary):\n%s", stdout, want)
	}
	wantPaths := []string{"/cli/v1/device", "/cli/v1/credentials", "/cli/v1/credentials", "/cli/v1/credentials"}
	if !reflect.DeepEqual(fb.paths, wantPaths) {
		t.Fatalf("paths %v", fb.paths)
	}
	wantBodies := []map[string]string{nil, deviceProof, deviceProof, {"login_session": "LS", "target": "surf:demo:s3admin"}}
	if !reflect.DeepEqual(fb.bodies, wantBodies) {
		t.Fatalf("bodies %v", fb.bodies)
	}
	if !strings.Contains(stderr, "Open: https://idp.example/device?code=ABCD") {
		t.Fatalf("no login URL in stderr:\n%s", stderr)
	}
}

func TestScriptRolesWithoutTerminal(t *testing.T) {
	_, c, _ := newFakeBroker(t, fakeReply{body: deviceReply}, fakeReply{body: chooseReply})
	stdout, stderr, code := runScript(t, c)
	if code != 1 || stdout != "" {
		t.Fatalf("exit %d, stdout %q", code, stdout)
	}
	if !strings.Contains(stderr, binaryRoles(t, chooseReply)+"ERROR: several roles: re-run with -p <target>\n") {
		t.Fatalf("stderr:\n%s\nwant the binary's role list:\n%s", stderr, binaryRoles(t, chooseReply))
	}
}

func TestScriptTargetNotHeld(t *testing.T) {
	_, c, _ := newFakeBroker(t, fakeReply{body: deviceReply}, fakeReply{body: chooseReply})
	_, stderr, code := runScript(t, c, "-p", "surf:other")
	if code != 1 || !strings.Contains(stderr, "You have no role for target \"surf:other\".\n"+binaryRoles(t, chooseReply)) {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
}

func TestScriptTargetNotTheOnlyRole(t *testing.T) {
	_, c, _ := newFakeBroker(t, fakeReply{body: deviceReply}, fakeReply{body: okReply})
	stdout, stderr, code := runScript(t, c, "-p", "surf:other")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "your only role is surf:demo:s3admin") {
		t.Fatalf("exit %d, stdout %q, stderr:\n%s", code, stdout, stderr)
	}
}

func TestScriptErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		reply []fakeReply
		err   apiError
	}{
		{"no role", nil,
			[]fakeReply{{body: deviceReply}, {403, `{"detail":"Your SRAM entitlements give you no role on any project of this broker"}`}},
			apiError{"/cli/v1/credentials", 403, "Your SRAM entitlements give you no role on any project of this broker"}},
		{"login expired", nil,
			[]fakeReply{{body: deviceReply}, {400, `{"detail":"expired_token"}`}},
			apiError{"/cli/v1/credentials", 400, "expired_token"}},
		{"PAM off", []string{"-u", "jdoe"},
			[]fakeReply{{404, `{"detail":"PAM weblogin is not enabled on this broker; use the device login"}`}},
			apiError{"/cli/v1/pam", 404, "PAM weblogin is not enabled on this broker; use the device login"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, c, _ := newFakeBroker(t, tc.reply...)
			stdout, stderr, code := runScript(t, c, tc.args...)
			want := "ERROR: " + tc.err.Error() + "\n" // the binary's message
			if code != 1 || stdout != "" || !strings.HasSuffix(stderr, want) {
				t.Fatalf("exit %d, stdout %q, stderr:\n%s\nwant suffix %q", code, stdout, stderr, want)
			}
		})
	}
}

func TestScriptDebugRedactsSecrets(t *testing.T) {
	_, c, _ := newFakeBroker(t, fakeReply{body: deviceReply}, fakeReply{body: okReply})
	_, stderr, code := runScript(t, c, "-d")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "DEBUG: POST /cli/v1/credentials: HTTP 200") || !strings.Contains(stderr, "<redacted>") {
		t.Fatalf("stderr:\n%s", stderr)
	}
	if strings.Contains(stderr, "SEC") || strings.Contains(stderr, "TOK") {
		t.Fatalf("secret in debug output:\n%s", stderr)
	}
}

func TestScriptUsage(t *testing.T) {
	_, c, _ := newFakeBroker(t)
	_, stderr, code := runScript(t, c, "extra")
	if code != 2 || !strings.Contains(stderr, "Usage: sram-s3-login.sh") {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
}
