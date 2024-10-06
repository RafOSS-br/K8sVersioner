package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/RafOSS-br/K8sVersioner/config"
	"github.com/RafOSS-br/K8sVersioner/git"
	"github.com/RafOSS-br/K8sVersioner/kubernetes"
	"github.com/go-playground/validator/v10"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
)

type ControllerArgs struct {
	CfgManager        *config.ConfigManager     `validate:"required"`
	K8sClient         *kubernetes.K8sClient     `validate:"required"`
	EnvironmentConfig *config.EnvironmentConfig `validate:"required"`
}

func StartController(ctx context.Context, args ControllerArgs) error {

	validate := validator.New()
	if err := validate.Struct(args); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}

	var (
		cfgManager = args.CfgManager
		k8sClient  = args.K8sClient
		env        = args.EnvironmentConfig
	)

	client := k8sClient.GetClientset()
	dynClient := k8sClient.GetDynamicClient()

	cachedDiscovery := memory.NewMemCacheClient(client)
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(cachedDiscovery)

	log.Info().Msg("Git client created successfully")
	if env.OneShot {
		if err := syncResources(ctx, cfgManager, dynClient, mapper); err != nil {
			log.Error().Err(err).Msg("Error synchronizing resources")
		}
		return nil
	}
	// Main loop
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	log.Info().Msg("Starting main controller loop")
	for {
		log.Info().Msg("Waiting for next synchronization cycle")
		select {
		case <-ticker.C:
			if err := syncResources(ctx, cfgManager, dynClient, mapper); err != nil {
				log.Error().Err(err).Msg("Error synchronizing resources")
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func syncResources(ctx context.Context, cfManager *config.ConfigManager, dynClient dynamic.Interface, mapper *restmapper.DeferredDiscoveryRESTMapper) error {
	log.Info().Msg("Starting resource synchronization")

	cfgMap := cfManager.GetConfigMap()
	gitMap := cfManager.GetGitMap()

	var (
		gitConfig *config.GitConfig
		ok        bool
	)

	for _, cfgStore := range cfgMap {
		gitConfig, ok = gitMap[cfgStore.Spec.GitRef+config.MapKeySeparator+cfgStore.Namespace]
		if !ok {
			log.Error().Str("config", cfgStore.Name).Str("namespace", cfgStore.Namespace).Msg("Git configuration not found")
			continue
		}
		gitClient, err := git.NewGitClient(ctx, gitConfig)
		if err != nil {
			log.Error().Err(err).Msg("Error creating Git client")
			continue
		}
		for _, resFilter := range cfgStore.Spec.IncludeResource {
			if err := sync(ctx, cfgStore, resFilter, dynClient, mapper, gitClient); err != nil {
				log.Error().Err(err).Msg("Error synchronizing resources")
				continue
			}
		}
	}

	log.Info().Msg("Resource synchronization completed successfully")
	return nil
}

// sync synchronizes Kubernetes resources based on the provided configuration
func sync(ctx context.Context, cfg *config.Config, resFilter config.ResourceFilter, dynClient dynamic.Interface, mapper *restmapper.DeferredDiscoveryRESTMapper, gitClient *git.GitClient) error {
	// Determine namespaces to process
	namespaces, err := determineNamespaces(ctx, cfg.Namespace, dynClient)
	if err != nil {
		log.Error().Err(err).Msg("Failed to determine namespaces")
		return err
	}

	var gvkList []schema.GroupVersionKind

	// Specific GroupVersionKind
	gvk := schema.FromAPIVersionAndKind(resFilter.APIVersion, resFilter.Name)
	gvkList = append(gvkList, gvk)

	for _, gvk := range gvkList {
		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			log.Error().
				Err(err).
				Str("kind", gvk.Kind).
				Msg("Error getting REST mapping")
			continue
		}

		for _, namespace := range namespaces {
			resourceClient := dynClient.Resource(mapping.Resource).Namespace(namespace)

			list, err := resourceClient.List(ctx, v1.ListOptions{})
			if err != nil {
				log.Error().
					Err(err).
					Str("resource", mapping.Resource.Resource).
					Str("namespace", namespace).
					Msg("Error listing resources")
				continue
			}

			for _, item := range list.Items {
				// Apply label and annotation filters if necessary
				if !matchFilters(&item, cfg.Labels, cfg.Annotations) {
					continue
				}

				var data []byte

				// Remove managed fields if not required
				if !resFilter.WithManagedFields {
					item.SetManagedFields(nil)
				}

				// Remove status field if not required
				if !resFilter.WithStatusField {
					delete(item.Object, "status")
				}

				// Serialize the resource
				if cfg.Spec.OutputType == "yaml" {
					data, err = yaml.Marshal(item.Object)
					if err != nil {
						log.Error().
							Err(err).
							Str("resource", mapping.Resource.Resource).
							Str("name", item.GetName()).
							Msg("Error serializing the resource to YAML")
						continue
					}
				} else {
					data, err = json.MarshalIndent(item.Object, "", "  ")
					if err != nil {
						log.Error().
							Err(err).
							Str("resource", mapping.Resource.Resource).
							Str("name", item.GetName()).
							Msg("Error serializing the resource to JSON")
						continue
					}
				}

				// Generate file path based on folder structure
				path := generateFilePath(cfg.Spec.FolderStructure, &item)

				// Save the resource to Git
				if err := gitClient.SaveResource(ctx, path, data); err != nil {
					log.Error().
						Err(err).
						Str("path", path).
						Msg("Error saving the resource to Git")
					continue
				}

				log.Info().
					Str("resource", mapping.Resource.Resource).
					Str("name", item.GetName()).
					Str("namespace", item.GetNamespace()).
					Str("path", path).
					Msg("Resource saved to Git")
			}
		}
	}

	// Commit and push the changes
	message := fmt.Sprintf("Resource synchronization on %s", time.Now().Format(time.RFC3339))
	if err := gitClient.CommitAndPush(ctx, message); err != nil {
		log.Error().
			Err(err).
			Msg("Error committing and pushing to Git")
		return err
	}
	return nil
}

// determineNamespaces determines the list of namespaces to process based on the configuration
func determineNamespaces(ctx context.Context, namespace string, dynClient dynamic.Interface) ([]string, error) {
	if namespace == "*" || namespace == "all" {
		nsList, err := dynClient.Resource(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}).List(ctx, v1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to list namespaces: %w", err)
		}
		namespaces := make([]string, 0, len(nsList.Items))
		for _, ns := range nsList.Items {
			namespaces = append(namespaces, ns.GetName())
		}
		return namespaces, nil
	} else if namespace != "" {
		return []string{namespace}, nil
	} else {
		// Cluster-wide resources (no namespace)
		return []string{""}, nil
	}
}

// // deleteStatusField removes the "status" field from the resource object
// func deleteStatusField(obj map[string]interface{}) map[string]interface{} {
// 	delete(obj, "status")
// 	return obj
// }

// // serializeResource serializes the resource object based on the specified output type
// func serializeResource(item *unstructured.Unstructured, outputType string) ([]byte, error) {
// 	if outputType == "yaml" {
// 		return yaml.Marshal(item.Object)
// 	}
// 	return json.MarshalIndent(item.Object, "", "  ")
// }

// matchFilters(item *unstructured.Unstructured, labels, annotations map[string]string)
func matchFilters(_ *unstructured.Unstructured, _, _ map[string]string) bool {
	// Implement logic to filter by labels and annotations
	return true
}

// generateFilePath(structure string, item *unstructured.Unstructured) string
func generateFilePath(_ string, item *unstructured.Unstructured) string {
	// Simple example of path generation
	namespace := item.GetNamespace()
	if namespace == "" {
		namespace = "all"
	}
	resourceType := item.GetKind()
	resourceName := item.GetName()
	return fmt.Sprintf("%s/%s/%s.yaml", namespace, resourceType, resourceName)
}
