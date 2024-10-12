package store

import (
	"errors"
	"sync"

	k8sversionerv1alpha1 "github.com/RafOSS-br/K8sVersionerls/api/v1alpha1"
)

var StoreSingleton Store = &store{}

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
	Config    *Config
	GitConfig *k8sversionerv1alpha1.GitConfig
}

// Store is an interface for manage Config and GitConfig resources
type Store interface {
	// CreateOrUpdateConfig creates or updates a Config resource
	CreateOrUpdateConfig(config *k8sversionerv1alpha1.Config) error
	// CreateOrUpdateGitConfig creates or updates a GitConfig resource
	CreateOrUpdateGitConfig(gitConfig *k8sversionerv1alpha1.GitConfig) error
	// ConfigProducer returns a channel with Config resources
	ConfigProducer() <-chan Buddle
	// SubmitConfig submits a Config resource
	SubmitConfig(cfgName string) error
}

// store is a struct that implements the Store interface
type store struct {
	configChan chan Buddle
	gitCfgMap  sync.Map
	cfgMap     sync.Map
}

// NewStore returns a new Store
func NewStore(poolSize int) Store {
	return &store{
		configChan: make(chan Buddle, poolSize),
	}
}

// CreateOrUpdateConfig creates or updates a Config resource
func (s *store) CreateOrUpdateConfig(config *k8sversionerv1alpha1.Config) error {
	s.cfgMap.Store(config.Name, &Config{Cfg: config})
	return nil
}

// CreateOrUpdateGitConfig creates or updates a GitConfig resource
func (s *store) CreateOrUpdateGitConfig(gitConfig *k8sversionerv1alpha1.GitConfig) error {
	s.gitCfgMap.Store(gitConfig.Name, gitConfig)
	return nil
}

// ConfigProducer returns a channel with Config resources
func (s *store) ConfigProducer() <-chan Buddle {
	return s.configChan
}

var (
	// ErrConfigNotFound is returned when a Config is not found
	ErrConfigNotFound = errors.New("config not found")
	// ErrGitConfigNotFound is returned when a GitConfig is not found
	ErrGitConfigNotFound = errors.New("gitconfig not found")
	// ErrUnexpectedTypeOfConfig is returned when the type of the Config is unexpected
	ErrConfigUnexpectedType = errors.New("expected type *Config, got another type")
	// ErrUnexpectedTypeOfGitConfig is returned when the type of the GitConfig is unexpected
	ErrGitConfigUnexpectedType = errors.New("expected type *k8sversionerv1alpha1.GitConfig, got another type")
)

// Produce sends a Config resource to the channel
func (s *store) SubmitConfig(cfgName string) error {
	c, ok := s.cfgMap.Load(cfgName)
	if !ok {
		return ErrConfigNotFound
	}
	cfg, ok := c.(*Config)
	if !ok {
		return ErrConfigUnexpectedType
	}
	gitConfig, ok := s.gitCfgMap.Load(cfg.Cfg.Spec.GitRef)
	if !ok {
		return ErrGitConfigNotFound
	}
	gitCfg, ok := gitConfig.(*k8sversionerv1alpha1.GitConfig)
	if !ok {
		return ErrGitConfigUnexpectedType
	}
	s.configChan <- Buddle{Config: cfg, GitConfig: gitCfg}
	return nil
}
