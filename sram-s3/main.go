// Command sram-s3 is a small end-user CLI for the SRAM S3 broker device-code flow.
//
//	eval "$(sram-s3 login)"
//	aws --endpoint-url "$AWS_ENDPOINT_URL" s3 ls
package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Overridden at link time: -ldflags "-X main.version=… -X main.defaultBrokerURL=…"
var version = "0.1.0"
var defaultBrokerURL = ""

// credentialProcessOutput is the AWS credential_process JSON format (Version 1).
// See https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-sourcing-external.html
type credentialProcessOutput struct {
	Version         int    `json:"Version"`
	AccessKeyId     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "login":
		os.Exit(runLogin(os.Args[2:]))
	case "version", "-version", "--version":
		fmt.Printf("sram-s3 %s\n", version)
	case "help", "-h", "--help":
		printUsage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		printUsage(os.Stderr)
		os.Exit(2)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `sram-s3 — SRAM S3 broker device-code CLI

Usage:
  sram-s3 login [options]
  sram-s3 version
  sram-s3 help

login options:
  -b, --broker URL       Broker base URL (or env SRAM_BROKER_URL)
  -p, --project TARGET   Optional <marker>[:members|admin]
  --credential-process   Print AWS credential_process JSON to stdout.
  -d, --debug            Debug to stderr, including raw broker responses at each step.

By default, shell export lines are printed to stdout (use with eval).

Examples:
  eval "$(sram-s3 login)"
  eval "$(sram-s3 login -p surf_demo:project2:members)"
  aws --endpoint-url "$AWS_ENDPOINT_URL" s3 ls
`)
}

const credCacheGracePeriod = 5 * time.Minute

func credCachePath(broker, project string) (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	key := fmt.Sprintf("%s\x00%s", broker, project)
	hash := sha256.Sum256([]byte(key))
	dir := filepath.Join(cacheDir, "sram-s3")
	return filepath.Join(dir, fmt.Sprintf("%x.json", hash)), nil
}

func loadCachedCredentials(broker, project string) *credentialProcessOutput {
	path, err := credCachePath(broker, project)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out credentialProcessOutput
	if err := json.Unmarshal(data, &out); err != nil {
		_ = os.Remove(path)
		return nil
	}
	if out.Expiration == "" {
		return nil
	}
	exp, err := time.Parse(time.RFC3339, out.Expiration)
	if err != nil {
		_ = os.Remove(path)
		return nil
	}
	if time.Until(exp) < credCacheGracePeriod {
		return nil
	}
	return &out
}

func writeCachedCredentials(broker, project string, out *credentialProcessOutput) error {
	path, err := credCachePath(broker, project)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func runLogin(args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		broker            string
		project           string
		debug             bool
		credentialProcess bool
	)
	fs.StringVar(&broker, "b", "", "broker URL")
	fs.StringVar(&broker, "broker", "", "broker URL")
	fs.StringVar(&project, "p", "", "project target")
	fs.StringVar(&project, "project", "", "project target")
	fs.BoolVar(&debug, "d", false, "debug")
	fs.BoolVar(&debug, "debug", false, "debug")
	fs.BoolVar(&credentialProcess, "credential-process", false, "print AWS credential_process JSON")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	if broker == "" {
		broker = firstNonEmpty(os.Getenv("SRAM_BROKER_URL"), os.Getenv("OIDC_ISSUER"), defaultBrokerURL)
	}
	if project == "" {
		project = firstNonEmpty(os.Getenv("SRAM_PROJECT"), os.Getenv("SRAM_CLI_TARGET"))
	}
	broker = strings.TrimRight(strings.TrimSpace(broker), "/")
	if broker == "" {
		fmt.Fprintln(os.Stderr, "ERROR: missing broker URL (−b / --broker or SRAM_BROKER_URL)")
		return 1
	}

	regionFallback := firstNonEmpty(os.Getenv("AWS_REGION"), os.Getenv("AWS_DEFAULT_REGION"), "default")

	if debug {
		fmt.Fprintf(os.Stderr, "DEBUG: broker=%s project=%q\n", broker, project)
	}

	if credentialProcess {
		if cached := loadCachedCredentials(broker, project); cached != nil {
			if debug {
				fmt.Fprintf(os.Stderr, "DEBUG: using cached credentials (expires %s)\n", cached.Expiration)
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(cached); err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
				return 1
			}
			return 0
		}
	}

	client := NewBrokerClient(broker)
	dev, err := client.StartDeviceAuth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		fmt.Fprintln(os.Stderr, "Hint: upstream IdP must support RFC 8628 device authorization.")
		return 1
	}

	if uri := strings.TrimSpace(dev.VerificationURIComplete); uri != "" {
		fmt.Fprintf(os.Stderr, "Open: %s\n", uri)
		openBrowser(uri)
	} else if uri := strings.TrimSpace(dev.VerificationURI); uri != "" {
		fmt.Fprintf(os.Stderr, "Open: %s\n", uri)
		openBrowser(uri)
		if code := strings.TrimSpace(dev.UserCode); code != "" {
			fmt.Fprintf(os.Stderr, "Enter code: %s\n", code)
		}
	} else if code := strings.TrimSpace(dev.UserCode); code != "" {
		fmt.Fprintf(os.Stderr, "Device code: %s\n", code)
	}
	fmt.Fprintf(os.Stderr, "Complete sign-in in the browser, then this tool will poll for tokens (expires in %ds).\n", dev.ExpiresIn)

	deadline := time.Now().Add(time.Duration(dev.ExpiresIn) * time.Second)
	accessToken, err := client.PollDeviceToken(dev.DeviceCode, time.Duration(dev.Interval)*time.Second, deadline, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stderr, "Signed in. Requesting STS credentials…")

	sts, err := client.DeviceSTS(accessToken, project, debug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}

	if sts.Status == "choose" {
		in := io.Reader(os.Stdin)
		out := io.Writer(os.Stderr)
		if tty, err := openTTY(); err == nil {
			defer tty.Close()
			in, out = tty, tty
		} else if project == "" {
			fmt.Fprintln(os.Stderr, "ERROR: multiple roles matched; re-run with -p <target>:")
			for _, t := range sts.Targets {
				label := t.Label
				if label == "" {
					label = t.Target
				}
				fmt.Fprintf(os.Stderr, "  %s  %s\n", t.Target, label)
			}
			return 1
		}
		chosen, err := pickTarget(sts.Targets, in, out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 1
		}
		project = chosen
		sts, err = client.DeviceSTS(accessToken, project, debug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 1
		}
	}

	if sts.Credentials == nil || sts.Credentials.AccessKeyID == "" {
		raw, _ := json.Marshal(sts)
		fmt.Fprintf(os.Stderr, "ERROR: broker did not return STS credentials: %s\n", truncate(string(raw), 300))
		return 1
	}

	if credentialProcess {
		out := credentialProcessOutput{
			Version:         1,
			AccessKeyId:     sts.Credentials.AccessKeyID,
			SecretAccessKey: sts.Credentials.SecretAccessKey,
			SessionToken:    sts.Credentials.SessionToken,
			Expiration:      sts.Credentials.Expiration,
		}
		_ = writeCachedCredentials(broker, project, &out)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 1
		}
		return 0
	}

	fmt.Fprintln(os.Stderr, "Session ready (device code flow).")
	if err := printExportEnv(os.Stdout, sts, regionFallback); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	return 0
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return
	}
	_ = cmd.Start()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
