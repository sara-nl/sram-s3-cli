package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

var testTargets = []target{
	{"org1:project1", "Project 1", "members", "", "b1", "org1-project1"},
	{"org1:project1:s3admin", "Project 1", "admin", "s3admin", "b1", "org1-project1-s3admin"},
	{"org1:project1:group1", "Project 1", "group", "group1", "b1", "org1-project1-group1"},
	{"org2:project2", "Project 2", "members", "", "b2", "org2-project2"},
	{"org2:project2:s3admin", "Project 2", "admin", "s3admin", "b2", "org2-project2-s3admin"},
	{"org2:project2:readonly", "Project 2", "group", "readonly", "b2", "org2-project2-readonly"},
}

const testRoleList = `  Project 1
    1) member                    org1:project1
    2) project admin (s3admin)   org1:project1:s3admin
    3) group group1              org1:project1:group1
  Project 2
    4) member                    org2:project2
    5) project admin (s3admin)   org2:project2:s3admin
    6) group readonly            org2:project2:readonly
`

func TestPrintRoles(t *testing.T) {
	var buf bytes.Buffer
	printRoles(&buf, testTargets)
	if buf.String() != testRoleList {
		t.Fatalf("got:\n%s\nwant:\n%s", buf.String(), testRoleList)
	}
}

func TestPrintRolesNumberWidth(t *testing.T) {
	var ts []target
	for i := 0; i < 10; i++ {
		ts = append(ts, target{Target: "p:x", Project: "P", Kind: "members"})
	}
	var buf bytes.Buffer
	printRoles(&buf, ts)
	lines := strings.Split(buf.String(), "\n")
	if lines[1] != "     1) member   p:x" || lines[10] != "    10) member   p:x" {
		t.Fatalf("got:\n%s", buf.String())
	}
}

// fakeTTY reads the typed input and collects what is written to the terminal.
type fakeTTY struct {
	io.Reader
	bytes.Buffer
}

func (f *fakeTTY) Read(p []byte) (int, error) { return f.Reader.Read(p) }

func TestChooseTargetInteractive(t *testing.T) {
	tty := &fakeTTY{Reader: strings.NewReader("x\n9\n2\n")}
	var msg bytes.Buffer
	got, err := chooseTarget(testTargets, "", tty, &msg)
	if err != nil {
		t.Fatal(err)
	}
	if got != "org1:project1:s3admin" {
		t.Fatalf("got %q", got)
	}
	screen := tty.Buffer.String()
	if !strings.HasPrefix(screen, "Choose a role:\n"+testRoleList+"Role (1-6): ") {
		t.Fatalf("screen:\n%s", screen)
	}
	if strings.Count(screen, "Role (1-6): ") != 3 {
		t.Fatalf("expected 3 prompts:\n%s", screen)
	}
	if msg.String() != "Using role org1-project1-s3admin (target org1:project1:s3admin)\n" {
		t.Fatalf("msg %q", msg.String())
	}
}

func TestChooseTargetCancelled(t *testing.T) {
	tty := &fakeTTY{Reader: strings.NewReader("")}
	if _, err := chooseTarget(testTargets, "", tty, io.Discard); err == nil {
		t.Fatal("expected an error at end of input")
	}
}

func TestChooseTargetWanted(t *testing.T) {
	var msg bytes.Buffer
	got, err := chooseTarget(testTargets, "org2:project2", nil, &msg)
	if err != nil || got != "org2:project2" {
		t.Fatalf("got %q, %v", got, err)
	}
	if msg.Len() != 0 {
		t.Fatalf("unexpected message %q", msg.String())
	}
}

func TestChooseTargetNoTerminal(t *testing.T) {
	var msg bytes.Buffer
	_, err := chooseTarget(testTargets, "", nil, &msg)
	if err == nil || !strings.Contains(err.Error(), "re-run with -p <target>") {
		t.Fatalf("err = %v", err)
	}
	if msg.String() != "Your roles:\n"+testRoleList {
		t.Fatalf("msg:\n%s", msg.String())
	}
}

func TestChooseTargetWantedNotHeld(t *testing.T) {
	tty := &fakeTTY{Reader: strings.NewReader("1\n")}
	var msg bytes.Buffer
	got, err := chooseTarget(testTargets, "surf:other", tty, &msg)
	if err != nil || got != "org1:project1" {
		t.Fatalf("got %q, %v", got, err)
	}
	if !strings.HasPrefix(msg.String(), `You have no role for target "surf:other".`) {
		t.Fatalf("msg %q", msg.String())
	}
	if !strings.Contains(tty.Buffer.String(), testRoleList) {
		t.Fatalf("no role list on the terminal:\n%s", tty.Buffer.String())
	}
}
