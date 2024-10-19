package store

import (
	"errors"
	"log"
	"sync"

	k8sversionerv1alpha1 "github.com/RafOSS-br/K8sVersioner/api/v1alpha1"
)

var StoreSingleton Store = NewStore(100)

// Config is a struct that stores a Config and mutex
type Config struct {
	Cfg *k8sversionerv1alpha1.Config
}

// Bundle is a struct that contains a Config and a GitConfig
type Bundle struct {
	Config *Config
	Git    *GitConfigEntry
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
	gitCfgMap  map[string]*GitConfigEntry // map of GitConfigEntries
	cfgMap     map[string]*Config         // map of Configs
	mu         sync.RWMutex               // mutex to protect access to gitCfgMap and cfgMap
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
func (s *store) CreateOrUpdateConfig(config *k8sversionerv1alpha1.Config) error {
	cfg := &Config{Cfg: config}

	// Lock the store for writing
	s.mu.Lock()
	defer s.mu.Unlock()

	// Store the Config in cfgMap
	s.cfgMap[config.Name] = cfg

	// Get or create the GitConfigEntry
	gitRef := config.Spec.GitRef
	entry, exists := s.gitCfgMap[gitRef]
	if !exists {
		// Create a new GitConfigEntry with an empty Configs map
		entry = &GitConfigEntry{
			Configs: make(map[string]*Config),
		}
		s.gitCfgMap[gitRef] = entry
	}

	// Lock the GitConfigEntry for modification
	entry.mu.Lock()
	defer entry.mu.Unlock()

	// Associate the Config with the GitConfigEntry
	entry.Configs[config.Name] = cfg

	if entry.GitConfig == nil {
		// GitConfig not found; cannot associate Config yet
		log.Printf("GitConfig '%s' not found; cannot process Config '%s' yet", gitRef, config.Name)
		return ErrGitConfigNotFound
	}

	// Submit the Config for processing
	return s.submitConfig(cfg, entry)
}

// DeleteConfig deletes a Config resource
func (s *store) DeleteConfig(configName string) error {
	// Lock the store for writing
	s.mu.Lock()
	defer s.mu.Unlock()

	// Retrieve and delete the Config from cfgMap
	cfg, exists := s.cfgMap[configName]
	if !exists {
		log.Printf("Config '%s' not found", configName)
		return ErrConfigNotFound
	}
	delete(s.cfgMap, configName)

	// Retrieve the associated GitConfigEntry
	gitRef := cfg.Cfg.Spec.GitRef
	entry, exists := s.gitCfgMap[gitRef]
	if exists {
		// Lock the GitConfigEntry for modification
		entry.mu.Lock()
		defer entry.mu.Unlock()

		// Remove the Config from the GitConfigEntry's Configs
		delete(entry.Configs, configName)

		// If no more Configs are associated, you might want to handle cleanup
		if len(entry.Configs) == 0 {
			log.Printf("No more Configs associated with GitConfig '%s'", gitRef)
		}
	}

	return nil
}

// CreateOrUpdateGitConfig creates or updates a GitConfig resource
func (s *store) CreateOrUpdateGitConfig(gitConfig *k8sversionerv1alpha1.GitConfig) error {
	// Lock the store for writing
	s.mu.Lock()
	defer s.mu.Unlock()

	// Get or create the GitConfigEntry
	entry, exists := s.gitCfgMap[gitConfig.Name]
	if !exists {
		// Create a new GitConfigEntry with an empty Configs map
		entry = &GitConfigEntry{
			Configs: make(map[string]*Config),
		}
		s.gitCfgMap[gitConfig.Name] = entry
	}

	// Lock the GitConfigEntry for modification
	entry.mu.Lock()
	defer entry.mu.Unlock()

	// Update the GitConfig
	entry.GitConfig = gitConfig

	// Submit all associated Configs for processing
	for _, cfg := range entry.Configs {
		if err := s.submitConfig(cfg, entry); err != nil {
			log.Printf("Error submitting Config '%s': %v", cfg.Cfg.Name, err)
			return err
		}
	}

	return nil
}

// DeleteGitConfig deletes a GitConfig resource
func (s *store) DeleteGitConfig(gitConfigName string) error {
	// Lock the store for writing
	s.mu.Lock()
	defer s.mu.Unlock()

	// Retrieve and delete the GitConfigEntry from gitCfgMap
	entry, exists := s.gitCfgMap[gitConfigName]
	if !exists {
		log.Printf("GitConfig '%s' not found", gitConfigName)
		return ErrGitConfigNotFound
	}
	delete(s.gitCfgMap, gitConfigName)

	// Lock the GitConfigEntry for modification
	entry.mu.Lock()
	defer entry.mu.Unlock()

	// Optionally, handle associated Configs (e.g., delete or disassociate them)
	for configName := range entry.Configs {
		// Remove Config from cfgMap
		delete(s.cfgMap, configName)
		// Optionally, notify or handle the deletion of Configs
		log.Printf("Deleted Config '%s' associated with GitConfig '%s'", configName, gitConfigName)
	}

	// Clear the Configs map
	entry.Configs = nil

	return nil
}

// ConfigProducer returns a channel with Config resources
func (s *store) ConfigProducer() <-chan *Bundle {
	return s.configChan
}

// submitConfig sends a Config to the configChan for processing
func (s *store) submitConfig(cfg *Config, gitEntry *GitConfigEntry) error {
	// Ensure the GitConfig name matches the reference
	if gitEntry.GitConfig.Name != cfg.Cfg.Spec.GitRef {
		log.Printf("GitConfig name '%s' does not match reference '%s' for Config '%s'",
			gitEntry.GitConfig.Name, cfg.Cfg.Spec.GitRef, cfg.Cfg.Name)
		return ErrGitConfigNameMismatch
	}

	// Send the Bundle to the channel in a non-blocking manner
	select {
	case s.configChan <- &Bundle{
		Config: cfg,
		Git:    gitEntry,
	}:
		log.Printf("Submitted Config '%s' for processing", cfg.Cfg.Name)
	default:
		log.Printf("Config channel is full; could not submit Config '%s'", cfg.Cfg.Name)
	}

	return nil
}

var (
	// ErrConfigNotFound is returned when a Config is not found
	ErrConfigNotFound = errors.New("config not found")
	// ErrGitConfigNotFound is returned when a GitConfig is not found
	ErrGitConfigNotFound = errors.New("gitconfig not found")
	// ErrConfigUnexpectedType is returned when the type of the Config is unexpected
	ErrConfigUnexpectedType = errors.New("expected type *Config, got another type")
	// ErrGitConfigUnexpectedType is returned when the type of the GitConfig is unexpected
	ErrGitConfigUnexpectedType = errors.New("expected type *GitConfigEntry, got another type")
	// ErrGitConfigNameMismatch is returned when GitConfig name does not match the reference
	ErrGitConfigNameMismatch = errors.New("gitconfig name does not match the reference")
)
