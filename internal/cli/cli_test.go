package cli

import (
	"bytes"
	"testing"
)

func TestParseBoolFlagsAllowsInterspersedFlags(t *testing.T) {
	positional, flags, err := parseBoolFlags([]string{"fix-1", "master", "-c"}, map[string]string{
		"-c":       "create",
		"--create": "create",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !flags["create"] {
		t.Fatal("expected create flag to be set")
	}
	if len(positional) != 2 || positional[0] != "fix-1" || positional[1] != "master" {
		t.Fatalf("unexpected positional args: %#v", positional)
	}
}

func TestHelpOmitsDoctorAndCleanup(t *testing.T) {
	var out bytes.Buffer
	printHelp(&out)
	text := out.String()
	if bytes.Contains(out.Bytes(), []byte("doctor")) || bytes.Contains(out.Bytes(), []byte("cleanup")) {
		t.Fatalf("unexpected help output:\n%s", text)
	}
}
