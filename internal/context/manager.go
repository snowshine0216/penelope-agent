package agentcontext

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Manager struct {
	mu       sync.Mutex
	workDir  string
	catalog  SkillCatalog
	composer Composer
	byName   map[string]SkillMeta
}

func NewManager(workDir string) (*Manager, error) {
	agents, err := LoadRootAgents(workDir)
	if err != nil {
		return nil, err
	}
	catalog, err := LoadSkillCatalog(workDir)
	if err != nil {
		return nil, err
	}
	byName := map[string]SkillMeta{}
	for _, skill := range catalog.Skills {
		byName[skill.Name] = skill
	}

	composer := NewComposer(ComposerInput{
		BaseInstructions: DefaultBaseInstructions,
		Agents:           agents,
		Catalog:          catalog,
	})

	return &Manager{workDir: workDir, catalog: catalog, composer: composer, byName: byName}, nil
}

func (m *Manager) SystemPrompt() string {
	if m == nil {
		return DefaultBaseInstructions
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.composer.SystemPrompt()
}

func (m *Manager) HasSkills() bool {
	return m != nil && len(m.catalog.Skills) > 0
}

func (m *Manager) AvailableSkillNames() []string {
	if m == nil {
		return nil
	}
	names := make([]string, 0, len(m.catalog.Skills))
	for _, skill := range m.catalog.Skills {
		names = append(names, skill.Name)
	}
	sort.Strings(names)
	return names
}

func (m *Manager) LoadSkill(name string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("skill manager is not configured")
	}
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return "", fmt.Errorf("skill name is required")
	}

	// Build the available-names list before acquiring the lock to avoid
	// a latent deadlock if AvailableSkillNames is ever made lock-guarded.
	availableNames := strings.Join(m.AvailableSkillNames(), ", ")

	m.mu.Lock()
	defer m.mu.Unlock()

	meta, ok := m.byName[trimmedName]
	if !ok {
		return "", fmt.Errorf("unknown local skill %q (available: %s)", trimmedName, availableNames)
	}
	if m.composer.HasLoadedSkill(trimmedName) {
		return fmt.Sprintf("skill %q already loaded", trimmedName), nil
	}

	loaded, err := LoadSkillBody(m.workDir, meta)
	if err != nil {
		return "", err
	}
	m.composer = m.composer.WithLoadedSkill(loaded)
	return fmt.Sprintf("loaded skill %q", trimmedName), nil
}
