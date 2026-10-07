// Command sram-s3 is a small end-user CLI for the SRAM S3 broker device-code flow.
//
//	eval "$(sram-s3 login)"
//	aws --endpoint-url "$AWS_ENDPOINT_URL" s3 ls
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Overridden at link time: -ldflags "-X main.version=… -X main.defaultBrokerURL=…"
var version = "0.1.0"
var defaultBrokerURL = ""

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
  -b, --broker URL     Broker base URL (or env SRAM_BROKER_URL)
  -p, --project TARGET Optional <marker>[:members|admin]
  --json               Print STS JSON instead of shell exports
  --eval               Force shell export lines on stdout (default when not --json)
  -d, --debug          Debug to stderr

Examples:
  eval "$(sram-s3 login)"
  eval "$(sram-s3 login -p surf_demo:project2:members)"
  aws --endpoint-url "$AWS_ENDPOINT_URL" s3 ls
`)
}

func runLogin(args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		broker    string
		project   string
		asJSON    bool
		forceEval bool
		debug     bool
	)
	fs.StringVar(&broker, "b", "", "broker URL")
	fs.StringVar(&broker, "broker", "", "broker URL")
	fs.StringVar(&project, "p", "", "project target")
	fs.StringVar(&project, "project", "", "project target")
	fs.BoolVar(&asJSON, "json", false, "print STS JSON")
	fs.BoolVar(&forceEval, "eval", false, "print shell exports")
	fs.BoolVar(&debug, "d", false, "debug")
	fs.BoolVar(&debug, "debug", false, "debug")
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

	entClaim := firstNonEmpty(os.Getenv("OIDC_ENTITLEMENTS_CLAIM"), "eduperson_entitlement")
	scope := "openid profile email groups " + entClaim
	regionFallback := firstNonEmpty(os.Getenv("AWS_REGION"), "default")

	if debug {
		fmt.Fprintf(os.Stderr, "DEBUG: broker=%s project=%q scope=%q\n", broker, project, scope)
	}

	client := NewBrokerClient(broker)
	dev, err := client.StartDeviceAuth(scope)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		fmt.Fprintln(os.Stderr, "Hint: upstream IdP must support RFC 8628 device authorization.")
		return 1
	}

	var openURL string
	if uri := strings.TrimSpace(dev.VerificationURIComplete); uri != "" {
		fmt.Fprintf(os.Stderr, "Open: %s\n", uri)
		openURL = uri
	} else if uri := strings.TrimSpace(dev.VerificationURI); uri != "" {
		fmt.Fprintf(os.Stderr, "Open: %s\n", uri)
		openURL = uri
		if code := strings.TrimSpace(dev.UserCode); code != "" {
			fmt.Fprintf(os.Stderr, "Enter code: %s\n", code)
		}
	} else if code := strings.TrimSpace(dev.UserCode); code != "" {
		fmt.Fprintf(os.Stderr, "Device code: %s\n", code)
	}
	fmt.Fprintf(os.Stderr, "Complete sign-in in the browser, then this tool will poll for tokens (expires in %ds).\n", dev.ExpiresIn)

	if openURL != "" {
		openBrowser(openURL)
	}

	deadline := time.Now().Add(time.Duration(dev.ExpiresIn) * time.Second)
	accessToken, err := client.PollDeviceToken(dev.DeviceCode, time.Duration(dev.Interval)*time.Second, deadline, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stderr, "Signed in. Requesting STS credentials…")

	sts, err := client.DeviceSTS(accessToken, project)
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
		sts, err = client.DeviceSTS(accessToken, project)
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

	if asJSON && !forceEval {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(sts); err != nil {
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
