package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"k8s-git-operator/config"
	"k8s-git-operator/git"
	"k8s-git-operator/kubernetes"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
)

func StartController(ctx context.Context, cfgManager *config.ConfigManager) error {
	// Creating the Kubernetes config
	kubeCfg, err := kubernetes.GetKubernetesConfig()
	if err != nil {
		return err
	}

	cfg := cfgManager.GetConfig()

	// Creating the dynamic client
	dynClient, err := dynamic.NewForConfig(kubeCfg)
	if err != nil {
		return err
	}

	// Creating the clientset
	client, err := kubernetes.GetClientConfig(kubeCfg)
	if err != nil {
		return err
	}

	cachedDiscovery := memory.NewMemCacheClient(client)
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(cachedDiscovery)

	// Creating the Git client
	gitClient, err := git.NewGitClient(ctx, &cfg.Git)
	if err != nil {
		return err
	}
	log.Info().Msg("Git client created successfully")
	// Main loop
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	log.Info().Msg("Starting main controller loop")

	if cfg.DryRun {
		log.Info().Msg("Dry run mode enabled")
		err := syncResources(ctx, cfgManager, dynClient, mapper, gitClient)
		if err != nil {
			log.Error().Err(err).Msg("Error synchronizing resources")
		}
		os.Exit(0)
		return nil
	}

	for {
		log.Info().Msg("Waiting for next synchronization cycle")
		select {
		case <-ticker.C:
			if err := syncResources(ctx, cfgManager, dynClient, mapper, gitClient); err != nil {
				log.Error().Err(err).Msg("Error synchronizing resources")
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func syncResources(ctx context.Context, cfManager *config.ConfigManager, dynClient dynamic.Interface, mapper *restmapper.DeferredDiscoveryRESTMapper, gitClient *git.GitClient) error {
	log.Info().Msg("Starting resource synchronization")

	cfManager.RLock()
	defer cfManager.RUnlock()

	cfg := cfManager.GetConfig()

	for _, resFilter := range cfg.IncludeResource {
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
			if isExcluded(&item, cfg.ExcludeResource) {
				continue
			}
			var data []byte

			// Verify if load managed fields
			if !resFilter.WithManagedFields {
				item.SetManagedFields(nil)
			}

			// Verify if load status
			if !resFilter.WithStatus {
				delete(item.Object, "status")
			}

			if cfg.OutputType == "yaml" {
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

			path := generateFilePath(cfg.FolderStructure, &item)

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
