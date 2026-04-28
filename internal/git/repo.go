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

func (r *Repo) HasRef(ref string) bool {
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", ref)
	cmd.Dir = r.root
	return cmd.Run() == nil
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

func (r *Repo) Checkout(branch string) error {
	return r.Run("switch", "--quiet", branch)
}

func (r *Repo) CreateBranch(branch string, base string, checkout bool) error {
	if checkout {
		return r.Run("switch", "--quiet", "-c", branch, r.BranchRef(base))
	}
	return r.Run("branch", branch, r.BranchRef(base))
}

func (r *Repo) DeleteBranch(branch string) error {
	if !r.BranchExists(branch) {
		return nil
	}
	return r.Run("branch", "-D", branch)
}

func (r *Repo) UpdateLocalRoot(root string) error {
	if !r.HasRemote("origin") {
		return nil
	}
	if err := r.Run("fetch", "origin"); err != nil {
		return err
	}

	remoteRef := "refs/remotes/origin/" + root
	localRef := "refs/heads/" + root
	if !r.HasRef(remoteRef) || !r.HasRef(localRef) {
		return nil
	}

	counts, err := r.Output("rev-list", "--left-right", "--count", r.BranchRef(root)+"...refs/remotes/origin/"+root)
	if err != nil {
		return err
	}
	fields := strings.Fields(counts)
	if len(fields) != 2 {
		return fmt.Errorf("unexpected rev-list output %q", counts)
	}
	if fields[0] != "0" {
		return fmt.Errorf("%s has diverged from origin/%s", root, root)
	}
	if fields[1] == "0" {
		return nil
	}

	newOID, err := r.Output("rev-parse", "refs/remotes/origin/"+root)
	if err != nil {
		return err
	}
	oldOID, err := r.Output("rev-parse", r.BranchRef(root))
	if err != nil {
		return err
	}
	return r.Run("update-ref", localRef, newOID, oldOID)
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

	if cur != branch {
		if err := r.Checkout(branch); err != nil {
			return err
		}
	}
	defer func() {
		if restore != branch {
			_ = r.Checkout(restore)
		}
	}()

	return r.Run("rebase", "--quiet", "--onto", r.BranchRef(newBase), oldBaseOID)
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
