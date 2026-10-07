package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type BrokerClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewBrokerClient(base string) *BrokerClient {
	return &BrokerClient{
		BaseURL: strings.TrimRight(strings.TrimSpace(base), "/"),
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

type deviceAuthResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	Interval                int    `json:"interval"`
	ExpiresIn               int    `json:"expires_in"`
	Error                   string `json:"error"`
	ErrorDescription        string `json:"error_description"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

type stsCredentials struct {
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration"`
}

type stsResponse struct {
	Status        string         `json:"status"`
	Targets       []stsTarget    `json:"targets"`
	Credentials   *stsCredentials `json:"Credentials"`
	S3EndpointURL string         `json:"S3EndpointUrl"`
	AwsRegion     string         `json:"AwsRegion"`
	Detail        any            `json:"detail"`
	Error         string         `json:"error"`
	Message       string         `json:"message"`
}

type stsTarget struct {
	Target           string `json:"target"`
	Label            string `json:"label"`
	CollaborationID  string `json:"collaboration_id"`
	Role             string `json:"role"`
	IamRoleName      string `json:"iam_role_name"`
}

func (c *BrokerClient) postForm(path string, form url.Values) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func (c *BrokerClient) postJSON(path string, payload any) ([]byte, int, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+path, strings.NewReader(string(raw)))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func (c *BrokerClient) StartDeviceAuth(scope string) (*deviceAuthResponse, error) {
	form := url.Values{}
	form.Set("scope", scope)
	body, code, err := c.postForm("/oidc/device", form)
	if err != nil {
		return nil, fmt.Errorf("POST /oidc/device: %w", err)
	}
	var out deviceAuthResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("POST /oidc/device: invalid JSON (HTTP %d): %s", code, truncate(string(body), 240))
	}
	if code < 200 || code >= 300 {
		msg := out.ErrorDescription
		if msg == "" {
			msg = out.Error
		}
		if msg == "" {
			msg = truncate(string(body), 240)
		}
		return nil, fmt.Errorf("POST /oidc/device: HTTP %d: %s", code, msg)
	}
	if out.DeviceCode == "" {
		return nil, fmt.Errorf("POST /oidc/device: missing device_code")
	}
	if out.Interval <= 0 {
		out.Interval = 5
	}
	if out.ExpiresIn <= 0 {
		out.ExpiresIn = 600
	}
	return &out, nil
}

func (c *BrokerClient) PollDeviceToken(deviceCode string, interval time.Duration, deadline time.Time, progress io.Writer) (string, error) {
	poll := interval
	if poll < time.Second {
		poll = 5 * time.Second
	}
	n := 0
	for time.Now().Before(deadline) {
		n++
		if progress != nil && (n == 1 || n%6 == 0) {
			left := int(time.Until(deadline).Seconds())
			fmt.Fprintf(progress, "Waiting for browser sign-in… (%ds left, poll #%d)\n", left, n)
		}
		form := url.Values{}
		form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
		form.Set("device_code", deviceCode)
		body, code, err := c.postForm("/oidc/token", form)
		if err != nil {
			return "", fmt.Errorf("POST /oidc/token: %w", err)
		}
		var tok tokenResponse
		_ = json.Unmarshal(body, &tok)
		if code >= 200 && code < 300 {
			access := strings.TrimSpace(tok.AccessToken)
			if access == "" {
				access = strings.TrimSpace(tok.IDToken)
			}
			if access == "" {
				return "", fmt.Errorf("POST /oidc/token: missing access_token")
			}
			return access, nil
		}
		switch tok.Error {
		case "authorization_pending":
			time.Sleep(poll)
		case "slow_down":
			poll += 2 * time.Second
			time.Sleep(poll)
		case "access_denied", "expired_token":
			return "", fmt.Errorf("device flow ended: %s", tok.Error)
		default:
			msg := tok.ErrorDesc
			if msg == "" {
				msg = tok.Error
			}
			if msg == "" {
				msg = truncate(string(body), 240)
			}
			return "", fmt.Errorf("POST /oidc/token: HTTP %d: %s", code, msg)
		}
	}
	return "", fmt.Errorf("device authorization timed out")
}

func (c *BrokerClient) DeviceSTS(accessToken, target string) (*stsResponse, error) {
	payload := map[string]string{"access_token": accessToken}
	if strings.TrimSpace(target) != "" {
		payload["target"] = strings.TrimSpace(target)
	}
	body, code, err := c.postJSON("/cli/device-sts", payload)
	if err != nil {
		return nil, fmt.Errorf("POST /cli/device-sts: %w", err)
	}
	var out stsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("POST /cli/device-sts: invalid JSON (HTTP %d): %s", code, truncate(string(body), 240))
	}
	if code < 200 || code >= 300 {
		msg := formatAPIError(body, &out)
		return nil, fmt.Errorf("POST /cli/device-sts: HTTP %d: %s", code, msg)
	}
	return &out, nil
}

func formatAPIError(body []byte, out *stsResponse) string {
	if out != nil {
		if s, ok := out.Detail.(string); ok && s != "" {
			return s
		}
		if out.Message != "" {
			return out.Message
		}
		if out.Error != "" {
			return out.Error
		}
	}
	return truncate(string(body), 300)
}

func pickTarget(targets []stsTarget, in io.Reader, out io.Writer) (string, error) {
	if len(targets) == 0 {
		return "", fmt.Errorf("broker returned no targets to choose")
	}
	if len(targets) == 1 {
		return targets[0].Target, nil
	}
	fmt.Fprintln(out, "\nSelect IAM role (enter number, then Enter):")
	for i, t := range targets {
		label := t.Label
		if label == "" {
			label = t.Target
		}
		fmt.Fprintf(out, "  %d) %s  (%s)\n", i+1, label, t.Target)
	}
	reader := bufio.NewReader(in)
	for attempt := 0; attempt < 10; attempt++ {
		fmt.Fprint(out, "Choice: ")
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		line = strings.TrimSpace(line)
		n, err := strconv.Atoi(line)
		if err != nil || n < 1 || n > len(targets) {
			fmt.Fprintf(out, "Enter a number from 1 to %d.\n", len(targets))
			continue
		}
		return targets[n-1].Target, nil
	}
	return "", fmt.Errorf("role selection cancelled or invalid")
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func printExportEnv(w io.Writer, sts *stsResponse, regionFallback string) error {
	if sts == nil || sts.Credentials == nil {
		return fmt.Errorf("missing STS credentials")
	}
	region := strings.TrimSpace(sts.AwsRegion)
	if region == "" {
		region = regionFallback
	}
	if region == "" {
		region = "default"
	}
	c := sts.Credentials
	fmt.Fprintf(w, "export AWS_ENDPOINT_URL=%s\n", shellQuote(sts.S3EndpointURL))
	fmt.Fprintf(w, "export AWS_REGION=%s\n", shellQuote(region))
	fmt.Fprintf(w, "export AWS_ACCESS_KEY_ID=%s\n", shellQuote(c.AccessKeyID))
	fmt.Fprintf(w, "export AWS_SECRET_ACCESS_KEY=%s\n", shellQuote(c.SecretAccessKey))
	fmt.Fprintf(w, "export AWS_SESSION_TOKEN=%s\n", shellQuote(c.SessionToken))
	if c.Expiration != "" {
		fmt.Fprintf(w, "export AWS_SESSION_EXPIRATION=%s\n", shellQuote(c.Expiration))
	}
	return nil
}

func openTTY() (*os.File, error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	return f, nil
}
