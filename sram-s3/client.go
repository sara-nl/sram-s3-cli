package main

// The broker's CLI API (/cli/v1): start a login, then one Credentials loop for both
// logins. The CLI never holds a token: it hands the broker only IdP codes (device code,
// or PAM session + PIN), and the broker redeems them at the IdP.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type BrokerClient struct {
	BaseURL    string
	HTTPClient *http.Client
	// Debug receives the raw /cli/v1 responses (secrets redacted) when non-nil.
	Debug io.Writer
	// Progress receives the "waiting for sign-in" lines of a device login when non-nil.
	Progress io.Writer

	sleep func(time.Duration) // time.Sleep; replaced in tests
	now   func() time.Time    // time.Now; replaced in tests
}

func NewBrokerClient(base string) *BrokerClient {
	return &BrokerClient{
		BaseURL:    strings.TrimRight(strings.TrimSpace(base), "/"),
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
		sleep:      time.Sleep,
		now:        time.Now,
	}
}

// deviceLogin is the answer of POST /cli/v1/device.
type deviceLogin struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// pamLogin is the answer of POST /cli/v1/pam.
type pamLogin struct {
	PAMSession string `json:"pam_session"`
	Challenge  string `json:"challenge"`
}

// awsCredentials uses the AWS credential_process field names, as the broker does.
type awsCredentials struct {
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration"`
}

// credentialsResponse is the answer of POST /cli/v1/credentials: status ok, pending or choose.
type credentialsResponse struct {
	Status       string          `json:"status"`
	Target       string          `json:"target"`
	EndpointURL  string          `json:"endpoint_url"`
	Region       string          `json:"region"`
	Credentials  *awsCredentials `json:"credentials"`
	Interval     int             `json:"interval"`
	LoginSession string          `json:"login_session"`
	Targets      []target        `json:"targets"`
}

// target is one role the user may choose; Target is what -p takes.
type target struct {
	Target      string `json:"target"`
	Project     string `json:"project"`
	Kind        string `json:"kind"` // members | admin | group
	Group       string `json:"group"`
	BrokerID    string `json:"broker_id"`
	IAMRoleName string `json:"iam_role_name"`
}

// apiError is an HTTP error answer of the broker: {"detail": "…"}.
type apiError struct {
	Path   string
	Status int
	Detail string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s (HTTP %d from %s)", e.Detail, e.Status, e.Path)
}

// StartDeviceLogin starts an OIDC device login.
func (c *BrokerClient) StartDeviceLogin() (*deviceLogin, error) {
	var out deviceLogin
	if err := c.post("/cli/v1/device", nil, &out); err != nil {
		return nil, err
	}
	if out.DeviceCode == "" {
		return nil, fmt.Errorf("POST /cli/v1/device: answer without device_code")
	}
	return &out, nil
}

// StartPAMLogin starts a SRAM PAM weblogin for an SRAM username.
func (c *BrokerClient) StartPAMLogin(userID string) (*pamLogin, error) {
	var out pamLogin
	if err := c.post("/cli/v1/pam", map[string]string{"user_id": userID}, &out); err != nil {
		return nil, err
	}
	if out.PAMSession == "" {
		return nil, fmt.Errorf("POST /cli/v1/pam: answer without pam_session")
	}
	return &out, nil
}

// chooseFunc picks one of the user's roles; wanted is the -p target ("" if none).
type chooseFunc func(targets []target, wanted string) (string, error)

// Credentials finishes a login: it posts the proof ({device_code} or {pam_session, pin})
// to /cli/v1/credentials until the broker answers ok.
//
//   - pending: wait interval seconds and ask again, until deadline (device login);
//   - choose: choose picks a role, then the login_session is redeemed for it;
//   - ok: the credentials.
//
// The proof is sent without a target: a target the user does not hold gets a bare 403,
// while the choose answer lets us show the user's roles instead. A wanted target is
// matched against that list by choose; when the user holds a single role (ok at once),
// it must be that role.
func (c *BrokerClient) Credentials(proof map[string]string, wanted string, deadline time.Time, choose chooseFunc) (*credentialsResponse, error) {
	body := proof
	chose := false
	for polls := 1; ; polls++ {
		var resp credentialsResponse
		if err := c.post("/cli/v1/credentials", body, &resp); err != nil {
			return nil, err
		}
		switch resp.Status {
		case "ok":
			if resp.Credentials == nil || resp.Credentials.AccessKeyID == "" {
				return nil, fmt.Errorf("POST /cli/v1/credentials: status ok without credentials")
			}
			if wanted != "" && !chose && resp.Target != wanted {
				return nil, fmt.Errorf("you have no role for target %q; your only role is %s", wanted, resp.Target)
			}
			return &resp, nil
		case "pending":
			interval := time.Duration(resp.Interval) * time.Second
			if interval <= 0 {
				interval = 5 * time.Second
			}
			if deadline.IsZero() || c.now().Add(interval).After(deadline) {
				return nil, errors.New("the login expired before sign-in was completed; run login again")
			}
			if c.Progress != nil && (polls == 1 || polls%6 == 0) {
				fmt.Fprintf(c.Progress, "Waiting for sign-in in the browser… (%s left)\n", shortDuration(deadline.Sub(c.now())))
			}
			c.sleep(interval)
		case "choose":
			if chose || resp.LoginSession == "" || len(resp.Targets) == 0 {
				return nil, fmt.Errorf("POST /cli/v1/credentials: unexpected choose answer")
			}
			t, err := choose(resp.Targets, wanted)
			if err != nil {
				return nil, err
			}
			chose = true
			body = map[string]string{"login_session": resp.LoginSession, "target": t}
		default:
			return nil, fmt.Errorf("POST /cli/v1/credentials: unknown status %q", resp.Status)
		}
	}
}

// post sends a JSON POST (no body when payload is nil) and decodes a 2xx answer into out.
func (c *BrokerClient) post(path string, payload any, out any) error {
	var body io.Reader = http.NoBody
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "sram-s3/"+version)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	if c.Debug != nil {
		fmt.Fprintf(c.Debug, "DEBUG: POST %s: HTTP %d: %s\n", path, resp.StatusCode, redactSecrets(data))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &apiError{Path: path, Status: resp.StatusCode, Detail: errorDetail(data)}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("POST %s: invalid JSON (HTTP %d): %s", path, resp.StatusCode, truncate(string(data), 240))
	}
	return nil
}

// errorDetail returns the broker's "detail" message, or the body itself.
func errorDetail(body []byte) string {
	var e struct {
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(body, &e) == nil && len(e.Detail) > 0 {
		var s string
		if json.Unmarshal(e.Detail, &s) == nil && s != "" {
			return s
		}
		return truncate(string(e.Detail), 300) // e.g. a 422 validation error list
	}
	if s := truncate(string(body), 300); s != "" {
		return s
	}
	return "no error message"
}

// redactSecrets hides the secret key and session token in a debug copy of an answer.
func redactSecrets(body []byte) string {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return truncate(string(body), 2000)
	}
	creds, ok := m["credentials"].(map[string]any)
	if !ok {
		return string(body)
	}
	for _, k := range []string{"SecretAccessKey", "SessionToken"} {
		if _, ok := creds[k]; ok {
			creds[k] = "<redacted>"
		}
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(m)
	return strings.TrimSpace(out.String())
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
