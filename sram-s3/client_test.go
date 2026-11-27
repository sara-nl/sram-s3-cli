package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeBroker answers each POST with the next scripted reply and records the request bodies.
type fakeBroker struct {
	t       *testing.T
	replies []fakeReply
	paths   []string
	bodies  []map[string]string
}

type fakeReply struct {
	status int
	body   string
}

func newFakeBroker(t *testing.T, replies ...fakeReply) (*fakeBroker, *BrokerClient, *[]time.Duration) {
	t.Helper()
	fb := &fakeBroker{t: t, replies: replies}
	srv := httptest.NewServer(http.HandlerFunc(fb.serve))
	t.Cleanup(srv.Close)
	c := NewBrokerClient(srv.URL)
	var slept []time.Duration
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	c.sleep = func(d time.Duration) { slept = append(slept, d); now = now.Add(d) }
	return fb, c, &slept
}

func (fb *fakeBroker) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fb.t.Errorf("method %s, want POST", r.Method)
	}
	raw, _ := io.ReadAll(r.Body)
	var body map[string]string
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			fb.t.Errorf("request body not a JSON object of strings: %s", raw)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			fb.t.Errorf("Content-Type %q", ct)
		}
	}
	fb.paths = append(fb.paths, r.URL.Path)
	fb.bodies = append(fb.bodies, body)
	if len(fb.replies) == 0 {
		fb.t.Errorf("unexpected request %s %s", r.URL.Path, raw)
		w.WriteHeader(500)
		return
	}
	rep := fb.replies[0]
	fb.replies = fb.replies[1:]
	w.Header().Set("Content-Type", "application/json")
	if rep.status == 0 {
		rep.status = 200
	}
	w.WriteHeader(rep.status)
	_, _ = io.WriteString(w, rep.body)
}

const (
	okReply = `{"status":"ok","target":"surf:demo:s3admin","endpoint_url":"https://s3.example","region":"us-east-1",
		"credentials":{"AccessKeyId":"AKI","SecretAccessKey":"SEC","SessionToken":"TOK","Expiration":"2026-11-27T13:00:00Z"}}`
	chooseReply = `{"status":"choose","login_session":"LS","targets":[
		{"target":"surf:demo","project":"Demo","kind":"members","group":"","broker_id":"b1","iam_role_name":"surf-demo"},
		{"target":"surf:demo:s3admin","project":"Demo","kind":"admin","group":"s3admin","broker_id":"b1","iam_role_name":"surf-demo-s3admin"}]}`
)

func noChoice(t *testing.T) chooseFunc {
	return func([]target, string) (string, error) {
		t.Fatal("choose called")
		return "", nil
	}
}

var deviceProof = map[string]string{"device_code": "DC"}

func farDeadline() time.Time { return time.Date(2026, 11, 27, 12, 10, 0, 0, time.UTC) }

func TestStartDeviceLogin(t *testing.T) {
	fb, c, _ := newFakeBroker(t, fakeReply{body: `{"device_code":"DC","user_code":"ABCD","verification_uri":"https://idp/device",
		"verification_uri_complete":"https://idp/device?code=ABCD","expires_in":600,"interval":5}`})
	dev, err := c.StartDeviceLogin()
	if err != nil {
		t.Fatal(err)
	}
	want := deviceLogin{"DC", "ABCD", "https://idp/device", "https://idp/device?code=ABCD", 600, 5}
	if *dev != want {
		t.Fatalf("got %+v", *dev)
	}
	if fb.paths[0] != "/cli/v1/device" || fb.bodies[0] != nil {
		t.Fatalf("request %s %v", fb.paths[0], fb.bodies[0])
	}
}

func TestDevicePendingThenOK(t *testing.T) {
	fb, c, slept := newFakeBroker(t,
		fakeReply{body: `{"status":"pending","interval":5}`},
		fakeReply{body: `{"status":"pending","interval":5}`},
		fakeReply{body: okReply})
	var progress bytes.Buffer
	c.Progress = &progress
	resp, err := c.Credentials(deviceProof, "", farDeadline(), noChoice(t))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Target != "surf:demo:s3admin" || resp.EndpointURL != "https://s3.example" || resp.Region != "us-east-1" ||
		resp.Credentials.SecretAccessKey != "SEC" || resp.Credentials.Expiration != "2026-11-27T13:00:00Z" {
		t.Fatalf("got %+v", resp)
	}
	if !reflect.DeepEqual(*slept, []time.Duration{5 * time.Second, 5 * time.Second}) {
		t.Fatalf("slept %v", *slept)
	}
	for i, b := range fb.bodies {
		if fb.paths[i] != "/cli/v1/credentials" || !reflect.DeepEqual(b, deviceProof) {
			t.Fatalf("request %d: %s %v", i, fb.paths[i], b)
		}
	}
	if !strings.Contains(progress.String(), "Waiting for sign-in") {
		t.Fatalf("no progress line: %q", progress.String())
	}
}

func TestDeviceSlowDown(t *testing.T) {
	_, c, slept := newFakeBroker(t,
		fakeReply{body: `{"status":"pending","interval":10}`},
		fakeReply{body: okReply})
	if _, err := c.Credentials(deviceProof, "", farDeadline(), noChoice(t)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*slept, []time.Duration{10 * time.Second}) {
		t.Fatalf("slept %v", *slept)
	}
}

func TestDeviceDeadline(t *testing.T) {
	replies := []fakeReply{}
	for i := 0; i < 10; i++ {
		replies = append(replies, fakeReply{body: `{"status":"pending","interval":5}`})
	}
	fb, c, _ := newFakeBroker(t, replies...)
	deadline := c.now().Add(12 * time.Second)
	_, err := c.Credentials(deviceProof, "", deadline, noChoice(t))
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("err = %v", err)
	}
	if len(fb.bodies) != 3 { // t=0, 5, 10; the next wait would pass the deadline
		t.Fatalf("%d polls, want 3", len(fb.bodies))
	}
}

func TestChooseThenOK(t *testing.T) {
	fb, c, _ := newFakeBroker(t, fakeReply{body: chooseReply}, fakeReply{body: okReply})
	var got []target
	resp, err := c.Credentials(deviceProof, "", farDeadline(), func(ts []target, wanted string) (string, error) {
		got = ts
		return ts[1].Target, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Target != "surf:demo:s3admin" {
		t.Fatalf("target %q", resp.Target)
	}
	want := target{"surf:demo:s3admin", "Demo", "admin", "s3admin", "b1", "surf-demo-s3admin"}
	if len(got) != 2 || got[1] != want {
		t.Fatalf("targets %+v", got)
	}
	if !reflect.DeepEqual(fb.bodies[1], map[string]string{"login_session": "LS", "target": "surf:demo:s3admin"}) {
		t.Fatalf("second request %v", fb.bodies[1])
	}
}

func TestTargetGivenUpFront(t *testing.T) {
	fb, c, _ := newFakeBroker(t, fakeReply{body: chooseReply}, fakeReply{body: okReply})
	resp, err := c.Credentials(deviceProof, "surf:demo:s3admin", farDeadline(), func(ts []target, wanted string) (string, error) {
		return chooseTarget(ts, wanted, nil, io.Discard)
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Target != "surf:demo:s3admin" {
		t.Fatalf("target %q", resp.Target)
	}
	if !reflect.DeepEqual(fb.bodies[0], deviceProof) {
		t.Fatalf("proof sent with %v, want the proof only", fb.bodies[0])
	}
	if fb.bodies[1]["target"] != "surf:demo:s3admin" {
		t.Fatalf("second request %v", fb.bodies[1])
	}
}

func TestTargetNotHeldShowsRoles(t *testing.T) {
	fb, c, _ := newFakeBroker(t, fakeReply{body: chooseReply})
	var msg bytes.Buffer
	_, err := c.Credentials(deviceProof, "surf:other", farDeadline(), func(ts []target, wanted string) (string, error) {
		return chooseTarget(ts, wanted, nil, &msg)
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, s := range []string{`no role for target "surf:other"`, "surf:demo:s3admin", "project admin (s3admin)"} {
		if !strings.Contains(msg.String(), s) {
			t.Fatalf("missing %q in:\n%s", s, msg.String())
		}
	}
	if len(fb.bodies) != 1 {
		t.Fatalf("%d requests, want 1", len(fb.bodies))
	}
}

func TestTargetNotTheOnlyRole(t *testing.T) {
	_, c, _ := newFakeBroker(t, fakeReply{body: okReply})
	_, err := c.Credentials(deviceProof, "surf:other", farDeadline(), noChoice(t))
	if err == nil || !strings.Contains(err.Error(), "your only role is surf:demo:s3admin") {
		t.Fatalf("err = %v", err)
	}
}

func TestPAMLogin(t *testing.T) {
	fb, c, _ := newFakeBroker(t,
		fakeReply{body: `{"pam_session":"PS","challenge":"Visit https://sram.example/pam/xyz to log in"}`},
		fakeReply{body: okReply})
	pam, err := c.StartPAMLogin("jdoe")
	if err != nil {
		t.Fatal(err)
	}
	if pam.PAMSession != "PS" || !strings.Contains(pam.Challenge, "https://sram.example/pam/xyz") {
		t.Fatalf("got %+v", pam)
	}
	proof := map[string]string{"pam_session": pam.PAMSession, "pin": "1234"}
	if _, err := c.Credentials(proof, "", time.Time{}, noChoice(t)); err != nil {
		t.Fatal(err)
	}
	if fb.paths[0] != "/cli/v1/pam" || !reflect.DeepEqual(fb.bodies[0], map[string]string{"user_id": "jdoe"}) {
		t.Fatalf("start request %s %v", fb.paths[0], fb.bodies[0])
	}
	if !reflect.DeepEqual(fb.bodies[1], proof) {
		t.Fatalf("credentials request %v", fb.bodies[1])
	}
}

func TestPAMNotEnabled(t *testing.T) {
	_, c, _ := newFakeBroker(t, fakeReply{404, `{"detail":"PAM weblogin is not enabled on this broker; use the device login"}`})
	_, err := c.StartPAMLogin("jdoe")
	var ae *apiError
	if !errors.As(err, &ae) || ae.Status != 404 || !strings.Contains(ae.Detail, "not enabled") {
		t.Fatalf("err = %v", err)
	}
}

func TestCredentialsErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		detail string
	}{
		{400, "login_session expired or already used; log in again"},
		{401, "PIN not accepted"},
		{403, "Your SRAM entitlements give you no role on any project of this broker"},
	} {
		_, c, _ := newFakeBroker(t, fakeReply{tc.status, `{"detail":"` + tc.detail + `"}`})
		_, err := c.Credentials(deviceProof, "", farDeadline(), noChoice(t))
		var ae *apiError
		if !errors.As(err, &ae) || ae.Status != tc.status || ae.Detail != tc.detail {
			t.Fatalf("HTTP %d: err = %v", tc.status, err)
		}
		if !strings.HasPrefix(err.Error(), tc.detail) {
			t.Fatalf("message %q", err.Error())
		}
	}
}

func TestErrorWithoutJSON(t *testing.T) {
	_, c, _ := newFakeBroker(t, fakeReply{502, `Bad Gateway`})
	_, err := c.StartDeviceLogin()
	if err == nil || !strings.Contains(err.Error(), "Bad Gateway (HTTP 502") {
		t.Fatalf("err = %v", err)
	}
}

func TestDebugRedactsSecrets(t *testing.T) {
	_, c, _ := newFakeBroker(t, fakeReply{body: okReply})
	var dbg bytes.Buffer
	c.Debug = &dbg
	if _, err := c.Credentials(deviceProof, "", farDeadline(), noChoice(t)); err != nil {
		t.Fatal(err)
	}
	s := dbg.String()
	if !strings.Contains(s, "/cli/v1/credentials") || !strings.Contains(s, "AKI") {
		t.Fatalf("debug output %q", s)
	}
	if strings.Contains(s, "SEC") || strings.Contains(s, "TOK") || !strings.Contains(s, `"<redacted>"`) {
		t.Fatalf("secret in debug output %q", s)
	}
}
