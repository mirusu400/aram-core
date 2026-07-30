package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestHelpAndUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("help exit code = %d", code)
	}
	if !strings.Contains(stdout.String(), "aram-debug <lua|serve>") {
		t.Fatalf("help output = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"unknown"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
		t.Fatalf("unknown exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), `unknown subcommand "unknown"`) {
		t.Fatalf("unknown stderr = %q", stderr.String())
	}
}
