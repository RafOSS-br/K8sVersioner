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
	Namespace       string            `json:"namespace"`       // Namespace is the namespace to watch for resources
	IncludeResource []ResourceFilter  `json:"includeResource"` // IncludeResource is a list of resources to include in the output
	ExcludeResource []ResourceFilter  `json:"excludeResource"` // ExcludeResource is a list of resources to exclude from the output
	Labels          map[string]string `json:"labels"`          // Labels is a list of labels to filter the resources
	OutputType      string            `json:"outputType"`      // OutputType is the type of output to generate (yaml or json)
	Annotations     map[string]string `json:"annotations"`     // Annotations is a list of annotations to filter the resources
	Git             GitConfig         `json:"git"`             // Git is the configuration for the git repository
	KubeConfig      string            `json:"kubeConfig"`      // KubeConfig is the path to the kubeconfig file
	FolderStructure string            `json:"folderStructure"` // FolderStructure is the folder structure to use for the output. Example: "namespace/resourceType/resourceName.yaml" or "resourceType/resourceName.yaml". This is replaced with the actual values from the resource
	OneShot         bool              `json:"oneShot"`         // OneShot is a flag to enable one-shot mode. In this mode, the resources are pushed to the git repository only once
}

type ResourceFilter struct {
	Name              string `json:"name"`              // Name is the name of the resource
	APIVersion        string `json:"apiVersion"`        // APIVersion is the API version of the resource
	WithManagedFields bool   `json:"withManagedFields"` // WithManagedFields is a flag to include the managed fields in the output
	WithStatusField   bool   `json:"withStatusField"`   // WithStatusField is a flag to include the status field in the output
}

type GitConfig struct {
	RepositoryURL  string `json:"repositoryUrl"`  // RepositoryURL is the URL of the git repository
	Branch         string `json:"branch"`         // Branch is the branch to clone and push to the git repository
	Username       string `json:"username"`       // Username is the username to authenticate with the git repository
	Password       string `json:"password"`       // Password is the password to authenticate with the git repository
	RepositoryPath string `json:"repositoryPath"` // RepositoryPath is the path to the local git repository
	DryRun         bool   `json:"dryRun"`         // DryRun is a flag to enable dry-run mode. In this mode, the resources are not pushed to the git repository

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
