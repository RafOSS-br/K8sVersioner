/*
Package config provides the configuration types and functions for the K8sVersioner application.
*/
package config

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-playground/validator/v10"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/klog/v2"

	"github.com/RafOSS-br/K8sVersioner/internal/kubernetes"
)

const (
	ResourceGroup      = "example.com" // Group of the resources
	ResourceVersion    = "v1alpha1"    // Version of the resources
	GITConfigsResource = "gitconfigs"  // Resource for GitConfig
	ConfigsResource    = "configs"     // Resource for Config
)

// ConfigManager is a struct that manages the configuration of the application
type ConfigManager struct {
	mu        sync.RWMutex
	cfg       []ConfigStore
	gitMap    map[string]*GitConfig
	configMap map[string]*Config
}

// Lock locks the ConfigManager
func (cm *ConfigManager) Lock() {
	cm.mu.Lock()
}

// Unlock unlocks the ConfigManager
func (cm *ConfigManager) Unlock() {
	cm.mu.Unlock()
}

// RLock locks the ConfigManager for reading
func (cm *ConfigManager) RLock() {
	cm.mu.RLock()
}

// RUnlock unlocks the ConfigManager after reading
func (cm *ConfigManager) RUnlock() {
	cm.mu.RUnlock()
}

// GetGitMap returns a map of GitConfig resources
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

// GetConfigMap returns a map of Config resources
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

// NewConfigManager creates a new ConfigManager
func NewConfigManager(cfg []ConfigStore) *ConfigManager {
	return &ConfigManager{
		cfg: cfg,
	}
}

// ConfigUpdated updates the configuration
func (cm *ConfigManager) ConfigUpdated(kubeFactory *kubernetes.KubeClientFactory) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cfg, err := LoadConfigStore(kubeFactory.GetDynamicClient())
	if err != nil {
		klog.ErrorS(err, "Error reloading configuration")
		return
	}
	cm.cfg = cfg
}

// ConfigSpec is a struct that contains the specification of a Config resource
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

// Config is a struct that represents a Config resource
type Config struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ConfigSpec `json:"spec,omitempty"`
}

// ResourceFilter is a struct that contains the specification of a resource filter
type ResourceFilter struct {
	Name              string `json:"name" validate:"required"`       // Name of the resource
	APIVersion        string `json:"apiVersion" validate:"required"` // API version of the resource
	WithManagedFields bool   `json:"withManagedFields,omitempty"`    // Include managed fields
	WithStatusField   bool   `json:"withStatusField,omitempty"`      // Include status field
}

// GitConfigSpec is a struct that contains the specification of a GitConfig resource
type GitConfigSpec struct {
	Protocol          string `json:"protocol" validate:"required,oneof=http https ssh"`               // Protocol
	RepositoryURL     string `json:"repositoryUrl" validate:"required,url"`                           // Repository URL
	Branch            string `json:"branch" validate:"required"`                                      // Branch
	Username          string `json:"username,omitempty" validate:"required"`                          // Username (optional)
	Password          string `json:"password,omitempty" validate:"required"`                          // Password (optional)
	SSHPrivateKeyPath string `json:"sshPrivateKeyPath,omitempty" validate:"required_if=Protocol ssh"` // SSH Private Key Path
	RepositoryPath    string `json:"repositoryPath" validate:"required"`                              // Repository Path
	RepositoryFolder  string `json:"repositoryFolder" validate:"required"`                            // Repository Folder
	DryRun            bool   `json:"dryRun,omitempty"`                                                // Dry run mode
}

// EnvironmentConfig is a struct that contains the configuration of the environment
type EnvironmentConfig struct {
	OneShot                       bool
	ExecutionMode                 string             `validate:"required,oneof=kube-controller standalone"`
	*kubernetes.KubeClientFactory                    // Generated in the run function, do not set manually
	Context                       context.Context    // Generated in the run function, do not set manually
	Cancel                        context.CancelFunc // Generated in the run function, do not set manually
}

// Validate validates the EnvironmentConfig
func (ec *EnvironmentConfig) Validate() error {
	validate := validator.New()
	return validate.Struct(ec)
}

const (
	DefaultRepositoryFolder = "repo" // Default folder for the repository
)

var (
	// GVRs for Config and GitConfig resources
	GitConfigGVR = schema.GroupVersionResource{
		Group:    ResourceGroup,
		Version:  ResourceVersion,
		Resource: GITConfigsResource,
	}

	// GVR for Config resources
	ConfigGVR = schema.GroupVersionResource{
		Group:    ResourceGroup,
		Version:  ResourceVersion,
		Resource: ConfigsResource,
	}
)

// GitConfig is a struct that represents a GitConfig resource
type GitConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              GitConfigSpec `json:"spec,omitempty"`
}

// HandleValidationErrors logs validation errors
func HandleValidationErrors(ctx context.Context, err error) bool {
	if validatorErr, ok := err.(validator.ValidationErrors); ok {
		for _, e := range validatorErr {
			klog.ErrorS(err, "Validation error", "field", e.Field(), "value", e.Value().(string), "tag", e.Tag(), "options", e.Param())
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

	var configs = make([]*Config, 0, len(unstructuredConfigList.Items))
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

const MapKeySeparator = "/" // Separator for map keys

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

// ConfigStore is a struct that associates Config resources with their corresponding GitConfig resources
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

	var pairs []ConfigStore = make([]ConfigStore, 0, len(configs))

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

// WatchConfig watches for changes in the Config resource
func WatchConfig(ctx context.Context, cfgManager *ConfigManager, kubeFactory *kubernetes.KubeClientFactory) {
	dynClient := kubeFactory.GetDynamicClient()

	factory := dynamicinformer.NewDynamicSharedInformerFactory(dynClient, 0)

	cfgInformer := factory.ForResource(ConfigGVR)
	errHandler := func(cause error) {
		if cause != nil {
			klog.Fatal(cause, "Error watching Config resource")
		}
	}

	go func() {
		err := kubernetes.Watch(ctx, cfgInformer, kubernetes.HandleInformer{
			Add:    func(obj interface{}) { cfgManager.ConfigUpdated(kubeFactory) },
			Del:    func(obj interface{}) { cfgManager.ConfigUpdated(kubeFactory) },
			Update: func(oldObj, newObj interface{}) { cfgManager.ConfigUpdated(kubeFactory) },
		})
		errHandler(err)
	}()

	gitInformer := factory.ForResource(GitConfigGVR)
	go func() {
		err := kubernetes.Watch(ctx, gitInformer, kubernetes.HandleInformer{
			Add:    func(obj interface{}) { cfgManager.ConfigUpdated(kubeFactory) },
			Del:    func(obj interface{}) { cfgManager.ConfigUpdated(kubeFactory) },
			Update: func(oldObj, newObj interface{}) { cfgManager.ConfigUpdated(kubeFactory) },
		})
		errHandler(err)
	}()
}
