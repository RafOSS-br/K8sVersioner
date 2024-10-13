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

// Store is an interface for manage Config and GitConfig resources
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

// store is a struct that implements the Store interface
type store struct {
	configChan chan *Buddle
	gitCfgMap  sync.Map
	cfgMap     sync.Map
	waitMap    sync.Map
	waitMu     sync.Mutex
}

// NewStore returns a new Store
func NewStore(poolSize int) Store {
	return &store{
		configChan: make(chan *Buddle, poolSize),
	}
}

// CreateOrUpdateConfig creates or updates a Config resource
func (s *store) CreateOrUpdateConfig(config *k8sversionerv1alpha1.Config) error {
	s.cfgMap.Store(config.Name, &Config{Cfg: config})
	err := s.SubmitConfig(config.Name)
	if err != nil {
		return err
	}
	return nil
}

// DeleteConfig deletes a Config resource
func (s *store) DeleteConfig(configName string) error {
	if _, ok := s.cfgMap.Load(configName); !ok {
		return ErrConfigNotFound
	}
	s.cfgMap.Delete(configName)
	return nil
}

// CreateOrUpdateGitConfig creates or updates a GitConfig resource
func (s *store) CreateOrUpdateGitConfig(gitConfig *k8sversionerv1alpha1.GitConfig) error {
	s.gitCfgMap.Store(gitConfig.Name, gitConfig)
	if v, ok := s.GetFromWaitMap(gitConfig.Name); ok {
		for _, cfg := range v {
			err := s.SubmitConfig(cfg.Cfg.Name)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// DeleteGitConfig deletes a GitConfig resource
func (s *store) DeleteGitConfig(gitConfigName string) error {
	if _, ok := s.gitCfgMap.Load(gitConfigName); !ok {
		return ErrGitConfigNotFound
	}
	s.gitCfgMap.Delete(gitConfigName)
	return nil
}

// ConfigProducer returns a channel with Config resources
func (s *store) ConfigProducer() <-chan *Buddle {
	return s.configChan
}

var (
	// ErrConfigNotFound is returned when a Config is not found
	ErrConfigNotFound = errors.New("config not found")
	// ErrGitConfigNotFound is returned when a GitConfig is not found
	ErrGitConfigNotFound = errors.New("gitconfig not found")
	// ErrUnexpectedTypeOfConfig is returned when the type of the Config is unexpected
	ErrConfigUnexpectedType = errors.New("expected type *Config, got another type")
	// ErrUnexpectedTypeOfGitConsfig is returned when the type of the GitConfig is unexpected
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
		s.AddToWaitMap(cfg.Cfg.Spec.GitRef, cfg)
		return ErrGitConfigNotFound
	}
	gitCfg, ok := gitConfig.(*k8sversionerv1alpha1.GitConfig)
	if !ok {
		return ErrGitConfigUnexpectedType
	}
	s.configChan <- &Buddle{Config: cfg, GitConfig: gitCfg}
	return nil
}

// Helper functions to manager waitMap

func (s *store) AddToWaitMap(key string, value *Config) {
	s.waitMu.Lock()
	defer s.waitMu.Unlock()

	v, ok := s.waitMap.Load(key)
	if !ok {
		s.waitMap.Store(key, []*Config{value})
	} else {
		s.waitMap.Store(key, append(v.([]*Config), value))
	}
}

func (s *store) GetFromWaitMap(key string) ([]*Config, bool) {
	s.waitMu.Lock()
	defer s.waitMu.Unlock()

	value, ok := s.waitMap.Load(key)
	if !ok {
		return nil, false
	}
	return value.([]*Config), true
}
