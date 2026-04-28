package weld

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/williamjaackson/git-weld/internal/git"
)

const rootBranch = "master"

type Service struct {
	repo *git.Repo
	meta *Metadata
}

type StatusEntry struct {
	Branch  string
	Parents []string
}

func Open(startDir string) (*Service, error) {
	repo, err := git.Open(startDir)
	if err != nil {
		return nil, err
	}
	return &Service{
		repo: repo,
		meta: NewMetadata(repo),
	}, nil
}

func (s *Service) NewBranch(branch string) error {
	if branch == "" {
		return errors.New("branch name is required")
	}
	if s.repo.BranchExists(branch) {
		return fmt.Errorf("branch %q already exists", branch)
	}
	if !s.repo.BranchExists(rootBranch) {
		return fmt.Errorf("root branch %q does not exist", rootBranch)
	}
	if err := s.repo.CreateBranch(branch, rootBranch, true); err != nil {
		return err
	}
	return s.meta.MarkManaged(branch)
}

func (s *Service) Stack(branch string, base string, create bool) error {
	oldBaseOIDs, err := s.snapshotEffectiveBaseOIDs()
	if err != nil {
		return err
	}
	if err := s.reconcileMetadata(); err != nil {
		return err
	}

	targetBranch, err := s.repo.ResolveBranch(branch)
	if create {
		targetBranch = branch
	}
	if err != nil {
		return err
	}
	if targetBranch == "" {
		return errors.New("branch name is required")
	}

	base, err = s.resolveBase(base)
	if err != nil {
		return err
	}
	if targetBranch == base {
		return errors.New("branch cannot depend on itself")
	}
	if !s.repo.BranchExists(base) {
		return fmt.Errorf("base branch %q does not exist", base)
	}

	if create {
		if s.repo.BranchExists(targetBranch) {
			return fmt.Errorf("branch %q already exists", targetBranch)
		}
		if err := s.repo.CreateBranch(targetBranch, base, true); err != nil {
			return err
		}
		if base == rootBranch {
			return s.meta.MarkManaged(targetBranch)
		}
		return s.meta.SetParents(targetBranch, []string{base})
	}

	if err := s.requireManagedBranch(targetBranch); err != nil {
		return err
	}
	if base == rootBranch {
		return errors.New("master is implicit; use `git weld unstack` to reset a branch back to master")
	}
	if ok, err := s.hasPath(base, targetBranch); err != nil {
		return err
	} else if ok {
		return fmt.Errorf("adding %q as a parent of %q would create a cycle", base, targetBranch)
	}

	parents, err := s.meta.Parents(targetBranch)
	if err != nil {
		return err
	}
	if len(parents) == 0 {
		if err := s.meta.SetParents(targetBranch, []string{base}); err != nil {
			return err
		}
		return s.syncBranchToCurrentBase(targetBranch, oldBaseOIDs[targetBranch])
	}
	if err := s.meta.AddParent(targetBranch, base); err != nil {
		return err
	}
	return s.syncBranchToCurrentBase(targetBranch, oldBaseOIDs[targetBranch])
}

func (s *Service) Unstack(branch string, base string) error {
	oldBaseOIDs, err := s.snapshotEffectiveBaseOIDs()
	if err != nil {
		return err
	}
	if err := s.reconcileMetadata(); err != nil {
		return err
	}

	targetBranch, err := s.repo.ResolveBranch(branch)
	if err != nil {
		return err
	}
	if err := s.requireManagedBranch(targetBranch); err != nil {
		return err
	}

	base, err = s.resolveBase(base)
	if err != nil {
		return err
	}

	parents, err := s.meta.Parents(targetBranch)
	if err != nil {
		return err
	}
	if len(parents) == 0 {
		return nil
	}

	found := false
	filtered := make([]string, 0, len(parents))
	for _, parent := range parents {
		if parent == base {
			found = true
			continue
		}
		filtered = append(filtered, parent)
	}
	if !found {
		return fmt.Errorf("branch %q does not depend on %q", targetBranch, base)
	}

	if err := s.meta.SetParents(targetBranch, filtered); err != nil {
		return err
	}
	if err := s.syncBranchToCurrentBase(targetBranch, oldBaseOIDs[targetBranch]); err != nil {
		return err
	}
	if len(filtered) <= 1 {
		return s.repo.DeleteBranch(s.repo.SyntheticBranchName(targetBranch))
	}
	return nil
}

func (s *Service) Show(branch string, includeChildren bool) (string, error) {
	if err := s.reconcileMetadata(); err != nil {
		return "", err
	}

	targetBranch, err := s.repo.ResolveBranch(branch)
	if err != nil {
		return "", err
	}
	if err := s.requireManagedOrRoot(targetBranch); err != nil {
		return "", err
	}
	if targetBranch == rootBranch {
		return rootBranch, nil
	}

	lines := []string{targetBranch}
	if includeChildren {
		lines = []string{"(parents)", targetBranch}
	}
	parentLines, err := s.renderParentTree(targetBranch, "", map[string]struct{}{targetBranch: {}})
	if err != nil {
		return "", err
	}
	lines = append(lines, parentLines...)

	if includeChildren {
		lines = append(lines, "", "(children)", targetBranch)
		childLines, err := s.renderChildTree(targetBranch, "", map[string]struct{}{targetBranch: {}})
		if err != nil {
			return "", err
		}
		if len(childLines) == 0 {
			lines = append(lines, "")
		} else {
			lines = append(lines, childLines...)
		}
	}
	return strings.Join(lines, "\n"), nil
}

func (s *Service) Status() ([]StatusEntry, error) {
	if err := s.reconcileMetadata(); err != nil {
		return nil, err
	}

	branches, err := s.meta.ManagedBranches()
	if err != nil {
		return nil, err
	}
	entries := make([]StatusEntry, 0, len(branches))
	for _, branch := range branches {
		if !s.repo.BranchExists(branch) {
			continue
		}
		parents, err := s.meta.Parents(branch)
		if err != nil {
			return nil, err
		}
		entries = append(entries, StatusEntry{Branch: branch, Parents: parents})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Branch < entries[j].Branch })
	return entries, nil
}

func (s *Service) Diff(branch string) (string, error) {
	if err := s.reconcileMetadata(); err != nil {
		return "", err
	}

	targetBranch, err := s.repo.ResolveBranch(branch)
	if err != nil {
		return "", err
	}
	if err := s.requireManagedBranch(targetBranch); err != nil {
		return "", err
	}
	base, err := s.ensureEffectiveBase(targetBranch)
	if err != nil {
		return "", err
	}
	return s.repo.Diff(base, targetBranch)
}

func (s *Service) Sync(branch string, tree bool) error {
	oldBaseOIDs, err := s.snapshotEffectiveBaseOIDs()
	if err != nil {
		return err
	}
	if err := s.reconcileMetadata(); err != nil {
		return err
	}

	clean, err := s.repo.WorkingTreeClean()
	if err != nil {
		return err
	}
	if !clean {
		return errors.New("working tree must be clean before sync")
	}

	targetBranch, err := s.repo.ResolveBranch(branch)
	if err != nil {
		return err
	}
	if err := s.requireManagedBranch(targetBranch); err != nil {
		return err
	}
	if err := s.repo.UpdateLocalRoot(rootBranch); err != nil {
		return err
	}

	order, err := s.syncOrder(targetBranch, tree)
	if err != nil {
		return err
	}
	originalBranch, err := s.repo.CurrentBranch()
	if err != nil {
		return err
	}
	defer func() {
		if originalBranch == "" {
			return
		}
		current, currentErr := s.repo.CurrentBranch()
		if currentErr == nil && current != originalBranch {
			_ = s.repo.Checkout(originalBranch)
		}
	}()

	for _, item := range order {
		if item == rootBranch {
			continue
		}
		base, err := s.ensureEffectiveBase(item)
		if err != nil {
			return err
		}
		newBaseOID, err := s.repo.Output("rev-parse", s.repo.BranchRef(base))
		if err != nil {
			return err
		}
		oldBaseOID := oldBaseOIDs[item]
		if oldBaseOID == "" || oldBaseOID == newBaseOID {
			continue
		}
		if err := s.repo.RebaseBranchOntoFrom(item, oldBaseOID, base); err != nil {
			return fmt.Errorf("sync %s: %w", item, err)
		}
	}
	return nil
}

func (s *Service) ensureEffectiveBase(branch string) (string, error) {
	parents, err := s.meta.Parents(branch)
	if err != nil {
		return "", err
	}
	switch len(parents) {
	case 0:
		_ = s.repo.DeleteBranch(s.repo.SyntheticBranchName(branch))
		return rootBranch, nil
	case 1:
		_ = s.repo.DeleteBranch(s.repo.SyntheticBranchName(branch))
		return parents[0], nil
	default:
		return s.repo.RebuildSyntheticBase(branch, parents)
	}
}

func (s *Service) resolveBase(base string) (string, error) {
	if base != "" {
		return base, nil
	}
	return s.repo.CurrentBranch()
}

func (s *Service) requireManagedBranch(branch string) error {
	if branch == "" {
		return errors.New("branch name is required")
	}
	if !s.repo.BranchExists(branch) {
		return fmt.Errorf("branch %q does not exist", branch)
	}
	if branch == rootBranch {
		return errors.New("master is the root branch and is not managed by weld")
	}
	managed, err := s.meta.IsManaged(branch)
	if err != nil {
		return err
	}
	if !managed {
		return fmt.Errorf("branch %q is not managed by weld", branch)
	}
	return nil
}

func (s *Service) requireManagedOrRoot(branch string) error {
	if branch == rootBranch {
		if !s.repo.BranchExists(rootBranch) {
			return fmt.Errorf("branch %q does not exist", rootBranch)
		}
		return nil
	}
	return s.requireManagedBranch(branch)
}

func (s *Service) directChildren(branch string) ([]string, error) {
	branches, err := s.meta.ManagedBranches()
	if err != nil {
		return nil, err
	}
	children := make([]string, 0)
	for _, candidate := range branches {
		if !s.repo.BranchExists(candidate) {
			continue
		}
		parents, err := s.meta.Parents(candidate)
		if err != nil {
			return nil, err
		}
		for _, parent := range parents {
			if parent == branch {
				children = append(children, candidate)
				break
			}
		}
	}
	sort.Strings(children)
	return children, nil
}

func (s *Service) ancestorClosure(branch string) ([]string, error) {
	seen := map[string]struct{}{}
	order := make([]string, 0)
	var visit func(string) error
	visit = func(node string) error {
		if _, ok := seen[node]; ok {
			return nil
		}
		seen[node] = struct{}{}
		order = append(order, node)
		parents, err := s.meta.Parents(node)
		if err != nil {
			return err
		}
		for _, parent := range parents {
			if parent == rootBranch {
				continue
			}
			if err := visit(parent); err != nil {
				return err
			}
		}
		return nil
	}
	if branch != rootBranch {
		if err := visit(branch); err != nil {
			return nil, err
		}
	}
	return order, nil
}

func (s *Service) descendants(branch string) ([]string, error) {
	seen := map[string]struct{}{}
	queue := []string{branch}
	order := make([]string, 0)
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		children, err := s.directChildren(node)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if _, ok := seen[child]; ok {
				continue
			}
			seen[child] = struct{}{}
			order = append(order, child)
			queue = append(queue, child)
		}
	}
	return order, nil
}

func (s *Service) syncOrder(branch string, tree bool) ([]string, error) {
	ancestors, err := s.topologicalSort(branch)
	if err != nil {
		return nil, err
	}
	if !tree {
		return ancestors, nil
	}

	descendants, err := s.descendants(branch)
	if err != nil {
		return nil, err
	}
	if len(descendants) == 0 {
		return ancestors, nil
	}

	descOrder, err := s.topologicalSort(descendants...)
	if err != nil {
		return nil, err
	}
	combined := append([]string{}, ancestors...)
	for _, item := range descOrder {
		if !contains(combined, item) {
			combined = append(combined, item)
		}
	}
	return combined, nil
}

func (s *Service) topologicalSort(start ...string) ([]string, error) {
	needed := map[string]struct{}{rootBranch: {}}
	var collect func(string) error
	collect = func(node string) error {
		if node == "" {
			return nil
		}
		if _, ok := needed[node]; ok {
			return nil
		}
		needed[node] = struct{}{}
		if node == rootBranch {
			return nil
		}
		parents, err := s.meta.Parents(node)
		if err != nil {
			return err
		}
		if len(parents) == 0 {
			return collect(rootBranch)
		}
		for _, parent := range parents {
			if err := collect(parent); err != nil {
				return err
			}
		}
		return nil
	}
	for _, node := range start {
		if err := collect(node); err != nil {
			return nil, err
		}
	}

	inDegree := map[string]int{}
	children := map[string][]string{}
	for node := range needed {
		if _, ok := inDegree[node]; !ok {
			inDegree[node] = 0
		}
		if node == rootBranch {
			continue
		}
		parents, err := s.meta.Parents(node)
		if err != nil {
			return nil, err
		}
		if len(parents) == 0 {
			parents = []string{rootBranch}
		}
		for _, parent := range parents {
			if _, ok := needed[parent]; !ok {
				continue
			}
			inDegree[node]++
			children[parent] = append(children[parent], node)
		}
	}

	queue := make([]string, 0)
	for node, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, node)
		}
	}
	sort.Strings(queue)

	order := make([]string, 0, len(needed))
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		order = append(order, node)
		for _, child := range children[node] {
			inDegree[child]--
			if inDegree[child] == 0 {
				queue = append(queue, child)
				sort.Strings(queue)
			}
		}
	}

	if len(order) != len(needed) {
		return nil, errors.New("dependency cycle detected")
	}
	return order, nil
}

func (s *Service) hasPath(from string, to string) (bool, error) {
	if from == to {
		return true, nil
	}
	if from == rootBranch {
		return false, nil
	}
	parents, err := s.meta.Parents(from)
	if err != nil {
		return false, err
	}
	for _, parent := range parents {
		if parent == to {
			return true, nil
		}
		found, err := s.hasPath(parent, to)
		if err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) reconcileMetadata() error {
	branches, err := s.meta.ManagedBranches()
	if err != nil {
		return err
	}
	for _, branch := range branches {
		if branch == rootBranch {
			_ = s.meta.Unmanage(branch)
			continue
		}
		if !s.repo.BranchExists(branch) {
			if err := s.meta.Unmanage(branch); err != nil {
				return err
			}
			_ = s.repo.DeleteBranch(s.repo.SyntheticBranchName(branch))
			continue
		}

		parents, err := s.meta.Parents(branch)
		if err != nil {
			return err
		}
		valid := make([]string, 0, len(parents))
		for _, parent := range parents {
			if parent == rootBranch {
				continue
			}
			if s.repo.BranchExists(parent) {
				valid = append(valid, parent)
			}
		}
		if err := s.meta.SetParents(branch, valid); err != nil {
			return err
		}
		if len(valid) <= 1 {
			_ = s.repo.DeleteBranch(s.repo.SyntheticBranchName(branch))
		}
	}
	return nil
}

func (s *Service) snapshotEffectiveBaseOIDs() (map[string]string, error) {
	branches, err := s.meta.ManagedBranches()
	if err != nil {
		return nil, err
	}
	oldBases := map[string]string{}
	for _, branch := range branches {
		if !s.repo.BranchExists(branch) {
			continue
		}
		base, err := s.currentEffectiveBase(branch)
		if err != nil {
			continue
		}
		oid, err := s.repo.Output("rev-parse", s.repo.BranchRef(base))
		if err != nil {
			continue
		}
		oldBases[branch] = oid
	}
	return oldBases, nil
}

func (s *Service) currentEffectiveBase(branch string) (string, error) {
	parents, err := s.meta.Parents(branch)
	if err != nil {
		return "", err
	}
	switch len(parents) {
	case 0:
		return rootBranch, nil
	case 1:
		return parents[0], nil
	default:
		if s.repo.BranchExists(s.repo.SyntheticBranchName(branch)) {
			return s.repo.SyntheticBranchName(branch), nil
		}
		return s.repo.RebuildSyntheticBase(branch, parents)
	}
}

func (s *Service) syncBranchToCurrentBase(branch string, oldBaseOID string) error {
	clean, err := s.repo.WorkingTreeClean()
	if err != nil {
		return err
	}
	if !clean {
		return errors.New("working tree must be clean before sync")
	}

	base, err := s.ensureEffectiveBase(branch)
	if err != nil {
		return err
	}
	newBaseOID, err := s.repo.Output("rev-parse", s.repo.BranchRef(base))
	if err != nil {
		return err
	}
	if oldBaseOID == "" || oldBaseOID == newBaseOID {
		return nil
	}
	return s.repo.RebaseBranchOntoFrom(branch, oldBaseOID, base)
}

func (s *Service) renderParentTree(branch string, prefix string, seen map[string]struct{}) ([]string, error) {
	parents, err := s.meta.Parents(branch)
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0)
	for i, parent := range parents {
		if parent == rootBranch {
			continue
		}
		if _, ok := seen[parent]; ok {
			continue
		}
		connector := "├─ "
		nextPrefix := prefix + "│  "
		if i == len(parents)-1 {
			connector = "└─ "
			nextPrefix = prefix + "   "
		}
		lines = append(lines, prefix+connector+parent)
		nextSeen := cloneSeen(seen)
		nextSeen[parent] = struct{}{}
		descendants, err := s.renderParentTree(parent, nextPrefix, nextSeen)
		if err != nil {
			return nil, err
		}
		lines = append(lines, descendants...)
	}
	return lines, nil
}

func (s *Service) renderChildTree(branch string, prefix string, seen map[string]struct{}) ([]string, error) {
	children, err := s.directChildren(branch)
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0)
	for i, child := range children {
		if _, ok := seen[child]; ok {
			continue
		}
		connector := "├─ "
		nextPrefix := prefix + "│  "
		if i == len(children)-1 {
			connector = "└─ "
			nextPrefix = prefix + "   "
		}
		lines = append(lines, prefix+connector+child)
		nextSeen := cloneSeen(seen)
		nextSeen[child] = struct{}{}
		subtree, err := s.renderChildTree(child, nextPrefix, nextSeen)
		if err != nil {
			return nil, err
		}
		lines = append(lines, subtree...)
	}
	return lines, nil
}

func cloneSeen(seen map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(seen))
	for key := range seen {
		out[key] = struct{}{}
	}
	return out
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
