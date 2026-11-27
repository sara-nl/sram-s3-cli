package main

// The role list: grouped by project, the names users know from SRAM, and the target to
// pass to -p. The choice is read from the terminal (/dev/tty), never from stdin.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

// roleName is how a role is shown: member, project admin (s3admin), group <name>.
func roleName(t target) string {
	switch t.Kind {
	case "members":
		return "member"
	case "admin":
		return "project admin (" + t.Group + ")"
	default:
		return "group " + t.Group
	}
}

// printRoles writes the role list, numbered across projects:
//
//	Project 1
//	  1) member                    org1:project1
//	  2) project admin (s3admin)   org1:project1:s3admin
func printRoles(w io.Writer, targets []target) {
	width := 0
	for _, t := range targets {
		width = max(width, len(roleName(t)))
	}
	numWidth := len(strconv.Itoa(len(targets)))
	project := ""
	for i, t := range targets {
		if i == 0 || t.Project != project {
			project = t.Project
			fmt.Fprintf(w, "  %s\n", project)
		}
		fmt.Fprintf(w, "    %*d) %-*s   %s\n", numWidth, i+1, width, roleName(t), t.Target)
	}
}

// chooseTarget returns the role to use: wanted when the user holds it, otherwise the one
// the user picks from the list on tty. Without a terminal (tty nil) the list goes to msg
// and the user is asked to re-run with -p. Messages go to msg (stderr).
func chooseTarget(targets []target, wanted string, tty io.ReadWriter, msg io.Writer) (string, error) {
	if wanted != "" {
		for _, t := range targets {
			if t.Target == wanted {
				return wanted, nil
			}
		}
		fmt.Fprintf(msg, "You have no role for target %q.\n", wanted)
	}
	if tty == nil {
		fmt.Fprintln(msg, "Your roles:")
		printRoles(msg, targets)
		return "", errors.New("several roles: re-run with -p <target>")
	}
	fmt.Fprintln(tty, "Choose a role:")
	printRoles(tty, targets)
	in := bufio.NewReader(tty)
	for attempt := 0; attempt < 10; attempt++ {
		fmt.Fprintf(tty, "Role (1-%d): ", len(targets))
		line, err := in.ReadString('\n')
		if err != nil && (err != io.EOF || line == "") {
			fmt.Fprintln(tty)
			return "", errors.New("no role chosen")
		}
		n, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || n < 1 || n > len(targets) {
			continue
		}
		t := targets[n-1]
		fmt.Fprintf(msg, "Using role %s (target %s)\n", t.IAMRoleName, t.Target)
		return t.Target, nil
	}
	return "", errors.New("no role chosen")
}

func openTTY() (*os.File, error) {
	return os.OpenFile("/dev/tty", os.O_RDWR, 0)
}

// readPIN asks for the PAM PIN on the terminal without echoing it.
func readPIN(tty *os.File) (string, error) {
	if err := stty(tty, "-echo"); err != nil {
		return "", fmt.Errorf("cannot hide the PIN on this terminal: %w", err)
	}
	// Restore echo also when the user presses Ctrl-C at the prompt.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case <-sig:
			_ = stty(tty, "echo")
			fmt.Fprintln(tty)
			os.Exit(130)
		case <-done:
		}
	}()
	defer func() {
		signal.Stop(sig)
		close(done)
		_ = stty(tty, "echo")
		fmt.Fprintln(tty)
	}()

	fmt.Fprint(tty, "PIN: ")
	line, err := bufio.NewReader(tty).ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return "", errors.New("no PIN entered")
	}
	pin := strings.TrimSpace(line)
	if pin == "" {
		return "", errors.New("no PIN entered")
	}
	return pin, nil
}

func stty(tty *os.File, arg string) error {
	cmd := exec.Command("stty", arg)
	cmd.Stdin = tty
	return cmd.Run()
}
