/*
Package controller provides the main controller logic for the K8sVersioner application.
*/
package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"text/template"

	"github.com/go-playground/validator/v10"

	"github.com/RafOSS-br/K8sVersioner/config"
	"github.com/RafOSS-br/K8sVersioner/git"
	"github.com/RafOSS-br/K8sVersioner/kubernetes"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/restmapper"
	"k8s.io/klog/v2"
)

// ControllerArgs holds the arguments for the controller
type ControllerArgs struct {
	CfgManager        *config.ConfigManager     `validate:"required"`
	Factory           *kubernetes.Factory       `validate:"required"`
	EnvironmentConfig *config.EnvironmentConfig `validate:"required"`
}

// StartController starts the main controller logic
func StartController(ctx context.Context, args ControllerArgs) error {
	validate := validator.New()
	if err := validate.Struct(args); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}

	var (
		cfgManager = args.CfgManager
		factory    = args.Factory
		env        = args.EnvironmentConfig
	)

	client := factory.GetClientset()
	dynClient := factory.GetDynamicClient()

	cachedDiscovery := memory.NewMemCacheClient(client)
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(cachedDiscovery)

	klog.Info("Git client created successfully")
	if env.OneShot {
		if err := syncResources(ctx, cfgManager, dynClient, mapper); err != nil {
			klog.ErrorS(err, "Error synchronizing resources")
		}
		return nil
	}

	// Collect resources to watch
	resourcesToWatch := getResourcesToWatch(ctx, cfgManager, dynClient)

	// Set up informers for resources to watch and return
	return setupInformers(ctx, dynClient, mapper, resourcesToWatch, cfgManager)
}

// getResourcesToWatch collects resources to watch based on the configurations
func getResourcesToWatch(ctx context.Context, cfManager *config.ConfigManager, dynClient dynamic.Interface) map[schema.GroupVersionKind]map[string]*ResourceWatchConfig {
	cfgMap := cfManager.GetConfigMap()

	resourcesToWatch := make(map[schema.GroupVersionKind]map[string]*ResourceWatchConfig)

	for _, cfgStore := range cfgMap {
		for _, resFilter := range cfgStore.Spec.IncludeResource {
			// Collect GVK and namespace
			gvk := schema.FromAPIVersionAndKind(resFilter.APIVersion, resFilter.Name)

			if resourcesToWatch[gvk] == nil {
				resourcesToWatch[gvk] = make(map[string]*ResourceWatchConfig)
			}

			namespaces, err := determineNamespaces(ctx, cfgStore.Namespace, dynClient)
			if err != nil {
				klog.ErrorS(err, "Failed to determine namespaces")
				continue
			}

			for _, ns := range namespaces {
				// Store the configuration and resource filter for later use
				resourcesToWatch[gvk][ns] = &ResourceWatchConfig{
					Config:    cfgStore,
					ResFilter: resFilter,
				}
			}
		}
	}

	return resourcesToWatch
}

// ResourceWatchConfig holds the configuration and resource filter for a resource
type ResourceWatchConfig struct {
	Config    *config.Config
	ResFilter config.ResourceFilter
}

// setupInformers sets up informers for the resources to watch
func setupInformers(ctx context.Context, dynClient dynamic.Interface, mapper *restmapper.DeferredDiscoveryRESTMapper, resourcesToWatch map[schema.GroupVersionKind]map[string]*ResourceWatchConfig, cfgManager *config.ConfigManager) error {
	for gvk, nsConfigMap := range resourcesToWatch {
		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			klog.ErrorS(err, "Error getting REST mapping", "gvk", gvk.String())
			continue
		}

		for ns, watchConfig := range nsConfigMap {
			// Create a dynamic informer factory for the namespace
			informerFactory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(dynClient, 0, ns, nil)
			informer := informerFactory.ForResource(mapping.Resource)

			handleInformer := kubernetes.HandleInformer{
				Add: func(obj interface{}) {
					u, ok := obj.(*unstructured.Unstructured)
					if !ok {
						klog.Error("Failed to cast object to Unstructured in Add handler")
						return
					}

					// Get the git client for this configuration
					gitClient, err := getGitClientForConfig(cfgManager, watchConfig.Config)
					if err != nil {
						klog.ErrorS(err, "Error getting Git client in Add handler")
						return
					}

					if err := syncResource(ctx, watchConfig.Config, watchConfig.ResFilter, gitClient, u, mapping); err != nil {
						klog.ErrorS(err, "Error synchronizing resource in Add handler")
					}
				},
				Update: func(oldObj, newObj interface{}) {
					u, ok := newObj.(*unstructured.Unstructured)
					if !ok {
						klog.Error("Failed to cast object to Unstructured in Update handler")
						return
					}

					// Get the git client for this configuration
					gitClient, err := getGitClientForConfig(cfgManager, watchConfig.Config)
					if err != nil {
						klog.ErrorS(err, "Error getting Git client in Update handler")
						return
					}

					if err := syncResource(ctx, watchConfig.Config, watchConfig.ResFilter, gitClient, u, mapping); err != nil {
						klog.ErrorS(err, "Error synchronizing resource in Update handler")
					}
				},
				Del: func(obj interface{}) {
					u, ok := obj.(*unstructured.Unstructured)
					if !ok {
						klog.Error("Failed to cast object to Unstructured in Delete handler")
						return
					}

					// Handle resource deletion if necessary
					klog.InfoS("Resource deleted", "name", u.GetName(), "namespace", u.GetNamespace())
				},
			}

			// Start watching in a separate goroutine
			return kubernetes.Watch(ctx, informer, handleInformer)
		}
	}
	return nil
}

// getGitClientForConfig retrieves the Git client for a given configuration
func getGitClientForConfig(cfgManager *config.ConfigManager, cfgStore *config.Config) (*git.GitClient, error) {
	gitMap := cfgManager.GetGitMap()
	gitConfigKey := cfgStore.Spec.GitRef + config.MapKeySeparator + cfgStore.Namespace

	gitConfig, ok := gitMap[gitConfigKey]
	if !ok {
		return nil, fmt.Errorf("git configuration not found for key %s", gitConfigKey)
	}

	gitClient, err := git.NewGitClient(context.Background(), gitConfig)
	if err != nil {
		return nil, fmt.Errorf("error creating Git client: %w", err)
	}

	return gitClient, nil
}

// syncResources synchronizes resources based on the provided configurations
func syncResources(ctx context.Context, cfManager *config.ConfigManager, dynClient dynamic.Interface, mapper *restmapper.DeferredDiscoveryRESTMapper) error {
	klog.Info("Starting resource synchronization")

	cfgMap := cfManager.GetConfigMap()
	gitMap := cfManager.GetGitMap()

	for _, cfgStore := range cfgMap {
		gitConfigKey := cfgStore.Spec.GitRef + config.MapKeySeparator + cfgStore.Namespace
		gitConfig, ok := gitMap[gitConfigKey]
		if !ok {
			klog.ErrorS(nil, "Git configuration not found", "config", cfgStore.Name, "namespace", cfgStore.Namespace)
			continue
		}
		gitClient, err := git.NewGitClient(ctx, gitConfig)
		if err != nil {
			klog.ErrorS(err, "Error creating Git client")
			continue
		}
		for _, resFilter := range cfgStore.Spec.IncludeResource {
			if err := sync(ctx, cfgStore, resFilter, dynClient, mapper, gitClient); err != nil {
				klog.ErrorS(err, "Error synchronizing resources")
				continue
			}
		}
	}

	klog.Info("Resource synchronization completed successfully")
	return nil
}

// sync synchronizes Kubernetes resources based on the provided configuration
func sync(ctx context.Context, cfg *config.Config, resFilter config.ResourceFilter, dynClient dynamic.Interface, mapper *restmapper.DeferredDiscoveryRESTMapper, gitClient *git.GitClient) error {
	// Determine namespaces to process
	namespaces, err := determineNamespaces(ctx, cfg.Namespace, dynClient)
	if err != nil {
		klog.ErrorS(err, "Failed to determine namespaces")
		return err
	}

	// Specific GroupVersionKind
	gvk := schema.FromAPIVersionAndKind(resFilter.APIVersion, resFilter.Name)

	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		klog.ErrorS(err, "Error getting REST mapping", "kind", gvk.Kind)
		return err
	}

	for _, namespace := range namespaces {
		resourceClient := dynClient.Resource(mapping.Resource).Namespace(namespace)

		list, err := resourceClient.List(ctx, v1.ListOptions{})
		if err != nil {
			klog.ErrorS(err, "Error listing resources", "resource", mapping.Resource.Resource, "namespace", namespace)
			continue
		}

		for _, item := range list.Items {
			// Apply label and annotation filters if necessary
			if !matchFilters(&item, cfg.Spec.Labels, cfg.Spec.Annotations) {
				continue
			}

			if err := syncResource(ctx, cfg, resFilter, gitClient, &item, mapping); err != nil {
				klog.ErrorS(err, "Error synchronizing resource", "resource", mapping.Resource.Resource, "name", item.GetName())
				continue
			}
		}
	}

	// Commit and push the changes
	message := fmt.Sprintf("Resources synchronized for %s/%s", cfg.Namespace, cfg.Name)
	if err := gitClient.CommitAndPush(ctx, message); err != nil {
		if err == git.ErrAlreadyUpToDate {
			klog.Warning("No changes to commit")
			return nil
		}
		klog.ErrorS(err, "Error committing and pushing to Git")
		return err
	}
	return nil
}

// syncResource synchronizes an individual Kubernetes resource
func syncResource(ctx context.Context, cfg *config.Config, resFilter config.ResourceFilter, gitClient *git.GitClient, item *unstructured.Unstructured, mapping *meta.RESTMapping) error {
	// Remove managed fields if not required
	if !resFilter.WithManagedFields {
		item.SetManagedFields(nil)
	}

	// Remove status field if not required
	if !resFilter.WithStatusField {
		delete(item.Object, "status")
	}

	var data []byte
	var err error

	// Serialize the resource
	if cfg.Spec.OutputType == "yaml" {
		data, err = yaml.Marshal(item.Object)
		if err != nil {
			klog.ErrorS(err, "Error serializing the resource to YAML", "resource", mapping.Resource.Resource, "name", item.GetName())
			return err
		}
	} else {
		data, err = json.MarshalIndent(item.Object, "", "  ")
		if err != nil {
			klog.ErrorS(err, "Error serializing the resource to JSON", "resource", mapping.Resource.Resource, "name", item.GetName())
			return err
		}
	}

	// Generate file path based on folder structure
	path := generateFilePath(cfg.Spec.FolderStructure, item)

	// Save the resource to Git
	if err := gitClient.SaveResource(ctx, path, data); err != nil {
		klog.ErrorS(err, "Error saving the resource to Git", "path", path)
		return err
	}

	klog.InfoS("Resource saved to Git", "resource", mapping.Resource.Resource, "name", item.GetName(), "namespace", item.GetNamespace(), "path", path)

	message := fmt.Sprintf("Resource %s/%s synchronized for %s/%s", item.GetNamespace(), item.GetName(), cfg.Namespace, cfg.Name)

	if err := gitClient.CommitAndPush(ctx, message); err != nil {
		if err == git.ErrAlreadyUpToDate {
			klog.Warning("No changes to commit")
		} else {
			klog.ErrorS(err, "Error committing and pushing to Git")
		}
		return err
	}
	return nil
}

// determineNamespaces determines the list of namespaces to process based on the configuration
func determineNamespaces(ctx context.Context, namespace string, dynClient dynamic.Interface) ([]string, error) {
	switch namespace {
	case "*", "all":
		nsList, err := dynClient.Resource(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}).List(ctx, v1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to list namespaces: %w", err)
		}
		namespaces := make([]string, 0, len(nsList.Items))
		for _, ns := range nsList.Items {
			namespaces = append(namespaces, ns.GetName())
		}
		return namespaces, nil
	case "":
		// Cluster-wide resources (no namespace)
		return []string{""}, nil
	default:
		return []string{namespace}, nil
	}
}

// matchFilters(item *unstructured.Unstructured, labels, annotations map[string]string)
func matchFilters(item *unstructured.Unstructured, labels, annotations map[string]string) bool {
	// Implement logic to filter by labels and annotations
	if len(labels) > 0 {
		for key, value := range labels {
			if item.GetLabels()[key] != value {
				return false
			}
		}
	}
	if len(annotations) > 0 {
		for key, value := range annotations {
			if item.GetAnnotations()[key] != value {
				return false
			}
		}
	}
	return true
}

// generateFilePath generates the file path based on the folder structure template
func generateFilePath(structure string, item *unstructured.Unstructured) string {
	// Extract namespace, resource type, and resource name from the input object
	namespace := item.GetNamespace()
	if namespace == "" {
		namespace = "all"
	}
	resourceType := item.GetKind()
	resourceName := item.GetName()

	// Parse the provided template structure
	templ, err := template.New("path").Parse(structure)
	if err != nil {
		klog.ErrorS(err, "Error parsing folder structure template")
		return ""
	}

	// Prepare the data to fill the template with proper capitalization for template keys
	data := struct {
		Namespace,
		ResourceType,
		ResourceName string
	}{
		Namespace:    namespace,
		ResourceType: resourceType,
		ResourceName: resourceName,
	}

	// Execute the template and capture the output
	var b bytes.Buffer
	if err := templ.Execute(&b, data); err != nil {
		klog.ErrorS(err, "Error executing folder structure template")
		return ""
	}

	// Return the generated file path
	return b.String()
}
