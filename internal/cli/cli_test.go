package cli

import (
	"bytes"
	"testing"

	"github.com/williamjaackson/git-weld/internal/weld"
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
	if !bytes.Contains(out.Bytes(), []byte("ship")) || !bytes.Contains(out.Bytes(), []byte("pr")) {
		t.Fatalf("expected phase 2 commands in help output:\n%s", text)
	}
	if !bytes.Contains(out.Bytes(), []byte("init")) {
		t.Fatalf("expected init command in help output:\n%s", text)
	}
}

func TestParsePRArgs(t *testing.T) {
	branch, title, body, draft, web, err := parsePRArgs([]string{"feature", "--title", "My Title", "--body", "Body text", "--draft", "--web"})
	if err != nil {
		t.Fatal(err)
	}
	if branch != "feature" || title != "My Title" || body != "Body text" || !draft || !web {
		t.Fatalf("unexpected parsed args: branch=%q title=%q body=%q draft=%v web=%v", branch, title, body, draft, web)
	}

	branch, title, body, draft, web, err = parsePRArgs([]string{"--title=My Title", "--body=Body text"})
	if err != nil {
		t.Fatal(err)
	}
	if branch != "" || title != "My Title" || body != "Body text" || draft || web {
		t.Fatalf("unexpected parsed args: branch=%q title=%q body=%q draft=%v web=%v", branch, title, body, draft, web)
	}
}

func TestSyncModeFromFlags(t *testing.T) {
	if got := syncModeFromFlags(map[string]bool{"local": true}); got != weld.SyncModeLocal {
		t.Fatalf("expected local mode, got %v", got)
	}
	if got := syncModeFromFlags(map[string]bool{"remote": true}); got != weld.SyncModeRemote {
		t.Fatalf("expected remote mode, got %v", got)
	}
	if got := syncModeFromFlags(map[string]bool{}); got != weld.SyncModeDefault {
		t.Fatalf("expected default mode, got %v", got)
	}
}

func TestParseInitArgs(t *testing.T) {
	mainBranch, remoteName, remoteDisabled, interactive, err := parseInitArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if mainBranch != "" || remoteName != "" || remoteDisabled || !interactive {
		t.Fatalf("unexpected interactive init args: main=%q remote=%q disabled=%v interactive=%v", mainBranch, remoteName, remoteDisabled, interactive)
	}

	mainBranch, remoteName, remoteDisabled, interactive, err = parseInitArgs([]string{"--main", "main", "--remote", "upstream"})
	if err != nil {
		t.Fatal(err)
	}
	if mainBranch != "main" || remoteName != "upstream" || remoteDisabled || interactive {
		t.Fatalf("unexpected parsed init args: main=%q remote=%q disabled=%v interactive=%v", mainBranch, remoteName, remoteDisabled, interactive)
	}

	mainBranch, remoteName, remoteDisabled, interactive, err = parseInitArgs([]string{"--no-remote"})
	if err != nil {
		t.Fatal(err)
	}
	if mainBranch != "" || remoteName != "" || !remoteDisabled || interactive {
		t.Fatalf("unexpected parsed init args: main=%q remote=%q disabled=%v interactive=%v", mainBranch, remoteName, remoteDisabled, interactive)
	}
}
