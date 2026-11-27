package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestShellQuote(t *testing.T) {
	got := shellQuote(`foo'bar`)
	want := `'foo'"'"'bar'`
	if got != want {
		t.Fatalf("shellQuote: got %q want %q", got, want)
	}
}

var testResponse = &credentialsResponse{
	Status:      "ok",
	Target:      "surf:demo",
	EndpointURL: "https://s3.example",
	Region:      "nl-ams",
	Credentials: &awsCredentials{
		AccessKeyID:     "AKI",
		SecretAccessKey: "SEC",
		SessionToken:    "TOK",
		Expiration:      "2099-01-01T00:00:00Z",
	},
}

func TestPrintExports(t *testing.T) {
	var buf bytes.Buffer
	printExports(&buf, testResponse)
	want := `export AWS_ENDPOINT_URL='https://s3.example'
export AWS_REGION='nl-ams'
export AWS_DEFAULT_REGION='nl-ams'
export AWS_ACCESS_KEY_ID='AKI'
export AWS_SECRET_ACCESS_KEY='SEC'
export AWS_SESSION_TOKEN='TOK'
export AWS_SESSION_EXPIRATION='2099-01-01T00:00:00Z'
`
	if buf.String() != want {
		t.Fatalf("got:\n%s", buf.String())
	}
}

func TestPrintCredentialProcess(t *testing.T) {
	var buf bytes.Buffer
	if err := printCredentialProcess(&buf, credentialProcessFrom(testResponse.Credentials)); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"Version": 1.0, "AccessKeyId": "AKI", "SecretAccessKey": "SEC",
		"SessionToken": "TOK", "Expiration": "2099-01-01T00:00:00Z"}
	if len(got) != len(want) {
		t.Fatalf("got fields %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %v, want %v", k, got[k], v)
		}
	}
}
