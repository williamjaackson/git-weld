package weld

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/williamjaackson/git-weld/internal/git"
)

type Metadata struct {
	repo *git.Repo
}

type Settings struct {
	RootBranch string
	RemoteName string
	RemoteOff  bool
}

func NewMetadata(repo *git.Repo) *Metadata {
	return &Metadata{repo: repo}
}

func DefaultSettings() Settings {
	return Settings{
		RootBranch: "master",
		RemoteName: "origin",
		RemoteOff:  false,
	}
}

func (m *Metadata) Parents(branch string) ([]string, error) {
	values, err := m.repo.ConfigGetAll(parentKey(branch))
	if err != nil {
		return nil, err
	}
	sort.Strings(values)
	return values, nil
}

func (m *Metadata) MarkManaged(branch string) error {
	return m.repo.Run("config", "--local", managedKey(branch), "true")
}

func (m *Metadata) Unmanage(branch string) error {
	if err := m.repo.ConfigUnsetAll(parentKey(branch)); err != nil {
		return err
	}
	return m.repo.ConfigUnsetAll(managedKey(branch))
}

func (m *Metadata) IsManaged(branch string) (bool, error) {
	values, err := m.repo.ConfigGetAll(managedKey(branch))
	if err != nil {
		return false, err
	}
	return len(values) > 0, nil
}

func (m *Metadata) SetParents(branch string, parents []string) error {
	parents = uniqueSorted(parents)
	if err := m.MarkManaged(branch); err != nil {
		return err
	}
	if len(parents) == 0 {
		return m.repo.ConfigUnsetAll(parentKey(branch))
	}
	return m.repo.ConfigSetAll(parentKey(branch), parents)
}

func (m *Metadata) AddParent(branch string, parent string) error {
	parents, err := m.Parents(branch)
	if err != nil {
		return err
	}
	parents = append(parents, parent)
	return m.SetParents(branch, parents)
}

func (m *Metadata) RemoveParent(branch string, parent string) error {
	parents, err := m.Parents(branch)
	if err != nil {
		return err
	}
	filtered := make([]string, 0, len(parents))
	for _, candidate := range parents {
		if candidate != parent {
			filtered = append(filtered, candidate)
		}
	}
	return m.SetParents(branch, filtered)
}

func (m *Metadata) ManagedBranches() ([]string, error) {
	parentKeys, err := m.repo.ConfigKeys("^weld\\.parents\\.")
	if err != nil {
		return nil, err
	}
	managedKeys, err := m.repo.ConfigKeys("^weld\\.managed\\.")
	if err != nil {
		return nil, err
	}

	seen := map[string]struct{}{}
	branches := make([]string, 0, len(parentKeys)+len(managedKeys))
	for _, key := range append(parentKeys, managedKeys...) {
		parts := strings.Split(key, ".")
		if len(parts) != 3 {
			return nil, fmt.Errorf("unexpected metadata key %q", key)
		}
		decoded, err := decode(parts[2])
		if err != nil {
			return nil, err
		}
		if _, ok := seen[decoded]; ok {
			continue
		}
		seen[decoded] = struct{}{}
		branches = append(branches, decoded)
	}
	sort.Strings(branches)
	return branches, nil
}

func (m *Metadata) Settings() (Settings, error) {
	settings := DefaultSettings()

	if values, err := m.repo.ConfigGetAll(rootKey()); err != nil {
		return Settings{}, err
	} else if len(values) > 0 && strings.TrimSpace(values[0]) != "" {
		settings.RootBranch = strings.TrimSpace(values[0])
	}

	if values, err := m.repo.ConfigGetAll(remoteKey()); err != nil {
		return Settings{}, err
	} else if len(values) > 0 {
		settings.RemoteName = strings.TrimSpace(values[0])
	}

	if values, err := m.repo.ConfigGetAll(remoteDisabledKey()); err != nil {
		return Settings{}, err
	} else if len(values) > 0 {
		switch strings.TrimSpace(values[0]) {
		case "true", "1", "yes", "on":
			settings.RemoteOff = true
		default:
			settings.RemoteOff = false
		}
	}

	return settings, nil
}

func (m *Metadata) SaveSettings(settings Settings) error {
	root := strings.TrimSpace(settings.RootBranch)
	if root == "" {
		root = DefaultSettings().RootBranch
	}
	remote := strings.TrimSpace(settings.RemoteName)

	if err := m.repo.Run("config", "--local", rootKey(), root); err != nil {
		return err
	}
	if settings.RemoteOff {
		if err := m.repo.Run("config", "--local", remoteDisabledKey(), "true"); err != nil {
			return err
		}
		return m.repo.ConfigUnsetAll(remoteKey())
	}
	if err := m.repo.Run("config", "--local", remoteDisabledKey(), "false"); err != nil {
		return err
	}
	if remote == "" {
		remote = DefaultSettings().RemoteName
	}
	return m.repo.Run("config", "--local", remoteKey(), remote)
}

func parentKey(branch string) string {
	return "weld.parents." + encode(branch)
}

func managedKey(branch string) string {
	return "weld.managed." + encode(branch)
}

func rootKey() string {
	return "weld.root"
}

func remoteKey() string {
	return "weld.remote"
}

func remoteDisabledKey() string {
	return "weld.remoteDisabled"
}

func encode(value string) string {
	return "b" + hex.EncodeToString([]byte(value))
}

func decode(value string) (string, error) {
	value = strings.TrimPrefix(value, "b")
	raw, err := hex.DecodeString(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func uniqueSorted(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
