package weld

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/williamjaackson/git-weld/internal/git"
)

type Service struct {
	repo     *git.Repo
	meta     *Metadata
	settings Settings
	reporter func(string)
}

type StatusEntry struct {
	Branch     string
	Upstream   []string
	SyncAction string
	ShipAction string
	Affects    []string
}

type ShipResult struct {
	BranchesPushed   []string
	SyntheticPushed  []string
	SyntheticDeleted []string
	PRBasesUpdated   []string
}

type SyncMode int

const (
	SyncModeDefault SyncMode = iota
	SyncModeLocal
	SyncModeRemote
)

type PRResult struct {
	Number int
	URL    string
	Base   string
	Head   string
}

type refSnapshot struct {
	Ref    string
	Exists bool
	OID    string
}

func Open(startDir string) (*Service, error) {
	repo, err := git.Open(startDir)
	if err != nil {
		return nil, err
	}
	meta := NewMetadata(repo)
	settings, err := meta.Settings()
	if err != nil {
		return nil, err
	}
	return &Service{
		repo:     repo,
		meta:     meta,
		settings: settings,
	}, nil
}

func (s *Service) rootBranch() string {
	return s.settings.RootBranch
}

func (s *Service) remoteEnabled() bool {
	return !s.settings.RemoteOff
}

func (s *Service) remoteName() string {
	return s.settings.RemoteName
}

func (s *Service) Settings() Settings {
	return s.settings
}

func (s *Service) CurrentBranch() (string, error) {
	return s.repo.CurrentBranch()
}

func (s *Service) SetReporter(reporter func(string)) {
	s.reporter = reporter
}

func (s *Service) stepf(format string, args ...any) {
	if s.reporter != nil {
		s.reporter(fmt.Sprintf(format, args...))
	}
}

func (s *Service) Init(rootBranch string, remoteName string, remoteDisabled bool) error {
	rootBranch = strings.TrimSpace(rootBranch)
	if rootBranch == "" {
		return errors.New("main branch is required")
	}
	if !s.repo.BranchExists(rootBranch) {
		return fmt.Errorf("branch %q does not exist", rootBranch)
	}

	remoteName = strings.TrimSpace(remoteName)
	if !remoteDisabled {
		if remoteName == "" {
			remoteName = DefaultSettings().RemoteName
		}
		if err := s.repo.RequireRemote(remoteName); err != nil {
			return err
		}
	}

	settings := Settings{
		RootBranch: rootBranch,
		RemoteName: remoteName,
		RemoteOff:  remoteDisabled,
	}
	if err := s.meta.SaveSettings(settings); err != nil {
		return err
	}
	s.settings = settings
	s.stepf("configured main branch: %s", rootBranch)
	if remoteDisabled {
		s.stepf("disabled remote features")
	} else {
		s.stepf("configured remote: %s", remoteName)
	}
	return nil
}

func (s *Service) InitInteractive(in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	current := s.rootBranch()
	if current == "" {
		current = DefaultSettings().RootBranch
	}

	if _, err := fmt.Fprintf(out, "main branch [%s]: ", current); err != nil {
		return err
	}
	rootInput, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	root := strings.TrimSpace(rootInput)
	if root == "" {
		root = current
	}

	defaultRemote := s.remoteName()
	if s.remoteEnabled() {
		if defaultRemote == "" {
			defaultRemote = DefaultSettings().RemoteName
		}
		if _, err := fmt.Fprintf(out, "remote name (or 'none') [%s]: ", defaultRemote); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprint(out, "remote name (or 'none') [none]: "); err != nil {
			return err
		}
	}
	remoteInput, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	remote := strings.TrimSpace(remoteInput)
	remoteDisabled := false
	switch strings.ToLower(remote) {
	case "", "origin":
		if !s.remoteEnabled() && remote == "" {
			remoteDisabled = true
		}
	case "none", "no", "off", "disabled":
		remoteDisabled = true
		remote = ""
	default:
		remoteDisabled = false
	}
	if remote == "" && !remoteDisabled {
		if s.remoteEnabled() && s.remoteName() != "" {
			remote = s.remoteName()
		} else {
			remote = DefaultSettings().RemoteName
		}
	}
	if !s.remoteEnabled() && strings.TrimSpace(remoteInput) == "" {
		remoteDisabled = true
		remote = ""
	}

	return s.Init(root, remote, remoteDisabled)
}

func (s *Service) NewBranch(branch string) error {
	if branch == "" {
		return errors.New("branch name is required")
	}
	if s.repo.BranchExists(branch) {
		return fmt.Errorf("branch %q already exists", branch)
	}
	if !s.repo.BranchExists(s.rootBranch()) {
		return fmt.Errorf("root branch %q does not exist", s.rootBranch())
	}
	s.stepf("creating branch %s from %s", branch, s.rootBranch())
	if err := s.createBranchPreservingChanges(branch, s.rootBranch(), true); err != nil {
		return err
	}
	s.stepf("tracking %s with implicit parent %s", branch, s.rootBranch())
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
		s.stepf("creating branch %s from %s", targetBranch, base)
		if err := s.createBranchPreservingChanges(targetBranch, base, true); err != nil {
			return err
		}
		if base == s.rootBranch() {
			s.stepf("tracking %s with implicit parent %s", targetBranch, s.rootBranch())
			return s.meta.MarkManaged(targetBranch)
		}
		s.stepf("adding parent %s to %s", base, targetBranch)
		return s.meta.SetParents(targetBranch, []string{base})
	}

	if err := s.requireManagedBranch(targetBranch); err != nil {
		return err
	}
	if base == s.rootBranch() {
		return fmt.Errorf("%s is implicit; use `git weld unstack` to reset a branch back to %s", s.rootBranch(), s.rootBranch())
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
		s.stepf("replacing implicit parent %s with %s on %s", s.rootBranch(), base, targetBranch)
		if err := s.meta.SetParents(targetBranch, []string{base}); err != nil {
			return err
		}
		return s.syncBranchToCurrentBase(targetBranch, oldBaseOIDs[targetBranch])
	}
	s.stepf("adding parent %s to %s", base, targetBranch)
	if err := s.meta.AddParent(targetBranch, base); err != nil {
		return err
	}
	return s.syncBranchToCurrentBase(targetBranch, oldBaseOIDs[targetBranch])
}

func (s *Service) createBranchPreservingChanges(branch string, base string, switchToNew bool) (err error) {
	if !switchToNew {
		return s.repo.CreateBranch(branch, base, false)
	}
	originalBranch, err := s.repo.CurrentBranch()
	if err != nil {
		return err
	}
	stashed, err := s.repo.StashPushIfNeeded("git-weld branch create")
	if err != nil {
		return err
	}
	if stashed {
		s.stepf("stashed local changes")
	}
	defer func() {
		if !stashed {
			return
		}
		if err != nil {
			current, currentErr := s.repo.CurrentBranch()
			if currentErr == nil && current != originalBranch {
				_ = s.repo.Checkout(originalBranch)
			}
		}
		if restoreErr := s.repo.StashPop(); restoreErr != nil && err == nil {
			err = restoreErr
		}
		if stashed && err == nil {
			s.stepf("restored local changes")
		}
	}()
	if err := s.repo.CreateBranch(branch, base, true); err != nil {
		return err
	}
	s.stepf("switched to %s", branch)
	return nil
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
	s.stepf("removing parent %s from %s", base, targetBranch)

	if err := s.meta.SetParents(targetBranch, filtered); err != nil {
		return err
	}
	if err := s.syncBranchToCurrentBase(targetBranch, oldBaseOIDs[targetBranch]); err != nil {
		return err
	}
	return nil
}

func (s *Service) Beside(target string, source string) error {
	oldBaseOIDs, err := s.snapshotEffectiveBaseOIDs()
	if err != nil {
		return err
	}
	if err := s.reconcileMetadata(); err != nil {
		return err
	}

	targetBranch, err := s.repo.ResolveBranch(target)
	if err != nil {
		return err
	}
	sourceBranch, err := s.repo.ResolveBranch(source)
	if err != nil {
		return err
	}
	if targetBranch == sourceBranch {
		return errors.New("target and source branch must be different")
	}
	if err := s.requireManagedBranch(targetBranch); err != nil {
		return err
	}
	if err := s.requireManagedOrRoot(sourceBranch); err != nil {
		return err
	}

	sourceParents, err := s.meta.Parents(sourceBranch)
	if err != nil {
		return err
	}
	if len(sourceParents) == 0 {
		s.stepf("no explicit parents to add from %s to %s", sourceBranch, targetBranch)
		return nil
	}
	for _, parent := range sourceParents {
		if parent == targetBranch {
			return errors.New("branch cannot depend on itself")
		}
		if ok, err := s.hasPath(parent, targetBranch); err != nil {
			return err
		} else if ok {
			return fmt.Errorf("adding parents from %q to %q would create a cycle", sourceBranch, targetBranch)
		}
	}

	targetParents, err := s.meta.Parents(targetBranch)
	if err != nil {
		return err
	}
	s.stepf("adding parents of %s to %s", sourceBranch, targetBranch)
	if err := s.meta.SetParents(targetBranch, append(targetParents, sourceParents...)); err != nil {
		return err
	}
	return s.syncBranchToCurrentBase(targetBranch, oldBaseOIDs[targetBranch])
}

func (s *Service) Prepend(branch string, beside string, switchToNew bool) error {
	oldBaseOIDs, err := s.snapshotEffectiveBaseOIDs()
	if err != nil {
		return err
	}
	if err := s.reconcileMetadata(); err != nil {
		return err
	}

	targetBranch, err := s.repo.CurrentBranch()
	if err != nil {
		return err
	}
	if err := s.requireManagedBranch(targetBranch); err != nil {
		return err
	}
	newBranch := strings.TrimSpace(branch)
	if newBranch == "" {
		return errors.New("branch name is required")
	}
	if s.repo.BranchExists(newBranch) {
		return fmt.Errorf("branch %q already exists", newBranch)
	}

	sourceBranch := targetBranch
	replaceParents := true
	if strings.TrimSpace(beside) != "" {
		sourceBranch, err = s.repo.ResolveBranch(beside)
		if err != nil {
			return err
		}
		if err := s.requireManagedOrRoot(sourceBranch); err != nil {
			return err
		}
		ok, err := s.hasPath(targetBranch, sourceBranch)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("branch %q is not upstream from %q", sourceBranch, targetBranch)
		}
		replaceParents = false
	}

	inheritedParents, err := s.meta.Parents(sourceBranch)
	if err != nil {
		return err
	}
	createBase, err := s.currentEffectiveBase(sourceBranch)
	if err != nil {
		return err
	}
	s.stepf("creating branch %s from %s", newBranch, createBase)
	if err := s.createBranchPreservingChanges(newBranch, createBase, switchToNew); err != nil {
		return err
	}
	if len(inheritedParents) == 0 {
		s.stepf("tracking %s with implicit parent %s", newBranch, s.rootBranch())
		if err := s.meta.MarkManaged(newBranch); err != nil {
			return err
		}
	} else {
		s.stepf("adding parents of %s to %s", sourceBranch, newBranch)
		if err := s.meta.SetParents(newBranch, inheritedParents); err != nil {
			return err
		}
	}

	if replaceParents {
		s.stepf("replacing parents of %s with %s", targetBranch, newBranch)
		if err := s.meta.SetParents(targetBranch, []string{newBranch}); err != nil {
			return err
		}
	} else {
		s.stepf("adding parent %s to %s", newBranch, targetBranch)
		if err := s.meta.AddParent(targetBranch, newBranch); err != nil {
			return err
		}
	}
	return s.syncBranchToCurrentBase(targetBranch, oldBaseOIDs[targetBranch])
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
	if targetBranch == s.rootBranch() {
		return s.rootBranch(), nil
	}

	lines := []string{targetBranch}
	if includeChildren {
		lines = []string{"(upstream)", targetBranch}
	}
	parentLines, err := s.renderParentTree(targetBranch, "", map[string]struct{}{targetBranch: {}})
	if err != nil {
		return "", err
	}
	lines = append(lines, parentLines...)

	if includeChildren {
		lines = append(lines, "", "(downstream)", targetBranch)
		childLines, err := s.renderChildTree(targetBranch, "", map[string]struct{}{targetBranch: {}})
		if err != nil {
			return "", err
		}
		if len(childLines) > 0 {
			lines = append(lines, childLines...)
		}
	}
	return strings.Join(lines, "\n"), nil
}

func (s *Service) Status(branch string, tree bool) ([]StatusEntry, error) {
	if err := s.reconcileMetadata(); err != nil {
		return nil, err
	}
	if s.remoteEnabled() && strings.TrimSpace(s.remoteName()) != "" && s.repo.HasRemote(s.remoteName()) {
		if err := s.repo.FetchQuiet(s.remoteName()); err != nil {
			return nil, err
		}
	}
	conflicted, err := s.inRebaseConflict()
	if err != nil {
		return nil, err
	}

	targetBranch := ""
	if branch != "" {
		targetBranch, err = s.repo.ResolveBranch(branch)
		if err != nil {
			return nil, err
		}
		if err := s.requireManagedBranch(targetBranch); err != nil {
			return nil, err
		}
	} else {
		targetBranch, err = s.repo.CurrentBranch()
		if err != nil {
			return nil, err
		}
		if err := s.requireManagedBranch(targetBranch); err != nil {
			return nil, err
		}
	}
	actionScope, err := s.syncOrder(targetBranch, tree)
	if err != nil {
		return nil, err
	}
	syncActions, err := s.statusSyncActionsForScope(actionScope)
	if err != nil {
		return nil, err
	}
	displayScope := actionScope
	if !tree {
		displayScope = []string{targetBranch}
	}
	entries := make([]StatusEntry, 0, len(displayScope))
	for _, item := range displayScope {
		if item == s.rootBranch() || !s.repo.BranchExists(item) {
			continue
		}
		parents, err := s.meta.Parents(item)
		if err != nil {
			return nil, err
		}
		upstream := append([]string{}, parents...)
		if len(upstream) == 0 {
			upstream = []string{s.rootBranch()}
		}
		shipAction, err := s.statusShipAction(item, conflicted, syncActions)
		if err != nil {
			return nil, err
		}
		affects, err := s.descendants(item)
		if err != nil {
			return nil, err
		}
		entries = append(entries, StatusEntry{
			Branch:     item,
			Upstream:   upstream,
			SyncAction: syncActions[item],
			ShipAction: shipAction,
			Affects:    affects,
		})
	}
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

func (s *Service) Sync(branch string, tree bool, mode SyncMode) error {
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

	order, err := s.syncOrder(targetBranch, tree)
	if err != nil {
		return err
	}
	s.stepf("syncing %s (%s)", targetBranch, syncScopeLabel(tree))
	originalBranch, err := s.repo.CurrentBranch()
	if err != nil {
		return err
	}
	snapshots, err := s.snapshotRefsForSync(order)
	if err != nil {
		return err
	}
	rollbackNeeded := true
	defer func() {
		if !rollbackNeeded {
			if originalBranch == "" {
				return
			}
			current, currentErr := s.repo.CurrentBranch()
			if currentErr == nil && current != originalBranch {
				_ = s.repo.Checkout(originalBranch)
			}
			return
		}
		_ = s.repo.RebaseAbort()
		_ = s.restoreSnapshots(snapshots)
		if originalBranch != "" {
			_ = s.repo.Checkout(originalBranch)
		}
	}()

	if err := s.refreshBranchesForSync(order, mode); err != nil {
		return err
	}

	for _, item := range order {
		if item == s.rootBranch() {
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
			s.stepf("skipping %s; already up to date", item)
			continue
		}
		if base == s.repo.SyntheticBranchName(item) {
			s.stepf("rebasing %s onto welded base", item)
		} else {
			s.stepf("rebasing %s onto %s", item, base)
		}
		if err := s.repo.RebaseBranchOntoFrom(item, oldBaseOID, base); err != nil {
			return fmt.Errorf("sync %s: %w", item, err)
		}
	}
	rollbackNeeded = false
	s.stepf("sync complete")
	return nil
}

func (s *Service) Ship(branch string, tree bool, mode SyncMode) (*ShipResult, error) {
	if err := s.reconcileMetadata(); err != nil {
		return nil, err
	}
	if !s.remoteEnabled() {
		return nil, errors.New("remote features are disabled; run `git weld init` to configure a remote")
	}
	if err := s.repo.RequireRemote(s.remoteName()); err != nil {
		return nil, err
	}

	targetBranch, err := s.repo.ResolveBranch(branch)
	if err != nil {
		return nil, err
	}
	if err := s.requireManagedBranch(targetBranch); err != nil {
		return nil, err
	}

	if needed, err := s.shipNeedsSync(targetBranch, tree, mode); err != nil {
		return nil, err
	} else if needed {
		if err := s.Sync(targetBranch, tree, mode); err != nil {
			return nil, err
		}
	}
	s.stepf("shipping %s (%s)", targetBranch, syncScopeLabel(tree))

	toPush, syntheticRefs, syntheticDeletes, err := s.shipPlan(targetBranch, tree)
	if err != nil {
		return nil, err
	}

	result := &ShipResult{
		BranchesPushed:   make([]string, 0, len(toPush)),
		SyntheticPushed:  make([]string, 0, len(syntheticRefs)),
		SyntheticDeleted: make([]string, 0, len(syntheticDeletes)),
		PRBasesUpdated:   make([]string, 0, len(toPush)),
	}
	for _, ref := range syntheticRefs {
		s.stepf("pushing welded base")
		if err := s.repo.PushBranch(s.remoteName(), ref); err != nil {
			return nil, err
		}
		result.SyntheticPushed = append(result.SyntheticPushed, ref)
	}
	for _, item := range toPush {
		s.stepf("pushing branch %s", item)
		if err := s.repo.PushBranch(s.remoteName(), item); err != nil {
			return nil, err
		}
		result.BranchesPushed = append(result.BranchesPushed, item)
		updated, err := s.refreshPRBaseIfExists(item)
		if err != nil {
			return nil, err
		}
		if updated {
			s.stepf("updated PR base for %s", item)
			result.PRBasesUpdated = append(result.PRBasesUpdated, item)
		}
	}
	for _, ref := range syntheticDeletes {
		deleted, err := s.repo.DeleteRemoteBranch(s.remoteName(), ref)
		if err != nil {
			return nil, err
		}
		if deleted {
			s.stepf("removed remote welded base")
			result.SyntheticDeleted = append(result.SyntheticDeleted, ref)
		}
	}
	s.stepf("ship complete")
	return result, nil
}

func (s *Service) shipNeedsSync(branch string, tree bool, mode SyncMode) (bool, error) {
	conflicted, err := s.inRebaseConflict()
	if err != nil {
		return false, err
	}
	if mode != SyncModeLocal && s.remoteEnabled() && strings.TrimSpace(s.remoteName()) != "" && s.repo.HasRemote(s.remoteName()) {
		if err := s.repo.FetchQuiet(s.remoteName()); err != nil {
			return false, err
		}
	}
	order, err := s.syncOrder(branch, tree)
	if err != nil {
		return false, err
	}
	actions, err := s.statusSyncActionsForScope(order)
	if err != nil {
		return false, err
	}
	for _, item := range order {
		if item == s.rootBranch() {
			continue
		}
		if conflicted || actions[item] != "none" {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) PR(branch string, title string, body string, draft bool, web bool) (*PRResult, error) {
	if err := s.reconcileMetadata(); err != nil {
		return nil, err
	}
	if !s.remoteEnabled() {
		return nil, errors.New("remote features are disabled; run `git weld init` to configure a remote")
	}
	targetBranch, err := s.repo.ResolveBranch(branch)
	if err != nil {
		return nil, err
	}
	if err := s.requireManagedBranch(targetBranch); err != nil {
		return nil, err
	}

	if _, err := s.Ship(targetBranch, false, SyncModeDefault); err != nil {
		return nil, err
	}

	base, err := s.prBase(targetBranch)
	if err != nil {
		return nil, err
	}

	existing, err := s.findPR(targetBranch)
	if err != nil {
		return nil, err
	}

	if existing.Number == 0 {
		s.stepf("creating PR for %s with base %s", targetBranch, base)
		createArgs := []string{"pr", "create", "--base", base, "--head", targetBranch}
		if title == "" && body == "" {
			createArgs = append(createArgs, "--fill")
		} else {
			if title != "" {
				createArgs = append(createArgs, "--title", title)
			}
			if body != "" {
				createArgs = append(createArgs, "--body", body)
			}
		}
		if draft {
			createArgs = append(createArgs, "--draft")
		}
		if _, err := s.repo.GH(createArgs...); err != nil {
			return nil, err
		}
		existing, err = s.findPR(targetBranch)
		if err != nil {
			return nil, err
		}
	}

	currentBody := body
	if currentBody == "" {
		existingDetails, err := s.viewPR(existing.Number)
		if err != nil {
			return nil, err
		}
		currentBody = stripPRStackSection(existingDetails.Body)
	}
	stackBody, err := s.buildPRBody(targetBranch, currentBody)
	if err != nil {
		return nil, err
	}

	s.stepf("updating PR #%d base to %s", existing.Number, base)
	editArgs := []string{"pr", "edit", fmt.Sprintf("%d", existing.Number), "--base", base}
	if title != "" {
		editArgs = append(editArgs, "--title", title)
	}
	editArgs = append(editArgs, "--body", stackBody)
	if _, err := s.repo.GH(editArgs...); err != nil {
		return nil, err
	}
	if web {
		s.stepf("opening PR #%d in browser", existing.Number)
		if _, err := s.repo.GH("pr", "view", fmt.Sprintf("%d", existing.Number), "--web"); err != nil {
			return nil, err
		}
	}
	existing.BaseRefName = base
	return &PRResult{
		Number: existing.Number,
		URL:    existing.URL,
		Base:   existing.BaseRefName,
		Head:   targetBranch,
	}, nil
}

func (s *Service) ensureEffectiveBase(branch string) (string, error) {
	parents, err := s.meta.Parents(branch)
	if err != nil {
		return "", err
	}
	switch len(parents) {
	case 0:
		if s.repo.BranchExists(s.repo.SyntheticBranchName(branch)) {
			s.stepf("collapsing welded base")
			_ = s.repo.DeleteBranch(s.repo.SyntheticBranchName(branch))
		}
		return s.rootBranch(), nil
	case 1:
		if s.repo.BranchExists(s.repo.SyntheticBranchName(branch)) {
			s.stepf("collapsing welded base")
			_ = s.repo.DeleteBranch(s.repo.SyntheticBranchName(branch))
		}
		return parents[0], nil
	default:
		if reusable, err := s.syntheticBaseReusable(branch, parents); err != nil {
			return "", err
		} else if reusable {
			return s.repo.SyntheticBranchName(branch), nil
		}
		s.stepf("rebuilding welded base")
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
	if branch == s.rootBranch() {
		return fmt.Errorf("%s is the root branch and is not managed by weld", s.rootBranch())
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
	if branch == s.rootBranch() {
		if !s.repo.BranchExists(s.rootBranch()) {
			return fmt.Errorf("branch %q does not exist", s.rootBranch())
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
			if parent == s.rootBranch() {
				continue
			}
			if err := visit(parent); err != nil {
				return err
			}
		}
		return nil
	}
	if branch != s.rootBranch() {
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

func (s *Service) refreshBranchesForSync(order []string, mode SyncMode) error {
	if mode == SyncModeLocal || !s.remoteEnabled() || strings.TrimSpace(s.remoteName()) == "" || !s.repo.HasRemote(s.remoteName()) {
		return nil
	}
	s.stepf("fetching %s", s.remoteName())
	if err := s.repo.Fetch(s.remoteName()); err != nil {
		return err
	}
	for _, branch := range order {
		if branch == s.rootBranch() {
			if mode == SyncModeRemote {
				s.stepf("refreshing %s from %s/%s", s.rootBranch(), s.remoteName(), s.rootBranch())
				if err := s.repo.UpdateLocalRoot(s.remoteName(), s.rootBranch()); err != nil {
					return err
				}
			}
			continue
		}
		if err := s.repo.EnsureTrackingBranchIfRemoteExists(s.remoteName(), branch); err != nil {
			return err
		}
		s.stepf("refreshing tracking branch for %s", branch)
		if err := s.repo.UpdateLocalBranchFromTracking(branch); err != nil {
			return err
		}
	}
	return nil
}

func syncScopeLabel(tree bool) string {
	if tree {
		return "upstream + downstream"
	}
	return "upstream"
}

func (s *Service) inRebaseConflict() (bool, error) {
	return s.repo.HasRef("REBASE_HEAD"), nil
}

func (s *Service) statusSyncAction(branch string, conflicted bool) (string, error) {
	if conflicted {
		return "conflicted", nil
	}
	order, err := s.syncOrder(branch, false)
	if err != nil {
		return "", err
	}
	actions, err := s.statusSyncActionsForScope(order)
	if err != nil {
		return "", err
	}
	return actions[branch], nil
}

func (s *Service) statusShipAction(branch string, conflicted bool, syncActions map[string]string) (string, error) {
	if conflicted {
		return "conflicted", nil
	}
	order, err := s.syncOrder(branch, false)
	if err != nil {
		return "", err
	}
	aggregate := "none"
	for _, item := range order {
		if item == s.rootBranch() {
			continue
		}
		if syncActions[item] != "none" {
			return "sync-first", nil
		}
		action, err := s.statusDirectShipAction(item)
		if err != nil {
			return "", err
		}
		switch action {
		case "force-push":
			aggregate = "force-push"
		case "push":
			if aggregate == "none" {
				aggregate = "push"
			}
		case "sync-first":
			return "sync-first", nil
		}
	}
	return aggregate, nil
}

func (s *Service) statusDirectShipAction(branch string) (string, error) {
	if !s.remoteEnabled() || strings.TrimSpace(s.remoteName()) == "" || !s.repo.HasRemote(s.remoteName()) {
		return "none", nil
	}
	exists, err := s.repo.RemoteBranchExists(s.remoteName(), branch)
	if err != nil {
		return "", err
	}
	if !exists {
		return "push", nil
	}
	remoteRef := s.repo.RemoteBranchRef(s.remoteName(), branch)
	localRef := s.repo.BranchRef(branch)
	remoteIsAncestor, err := s.repo.IsRefAncestor(remoteRef, localRef)
	if err != nil {
		return "", err
	}
	localIsAncestor, err := s.repo.IsRefAncestor(localRef, remoteRef)
	if err != nil {
		return "", err
	}
	switch {
	case remoteIsAncestor && localIsAncestor:
		return "none", nil
	case remoteIsAncestor:
		return "push", nil
	case localIsAncestor:
		return "sync-first", nil
	default:
		return "force-push", nil
	}
}

func (s *Service) statusSyncActionsForScope(order []string) (map[string]string, error) {
	changed := map[string]bool{}
	actions := map[string]string{}
	for _, item := range order {
		if item == s.rootBranch() {
			continue
		}
		update, err := s.statusNeedsTrackingUpdate(item)
		if err != nil {
			return nil, err
		}
		rebase, err := s.statusNeedsRebaseWithChangedAncestors(item, changed)
		if err != nil {
			return nil, err
		}
		switch {
		case update && rebase:
			actions[item] = "update+rebase"
		case update:
			actions[item] = "update"
		case rebase:
			actions[item] = "rebase"
		default:
			actions[item] = "none"
		}
		changed[item] = update || rebase
	}
	return actions, nil
}

func (s *Service) statusNeedsTrackingUpdate(branch string) (bool, error) {
	if !s.remoteEnabled() || strings.TrimSpace(s.remoteName()) == "" || !s.repo.HasRemote(s.remoteName()) {
		return false, nil
	}
	trackingRef, err := s.repo.TrackingRef(branch)
	if err != nil {
		return false, err
	}
	if trackingRef == "" {
		if err := s.repo.EnsureTrackingBranchIfRemoteExists(s.remoteName(), branch); err != nil {
			return false, err
		}
		trackingRef, err = s.repo.TrackingRef(branch)
		if err != nil {
			return false, err
		}
	}
	if trackingRef == "" || !s.repo.HasRef(trackingRef) || !s.repo.HasRef(s.repo.BranchRef(branch)) {
		return false, nil
	}
	_, remoteOnly, err := s.repo.UniqueTrackingCommits(branch)
	if err != nil {
		return false, err
	}
	return remoteOnly > 0, nil
}

func (s *Service) statusNeedsRebase(branch string) (bool, error) {
	base, err := s.currentEffectiveBase(branch)
	if err != nil {
		return false, nil
	}
	headOID, err := s.repo.BranchOID(branch)
	if err != nil {
		return false, err
	}
	baseOID, err := s.repo.BranchOID(base)
	if err != nil {
		return false, err
	}
	mergeBase, err := s.repo.Output("merge-base", s.repo.BranchRef(branch), s.repo.BranchRef(base))
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(headOID) == strings.TrimSpace(baseOID) {
		return false, nil
	}
	return strings.TrimSpace(mergeBase) != strings.TrimSpace(baseOID), nil
}

func (s *Service) statusNeedsRebaseWithChangedAncestors(branch string, changed map[string]bool) (bool, error) {
	parents, err := s.meta.Parents(branch)
	if err != nil {
		return false, err
	}
	for _, parent := range parents {
		if changed[parent] {
			return true, nil
		}
	}
	return s.statusNeedsRebase(branch)
}

func (s *Service) syntheticBaseReusable(branch string, parents []string) (bool, error) {
	synthetic := s.repo.SyntheticBranchName(branch)
	if !s.repo.BranchExists(synthetic) {
		return false, nil
	}
	expected := make([]string, 0, len(parents))
	for _, parent := range parents {
		oid, err := s.repo.BranchOID(parent)
		if err != nil {
			return false, err
		}
		expected = append(expected, oid)
	}
	actual, err := s.repo.CommitParentOIDs(s.repo.BranchRef(synthetic))
	if err != nil {
		return false, err
	}
	if len(actual) != len(expected) {
		return false, nil
	}
	for i := range expected {
		if actual[i] != expected[i] {
			return false, nil
		}
	}
	return true, nil
}

func (s *Service) snapshotRefsForSync(order []string) ([]refSnapshot, error) {
	refs := map[string]struct{}{}
	for _, branch := range order {
		refs[s.repo.BranchRef(branch)] = struct{}{}
		if branch == s.rootBranch() {
			continue
		}
		refs[s.repo.BranchRef(s.repo.SyntheticBranchName(branch))] = struct{}{}
	}

	snapshots := make([]refSnapshot, 0, len(refs))
	for ref := range refs {
		if !s.repo.HasRef(ref) {
			snapshots = append(snapshots, refSnapshot{Ref: ref, Exists: false})
			continue
		}
		oid, err := s.repo.RefOID(ref)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, refSnapshot{Ref: ref, Exists: true, OID: oid})
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].Ref < snapshots[j].Ref })
	return snapshots, nil
}

func (s *Service) restoreSnapshots(snapshots []refSnapshot) error {
	for _, snapshot := range snapshots {
		if snapshot.Exists {
			if err := s.repo.UpdateRef(snapshot.Ref, snapshot.OID, ""); err != nil {
				return err
			}
			continue
		}
		if err := s.repo.DeleteRef(snapshot.Ref, ""); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) topologicalSort(start ...string) ([]string, error) {
	needed := map[string]struct{}{s.rootBranch(): {}}
	var collect func(string) error
	collect = func(node string) error {
		if node == "" {
			return nil
		}
		if _, ok := needed[node]; ok {
			return nil
		}
		needed[node] = struct{}{}
		if node == s.rootBranch() {
			return nil
		}
		parents, err := s.meta.Parents(node)
		if err != nil {
			return err
		}
		if len(parents) == 0 {
			return collect(s.rootBranch())
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
		if node == s.rootBranch() {
			continue
		}
		parents, err := s.meta.Parents(node)
		if err != nil {
			return nil, err
		}
		if len(parents) == 0 {
			parents = []string{s.rootBranch()}
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
	if from == s.rootBranch() {
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
		if branch == s.rootBranch() {
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
			if parent == s.rootBranch() {
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
		return s.rootBranch(), nil
	case 1:
		return parents[0], nil
	default:
		if s.repo.BranchExists(s.repo.SyntheticBranchName(branch)) {
			return s.repo.SyntheticBranchName(branch), nil
		}
		return s.repo.RebuildSyntheticBase(branch, parents)
	}
}

func (s *Service) prBase(branch string) (string, error) {
	parents, err := s.meta.Parents(branch)
	if err != nil {
		return "", err
	}
	if len(parents) <= 1 {
		if len(parents) == 0 {
			return s.rootBranch(), nil
		}
		return parents[0], nil
	}
	return s.currentEffectiveBase(branch)
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
	if base == s.repo.SyntheticBranchName(branch) {
		s.stepf("rebasing %s onto welded base", branch)
	} else {
		s.stepf("rebasing %s onto %s", branch, base)
	}
	return s.repo.RebaseBranchOntoFrom(branch, oldBaseOID, base)
}

func (s *Service) shipPlan(branch string, tree bool) ([]string, []string, []string, error) {
	order, err := s.syncOrder(branch, tree)
	if err != nil {
		return nil, nil, nil, err
	}

	realBranches := make([]string, 0)
	syntheticBranches := make([]string, 0)
	syntheticDeletes := make([]string, 0)
	seenReal := map[string]struct{}{}
	seenSynthetic := map[string]struct{}{}
	seenSyntheticDeletes := map[string]struct{}{}
	for _, item := range order {
		if item == s.rootBranch() {
			continue
		}
		parents, err := s.meta.Parents(item)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, parent := range parents {
			if parent == s.rootBranch() {
				continue
			}
			if _, ok := seenReal[parent]; !ok {
				seenReal[parent] = struct{}{}
				realBranches = append(realBranches, parent)
			}
		}
		if len(parents) > 1 {
			synth := s.repo.SyntheticBranchName(item)
			if _, ok := seenSynthetic[synth]; !ok {
				seenSynthetic[synth] = struct{}{}
				syntheticBranches = append(syntheticBranches, synth)
			}
		} else {
			synth := s.repo.SyntheticBranchName(item)
			if _, ok := seenSyntheticDeletes[synth]; !ok {
				seenSyntheticDeletes[synth] = struct{}{}
				syntheticDeletes = append(syntheticDeletes, synth)
			}
		}
		if _, ok := seenReal[item]; !ok {
			seenReal[item] = struct{}{}
			realBranches = append(realBranches, item)
		}
	}
	return realBranches, syntheticBranches, syntheticDeletes, nil
}

type ghPR struct {
	Number      int    `json:"number"`
	URL         string `json:"url"`
	BaseRefName string `json:"baseRefName"`
	Body        string `json:"body,omitempty"`
}

const (
	prStackStart = "<!-- git-weld:stack:start -->"
	prStackEnd   = "<!-- git-weld:stack:end -->"
)

func (s *Service) findPR(branch string) (*ghPR, error) {
	out, err := s.repo.GH("pr", "list", "--head", branch, "--json", "number,url,baseRefName")
	if err != nil {
		return nil, err
	}
	var prs []ghPR
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		return nil, err
	}
	if len(prs) == 0 {
		return &ghPR{}, nil
	}
	return &prs[0], nil
}

func (s *Service) viewPR(number int) (*ghPR, error) {
	out, err := s.repo.GH("pr", "view", fmt.Sprintf("%d", number), "--json", "body,number,url,baseRefName")
	if err != nil {
		return nil, err
	}
	var pr ghPR
	if err := json.Unmarshal([]byte(out), &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

func (s *Service) buildPRBody(branch string, body string) (string, error) {
	graph, err := s.renderPRTreeMarkdown(branch)
	if err != nil {
		return "", err
	}
	section := strings.Join([]string{
		prStackStart,
		"<details>",
		"<summary><strong>Branch Tree</strong></summary>",
		"",
		graph,
		"",
		"_generated by [git-weld](https://github.com/williamjaackson/git-weld)_",
		"</details>",
		prStackEnd,
	}, "\n")
	trimmed := strings.TrimSpace(stripPRStackSection(body))
	if trimmed == "" {
		return section, nil
	}
	return section + "\n\n" + trimmed, nil
}

func stripPRStackSection(body string) string {
	start := strings.Index(body, prStackStart)
	if start == -1 {
		return body
	}
	end := strings.Index(body[start:], prStackEnd)
	if end == -1 {
		return body
	}
	end += start + len(prStackEnd)
	left := strings.TrimRight(body[:start], "\n")
	right := strings.TrimLeft(body[end:], "\n")
	switch {
	case left == "":
		return right
	case right == "":
		return left
	default:
		return left + "\n\n" + right
	}
}

func (s *Service) renderPRTreeMarkdown(branch string) (string, error) {
	labels, err := s.prBranchLabels(branch)
	if err != nil {
		return "", err
	}
	lines := []string{"- " + labels[branch], "  - upstream"}
	parentLines, err := s.renderPRParentMarkdown(branch, "    ", map[string]struct{}{branch: {}}, labels)
	if err != nil {
		return "", err
	}
	if len(parentLines) == 0 {
		lines = append(lines, "    - "+labels[s.rootBranch()])
	} else {
		lines = append(lines, parentLines...)
	}
	lines = append(lines, "  - downstream")
	childLines, err := s.renderPRChildMarkdown(branch, "    ", map[string]struct{}{branch: {}}, labels)
	if err != nil {
		return "", err
	}
	if len(childLines) == 0 {
		lines = append(lines, "    - (none)")
	} else {
		lines = append(lines, childLines...)
	}
	return strings.Join(lines, "\n"), nil
}

func (s *Service) prBranchLabels(branch string) (map[string]string, error) {
	labels := map[string]string{s.rootBranch(): s.rootBranch()}
	nodes := []string{branch}
	ancestors, err := s.ancestorClosure(branch)
	if err != nil {
		return nil, err
	}
	nodes = append(nodes, ancestors...)
	descendants, err := s.descendants(branch)
	if err != nil {
		return nil, err
	}
	nodes = append(nodes, descendants...)
	seen := map[string]struct{}{}
	for _, node := range nodes {
		if node == "" {
			continue
		}
		if _, ok := seen[node]; ok {
			continue
		}
		seen[node] = struct{}{}
		labels[node] = node
	}
	if !s.repo.HasGH() {
		return labels, nil
	}
	for node := range seen {
		if node == s.rootBranch() {
			continue
		}
		pr, err := s.findPR(node)
		if err != nil || pr.Number == 0 || pr.URL == "" {
			continue
		}
		labels[node] = fmt.Sprintf("[%s #%d](%s)", node, pr.Number, pr.URL)
	}
	return labels, nil
}

func (s *Service) renderPRParentMarkdown(branch string, prefix string, seen map[string]struct{}, labels map[string]string) ([]string, error) {
	parents, err := s.meta.Parents(branch)
	if err != nil {
		return nil, err
	}
	if len(parents) == 0 {
		return nil, nil
	}
	lines := make([]string, 0)
	for _, parent := range parents {
		if _, ok := seen[parent]; ok {
			continue
		}
		lines = append(lines, prefix+"- "+labels[parent])
		nextSeen := cloneSeen(seen)
		nextSeen[parent] = struct{}{}
		if parent == s.rootBranch() {
			continue
		}
		descendants, err := s.renderPRParentMarkdown(parent, prefix+"  ", nextSeen, labels)
		if err != nil {
			return nil, err
		}
		lines = append(lines, descendants...)
	}
	return lines, nil
}

func (s *Service) renderPRChildMarkdown(branch string, prefix string, seen map[string]struct{}, labels map[string]string) ([]string, error) {
	children, err := s.directChildren(branch)
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0)
	for _, child := range children {
		if _, ok := seen[child]; ok {
			continue
		}
		lines = append(lines, prefix+"- "+labels[child])
		nextSeen := cloneSeen(seen)
		nextSeen[child] = struct{}{}
		subtree, err := s.renderPRChildMarkdown(child, prefix+"  ", nextSeen, labels)
		if err != nil {
			return nil, err
		}
		lines = append(lines, subtree...)
	}
	return lines, nil
}

func (s *Service) refreshPRBaseIfExists(branch string) (bool, error) {
	if !s.repo.HasGH() {
		return false, nil
	}
	existing, err := s.findPR(branch)
	if err != nil {
		return false, nil
	}
	if existing.Number == 0 {
		return false, nil
	}
	base, err := s.prBase(branch)
	if err != nil {
		return false, err
	}
	existingDetails, err := s.viewPR(existing.Number)
	if err != nil {
		return false, nil
	}
	body, err := s.buildPRBody(branch, stripPRStackSection(existingDetails.Body))
	if err != nil {
		return false, err
	}
	if _, err := s.repo.GH("pr", "edit", fmt.Sprintf("%d", existing.Number), "--base", base, "--body", body); err != nil {
		return false, nil
	}
	return true, nil
}

func (s *Service) renderParentTree(branch string, prefix string, seen map[string]struct{}) ([]string, error) {
	parents, err := s.meta.Parents(branch)
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0)
	for i, parent := range parents {
		if parent == s.rootBranch() {
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

func (s *Service) renderParentTreeWithRoot(branch string, prefix string, seen map[string]struct{}) ([]string, error) {
	parents, err := s.meta.Parents(branch)
	if err != nil {
		return nil, err
	}
	if len(parents) == 0 {
		return []string{prefix + "└─ " + s.rootBranch()}, nil
	}
	lines := make([]string, 0)
	for i, parent := range parents {
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
		if parent == s.rootBranch() {
			continue
		}
		descendants, err := s.renderParentTreeWithRoot(parent, nextPrefix, nextSeen)
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
