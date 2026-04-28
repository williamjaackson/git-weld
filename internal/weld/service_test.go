package weld

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewBranchIsManagedWithImplicitMasterParent(t *testing.T) {
	repoDir := initRepo(t)
	svc := openService(t, repoDir)

	if err := svc.NewBranch("fix-1"); err != nil {
		t.Fatal(err)
	}

	parents, err := svc.meta.Parents("fix-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(parents) != 0 {
		t.Fatalf("expected no explicit parents, got: %#v", parents)
	}

	entries, err := svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Branch != "fix-1" || len(entries[0].Parents) != 0 {
		t.Fatalf("unexpected status: %+v", entries)
	}
}

func TestStackOnMasterRootedBranchReplacesImplicitMaster(t *testing.T) {
	repoDir := initRepo(t)
	svc := openService(t, repoDir)
	if err := svc.NewBranch("feature"); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoDir, "checkout", "master")
	if err := svc.NewBranch("branch"); err != nil {
		t.Fatal(err)
	}

	if err := svc.Stack("branch", "feature", false); err != nil {
		t.Fatal(err)
	}

	parents, err := svc.meta.Parents("branch")
	if err != nil {
		t.Fatal(err)
	}
	if len(parents) != 1 || parents[0] != "feature" {
		t.Fatalf("unexpected parents: %#v", parents)
	}
}

func TestStackCreateSwitchesBranches(t *testing.T) {
	repoDir := initRepo(t)
	svc := openService(t, repoDir)
	if err := svc.NewBranch("fix-1"); err != nil {
		t.Fatal(err)
	}

	if err := svc.Stack("feature", "", true); err != nil {
		t.Fatal(err)
	}

	current := runGit(t, repoDir, "branch", "--show-current")
	if current != "feature" {
		t.Fatalf("expected to switch to feature, got %q", current)
	}
}

func TestUnstackLastParentFallsBackToImplicitMaster(t *testing.T) {
	repoDir := initRepo(t)
	svc := openService(t, repoDir)
	if err := svc.NewBranch("feature"); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoDir, "checkout", "master")
	if err := svc.NewBranch("branch"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Stack("branch", "feature", false); err != nil {
		t.Fatal(err)
	}

	if err := svc.Unstack("branch", "feature"); err != nil {
		t.Fatal(err)
	}

	parents, err := svc.meta.Parents("branch")
	if err != nil {
		t.Fatal(err)
	}
	if len(parents) != 0 {
		t.Fatalf("expected branch to fall back to master, got: %#v", parents)
	}
}

func TestServiceCreatesMultiParentGraphAndDiffsOnlyChildChanges(t *testing.T) {
	repoDir := initRepo(t)
	svc := buildBasicGraph(t, repoDir)

	diff, err := svc.Diff("feature")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(diff, "fix1.txt") || strings.Contains(diff, "fix2.txt") {
		t.Fatalf("diff included parent changes:\n%s", diff)
	}
	if !strings.Contains(diff, "feature.txt") {
		t.Fatalf("diff did not include feature changes:\n%s", diff)
	}
}

func TestDiffUsesQualifiedRefsWhenBranchNameMatchesFileName(t *testing.T) {
	repoDir := initRepo(t)
	writeFile(t, repoDir, "fix-1", "root file\n")
	runGit(t, repoDir, "add", "fix-1")
	runGit(t, repoDir, "commit", "-m", "add root file")

	svc := openService(t, repoDir)
	if err := svc.NewBranch("fix-1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repoDir, "fix-1", "branch file\n")
	runGit(t, repoDir, "add", "fix-1")
	runGit(t, repoDir, "commit", "-m", "update file")

	diff, err := svc.Diff("fix-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "branch file") {
		t.Fatalf("expected diff output, got:\n%s", diff)
	}
}

func TestServiceRejectsCycles(t *testing.T) {
	repoDir := initRepo(t)

	svc := openService(t, repoDir)
	if err := svc.NewBranch("fix-1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Stack("feature", "fix-1", true); err != nil {
		t.Fatal(err)
	}
	err := svc.Stack("fix-1", "feature", false)
	if err == nil {
		t.Fatal("expected cycle detection error")
	}
}

func TestShowRendersPrettyAncestorTreeWithoutMaster(t *testing.T) {
	repoDir := initRepo(t)
	svc := buildBasicGraph(t, repoDir)

	out, err := svc.Show("feature", false)
	if err != nil {
		t.Fatal(err)
	}
	expected := "feature\n├─ fix-1\n└─ fix-2"
	if !strings.Contains(out, expected) {
		t.Fatalf("unexpected show output:\n%s", out)
	}
	if strings.Contains(out, "master") {
		t.Fatalf("show should not render master:\n%s", out)
	}
}

func TestShowTreeAddsDescendantsWithoutSiblingBranches(t *testing.T) {
	repoDir := initRepo(t)
	svc := buildBasicGraph(t, repoDir)

	runGit(t, repoDir, "checkout", "master")
	if err := svc.NewBranch("test"); err != nil {
		t.Fatal(err)
	}

	out, err := svc.Show("feature", true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "test") {
		t.Fatalf("show --tree should not include sibling branch:\n%s", out)
	}

	out, err = svc.Show("fix-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(parents)\nfix-1\n\n(children)\nfix-1\n└─ feature") {
		t.Fatalf("expected descendant tree in show --tree output:\n%s", out)
	}
}

func TestDeletedBranchesAreAutoPrunedFromStatus(t *testing.T) {
	repoDir := initRepo(t)
	svc := openService(t, repoDir)
	if err := svc.NewBranch("fix-2"); err != nil {
		t.Fatal(err)
	}

	runGit(t, repoDir, "checkout", "master")
	runGit(t, repoDir, "branch", "-D", "fix-2")

	entries, err := svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected deleted branch metadata to be pruned, got: %+v", entries)
	}
}

func TestDeletedParentIsAutoPrunedDuringSync(t *testing.T) {
	repoDir := initRepo(t)
	svc := buildBasicGraph(t, repoDir)

	runGit(t, repoDir, "branch", "-D", "fix-2")

	if err := svc.Sync("feature", false); err != nil {
		t.Fatal(err)
	}
	parents, err := svc.meta.Parents("feature")
	if err != nil {
		t.Fatal(err)
	}
	if len(parents) != 1 || parents[0] != "fix-1" {
		t.Fatalf("expected missing parent to be pruned, got: %#v", parents)
	}
}

func TestUnstackCollapsesSyntheticBase(t *testing.T) {
	repoDir := initRepo(t)
	svc := buildBasicGraph(t, repoDir)

	synthetic := svc.repo.SyntheticBranchName("feature")
	if !svc.repo.BranchExists(synthetic) {
		t.Fatalf("expected synthetic branch %q to exist", synthetic)
	}

	if err := svc.Unstack("feature", "fix-2"); err != nil {
		t.Fatal(err)
	}
	if svc.repo.BranchExists(synthetic) {
		t.Fatalf("expected synthetic branch %q to be removed", synthetic)
	}
}

func buildBasicGraph(t *testing.T, repoDir string) *Service {
	t.Helper()
	svc := openService(t, repoDir)
	if err := svc.NewBranch("fix-1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repoDir, "fix1.txt", "fix1\n")
	runGit(t, repoDir, "add", "fix1.txt")
	runGit(t, repoDir, "commit", "-m", "fix-1")

	runGit(t, repoDir, "checkout", "master")
	if err := svc.NewBranch("fix-2"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repoDir, "fix2.txt", "fix2\n")
	runGit(t, repoDir, "add", "fix2.txt")
	runGit(t, repoDir, "commit", "-m", "fix-2")

	runGit(t, repoDir, "checkout", "fix-1")
	if err := svc.Stack("feature", "fix-1", true); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoDir, "checkout", "feature")
	writeFile(t, repoDir, "feature.txt", "feature\n")
	runGit(t, repoDir, "add", "feature.txt")
	runGit(t, repoDir, "commit", "-m", "feature")
	if err := svc.Stack("feature", "fix-2", false); err != nil {
		t.Fatal(err)
	}
	return svc
}

func openService(t *testing.T, repoDir string) *Service {
	t.Helper()
	svc, err := Open(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func initRepo(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()
	runGit(t, repoDir, "init", "-b", "master")
	runGit(t, repoDir, "config", "user.name", "Test User")
	runGit(t, repoDir, "config", "user.email", "test@example.com")
	writeFile(t, repoDir, "README.md", "base\n")
	runGit(t, repoDir, "add", "README.md")
	runGit(t, repoDir, "commit", "-m", "initial")
	return repoDir
}

func writeFile(t *testing.T, repoDir string, name string, contents string) {
	t.Helper()
	path := filepath.Join(repoDir, name)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, repoDir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return strings.TrimSpace(string(out))
}
