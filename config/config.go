package config

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-playground/validator/v10"
	"github.com/rs/zerolog/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	ResourceGroup      = "example.com"
	ResourceVersion    = "v1alpha1"
	GITConfigsResource = "gitconfigs"
	ConfigsResource    = "configs"
)

type ConfigManager struct {
	mu        sync.RWMutex
	cfg       []ConfigStore
	gitMap    map[string]*GitConfig
	configMap map[string]*Config
}

func (cm *ConfigManager) GetGitMap() map[string]*GitConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if cm.gitMap != nil {
		return cm.gitMap
	}

	gitMap := make(map[string]*GitConfig)
	for _, pair := range cm.cfg {
		gitMap[pair.GitConfig.Name+MapKeySeparator+pair.GitConfig.Namespace] = pair.GitConfig
	}

	return gitMap
}

func (cm *ConfigManager) GetConfigMap() map[string]*Config {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if cm.configMap != nil {
		return cm.configMap
	}

	configMap := make(map[string]*Config)
	for _, pair := range cm.cfg {
		configMap[pair.Config.Name] = pair.Config
	}

	return configMap
}

func NewConfigManager(cfg []ConfigStore) *ConfigManager {
	return &ConfigManager{
		cfg: cfg,
	}
}

func (cm *ConfigManager) Reload(path string, dynamicClient *dynamic.DynamicClient) error {
	cfg, err := LoadConfigStore(dynamicClient)
	if err != nil {
		return err
	}

	cm.mu.Lock()
	cm.cfg = cfg
	cm.gitMap = nil
	cm.configMap = nil
	cm.mu.Unlock()

	return nil
}

type ConfigSpec struct {
	Namespace       string            `json:"namespace" validate:"required"`                  // Namespace to watch
	IncludeResource []ResourceFilter  `json:"includeResource,omitempty" validate:"dive"`      // Resources to include
	Labels          map[string]string `json:"labels,omitempty"`                               // Label filters
	OutputType      string            `json:"outputType" validate:"required,oneof=yaml json"` // Output type
	Annotations     map[string]string `json:"annotations,omitempty"`                          // Annotation filters
	GitRef          string            `json:"gitRef" validate:"required"`                     // Reference to GitConfig
	KubeConfig      string            `json:"kubeConfig" validate:"required,file"`            // KubeConfig path
	FolderStructure string            `json:"folderStructure" validate:"required"`            // Folder structure
}

type Config struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ConfigSpec `json:"spec,omitempty"`
}

type ResourceFilter struct {
	Name              string `json:"name" validate:"required"`       // Name of the resource
	APIVersion        string `json:"apiVersion" validate:"required"` // API version of the resource
	WithManagedFields bool   `json:"withManagedFields,omitempty"`    // Include managed fields
	WithStatusField   bool   `json:"withStatusField,omitempty"`      // Include status field
}

type GitConfigSpec struct {
	Protocol          string `json:"protocol" validate:"required,oneof=http https ssh"`               // Protocol
	RepositoryURL     string `json:"repositoryUrl" validate:"required,url"`                           // Repository URL
	Branch            string `json:"branch" validate:"required"`                                      // Branch
	Username          string `json:"username,omitempty" validate:"required"`                          // Username (optional)
	Password          string `json:"password,omitempty" validate:"required"`                          // Password (optional)
	SSHPrivateKeyPath string `json:"sshPrivateKeyPath,omitempty" validate:"required_if=Protocol ssh"` // SSH Private Key Path
	RepositoryPath    string `json:"repositoryPath" validate:"required"`                              // Repository Path
	RepositoryFolder  string `json:"repositoryFolder" validate:"required"`                            // Repository Folder
	DryRun            bool   `json:"dryRun,omitempty" validate:"required"`                            // Dry run mode
}

type EnvironmentConfig struct {
	OneShot       bool
	ExecutionMode string `validate:"required,oneof=kube-controller standalone"`
}

func (ec *EnvironmentConfig) Validate() error {
	validate := validator.New()
	return validate.Struct(ec)
}

const (
	DefaultRepositoryPath   = "/tmp"
	DefaultRepositoryFolder = "repo"
)

var (
	GitConfigGVR = schema.GroupVersionResource{
		Group:    ResourceGroup,
		Version:  ResourceVersion,
		Resource: GITConfigsResource,
	}

	ConfigGVR = schema.GroupVersionResource{
		Group:    ResourceGroup,
		Version:  ResourceVersion,
		Resource: ConfigsResource,
	}
)

type GitConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              GitConfigSpec `json:"spec,omitempty"`
}

func HandleValidationErrors(ctx context.Context, err error) bool {
	if validatorErr, ok := err.(validator.ValidationErrors); ok {
		for _, e := range validatorErr {
			log.Error().Str("field", e.Field()).Str("value", e.Value().(string)).Str("tag", e.Tag()).Str("options", e.Param()).Msg("Validation error")
		}
		return true
	}
	return false
}

// LoadConfigs retrieves all Config resources across all namespaces
func LoadConfigs(dynamicClient *dynamic.DynamicClient) ([]*Config, error) {
	// List all Config resources across all namespaces
	unstructuredConfigList, err := dynamicClient.Resource(ConfigGVR).
		Namespace(metav1.NamespaceAll).
		List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list Config resources: %w", err)
	}

	var configs []*Config
	validator := validator.New()

	// Iterate over the list of Config resources
	for _, item := range unstructuredConfigList.Items {
		var cfg Config
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(item.UnstructuredContent(), &cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to convert Config resource: %w", err)
		}

		// Validate the Config resource
		if err := validator.Struct(cfg.Spec); err != nil {
			return nil, fmt.Errorf("validation error in Config '%s': %w", cfg.Name, err)
		}

		configs = append(configs, &cfg)
	}

	return configs, nil
}

const MapKeySeparator = "/"

// LoadGitConfigs retrieves all GitConfig resources across all namespaces
func LoadGitConfigs(dynamicClient *dynamic.DynamicClient) (map[string]*GitConfig, error) {
	// List all GitConfig resources across all namespaces
	unstructuredGitConfigList, err := dynamicClient.Resource(GitConfigGVR).
		Namespace(metav1.NamespaceAll).
		List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list GitConfig resources: %w", err)
	}

	gitConfigs := make(map[string]*GitConfig)
	validator := validator.New()

	// Iterate over the list of GitConfig resources
	for _, item := range unstructuredGitConfigList.Items {
		var gitCfg GitConfig
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(item.UnstructuredContent(), &gitCfg)
		if err != nil {
			return nil, fmt.Errorf("failed to convert GitConfig resource: %w", err)
		}

		// Validate the GitConfig resource
		if err := validator.Struct(gitCfg.Spec); err != nil {
			return nil, fmt.Errorf("validation error in GitConfig '%s': %w", gitCfg.Name, err)
		}

		// Store GitConfig in map using its name as the key
		gitConfigs[gitCfg.Name+MapKeySeparator+gitCfg.Namespace] = &gitCfg
	}

	return gitConfigs, nil
}

type ConfigStore struct {
	Config    *Config
	GitConfig *GitConfig
}

// LoadConfigStore associates Config resources with their corresponding GitConfig resources
func LoadConfigStore(dynamicClient *dynamic.DynamicClient) ([]ConfigStore, error) {
	configs, err := LoadConfigs(dynamicClient)
	if err != nil {
		return nil, fmt.Errorf("failed to load Configs: %w", err)
	}

	gitConfigs, err := LoadGitConfigs(dynamicClient)
	if err != nil {
		return nil, fmt.Errorf("failed to load GitConfigs: %w", err)
	}

	var pairs []ConfigStore

	// Associate each Config with its corresponding GitConfig
	for _, cfg := range configs {
		gitCfg, exists := gitConfigs[cfg.Spec.GitRef+MapKeySeparator+cfg.Namespace]
		if !exists {
			return nil, fmt.Errorf("GitConfig '%s' referenced by Config '%s' not found", cfg.Spec.GitRef, cfg.Name)
		}

		pairs = append(pairs, ConfigStore{
			Config:    cfg,
			GitConfig: gitCfg,
		})
	}
	return pairs, nil
}

// func WatchConfig(ctx context.Context, cfg *ConfigManager, path string, dynamicClient *dynamic.DynamicClient) error {
// 	watcher, err := fsnotify.NewWatcher()
// 	if err != nil {
// 		return err
// 	}
// 	defer watcher.Close()

// 	err = watcher.Add(path)
// 	if err != nil {
// 		return err
// 	}

// 	for {
// 		select {
// 		case event := <-watcher.Events:
// 			if event.Op&fsnotify.Write == fsnotify.Write {
// 				if err := cfg.Reload(path, dynamicClient); err != nil {
// 					if HandleValidationErrors(ctx, err) {
// 						continue
// 					}
// 					log.Error().Err(err).Msg("Error reloading configuration")
// 				}
// 			}
// 		case err := <-watcher.Errors:
// 			log.Error().Err(err).Msg("Watcher error")
// 		case <-ctx.Done():
// 			return nil
// 		}
// 	}
// }
