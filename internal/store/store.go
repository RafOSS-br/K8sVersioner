package store

import (
	"context"
	"errors"
	"sync"

	k8sversionerv1alpha1 "github.com/RafOSS-br/K8sVersioner/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// StoreSingleton is a global instance of the store
var StoreSingleton Store = NewStore(100)

// Config represents a configuration resource
type Config struct {
	Cfg *k8sversionerv1alpha1.Config
}

// Bundle contains a Config and its associated GitConfigEntry
type Bundle struct {
	Config *Config
	Git    *GitConfigEntry
	Del    bool
}

// Store is an interface for managing Config and GitConfig resources
type Store interface {
	CreateOrUpdateConfig(ctx context.Context, config *k8sversionerv1alpha1.Config) error
	DeleteConfig(ctx context.Context, configName string) error
	CreateOrUpdateGitConfig(ctx context.Context, gitConfig *k8sversionerv1alpha1.GitConfig) error
	DeleteGitConfig(ctx context.Context, gitConfigName string) error
	ConfigProducer() <-chan *Bundle
}

// GitConfigEntry holds a GitConfig and its associated Configs, along with a mutex for synchronization
type GitConfigEntry struct {
	GitConfig *k8sversionerv1alpha1.GitConfig
	Configs   map[string]*Config
	mu        sync.Mutex
}

// Lock locks the GitConfigEntry
func (g *GitConfigEntry) Lock() {
	g.mu.Lock()
}

// Unlock unlocks the GitConfigEntry
func (g *GitConfigEntry) Unlock() {
	g.mu.Unlock()
}

// TryLock tries to lock the GitConfigEntry
func (g *GitConfigEntry) TryLock() bool {
	return g.mu.TryLock()
}

// store implements the Store interface and manages Config and GitConfig resources
type store struct {
	configChan chan *Bundle
	gitCfgMap  map[string]*GitConfigEntry
	cfgMap     map[string]*Config
	mu         sync.RWMutex
}

// NewStore returns a new instance of Store
func NewStore(poolSize int) Store {
	return &store{
		configChan: make(chan *Bundle, poolSize),
		gitCfgMap:  make(map[string]*GitConfigEntry),
		cfgMap:     make(map[string]*Config),
	}
}

// CreateOrUpdateConfig creates or updates a Config resource
func (s *store) CreateOrUpdateConfig(ctx context.Context, config *k8sversionerv1alpha1.Config) error {
	cfg := &Config{Cfg: config}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cfgMap[config.Name] = cfg

	gitRef := config.Spec.GitRef
	entry, exists := s.gitCfgMap[gitRef]
	if !exists {
		entry = &GitConfigEntry{
			Configs: make(map[string]*Config),
		}
		s.gitCfgMap[gitRef] = entry
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	entry.Configs[config.Name] = cfg

	if entry.GitConfig == nil {
		return ErrGitConfigNotFound
	}

	return s.submitConfig(ctx, cfg, entry, false)
}

// DeleteConfig deletes a Config resource
func (s *store) DeleteConfig(ctx context.Context, configName string) error {
	log := log.FromContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, exists := s.cfgMap[configName]
	if !exists {
		return ErrConfigNotFound
	}
	delete(s.cfgMap, configName)

	gitRef := cfg.Cfg.Spec.GitRef
	entry, exists := s.gitCfgMap[gitRef]
	if exists {
		entry.mu.Lock()
		defer entry.mu.Unlock()

		delete(entry.Configs, configName)

		if entry.GitConfig != nil {
			if err := s.submitConfig(ctx, cfg, entry, true); err != nil {
				return err
			}
		}

		if len(entry.Configs) == 0 {
			if entry.GitConfig == nil {
				delete(s.gitCfgMap, gitRef)
			}
			log.Info("No more configs associated with GitConfig", "gitConfigName", gitRef)
		}
		return nil
	}
	log.Info("GitConfig not found", "gitConfigName", gitRef)
	return nil
}

// CreateOrUpdateGitConfig creates or updates a GitConfig resource
func (s *store) CreateOrUpdateGitConfig(ctx context.Context, gitConfig *k8sversionerv1alpha1.GitConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	logger := log.FromContext(ctx)

	entry, exists := s.gitCfgMap[gitConfig.Name]
	if !exists {
		entry = &GitConfigEntry{
			Configs: make(map[string]*Config),
		}
		s.gitCfgMap[gitConfig.Name] = entry
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	entry.GitConfig = gitConfig

	for _, cfg := range entry.Configs {
		if err := s.submitConfig(ctx, cfg, entry, false); err != nil {
			logger.Error(err, "Failed to submit Config for processing", "name", cfg.Cfg.Name)
			return err
		}
	}

	return nil
}

// DeleteGitConfig deletes a GitConfig resource
func (s *store) DeleteGitConfig(ctx context.Context, gitConfigName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	logger := log.FromContext(ctx)

	entry, exists := s.gitCfgMap[gitConfigName]
	if !exists {
		logger.Error(ErrGitConfigNotFound, "GitConfig not found", "name", gitConfigName)
		return ErrGitConfigNotFound
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	// Submit Configs for deletion before removing GitConfig
	for configName, cfg := range entry.Configs {
		delete(s.cfgMap, configName)
		if err := s.submitConfig(ctx, cfg, entry, true); err != nil {
			logger.Error(err, "Failed to submit Config for deletion", "name", configName)
			return err
		}
		logger.Info("Deleted Config", "name", configName)
	}

	// Remove GitConfigEntry from gitCfgMap
	delete(s.gitCfgMap, gitConfigName)

	entry.Configs = nil
	entry.GitConfig = nil

	return nil
}

// ConfigProducer returns a channel with Config resources
func (s *store) ConfigProducer() <-chan *Bundle {
	return s.configChan
}

// submitConfig sends a Config to the configChan for processing
func (s *store) submitConfig(ctx context.Context, cfg *Config, gitEntry *GitConfigEntry, isDel bool) error {
	logger := log.FromContext(ctx)

	if !isDel && gitEntry.GitConfig == nil {
		logger.Error(ErrGitConfigNotFound, "GitConfig is nil in submitConfig", "configName", cfg.Cfg.Name)
		return ErrGitConfigNotFound
	}

	if gitEntry.GitConfig != nil && gitEntry.GitConfig.Name != cfg.Cfg.Spec.GitRef {
		logger.Error(ErrGitConfigNameMismatch, "GitConfig name does not match reference",
			"gitConfigName", gitEntry.GitConfig.Name, "gitRef", cfg.Cfg.Spec.GitRef, "configName", cfg.Cfg.Name)
		return ErrGitConfigNameMismatch
	}

	select {
	case s.configChan <- &Bundle{
		Config: cfg,
		Git:    gitEntry,
		Del:    isDel,
	}:
		logger.Info("Submitted Config for processing", "configName", cfg.Cfg.Name, "isDel", isDel)
	default:
		logger.Info("Config channel is full; could not submit Config", "configName", cfg.Cfg.Name)
	}

	return nil
}

var (
	ErrConfigNotFound          = errors.New("config not found")
	ErrGitConfigNotFound       = errors.New("gitconfig not found")
	ErrConfigUnexpectedType    = errors.New("expected type *Config, got another type")
	ErrGitConfigUnexpectedType = errors.New("expected type *GitConfigEntry, got another type")
	ErrGitConfigNameMismatch   = errors.New("gitconfig name does not match the reference")
	ErrNoMoreConfigsAssociated = errors.New("no more configs associated with gitconfig")
)
