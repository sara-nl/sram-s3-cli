// Command sram-s3 logs in with SRAM through the SRAM-S3 broker and prints temporary S3
// credentials: shell export lines, or AWS credential_process JSON.
//
//	eval "$(sram-s3 login)"
//	aws s3 ls
package main

import (
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
var version = "dev"
var defaultBrokerURL = ""

func main() {
	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "login":
		os.Exit(runLogin(os.Args[2:]))
	case "logout":
		os.Exit(runLogout(os.Args[2:]))
	case "status":
		os.Exit(runStatus(os.Args[2:]))
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
	fmt.Fprintf(w, `sram-s3 — log in with SRAM and get temporary S3 credentials

Usage:
  sram-s3 login [options]
  sram-s3 status [-p TARGET]
  sram-s3 logout [-b URL] [-p TARGET]
  sram-s3 version
  sram-s3 help

login options:
  -b, --broker URL       Broker base URL (or env SRAM_BROKER_URL)
  -p, --project TARGET   The role's target: its SRAM entitlement (e.g.
                         org1:project1:s3admin), or <id>:<role> as listed
                         (or env SRAM_PROJECT). Without it, you choose a role
                         when you have several.
  --pam USERNAME         Log in with SRAM PAM weblogin (for your SRAM username)
                         instead of a device code in the browser.
  --credential-process   Print AWS credential_process JSON to stdout.
                         Credentials are cached on disk until shortly before
                         they expire.
  -d, --debug            Debug to stderr, including the raw broker responses
                         (secrets redacted).

By default, shell export lines are printed to stdout (use with eval).

status shows the cached --credential-process credentials per target, with a
ready-to-paste ~/.aws/config profile.

logout removes cached --credential-process credentials: all of them, or only
those for -p TARGET. Issued credentials stay valid until they expire.

Examples:
  eval "$(sram-s3 login)"
  eval "$(sram-s3 login -p org1:project1:s3admin)"
  eval "$(sram-s3 login --pam jdoe)"
  aws s3 ls
  sram-s3 status
  sram-s3 logout
`)
}

func runLogin(args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		broker            string
		project           string
		pamUser           string
		debug             bool
		credentialProcess bool
	)
	fs.StringVar(&broker, "b", "", "broker URL")
	fs.StringVar(&broker, "broker", "", "broker URL")
	fs.StringVar(&project, "p", "", "target")
	fs.StringVar(&project, "project", "", "target")
	fs.StringVar(&pamUser, "pam", "", "SRAM username for PAM weblogin")
	fs.BoolVar(&debug, "d", false, "debug")
	fs.BoolVar(&debug, "debug", false, "debug")
	fs.BoolVar(&credentialProcess, "credential-process", false, "print AWS credential_process JSON")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	broker = resolveBroker(broker)
	project = firstNonEmpty(project, os.Getenv("SRAM_PROJECT"))
	pamUser = strings.TrimSpace(pamUser)
	if broker == "" {
		fmt.Fprintln(os.Stderr, "ERROR: missing broker URL (-b / --broker or SRAM_BROKER_URL)")
		return 1
	}
	if debug {
		fmt.Fprintf(os.Stderr, "DEBUG: broker=%s target=%q pam=%q\n", broker, project, pamUser)
	}

	if credentialProcess && project != "" {
		if cached := loadCachedCredentials(broker, project); cached != nil {
			if debug {
				fmt.Fprintf(os.Stderr, "DEBUG: using cached credentials (expire %s)\n", cached.Expiration)
			}
			return finish(printCredentialProcess(os.Stdout, cached.credentialProcessOutput))
		}
	}

	client := NewBrokerClient(broker)
	client.Progress = os.Stderr
	if debug {
		client.Debug = os.Stderr
	}

	var (
		proof    map[string]string
		deadline time.Time
	)
	if pamUser != "" {
		pam, err := client.StartPAMLogin(pamUser)
		if err != nil {
			return fail(err)
		}
		tty, err := openTTY()
		if err != nil {
			return fail(fmt.Errorf("PAM login needs a terminal to read the PIN: %w", err))
		}
		defer tty.Close()
		fmt.Fprintln(os.Stderr, strings.TrimSpace(pam.Challenge))
		pin, err := readPIN(tty)
		if err != nil {
			return fail(err)
		}
		proof = map[string]string{"pam_session": pam.PAMSession, "pin": pin}
	} else {
		dev, err := client.StartDeviceLogin()
		if err != nil {
			return fail(err)
		}
		if uri := strings.TrimSpace(dev.VerificationURIComplete); uri != "" {
			fmt.Fprintf(os.Stderr, "Open: %s\n", uri)
			openBrowser(uri)
		} else {
			fmt.Fprintf(os.Stderr, "Open: %s\nEnter code: %s\n", dev.VerificationURI, dev.UserCode)
			openBrowser(dev.VerificationURI)
		}
		fmt.Fprintf(os.Stderr, "Complete the sign-in in the browser (within %s).\n", shortDuration(time.Duration(dev.ExpiresIn)*time.Second))
		proof = map[string]string{"device_code": dev.DeviceCode}
		deadline = time.Now().Add(time.Duration(dev.ExpiresIn) * time.Second)
	}

	choose := func(targets []target, wanted string) (string, error) {
		tty, err := openTTY()
		if err != nil {
			return chooseTarget(targets, wanted, nil, os.Stderr)
		}
		defer tty.Close()
		return chooseTarget(targets, wanted, tty, os.Stderr)
	}
	resp, err := client.Credentials(proof, project, deadline, choose)
	if err != nil {
		return fail(err)
	}
	if debug {
		fmt.Fprintf(os.Stderr, "DEBUG: credentials for %s, endpoint %s, region %s, valid until %s\n",
			resp.Target, resp.EndpointURL, resp.Region, resp.Credentials.Expiration)
	}

	if credentialProcess {
		out := credentialProcessFrom(resp.Credentials)
		// Cached under the final target. If the user had to choose (no -p, or a -p
		// matching none of the roles), the requested key is deliberately not cached:
		// reusing it would silently skip the choice next time.
		if err := writeCachedCredentials(&cacheEntry{
			credentialProcessOutput: out,
			Broker:                  broker,
			Target:                  resp.Target,
			EndpointURL:             resp.EndpointURL,
			Region:                  resp.Region,
		}); err != nil && debug {
			fmt.Fprintf(os.Stderr, "DEBUG: cache not written: %v\n", err)
		}
		if resp.Target != project {
			fmt.Fprintf(os.Stderr, "Tip: add -p %s to the credential_process command to reuse cached credentials; see `sram-s3 status` for the profile.\n", resp.Target)
		}
		return finish(printCredentialProcess(os.Stdout, out))
	}

	fmt.Fprintf(os.Stderr, "Credentials for %s, valid until %s.\n", resp.Target, resp.Credentials.Expiration)
	printExports(os.Stdout, resp)
	return 0
}

func fail(err error) int {
	fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
	return 1
}

func finish(err error) int {
	if err != nil {
		return fail(err)
	}
	return 0
}

// resolveBroker applies the broker URL order: flag → SRAM_BROKER_URL → compiled-in default.
func resolveBroker(flagValue string) string {
	b := firstNonEmpty(flagValue, os.Getenv("SRAM_BROKER_URL"), defaultBrokerURL)
	return strings.TrimRight(b, "/")
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
