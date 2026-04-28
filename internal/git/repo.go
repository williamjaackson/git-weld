package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type Repo struct {
	root string
}

func Open(startDir string) (*Repo, error) {
	root, err := output(startDir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("open git repo: %w", err)
	}

	return &Repo{root: strings.TrimSpace(root)}, nil
}

func (r *Repo) Root() string {
	return r.root
}

func (r *Repo) Run(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (r *Repo) RunQuiet(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.root
	return cmd.Run()
}

func (r *Repo) Output(args ...string) (string, error) {
	return output(r.root, "git", args...)
}

func output(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}

	return strings.TrimSpace(stdout.String()), nil
}

func (r *Repo) CurrentBranch() (string, error) {
	return r.Output("symbolic-ref", "--quiet", "--short", "HEAD")
}

func (r *Repo) ResolveBranch(branch string) (string, error) {
	if branch != "" {
		return branch, nil
	}
	return r.CurrentBranch()
}

func (r *Repo) BranchRef(branch string) string {
	return "refs/heads/" + branch
}

func (r *Repo) BranchExists(branch string) bool {
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = r.root
	return cmd.Run() == nil
}

func (r *Repo) HasRemote(remote string) bool {
	cmd := exec.Command("git", "remote", "get-url", remote)
	cmd.Dir = r.root
	return cmd.Run() == nil
}

func (r *Repo) RequireRemote(remote string) error {
	if strings.TrimSpace(remote) == "" {
		return errors.New("remote is not configured")
	}
	if !r.HasRemote(remote) {
		return fmt.Errorf("remote %q does not exist", remote)
	}
	return nil
}

func (r *Repo) HasRef(ref string) bool {
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", ref)
	cmd.Dir = r.root
	return cmd.Run() == nil
}

func (r *Repo) RemoteBranchRef(remote string, branch string) string {
	return "refs/remotes/" + remote + "/" + branch
}

func (r *Repo) ListLocalBranches() ([]string, error) {
	out, err := r.Output("for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}

	branches := strings.Split(out, "\n")
	sort.Strings(branches)
	return branches, nil
}

func (r *Repo) WorkingTreeClean() (bool, error) {
	out, err := r.Output("status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

func (r *Repo) StashPushIfNeeded(message string) (bool, error) {
	clean, err := r.WorkingTreeClean()
	if err != nil {
		return false, err
	}
	if clean {
		return false, nil
	}
	if message == "" {
		message = "git-weld"
	}
	if err := r.RunQuiet("stash", "push", "--include-untracked", "--message", message); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Repo) StashPop() error {
	if _, err := r.Output("rev-parse", "--verify", "refs/stash"); err != nil {
		return nil
	}
	if err := r.Run("stash", "pop", "--index", "--quiet"); err != nil {
		return fmt.Errorf("restore local changes: %w", err)
	}
	return nil
}

func (r *Repo) Checkout(branch string) error {
	return r.Run("switch", "--quiet", branch)
}

func (r *Repo) CreateBranch(branch string, base string, checkout bool) error {
	if checkout {
		return r.Run("switch", "--quiet", "-c", branch, r.BranchRef(base))
	}
	return r.RunQuiet("branch", branch, r.BranchRef(base))
}

func (r *Repo) DeleteBranch(branch string) error {
	if !r.BranchExists(branch) {
		return nil
	}
	return r.RunQuiet("branch", "-D", branch)
}

func (r *Repo) BranchOID(branch string) (string, error) {
	return r.Output("rev-parse", r.BranchRef(branch))
}

func (r *Repo) RefOID(ref string) (string, error) {
	return r.Output("rev-parse", ref)
}

func (r *Repo) UpdateRef(ref string, newOID string, oldOID string) error {
	args := []string{"update-ref", ref, newOID}
	if strings.TrimSpace(oldOID) != "" {
		args = append(args, oldOID)
	}
	return r.RunQuiet(args...)
}

func (r *Repo) DeleteRef(ref string, oldOID string) error {
	if !r.HasRef(ref) {
		return nil
	}
	args := []string{"update-ref", "-d", ref}
	if strings.TrimSpace(oldOID) != "" {
		args = append(args, oldOID)
	}
	return r.RunQuiet(args...)
}

func (r *Repo) RebaseAbort() error {
	if _, err := r.Output("rev-parse", "--verify", "REBASE_HEAD"); err != nil {
		return nil
	}
	return r.RunQuiet("rebase", "--abort")
}

func (r *Repo) PushBranch(remote string, branch string) error {
	if err := r.RequireRemote(remote); err != nil {
		return err
	}
	remoteRef := r.RemoteBranchRef(remote, branch)
	localOID, err := r.Output("rev-parse", r.BranchRef(branch))
	if err != nil {
		return err
	}
	shouldTrack := !strings.HasPrefix(branch, "_weld/")
	if !r.HasRef(remoteRef) {
		if err := r.Run("push", remote, r.BranchRef(branch)+":"+branch); err != nil {
			return err
		}
		if err := r.UpdateRef(remoteRef, localOID, ""); err != nil {
			return fmt.Errorf("update local tracking ref %s after push: %w", remoteRef, err)
		}
		if shouldTrack {
			return r.EnsureTrackingBranch(remote, branch)
		}
		return nil
	}
	ff, err := r.isRefAncestor(remoteRef, r.BranchRef(branch))
	if err != nil {
		return err
	}
	if ff {
		if err := r.Run("push", remote, r.BranchRef(branch)+":"+branch); err != nil {
			return err
		}
		if err := r.UpdateRef(remoteRef, localOID, ""); err != nil {
			return fmt.Errorf("update local tracking ref %s after push: %w", remoteRef, err)
		}
		if shouldTrack {
			return r.EnsureTrackingBranch(remote, branch)
		}
		return nil
	}
	if err := r.Run("push", "--force-with-lease="+branch, remote, r.BranchRef(branch)+":"+branch); err != nil {
		return err
	}
	if err := r.UpdateRef(remoteRef, localOID, ""); err != nil {
		return fmt.Errorf("update local tracking ref %s after force-push: %w", remoteRef, err)
	}
	if shouldTrack {
		return r.EnsureTrackingBranch(remote, branch)
	}
	return nil
}

func (r *Repo) RemoteBranchExists(remote string, branch string) (bool, error) {
	out, err := r.Output("ls-remote", "--heads", remote, branch)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func (r *Repo) DeleteRemoteBranch(remote string, branch string) (bool, error) {
	if err := r.RequireRemote(remote); err != nil {
		return false, err
	}
	exists, err := r.RemoteBranchExists(remote, branch)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	if err := r.Run("push", remote, "--delete", branch); err != nil {
		return false, err
	}
	if err := r.DeleteRef(r.RemoteBranchRef(remote, branch), ""); err != nil {
		return false, fmt.Errorf("delete local tracking ref %s: %w", r.RemoteBranchRef(remote, branch), err)
	}
	return true, nil
}

func (r *Repo) UpdateLocalRoot(remote string, root string) error {
	if strings.TrimSpace(remote) == "" || !r.HasRemote(remote) {
		return nil
	}
	remoteRef := "refs/remotes/" + remote + "/" + root
	localRef := "refs/heads/" + root
	if !r.HasRef(remoteRef) || !r.HasRef(localRef) {
		return nil
	}

	counts, err := r.Output("rev-list", "--left-right", "--count", r.BranchRef(root)+"...refs/remotes/"+remote+"/"+root)
	if err != nil {
		return err
	}
	fields := strings.Fields(counts)
	if len(fields) != 2 {
		return fmt.Errorf("unexpected rev-list output %q", counts)
	}
	if fields[0] != "0" {
		return fmt.Errorf("%s has diverged from %s/%s", root, remote, root)
	}
	if fields[1] == "0" {
		return nil
	}

	newOID, err := r.Output("rev-parse", "refs/remotes/"+remote+"/"+root)
	if err != nil {
		return err
	}
	oldOID, err := r.Output("rev-parse", r.BranchRef(root))
	if err != nil {
		return err
	}
	return r.Run("update-ref", localRef, newOID, oldOID)
}

func (r *Repo) Fetch(remote string) error {
	if err := r.RequireRemote(remote); err != nil {
		return err
	}
	return r.Run("fetch", remote)
}

func (r *Repo) FetchQuiet(remote string) error {
	if err := r.RequireRemote(remote); err != nil {
		return err
	}
	return r.RunQuiet("fetch", "--quiet", remote)
}

func (r *Repo) TrackingRef(branch string) (string, error) {
	remote, err := r.Output("config", "--local", "--get", "branch."+branch+".remote")
	if err != nil || strings.TrimSpace(remote) == "" {
		return "", nil
	}
	mergeRef, err := r.Output("config", "--local", "--get", "branch."+branch+".merge")
	if err != nil || strings.TrimSpace(mergeRef) == "" {
		return "", nil
	}
	mergeRef = strings.TrimSpace(mergeRef)
	const headsPrefix = "refs/heads/"
	if strings.HasPrefix(mergeRef, headsPrefix) {
		mergeRef = strings.TrimPrefix(mergeRef, headsPrefix)
	}
	return "refs/remotes/" + strings.TrimSpace(remote) + "/" + mergeRef, nil
}

func (r *Repo) EnsureTrackingBranch(remote string, branch string) error {
	trackingRef, err := r.TrackingRef(branch)
	if err != nil {
		return err
	}
	expected := "refs/remotes/" + strings.TrimSpace(remote) + "/" + branch
	if trackingRef == expected {
		return nil
	}
	return r.RunQuiet("branch", "--quiet", "--set-upstream-to="+remote+"/"+branch, branch)
}

func (r *Repo) EnsureTrackingBranchIfRemoteExists(remote string, branch string) error {
	if err := r.RequireRemote(remote); err != nil {
		return err
	}
	trackingRef, err := r.TrackingRef(branch)
	if err != nil {
		return err
	}
	if trackingRef != "" {
		return nil
	}
	exists, err := r.RemoteBranchExists(remote, branch)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return r.EnsureTrackingBranch(remote, branch)
}

func (r *Repo) UpdateLocalBranchFromTracking(branch string) error {
	trackingRef, err := r.TrackingRef(branch)
	if err != nil {
		return err
	}
	if trackingRef == "" || !r.HasRef(trackingRef) || !r.HasRef(r.BranchRef(branch)) {
		return nil
	}

	counts, err := r.Output("rev-list", "--left-right", "--count", r.BranchRef(branch)+"..."+trackingRef)
	if err != nil {
		return err
	}
	fields := strings.Fields(counts)
	if len(fields) != 2 {
		return fmt.Errorf("unexpected rev-list output %q", counts)
	}
	if fields[0] == "0" && fields[1] == "0" {
		return nil
	}
	if fields[0] != "0" && fields[1] == "0" {
		return nil
	}
	if fields[0] == "0" {
		newOID, err := r.Output("rev-parse", trackingRef)
		if err != nil {
			return err
		}
		oldOID, err := r.Output("rev-parse", r.BranchRef(branch))
		if err != nil {
			return err
		}
		return r.Run("update-ref", r.BranchRef(branch), newOID, oldOID)
	}
	return r.RebaseBranchOntoRef(branch, trackingRef)
}

func (r *Repo) RebaseBranchOntoRef(branch string, targetRef string) error {
	cur, err := r.CurrentBranch()
	if err != nil {
		return err
	}
	restore := cur
	success := false

	if cur != branch {
		if err := r.Checkout(branch); err != nil {
			return err
		}
	}
	defer func() {
		if success && restore != branch {
			_ = r.Checkout(restore)
		}
	}()

	if err := r.runGitQuietWithOutput("rebase", "--quiet", targetRef); err != nil {
		return fmt.Errorf("rebase %s onto tracking branch: %w", branch, err)
	}
	success = true
	return nil
}

func (r *Repo) RebaseBranchOnto(branch string, base string) error {
	oldBaseOID, err := r.Output("rev-parse", r.BranchRef(base))
	if err != nil {
		return err
	}
	return r.RebaseBranchOntoFrom(branch, oldBaseOID, base)
}

func (r *Repo) RebaseBranchOntoFrom(branch string, oldBaseOID string, newBase string) error {
	cur, err := r.CurrentBranch()
	if err != nil {
		return err
	}
	restore := cur
	success := false

	if cur != branch {
		if err := r.Checkout(branch); err != nil {
			return err
		}
	}
	defer func() {
		if success && restore != branch {
			_ = r.Checkout(restore)
		}
	}()

	if err := r.runGitQuietWithOutput("rebase", "--quiet", "--onto", r.BranchRef(newBase), oldBaseOID); err != nil {
		return err
	}
	success = true
	return nil
}

func (r *Repo) runGitQuietWithOutput(args ...string) error {
	cmdArgs := append([]string{"-c", "advice.skippedCherryPicks=false"}, args...)
	return r.Run(cmdArgs...)
}

func (r *Repo) isRefAncestor(ancestorRef string, descendantRef string) (bool, error) {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", ancestorRef, descendantRef)
	cmd.Dir = r.root
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r *Repo) IsRefAncestor(ancestorRef string, descendantRef string) (bool, error) {
	return r.isRefAncestor(ancestorRef, descendantRef)
}

func (r *Repo) Diff(base string, branch string) (string, error) {
	return r.Output("diff", r.BranchRef(base), r.BranchRef(branch), "--")
}

func (r *Repo) SyntheticBranchName(branch string) string {
	return "_weld/" + encodeName(branch)
}

func (r *Repo) RebuildSyntheticBase(branch string, parents []string) (string, error) {
	if len(parents) < 2 {
		return "", fmt.Errorf("synthetic base requires at least two parents")
	}

	synthetic := r.SyntheticBranchName(branch)
	tempDir, err := os.MkdirTemp("", "git-weld-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tempDir)

	if err := r.Run("worktree", "add", "--quiet", "--detach", tempDir, r.BranchRef(parents[0])); err != nil {
		return "", err
	}
	defer func() {
		_ = exec.Command("git", "-C", r.root, "worktree", "remove", "--force", tempDir).Run()
	}()

	if _, err := output(tempDir, "git", "switch", "--quiet", "-C", synthetic, r.BranchRef(parents[0])); err != nil {
		return "", err
	}
	for _, parent := range parents[1:] {
		if _, err := output(tempDir, "git", "merge", "--no-edit", r.BranchRef(parent)); err != nil {
			_, _ = output(tempDir, "git", "merge", "--abort")
			return "", fmt.Errorf("merge %s into %s: %w", parent, synthetic, err)
		}
	}

	return synthetic, nil
}

func (r *Repo) CommitParentOIDs(ref string) ([]string, error) {
	out, err := r.Output("show", "--no-patch", "--format=%P", ref)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	return strings.Fields(out), nil
}

func encodeName(value string) string {
	replacer := strings.NewReplacer("/", "_", " ", "_", "+", "_", ":", "_")
	return replacer.Replace(value)
}

func (r *Repo) ConfigGetAll(key string) ([]string, error) {
	cmd := exec.Command("git", "config", "--local", "--get-all", key)
	cmd.Dir = r.root
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		out := strings.TrimSpace(stdout.String())
		if out == "" {
			return nil, nil
		}
		return strings.Split(out, "\n"), nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return nil, nil
	}

	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		msg = err.Error()
	}
	return nil, errors.New(msg)
}

func (r *Repo) ConfigSetAll(key string, values []string) error {
	if err := exec.Command("git", "-C", r.root, "config", "--local", "--unset-all", key).Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 5 {
			return err
		}
	}
	for _, value := range values {
		if err := r.Run("config", "--local", "--add", key, value); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repo) ConfigUnsetAll(key string) error {
	if err := exec.Command("git", "-C", r.root, "config", "--local", "--unset-all", key).Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 5 {
			return nil
		}
		return err
	}
	return nil
}

func (r *Repo) ConfigKeys(expr string) ([]string, error) {
	cmd := exec.Command("git", "config", "--local", "--name-only", "--get-regexp", expr)
	cmd.Dir = r.root
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return nil, nil
	}
	keys := strings.Split(out, "\n")
	sort.Strings(keys)
	return keys, nil
}

func (r *Repo) EnsureDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

func (r *Repo) GH(args ...string) (string, error) {
	return output(r.root, "gh", args...)
}

func (r *Repo) HasGH() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}
