package weld

import (
	"fmt"
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

	entries, err := svc.Status("fix-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Branch != "fix-1" || len(entries[0].Upstream) != 1 || entries[0].Upstream[0] != "master" {
		t.Fatalf("unexpected status: %+v", entries)
	}
	if entries[0].SyncAction != "none" || entries[0].ShipAction != "none" {
		t.Fatalf("unexpected action state: %+v", entries[0])
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

func TestNewBranchPreservesWorktreeChanges(t *testing.T) {
	repoDir := initRepo(t)
	svc := openService(t, repoDir)

	writeFile(t, repoDir, "tracked.txt", "one\n")
	runGit(t, repoDir, "add", "tracked.txt")
	runGit(t, repoDir, "commit", "-m", "tracked")

	writeFile(t, repoDir, "tracked.txt", "two\n")
	writeFile(t, repoDir, "staged.txt", "staged\n")
	writeFile(t, repoDir, "untracked.txt", "untracked\n")
	runGit(t, repoDir, "add", "staged.txt")

	if err := svc.NewBranch("fix-1"); err != nil {
		t.Fatal(err)
	}

	if current := runGit(t, repoDir, "branch", "--show-current"); current != "fix-1" {
		t.Fatalf("expected to switch to fix-1, got %q", current)
	}
	if status := runGit(t, repoDir, "status", "--short"); !strings.Contains(status, "A  staged.txt") || !strings.Contains(status, " M tracked.txt") || !strings.Contains(status, "?? untracked.txt") {
		t.Fatalf("expected staged, unstaged, and untracked changes to survive, got:\n%s", status)
	}
}

func TestStackCreatePreservesWorktreeChanges(t *testing.T) {
	repoDir := initRepo(t)
	svc := openService(t, repoDir)
	if err := svc.NewBranch("fix-1"); err != nil {
		t.Fatal(err)
	}

	writeFile(t, repoDir, "tracked.txt", "one\n")
	runGit(t, repoDir, "add", "tracked.txt")
	runGit(t, repoDir, "commit", "-m", "tracked")

	writeFile(t, repoDir, "tracked.txt", "two\n")
	writeFile(t, repoDir, "staged.txt", "staged\n")
	writeFile(t, repoDir, "untracked.txt", "untracked\n")
	runGit(t, repoDir, "add", "staged.txt")

	if err := svc.Stack("feature", "", true); err != nil {
		t.Fatal(err)
	}

	if current := runGit(t, repoDir, "branch", "--show-current"); current != "feature" {
		t.Fatalf("expected to switch to feature, got %q", current)
	}
	if status := runGit(t, repoDir, "status", "--short"); !strings.Contains(status, "A  staged.txt") || !strings.Contains(status, " M tracked.txt") || !strings.Contains(status, "?? untracked.txt") {
		t.Fatalf("expected staged, unstaged, and untracked changes to survive, got:\n%s", status)
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
	if !strings.Contains(out, "(upstream)\nfix-1\n\n(downstream)\nfix-1\n└─ feature") {
		t.Fatalf("expected descendant tree in show --tree output:\n%s", out)
	}
}

func TestDeletedBranchesAreAutoPrunedFromStatus(t *testing.T) {
	repoDir := initRepo(t)
	svc := openService(t, repoDir)
	if err := svc.NewBranch("fix-1"); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoDir, "checkout", "master")
	if err := svc.NewBranch("fix-2"); err != nil {
		t.Fatal(err)
	}

	runGit(t, repoDir, "checkout", "master")
	runGit(t, repoDir, "branch", "-D", "fix-2")

	entries, err := svc.Status("fix-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Branch != "fix-1" {
		t.Fatalf("expected deleted branch metadata to be pruned while keeping fix-1, got: %+v", entries)
	}
}

func TestStatusReportsUpdateAndSyncFirstForRemoteAheadBranch(t *testing.T) {
	repoDir := initRepo(t)
	remoteDir := t.TempDir()
	runGit(t, remoteDir, "init", "--bare")
	runGit(t, repoDir, "remote", "add", "origin", remoteDir)
	runGit(t, repoDir, "push", "-u", "origin", "master")

	svc := openService(t, repoDir)
	if err := svc.NewBranch("fix-1"); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoDir, "push", "-u", "origin", "fix-1")

	cloneDir := t.TempDir()
	runGit(t, cloneDir, "clone", remoteDir, ".")
	runGit(t, cloneDir, "config", "user.name", "Other User")
	runGit(t, cloneDir, "config", "user.email", "other@example.com")
	runGit(t, cloneDir, "switch", "fix-1")
	writeFile(t, cloneDir, "remote-fix.txt", "remote fix\n")
	runGit(t, cloneDir, "add", "remote-fix.txt")
	runGit(t, cloneDir, "commit", "-m", "remote fix")
	runGit(t, cloneDir, "push", "origin", "fix-1")

	entries, err := svc.Status("fix-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("unexpected status entries: %+v", entries)
	}
	if entries[0].SyncAction != "update" || entries[0].ShipAction != "sync-first" {
		t.Fatalf("unexpected status entry: %+v", entries[0])
	}
}

func TestStatusPropagatesUpstreamUpdateToChildBranch(t *testing.T) {
	repoDir := initRepo(t)
	remoteDir := t.TempDir()
	runGit(t, remoteDir, "init", "--bare")
	runGit(t, repoDir, "remote", "add", "origin", remoteDir)
	runGit(t, repoDir, "push", "-u", "origin", "master")

	svc := openService(t, repoDir)
	if err := svc.NewBranch("fix-1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repoDir, "fix-1.txt", "base\n")
	runGit(t, repoDir, "add", "fix-1.txt")
	runGit(t, repoDir, "commit", "-m", "fix-1")
	runGit(t, repoDir, "push", "-u", "origin", "fix-1")

	if err := svc.Stack("feature", "fix-1", true); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repoDir, "feature.txt", "feature\n")
	runGit(t, repoDir, "add", "feature.txt")
	runGit(t, repoDir, "commit", "-m", "feature")
	runGit(t, repoDir, "push", "-u", "origin", "feature")

	cloneDir := t.TempDir()
	runGit(t, cloneDir, "clone", remoteDir, ".")
	runGit(t, cloneDir, "config", "user.name", "Other User")
	runGit(t, cloneDir, "config", "user.email", "other@example.com")
	runGit(t, cloneDir, "switch", "fix-1")
	writeFile(t, cloneDir, "fix-1.txt", "remote update\n")
	runGit(t, cloneDir, "add", "fix-1.txt")
	runGit(t, cloneDir, "commit", "-m", "remote update")
	runGit(t, cloneDir, "push", "origin", "fix-1")

	entries, err := svc.Status("feature", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("unexpected status entries: %+v", entries)
	}
	if entries[0].SyncAction != "rebase" || entries[0].ShipAction != "sync-first" {
		t.Fatalf("unexpected propagated status entry: %+v", entries[0])
	}
}

func TestStatusReportsDownstreamImpact(t *testing.T) {
	repoDir := initRepo(t)
	svc := buildBasicGraph(t, repoDir)

	entries, err := svc.Status("fix-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("unexpected status entries: %+v", entries)
	}
	if !contains(entries[0].Affects, "feature") {
		t.Fatalf("expected downstream impact to include feature, got %+v", entries[0])
	}
}

func TestStatusShipActionIncludesUpstreamPushes(t *testing.T) {
	repoDir := initRepo(t)
	remoteDir := t.TempDir()
	runGit(t, remoteDir, "init", "--bare")
	runGit(t, repoDir, "remote", "add", "origin", remoteDir)
	runGit(t, repoDir, "push", "-u", "origin", "master")

	svc := openService(t, repoDir)
	if err := svc.NewBranch("fix-1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repoDir, "fix-1.txt", "base\n")
	runGit(t, repoDir, "add", "fix-1.txt")
	runGit(t, repoDir, "commit", "-m", "fix-1")

	if err := svc.Stack("feature", "fix-1", true); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repoDir, "feature.txt", "feature\n")
	runGit(t, repoDir, "add", "feature.txt")
	runGit(t, repoDir, "commit", "-m", "feature")
	runGit(t, repoDir, "push", "-u", "origin", "feature")

	entries, err := svc.Status("feature", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("unexpected status entries: %+v", entries)
	}
	if entries[0].ShipAction != "push" {
		t.Fatalf("expected child ship status to include upstream push, got %+v", entries[0])
	}
}

func TestStatusTreeIncludesUpstreamAndDownstreamBranches(t *testing.T) {
	repoDir := initRepo(t)
	svc := buildBasicGraph(t, repoDir)

	entries, err := svc.Status("fix-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected tree status for fix-1 to include upstream and downstream branches, got %+v", entries)
	}
	branches := []string{entries[0].Branch, entries[1].Branch, entries[2].Branch}
	if !contains(branches, "fix-1") || !contains(branches, "fix-2") || !contains(branches, "feature") {
		t.Fatalf("unexpected status tree branches: %+v", branches)
	}
}

func TestDeletedParentIsAutoPrunedDuringSync(t *testing.T) {
	repoDir := initRepo(t)
	svc := buildBasicGraph(t, repoDir)

	runGit(t, repoDir, "branch", "-D", "fix-2")

	if err := svc.Sync("feature", false, SyncModeDefault); err != nil {
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

func TestShipPushesBranchesAndSyntheticBase(t *testing.T) {
	repoDir := initRepo(t)
	remoteDir := t.TempDir()
	runGit(t, remoteDir, "init", "--bare")
	runGit(t, repoDir, "remote", "add", "origin", remoteDir)
	runGit(t, repoDir, "push", "-u", "origin", "master")
	fakeBin := filepath.Join(t.TempDir(), "gh")
	ghLog := filepath.Join(t.TempDir(), "gh.log")
	ghState := filepath.Join(t.TempDir(), "gh.state")
	writeFakeGH(t, fakeBin, ghLog, ghState)
	if err := os.WriteFile(ghState, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(fakeBin)+string(os.PathListSeparator)+os.Getenv("PATH"))

	svc := buildBasicGraph(t, repoDir)
	result, err := svc.Ship("feature", false, SyncModeDefault)
	if err != nil {
		t.Fatal(err)
	}

	if !contains(result.BranchesPushed, "feature") || !contains(result.BranchesPushed, "fix-1") || !contains(result.BranchesPushed, "fix-2") {
		t.Fatalf("unexpected ship branches: %+v", result)
	}
	if !contains(result.SyntheticPushed, "_weld/feature") {
		t.Fatalf("expected synthetic branch push: %+v", result)
	}
	if !contains(result.PRBasesUpdated, "feature") {
		t.Fatalf("expected PR base refresh for feature: %+v", result)
	}

	if runGit(t, repoDir, "ls-remote", "--heads", "origin", "feature") == "" {
		t.Fatal("expected feature on remote")
	}
	if runGit(t, repoDir, "ls-remote", "--heads", "origin", "_weld/feature") == "" {
		t.Fatal("expected synthetic branch on remote")
	}
}

func TestPRCreatesOrRefreshesUsingGH(t *testing.T) {
	repoDir := initRepo(t)
	remoteDir := t.TempDir()
	runGit(t, remoteDir, "init", "--bare")
	runGit(t, repoDir, "remote", "add", "origin", remoteDir)
	runGit(t, repoDir, "push", "-u", "origin", "master")
	fakeBin := filepath.Join(t.TempDir(), "gh")
	ghLog := filepath.Join(t.TempDir(), "gh.log")
	ghState := filepath.Join(t.TempDir(), "gh.state")
	writeFakeGH(t, fakeBin, ghLog, ghState)
	t.Setenv("PATH", filepath.Dir(fakeBin)+string(os.PathListSeparator)+os.Getenv("PATH"))

	svc := buildBasicGraph(t, repoDir)
	result, err := svc.PR("feature", "My Title", "Body text", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Number != 42 || result.Base != "_weld/feature" || result.Head != "feature" {
		t.Fatalf("unexpected pr result: %+v", result)
	}

	logBytes, err := os.ReadFile(ghLog)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logBytes)
	if !strings.Contains(logText, "pr create --base _weld/feature --head feature --title My Title --body Body text --draft") {
		t.Fatalf("expected gh create call, got:\n%s", logText)
	}
	if !strings.Contains(logText, "pr edit 42 --base _weld/feature --title My Title --body Body text") {
		t.Fatalf("expected gh edit call, got:\n%s", logText)
	}
	if !strings.Contains(logText, "pr view 42 --web") {
		t.Fatalf("expected gh web call, got:\n%s", logText)
	}
}

func TestShipDeletesStaleRemoteSyntheticBranch(t *testing.T) {
	repoDir := initRepo(t)
	remoteDir := t.TempDir()
	runGit(t, remoteDir, "init", "--bare")
	runGit(t, repoDir, "remote", "add", "origin", remoteDir)
	runGit(t, repoDir, "push", "-u", "origin", "master")
	fakeBin := filepath.Join(t.TempDir(), "gh")
	ghLog := filepath.Join(t.TempDir(), "gh.log")
	ghState := filepath.Join(t.TempDir(), "gh.state")
	writeFakeGH(t, fakeBin, ghLog, ghState)
	if err := os.WriteFile(ghState, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(fakeBin)+string(os.PathListSeparator)+os.Getenv("PATH"))

	svc := buildBasicGraph(t, repoDir)
	if _, err := svc.Ship("feature", false, SyncModeDefault); err != nil {
		t.Fatal(err)
	}
	if runGit(t, repoDir, "ls-remote", "--heads", "origin", "_weld/feature") == "" {
		t.Fatal("expected remote synthetic branch after initial ship")
	}

	if err := svc.Unstack("feature", "fix-2"); err != nil {
		t.Fatal(err)
	}
	result, err := svc.Ship("feature", false, SyncModeDefault)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(result.SyntheticDeleted, "_weld/feature") {
		t.Fatalf("expected stale synthetic remote deletion: %+v", result)
	}
	if !contains(result.PRBasesUpdated, "feature") {
		t.Fatalf("expected PR base refresh before synthetic deletion: %+v", result)
	}
	if out := runGit(t, repoDir, "ls-remote", "--heads", "origin", "_weld/feature"); out != "" {
		t.Fatalf("expected remote synthetic branch to be deleted, got: %s", out)
	}
}

func TestShipDoesNotReportSyntheticDeletionWhenNothingWasDeleted(t *testing.T) {
	repoDir := initRepo(t)
	remoteDir := t.TempDir()
	runGit(t, remoteDir, "init", "--bare")
	runGit(t, repoDir, "remote", "add", "origin", remoteDir)
	runGit(t, repoDir, "push", "-u", "origin", "master")

	svc := openService(t, repoDir)
	if err := svc.NewBranch("fix-1"); err != nil {
		t.Fatal(err)
	}
	result, err := svc.Ship("fix-1", false, SyncModeDefault)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SyntheticDeleted) != 0 {
		t.Fatalf("expected no synthetic deletions, got: %+v", result.SyntheticDeleted)
	}
}

func TestSecondShipWithNoChangesDoesNotForcePushAgain(t *testing.T) {
	repoDir := initRepo(t)
	remoteDir := t.TempDir()
	runGit(t, remoteDir, "init", "--bare")
	runGit(t, repoDir, "remote", "add", "origin", remoteDir)
	runGit(t, repoDir, "push", "-u", "origin", "master")

	svc := buildBasicGraph(t, repoDir)
	if _, err := svc.Ship("feature", false, SyncModeDefault); err != nil {
		t.Fatal(err)
	}
	firstFeatureRemote := runGit(t, repoDir, "rev-parse", "refs/remotes/origin/feature")
	firstSyntheticRemote := runGit(t, repoDir, "rev-parse", "refs/remotes/origin/_weld/feature")

	result, err := svc.Ship("feature", false, SyncModeDefault)
	if err != nil {
		t.Fatal(err)
	}
	secondFeatureRemote := runGit(t, repoDir, "rev-parse", "refs/remotes/origin/feature")
	secondSyntheticRemote := runGit(t, repoDir, "rev-parse", "refs/remotes/origin/_weld/feature")

	if firstFeatureRemote != secondFeatureRemote {
		t.Fatalf("expected feature remote ref to stay unchanged, got %s then %s", firstFeatureRemote, secondFeatureRemote)
	}
	if firstSyntheticRemote != secondSyntheticRemote {
		t.Fatalf("expected welded base remote ref to stay unchanged, got %s then %s", firstSyntheticRemote, secondSyntheticRemote)
	}
	if len(result.BranchesPushed) != 3 {
		t.Fatalf("expected ship plan to still include branches, got %+v", result)
	}
}

func TestShipWithExistingPRLeavesStatusClean(t *testing.T) {
	repoDir := initRepo(t)
	remoteDir := t.TempDir()
	runGit(t, remoteDir, "init", "--bare")
	runGit(t, repoDir, "remote", "add", "origin", remoteDir)
	runGit(t, repoDir, "push", "-u", "origin", "master")
	fakeBin := filepath.Join(t.TempDir(), "gh")
	ghLog := filepath.Join(t.TempDir(), "gh.log")
	ghState := filepath.Join(t.TempDir(), "gh.state")
	writeFakeGH(t, fakeBin, ghLog, ghState)
	if err := os.WriteFile(ghState, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(fakeBin)+string(os.PathListSeparator)+os.Getenv("PATH"))

	svc := buildBasicGraph(t, repoDir)
	if _, err := svc.Ship("feature", false, SyncModeDefault); err != nil {
		t.Fatal(err)
	}

	entries, err := svc.Status("feature", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("unexpected status entries: %+v", entries)
	}
	if entries[0].SyncAction != "none" || entries[0].ShipAction != "none" {
		t.Fatalf("expected status to be clean after ship, got %+v", entries[0])
	}
}

func TestInitPersistsRootAndDisablesRemoteFeatures(t *testing.T) {
	repoDir := initRepo(t)
	runGit(t, repoDir, "checkout", "-b", "main")

	svc := openService(t, repoDir)
	if err := svc.Init("main", "", true); err != nil {
		t.Fatal(err)
	}
	if err := svc.NewBranch("fix-1"); err != nil {
		t.Fatal(err)
	}
	branchBase := runGit(t, repoDir, "merge-base", "fix-1", "main")
	mainOID := runGit(t, repoDir, "rev-parse", "main")
	if branchBase != mainOID {
		t.Fatalf("expected new branch to be based on main, merge-base=%q main=%q", branchBase, mainOID)
	}
	if _, err := svc.Ship("fix-1", false, SyncModeDefault); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected remote-disabled ship error, got: %v", err)
	}
}

func TestSyncDefaultRefreshesTrackedManagedBranchesButNotRoot(t *testing.T) {
	repoDir := initRepo(t)
	remoteDir := t.TempDir()
	runGit(t, remoteDir, "init", "--bare")
	runGit(t, repoDir, "remote", "add", "origin", remoteDir)
	runGit(t, repoDir, "push", "-u", "origin", "master")

	svc := openService(t, repoDir)
	if err := svc.NewBranch("fix-1"); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoDir, "push", "-u", "origin", "fix-1")

	cloneDir := t.TempDir()
	runGit(t, cloneDir, "clone", remoteDir, ".")
	runGit(t, cloneDir, "config", "user.name", "Other User")
	runGit(t, cloneDir, "config", "user.email", "other@example.com")

	runGit(t, cloneDir, "switch", "fix-1")
	writeFile(t, cloneDir, "remote-fix.txt", "remote fix\n")
	runGit(t, cloneDir, "add", "remote-fix.txt")
	runGit(t, cloneDir, "commit", "-m", "remote fix")
	runGit(t, cloneDir, "push", "origin", "fix-1")

	runGit(t, cloneDir, "switch", "master")
	writeFile(t, cloneDir, "remote-master.txt", "remote master\n")
	runGit(t, cloneDir, "add", "remote-master.txt")
	runGit(t, cloneDir, "commit", "-m", "remote master")
	runGit(t, cloneDir, "push", "origin", "master")

	beforeMaster := runGit(t, repoDir, "rev-parse", "master")
	if err := svc.Sync("fix-1", false, SyncModeDefault); err != nil {
		t.Fatal(err)
	}
	afterMaster := runGit(t, repoDir, "rev-parse", "master")
	if beforeMaster != afterMaster {
		t.Fatalf("expected default sync to leave root unchanged: before=%s after=%s", beforeMaster, afterMaster)
	}
	if !strings.Contains(runGit(t, repoDir, "show", "--stat", "--oneline", "fix-1"), "remote-fix.txt") {
		t.Fatal("expected tracked branch update on fix-1")
	}
}

func TestSyncRemoteAlsoRefreshesRootBranch(t *testing.T) {
	repoDir := initRepo(t)
	remoteDir := t.TempDir()
	runGit(t, remoteDir, "init", "--bare")
	runGit(t, repoDir, "remote", "add", "origin", remoteDir)
	runGit(t, repoDir, "push", "-u", "origin", "master")

	svc := openService(t, repoDir)
	if err := svc.NewBranch("fix-1"); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoDir, "push", "-u", "origin", "fix-1")

	cloneDir := t.TempDir()
	runGit(t, cloneDir, "clone", remoteDir, ".")
	runGit(t, cloneDir, "config", "user.name", "Other User")
	runGit(t, cloneDir, "config", "user.email", "other@example.com")
	writeFile(t, cloneDir, "remote-master.txt", "remote master\n")
	runGit(t, cloneDir, "add", "remote-master.txt")
	runGit(t, cloneDir, "commit", "-m", "remote master")
	runGit(t, cloneDir, "push", "origin", "master")

	beforeMaster := runGit(t, repoDir, "rev-parse", "master")
	if err := svc.Sync("fix-1", false, SyncModeRemote); err != nil {
		t.Fatal(err)
	}
	afterMaster := runGit(t, repoDir, "rev-parse", "master")
	if beforeMaster == afterMaster {
		t.Fatal("expected remote sync to refresh root branch")
	}
}

func TestSyncRefreshesRemoteParentBranchesAfterShipSetsTracking(t *testing.T) {
	repoDir := initRepo(t)
	remoteDir := t.TempDir()
	runGit(t, remoteDir, "init", "--bare")
	runGit(t, repoDir, "remote", "add", "origin", remoteDir)
	runGit(t, repoDir, "push", "-u", "origin", "master")

	svc := buildBasicGraph(t, repoDir)
	if _, err := svc.Ship("feature", false, SyncModeDefault); err != nil {
		t.Fatal(err)
	}

	cloneDir := t.TempDir()
	runGit(t, cloneDir, "clone", remoteDir, ".")
	runGit(t, cloneDir, "config", "user.name", "Other User")
	runGit(t, cloneDir, "config", "user.email", "other@example.com")
	runGit(t, cloneDir, "switch", "fix-1")
	writeFile(t, cloneDir, "remote-fix-parent.txt", "remote parent\n")
	runGit(t, cloneDir, "add", "remote-fix-parent.txt")
	runGit(t, cloneDir, "commit", "-m", "remote parent update")
	runGit(t, cloneDir, "push", "origin", "fix-1")

	if err := svc.Sync("feature", false, SyncModeDefault); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(runGit(t, repoDir, "show", "--stat", "--oneline", "fix-1"), "remote-fix-parent.txt") {
		t.Fatal("expected sync to refresh parent branch from remote tracking branch")
	}
}

func TestSyncFromFeaturePropagatesRemoteParentChangeAndShipPreservesIt(t *testing.T) {
	repoDir := initRepo(t)
	remoteDir := t.TempDir()
	runGit(t, remoteDir, "init", "--bare")
	runGit(t, repoDir, "remote", "add", "origin", remoteDir)
	runGit(t, repoDir, "push", "-u", "origin", "master")

	svc := openService(t, repoDir)
	if err := svc.NewBranch("fix-1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repoDir, "fix-shared.txt", "local base\n")
	runGit(t, repoDir, "add", "fix-shared.txt")
	runGit(t, repoDir, "commit", "-m", "fix-1 base")

	if err := svc.Stack("feature", "fix-1", true); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repoDir, "feature.txt", "feature\n")
	runGit(t, repoDir, "add", "feature.txt")
	runGit(t, repoDir, "commit", "-m", "feature")

	if _, err := svc.Ship("feature", false, SyncModeDefault); err != nil {
		t.Fatal(err)
	}

	cloneDir := t.TempDir()
	runGit(t, cloneDir, "clone", remoteDir, ".")
	runGit(t, cloneDir, "config", "user.name", "Other User")
	runGit(t, cloneDir, "config", "user.email", "other@example.com")
	runGit(t, cloneDir, "switch", "fix-1")
	writeFile(t, cloneDir, "fix-shared.txt", "remote edit\n")
	runGit(t, cloneDir, "add", "fix-shared.txt")
	runGit(t, cloneDir, "commit", "-m", "remote edit on fix-1")
	runGit(t, cloneDir, "push", "origin", "fix-1")

	runGit(t, repoDir, "switch", "feature")
	if err := svc.Sync("feature", false, SyncModeDefault); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(repoDir, "fix-shared.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(contents)) != "remote edit" {
		t.Fatalf("expected feature to include updated parent contents, got %q", string(contents))
	}
	rangeAfterSync := runGit(t, repoDir, "log", "--oneline", "fix-1..feature")
	if strings.Contains(rangeAfterSync, "remote edit on fix-1") {
		t.Fatalf("expected child branch range to exclude parent commit after sync, got:\n%s", rangeAfterSync)
	}

	if _, err := svc.Ship("feature", false, SyncModeDefault); err != nil {
		t.Fatal(err)
	}
	rangeLog := runGit(t, repoDir, "log", "--oneline", "fix-1..feature")
	if strings.Contains(rangeLog, "remote edit on fix-1") {
		t.Fatalf("expected child branch range to exclude parent commit, got:\n%s", rangeLog)
	}
	if !strings.Contains(rangeLog, "feature") {
		t.Fatalf("expected child branch range to keep feature commit, got:\n%s", rangeLog)
	}
	verifyDir := t.TempDir()
	runGit(t, verifyDir, "clone", remoteDir, ".")
	runGit(t, verifyDir, "switch", "fix-1")
	verifyContents, err := os.ReadFile(filepath.Join(verifyDir, "fix-shared.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(verifyContents)) != "remote edit" {
		t.Fatalf("expected remote fix-1 change to survive ship, got %q", string(verifyContents))
	}
}

func TestSyncBackfillsTrackingForExistingRemoteBranch(t *testing.T) {
	repoDir := initRepo(t)
	remoteDir := t.TempDir()
	runGit(t, remoteDir, "init", "--bare")
	runGit(t, repoDir, "remote", "add", "origin", remoteDir)
	runGit(t, repoDir, "push", "-u", "origin", "master")

	svc := openService(t, repoDir)
	if err := svc.NewBranch("fix-1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repoDir, "fix-shared.txt", "local base\n")
	runGit(t, repoDir, "add", "fix-shared.txt")
	runGit(t, repoDir, "commit", "-m", "fix-1 base")
	runGit(t, repoDir, "push", "origin", "fix-1")

	if err := svc.Stack("feature", "fix-1", true); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repoDir, "feature.txt", "feature\n")
	runGit(t, repoDir, "add", "feature.txt")
	runGit(t, repoDir, "commit", "-m", "feature")

	cloneDir := t.TempDir()
	runGit(t, cloneDir, "clone", remoteDir, ".")
	runGit(t, cloneDir, "config", "user.name", "Other User")
	runGit(t, cloneDir, "config", "user.email", "other@example.com")
	runGit(t, cloneDir, "switch", "fix-1")
	writeFile(t, cloneDir, "fix-shared.txt", "remote edit\n")
	runGit(t, cloneDir, "add", "fix-shared.txt")
	runGit(t, cloneDir, "commit", "-m", "remote edit on fix-1")
	runGit(t, cloneDir, "push", "origin", "fix-1")

	runGit(t, repoDir, "switch", "feature")
	if err := svc.Sync("feature", false, SyncModeDefault); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(repoDir, "fix-shared.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(contents)) != "remote edit" {
		t.Fatalf("expected sync to backfill tracking and include remote parent contents, got %q", string(contents))
	}
	if got := runGit(t, repoDir, "config", "--local", "--get", "branch.fix-1.remote"); got != "origin" {
		t.Fatalf("expected tracking to be restored for fix-1, got %q", got)
	}
}

func TestSyncConflictRollsBackToStartingState(t *testing.T) {
	repoDir := initRepo(t)
	remoteDir := t.TempDir()
	runGit(t, remoteDir, "init", "--bare")
	runGit(t, repoDir, "remote", "add", "origin", remoteDir)
	runGit(t, repoDir, "push", "-u", "origin", "master")

	svc := openService(t, repoDir)
	if err := svc.NewBranch("fix-1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repoDir, "shared.txt", "base\n")
	runGit(t, repoDir, "add", "shared.txt")
	runGit(t, repoDir, "commit", "-m", "base fix")
	runGit(t, repoDir, "push", "-u", "origin", "fix-1")

	if err := svc.Stack("feature", "fix-1", true); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repoDir, "feature.txt", "feature\n")
	runGit(t, repoDir, "add", "feature.txt")
	runGit(t, repoDir, "commit", "-m", "feature")

	runGit(t, repoDir, "switch", "fix-1")
	writeFile(t, repoDir, "shared.txt", "local fix-1\n")
	runGit(t, repoDir, "add", "shared.txt")
	runGit(t, repoDir, "commit", "-m", "local conflicting fix")
	localFixOID := runGit(t, repoDir, "rev-parse", "fix-1")
	localFeatureOID := runGit(t, repoDir, "rev-parse", "feature")

	cloneDir := t.TempDir()
	runGit(t, cloneDir, "clone", remoteDir, ".")
	runGit(t, cloneDir, "config", "user.name", "Other User")
	runGit(t, cloneDir, "config", "user.email", "other@example.com")
	runGit(t, cloneDir, "switch", "fix-1")
	writeFile(t, cloneDir, "shared.txt", "remote conflicting fix\n")
	runGit(t, cloneDir, "add", "shared.txt")
	runGit(t, cloneDir, "commit", "-m", "remote conflicting fix")
	runGit(t, cloneDir, "push", "origin", "fix-1")

	runGit(t, repoDir, "switch", "feature")
	err := svc.Sync("feature", false, SyncModeDefault)
	if err == nil {
		t.Fatal("expected sync conflict")
	}

	if current := runGit(t, repoDir, "branch", "--show-current"); current != "feature" {
		t.Fatalf("expected branch restored to feature, got %q", current)
	}
	if got := runGit(t, repoDir, "rev-parse", "fix-1"); got != localFixOID {
		t.Fatalf("expected fix-1 restored, got %s want %s", got, localFixOID)
	}
	if got := runGit(t, repoDir, "rev-parse", "feature"); got != localFeatureOID {
		t.Fatalf("expected feature restored, got %s want %s", got, localFeatureOID)
	}
	if status := runGit(t, repoDir, "status", "--porcelain"); status != "" {
		t.Fatalf("expected clean working tree after rollback, got %q", status)
	}
}

func TestShipRebasesOntoTrackingBranchBeforePush(t *testing.T) {
	repoDir := initRepo(t)
	remoteDir := t.TempDir()
	runGit(t, remoteDir, "init", "--bare")
	runGit(t, repoDir, "remote", "add", "origin", remoteDir)
	runGit(t, repoDir, "push", "-u", "origin", "master")

	svc := openService(t, repoDir)
	if err := svc.NewBranch("fix-1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repoDir, "local-base.txt", "base\n")
	runGit(t, repoDir, "add", "local-base.txt")
	runGit(t, repoDir, "commit", "-m", "base fix")
	runGit(t, repoDir, "push", "-u", "origin", "fix-1")

	writeFile(t, repoDir, "local-only.txt", "local\n")
	runGit(t, repoDir, "add", "local-only.txt")
	runGit(t, repoDir, "commit", "-m", "local only")

	cloneDir := t.TempDir()
	runGit(t, cloneDir, "clone", remoteDir, ".")
	runGit(t, cloneDir, "config", "user.name", "Other User")
	runGit(t, cloneDir, "config", "user.email", "other@example.com")
	runGit(t, cloneDir, "switch", "fix-1")
	writeFile(t, cloneDir, "remote-only.txt", "remote\n")
	runGit(t, cloneDir, "add", "remote-only.txt")
	runGit(t, cloneDir, "commit", "-m", "remote only")
	runGit(t, cloneDir, "push", "origin", "fix-1")

	if _, err := svc.Ship("fix-1", false, SyncModeDefault); err != nil {
		t.Fatal(err)
	}

	verifyDir := t.TempDir()
	runGit(t, verifyDir, "clone", remoteDir, ".")
	runGit(t, verifyDir, "switch", "fix-1")
	if _, err := os.Stat(filepath.Join(verifyDir, "local-only.txt")); err != nil {
		t.Fatalf("expected local commit to remain after ship: %v", err)
	}
	if _, err := os.Stat(filepath.Join(verifyDir, "remote-only.txt")); err != nil {
		t.Fatalf("expected remote commit to be integrated before ship: %v", err)
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

func writeFakeGH(t *testing.T, path string, logPath string, statePath string) {
	t.Helper()
	script := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %s
if [ "$1" = "pr" ] && [ "$2" = "list" ]; then
  if [ -f %s ]; then
    echo '[{"number":42,"url":"https://example.test/pr/42","baseRefName":"_weld/feature"}]'
  else
    echo '[]'
  fi
  exit 0
fi
if [ "$1" = "pr" ] && [ "$2" = "create" ]; then
  touch %s
  echo "https://example.test/pr/42"
  exit 0
fi
if [ "$1" = "pr" ] && [ "$2" = "edit" ]; then
  exit 0
fi
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  exit 0
fi
exit 1
`, logPath, statePath, statePath)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
