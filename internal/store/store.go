package store

import (
	"errors"
	"sync"

	k8sversionerv1alpha1 "github.com/RafOSS-br/K8sVersioner/api/v1alpha1"
)

var StoreSingleton Store = NewStore(100)

// Config is a struct that stores a Config and mutex
type Config struct {
	Cfg *k8sversionerv1alpha1.Config
	mu  sync.Mutex
}

// Lock locks the Config
func (c *Config) Lock() {
	c.mu.Lock()
}

// Unlock unlocks the Config
func (c *Config) Unlock() {
	c.mu.Unlock()
}

// Buddle is a struct that contains a Config and a GitConfig
type Buddle struct {
	*Config
	GitConfig *k8sversionerv1alpha1.GitConfig
}

// Store is an interface for managing Config and GitConfig resources
type Store interface {
	// CreateOrUpdateConfig creates or updates a Config resource
	CreateOrUpdateConfig(config *k8sversionerv1alpha1.Config) error
	// DeleteConfig deletes a Config resource
	DeleteConfig(configName string) error
	// CreateOrUpdateGitConfig creates or updates a GitConfig resource
	CreateOrUpdateGitConfig(gitConfig *k8sversionerv1alpha1.GitConfig) error
	// DeleteGitConfig deletes a GitConfig resource
	DeleteGitConfig(gitConfigName string) error
	// ConfigProducer returns a channel with Config resources
	ConfigProducer() <-chan *Buddle
}

// GitConfigEntry is an intermediate struct that holds a GitConfig and its associated Configs
type GitConfigEntry struct {
	GitConfig *k8sversionerv1alpha1.GitConfig
	Configs   []*Config
	mu        sync.RWMutex
}

// store is a struct that implements the Store interface
type store struct {
	configChan chan *Buddle
	gitCfgMap  sync.Map // map[string]*GitConfigEntry
	cfgMap     sync.Map // map[string]*Config
}

// NewStore returns a new Store
func NewStore(poolSize int) Store {
	return &store{
		configChan: make(chan *Buddle, poolSize),
	}
}

// CreateOrUpdateConfig creates or updates a Config resource
func (s *store) CreateOrUpdateConfig(config *k8sversionerv1alpha1.Config) error {
	cfg := &Config{Cfg: config}
	s.cfgMap.Store(config.Name, cfg)

	gitEntryInterface, ok := s.gitCfgMap.Load(config.Spec.GitRef)
	if !ok {
		// GitConfig not found; cannot associate Config yet
		return ErrGitConfigNotFound
	}

	gitEntry, ok := gitEntryInterface.(*GitConfigEntry)
	if !ok {
		return ErrGitConfigUnexpectedType
	}

	// Associate the Config with the GitConfigEntry
	gitEntry.mu.Lock()
	defer gitEntry.mu.Unlock()
	gitEntry.Configs = append(gitEntry.Configs, cfg)

	// Submit the Config for processing
	return s.submitConfig(cfg)
}

// DeleteConfig deletes a Config resource
func (s *store) DeleteConfig(configName string) error {
	cfgInterface, ok := s.cfgMap.Load(configName)
	if !ok {
		return ErrConfigNotFound
	}

	cfg, ok := cfgInterface.(*Config)
	if !ok {
		return ErrConfigUnexpectedType
	}

	// Remove the Config from its associated GitConfigEntry
	gitRef := cfg.Cfg.Spec.GitRef
	gitEntryInterface, ok := s.gitCfgMap.Load(gitRef)
	if ok {
		gitEntry, ok := gitEntryInterface.(*GitConfigEntry)
		if ok {
			gitEntry.mu.Lock()
			defer gitEntry.mu.Unlock()
			for i, c := range gitEntry.Configs {
				if c.Cfg.Name == configName {
					// Remove the Config from the slice
					gitEntry.Configs = append(gitEntry.Configs[:i], gitEntry.Configs[i+1:]...)
					break
				}
			}
		}
	}

	s.cfgMap.Delete(configName)
	return nil
}

// CreateOrUpdateGitConfig creates or updates a GitConfig resource
func (s *store) CreateOrUpdateGitConfig(gitConfig *k8sversionerv1alpha1.GitConfig) error {
	var entry *GitConfigEntry

	entryInterface, loaded := s.gitCfgMap.LoadOrStore(gitConfig.Name, &GitConfigEntry{
		GitConfig: gitConfig,
		Configs:   []*Config{},
	})
	entry = entryInterface.(*GitConfigEntry)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if loaded {
		// Update the existing GitConfig
		entry.GitConfig = gitConfig
	}

	// Submit all associated Configs for processing
	for _, cfg := range entry.Configs {
		if err := s.submitConfig(cfg); err != nil {
			return err
		}
	}

	return nil
}

// DeleteGitConfig deletes a GitConfig resource
func (s *store) DeleteGitConfig(gitConfigName string) error {
	entryInterface, ok := s.gitCfgMap.Load(gitConfigName)
	if !ok {
		return ErrGitConfigNotFound
	}

	entry, ok := entryInterface.(*GitConfigEntry)
	if !ok {
		return ErrGitConfigUnexpectedType
	}

	// Optionally, handle associated Configs (e.g., delete or disassociate them)
	entry.mu.Lock()
	for _, cfg := range entry.Configs {
		s.cfgMap.Delete(cfg.Cfg.Name)
		// Optionally, notify or handle the deletion of Configs
	}
	entry.Configs = nil
	entry.mu.Unlock()

	s.gitCfgMap.Delete(gitConfigName)
	return nil
}

// ConfigProducer returns a channel with Config resources
func (s *store) ConfigProducer() <-chan *Buddle {
	return s.configChan
}

// submitConfig sends a Config to the configChan
func (s *store) submitConfig(cfg *Config) error {
	gitConfigInterface, ok := s.gitCfgMap.Load(cfg.Cfg.Spec.GitRef)
	if !ok {
		return ErrGitConfigNotFound
	}

	gitEntry, ok := gitConfigInterface.(*GitConfigEntry)
	if !ok {
		return ErrGitConfigUnexpectedType
	}

	// Ensure the GitConfig name matches the reference
	if gitEntry.GitConfig.Name != cfg.Cfg.Spec.GitRef {
		return ErrGitConfigNameMismatch
	}

	// Send the Buddle to the channel
	s.configChan <- &Buddle{
		Config:    cfg,
		GitConfig: gitEntry.GitConfig,
	}
	return nil
}

var (
	// ErrConfigNotFound is returned when a Config is not found
	ErrConfigNotFound = errors.New("config not found")
	// ErrGitConfigNotFound is returned when a GitConfig is not found
	ErrGitConfigNotFound = errors.New("gitconfig not found")
	// ErrUnexpectedTypeOfConfig is returned when the type of the Config is unexpected
	ErrConfigUnexpectedType = errors.New("expected type *Config, got another type")
	// ErrUnexpectedTypeOfGitConfig is returned when the type of the GitConfig is unexpected
	ErrGitConfigUnexpectedType = errors.New("expected type *GitConfigEntry, got another type")
	// ErrGitConfigNameMismatch is returned when GitConfig name does not match the reference
	ErrGitConfigNameMismatch = errors.New("gitconfig name does not match the reference")
)
