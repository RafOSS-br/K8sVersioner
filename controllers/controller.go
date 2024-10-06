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

		if err := sync(ctx, cfgStore, dynClient, mapper, gitClient); err != nil {
			log.Error().Err(err).Msg("Error synchronizing resources")
			continue
		}
	}

	log.Info().Msg("Resource synchronization completed successfully")
	return nil
}

func sync(ctx context.Context, cfg *config.Config, dynClient dynamic.Interface, mapper *restmapper.DeferredDiscoveryRESTMapper, gitClient *git.GitClient) error {
	for _, resFilter := range cfg.Spec.IncludeResource {
		var gvk schema.GroupVersionKind
		switch {
		case resFilter.Name == "*" || resFilter.Name == "":
			log.Error().Str("resource", resFilter.Name).Str("apiVersion", resFilter.APIVersion).Msg(fmt.Sprintf("Resource name cannot be \"%s\"", resFilter.Name))
			continue
		case resFilter.APIVersion == "*":
			log.Error().Str("resource", resFilter.Name).Str("apiVersion", resFilter.APIVersion).Msg(fmt.Sprintf("API version cannot be \"%s\"", resFilter.APIVersion))
			continue
		default:
			gvk = schema.FromAPIVersionAndKind(resFilter.APIVersion, resFilter.Name)
		}
		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			log.Error().Err(err).Msgf("Error getting mapping for %s", resFilter.Name)
			continue
		}

		var namespace string
		if cfg.Namespace != "" && cfg.Namespace != "all" {
			namespace = cfg.Namespace
		} else {
			namespace = ""
		}

		resourceClient := dynClient.Resource(mapping.Resource).Namespace(namespace)

		list, err := resourceClient.List(ctx, v1.ListOptions{})
		if err != nil {
			log.Error().Err(err).Msgf("Error listing resources %s", resFilter.Name)
			continue
		}

		for _, item := range list.Items {
			// Apply label and annotation filters if necessary
			if !matchFilters(&item, cfg.Labels, cfg.Annotations) {
				continue
			}

			// Exclude resources if they are in the exclusion list
			if isExcluded(&item, cfg.Spec.ExcludeResource) {
				continue
			}
			var data []byte

			// Verify if load managed fields
			if !resFilter.WithManagedFields {
				item.SetManagedFields(nil)
			}

			// Verify if load status
			if !resFilter.WithStatusField {
				delete(item.Object, "status")
			}

			if cfg.Spec.OutputType == "yaml" {
				data, err = yaml.Marshal(item.Object)
				if err != nil {
					log.Error().Err(err).Msg("Error serializing the resource")
					continue
				}
			} else {
				data, err = json.MarshalIndent(item.Object, "", "  ")
				if err != nil {
					log.Error().Err(err).Msg("Error serializing the resource")
					continue
				}
			}

			path := generateFilePath(cfg.Spec.FolderStructure, &item)

			if err := gitClient.SaveResource(ctx, path, data); err != nil {
				log.Error().Err(err).Msg("Error saving the resource to Git")
				continue
			}
		}
	}

	// Commit and push the changes
	message := fmt.Sprintf("Resource synchronization on %s", time.Now().Format(time.RFC3339))
	if err := gitClient.CommitAndPush(ctx, message); err != nil {
		log.Error().Err(err).Msg("Error committing and pushing to Git")
		return err
	}

	log.Info().Msg("Synchronization completed successfully")
	return nil
}

// matchFilters(item *unstructured.Unstructured, labels, annotations map[string]string)
func matchFilters(_ *unstructured.Unstructured, _, _ map[string]string) bool {
	// Implement logic to filter by labels and annotations
	return true
}

// isExcluded(item *unstructured.Unstructured, exclude []config.ResourceFilter)
func isExcluded(_ *unstructured.Unstructured, _ []config.ResourceFilter) bool {
	// Implement logic to check if the resource is in the exclusion list
	return false
}

// generateFilePath(structure string, item *unstructured.Unstructured) string
func generateFilePath(_ string, item *unstructured.Unstructured) string {
	// Simple example of path generation
	namespace := item.GetNamespace()
	if namespace == "" {
		namespace = "cluster"
	}
	resourceType := item.GetKind()
	resourceName := item.GetName()
	return fmt.Sprintf("%s/%s/%s.yaml", namespace, resourceType, resourceName)
}
