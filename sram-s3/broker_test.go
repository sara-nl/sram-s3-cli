package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestShellQuote(t *testing.T) {
	got := shellQuote(`foo'bar`)
	want := `'foo'"'"'bar'`
	if got != want {
		t.Fatalf("shellQuote: got %q want %q", got, want)
	}
}

func TestPickTargetSingle(t *testing.T) {
	targets := []stsTarget{{Target: "a:members", Label: "A"}}
	got, err := pickTarget(targets, strings.NewReader(""), ioDiscard{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "a:members" {
		t.Fatalf("got %q", got)
	}
}

func TestPickTargetInteractive(t *testing.T) {
	targets := []stsTarget{
		{Target: "a:members", Label: "A"},
		{Target: "b:admin", Label: "B"},
	}
	var out bytes.Buffer
	got, err := pickTarget(targets, strings.NewReader("2\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	if got != "b:admin" {
		t.Fatalf("got %q", got)
	}
}

func TestPrintExportEnv(t *testing.T) {
	var buf bytes.Buffer
	err := printExportEnv(&buf, &stsResponse{
		S3EndpointURL: "https://s3.example",
		AwsRegion:     "nl-ams",
		Credentials: &stsCredentials{
			AccessKeyID:     "AKI",
			SecretAccessKey: "SEC",
			SessionToken:    "TOK",
			Expiration:      "2099-01-01T00:00:00Z",
		},
	}, "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	for _, needle := range []string{
		"export AWS_ENDPOINT_URL='https://s3.example'",
		"export AWS_REGION='nl-ams'",
		"export AWS_ACCESS_KEY_ID='AKI'",
		"export AWS_SECRET_ACCESS_KEY='SEC'",
		"export AWS_SESSION_TOKEN='TOK'",
	} {
		if !strings.Contains(s, needle) {
			t.Fatalf("missing %q in:\n%s", needle, s)
		}
	}
}

// ioDiscard is a tiny Writer for tests (avoid importing io only for Discard in pick).
type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
