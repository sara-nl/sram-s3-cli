package main

// What login prints on stdout: shell export lines (default) or AWS credential_process JSON.

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// credentialProcessOutput is the AWS credential_process JSON format (Version 1).
// See https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-sourcing-external.html
type credentialProcessOutput struct {
	Version         int    `json:"Version"`
	AccessKeyId     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration,omitempty"`
}

func credentialProcessFrom(c *awsCredentials) credentialProcessOutput {
	return credentialProcessOutput{
		Version:         1,
		AccessKeyId:     c.AccessKeyID,
		SecretAccessKey: c.SecretAccessKey,
		SessionToken:    c.SessionToken,
		Expiration:      c.Expiration,
	}
}

func printCredentialProcess(w io.Writer, out credentialProcessOutput) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// printExports writes shell export lines for the credentials, endpoint and region.
func printExports(w io.Writer, r *credentialsResponse) {
	c := r.Credentials
	fmt.Fprintf(w, "export AWS_ENDPOINT_URL=%s\n", shellQuote(r.EndpointURL))
	fmt.Fprintf(w, "export AWS_REGION=%s\n", shellQuote(r.Region))
	fmt.Fprintf(w, "export AWS_DEFAULT_REGION=%s\n", shellQuote(r.Region))
	fmt.Fprintf(w, "export AWS_ACCESS_KEY_ID=%s\n", shellQuote(c.AccessKeyID))
	fmt.Fprintf(w, "export AWS_SECRET_ACCESS_KEY=%s\n", shellQuote(c.SecretAccessKey))
	fmt.Fprintf(w, "export AWS_SESSION_TOKEN=%s\n", shellQuote(c.SessionToken))
	fmt.Fprintf(w, "export AWS_SESSION_EXPIRATION=%s\n", shellQuote(c.Expiration))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
