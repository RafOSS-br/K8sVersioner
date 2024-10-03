package config

import (
	"context"
	"encoding/json"
	"os"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog/log"
)

type ConfigManager struct {
	mu  sync.RWMutex
	cfg *Config
}

func (cm *ConfigManager) Lock() {
	cm.mu.Lock()
}

func (cm *ConfigManager) Unlock() {
	cm.mu.Unlock()
}

func (cm *ConfigManager) RLock() {
	cm.mu.RLock()
}

func (cm *ConfigManager) RUnlock() {
	cm.mu.RUnlock()
}

func NewConfigManager(cfg *Config) *ConfigManager {
	return &ConfigManager{
		cfg: cfg,
	}
}

func (cm *ConfigManager) Reload(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var newCfg *Config
	if err := json.Unmarshal(data, newCfg); err != nil {
		return err
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.cfg = newCfg
	log.Info().Msg("Configuration reloaded successfully")
	return nil
}

func (cm *ConfigManager) GetConfig() *Config {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.cfg
}

type Config struct {
	Namespace       string            `json:"namespace"`
	IncludeResource []ResourceFilter  `json:"includeResource"`
	ExcludeResource []ResourceFilter  `json:"excludeResource"`
	Labels          map[string]string `json:"labels"`
	OutputType      string            `json:"outputType"`
	Annotations     map[string]string `json:"annotations"`
	Git             GitConfig         `json:"git"`
	KubeConfig      string            `json:"kubeConfig"`
	FolderStructure string            `json:"folderStructure"`
	DryRun          bool              `json:"dryRun"`
}

type ResourceFilter struct {
	Name              string `json:"name"`
	APIVersion        string `json:"apiVersion"`
	WithManagedFields bool   `json:"withManagedFields"`
	WithStatus        bool   `json:"withStatus"`
}

type GitConfig struct {
	RepositoryURL string `json:"repositoryUrl"`
	Branch        string `json:"branch"`
	Username      string `json:"username"`
	Password      string `json:"password"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func WatchConfig(ctx context.Context, cfg *ConfigManager, path string) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	err = watcher.Add(path)
	if err != nil {
		return err
	}

	for {
		select {
		case event := <-watcher.Events:
			if event.Op&fsnotify.Write == fsnotify.Write {
				if err := cfg.Reload(path); err != nil {
					log.Error().Err(err).Msg("Error reloading configuration")
				}
			}
		case err := <-watcher.Errors:
			log.Error().Err(err).Msg("Watcher error")
		case <-ctx.Done():
			return nil
		}
	}
}
