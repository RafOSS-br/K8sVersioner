package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"text/template"

	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"

	k8sversionerv1alpha1 "github.com/RafOSS-br/K8sVersioner/api/v1alpha1"
	"github.com/RafOSS-br/K8sVersioner/internal/git"
	"github.com/RafOSS-br/K8sVersioner/internal/store"
)

// Sync defines the interface for resource synchronization
type Sync interface {
	Synchronize(ctx context.Context, buddle *store.Buddle) error
}

// SyncImpl is the concrete implementation of the Sync interface
type SyncImpl struct {
	dynClient  dynamic.Interface
	restMapper meta.RESTMapper
	gitClients map[string]*git.GitClient
}

// NewSync creates a new instance of SyncImpl
func NewSync(dyn dynamic.Interface, mapper meta.RESTMapper) Sync {
	return &SyncImpl{
		dynClient:  dyn,
		restMapper: mapper,
		gitClients: make(map[string]*git.GitClient),
	}
}

// Synchronize initiates the synchronization process
func (s *SyncImpl) Synchronize(ctx context.Context, buddle *store.Buddle) error {
	klog.Info("Starting resource synchronization")

	gitClient, err := s.getGitClient(ctx, buddle)
	if err != nil {
		klog.ErrorS(err, "Skipping config due to Git client error", "config", buddle.Cfg.Name, "namespace", buddle.Cfg.Namespace)
		return err
	}

	for _, resFilter := range buddle.Cfg.Spec.IncludeResource {
		if err := s.syncResourceFilter(ctx, buddle, resFilter, gitClient); err != nil {
			klog.ErrorS(err, "Error synchronizing resource filter", "filter", resFilter)
		}
	}

	klog.Info("Resource synchronization completed successfully")
	return nil
}

const (
	// MapKeySeparator is the separator used in the Git client map key
	MapKeySeparator = "/"
)

// getGitClient retrieves or creates a Git client for the given configuration
func (s *SyncImpl) getGitClient(ctx context.Context, buddle *store.Buddle) (*git.GitClient, error) {
	gitConfigKey := fmt.Sprintf("%s%s%s", buddle.Cfg.Spec.GitRef, MapKeySeparator, buddle.Cfg.Namespace)
	gitClient, exists := s.gitClients[gitConfigKey]
	if !exists {
		gitClient, err := git.NewGitClient(ctx, buddle)
		if err != nil {
			klog.ErrorS(err, "Error creating Git client")
			return nil, err
		}
		s.gitClients[gitConfigKey] = gitClient
	}
	return gitClient, nil
}

// syncResourceFilter handles synchronization for a specific resource filter
func (s *SyncImpl) syncResourceFilter(ctx context.Context, buddle *store.Buddle, resFilter k8sversionerv1alpha1.ResourceFilter, gitClient *git.GitClient) error {
	namespaces, err := s.determineNamespaces(ctx, buddle.Cfg.Namespace)
	if err != nil {
		klog.ErrorS(err, "Failed to determine namespaces", "config", buddle.Cfg.Name)
		return err
	}

	gvk := schema.FromAPIVersionAndKind(resFilter.APIVersion, resFilter.Name)
	mapping, err := s.restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		klog.ErrorS(err, "Error getting REST mapping", "kind", gvk.Kind)
		return err
	}

	for _, namespace := range namespaces {
		if err := s.syncNamespace(ctx, buddle, resFilter, mapping, namespace, gitClient); err != nil {
			klog.ErrorS(err, "Error syncing namespace", "namespace", namespace)
		}
	}

	commitMsg := fmt.Sprintf("Resources synchronized for %s/%s", buddle.Cfg.Namespace, buddle.Cfg.Name)
	if err := gitClient.CommitAndPush(ctx, commitMsg); err != nil {
		if err == git.ErrAlreadyUpToDate {
			klog.Warning("No changes to commit")
			return nil
		}
		klog.ErrorS(err, "Error committing and pushing to Git")
		return err
	}

	return nil
}

// determineNamespaces determines the list of namespaces to process based on the configuration
func (s *SyncImpl) determineNamespaces(ctx context.Context, namespace string) ([]string, error) {
	switch namespace {
	case "*", "all":
		return s.listAllNamespaces(ctx)
	case "":
		// Cluster-wide resources (no namespace)
		return []string{""}, nil
	default:
		return []string{namespace}, nil
	}
}

// listAllNamespaces retrieves all namespaces in the cluster
func (s *SyncImpl) listAllNamespaces(ctx context.Context) ([]string, error) {
	nsList, err := s.dynClient.Resource(schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "namespaces",
	}).List(ctx, metav1ListOptions())
	if err != nil {
		return nil, fmt.Errorf("failed to list namespaces: %w", err)
	}

	namespaces := make([]string, 0, len(nsList.Items))
	for _, ns := range nsList.Items {
		namespaces = append(namespaces, ns.GetName())
	}
	return namespaces, nil
}

// syncNamespace synchronizes resources within a specific namespace
func (s *SyncImpl) syncNamespace(ctx context.Context, buddle *store.Buddle, resFilter k8sversionerv1alpha1.ResourceFilter, mapping *meta.RESTMapping, namespace string, gitClient *git.GitClient) error {
	resourceClient := s.dynClient.Resource(mapping.Resource).Namespace(namespace)

	list, err := resourceClient.List(ctx, metav1ListOptions())
	if err != nil {
		klog.ErrorS(err, "Error listing resources", "resource", mapping.Resource.Resource, "namespace", namespace)
		return err
	}

	for _, item := range list.Items {
		if !matchesFilters(&item, buddle.Cfg.Spec.Labels, buddle.Cfg.Spec.Annotations) {
			continue
		}

		if err := s.syncIndividualResource(ctx, buddle, resFilter, gitClient, &item, mapping); err != nil {
			klog.ErrorS(err, "Error synchronizing resource", "resource", mapping.Resource.Resource, "name", item.GetName())
		}
	}

	return nil
}

// syncIndividualResource synchronizes an individual Kubernetes resource
func (s *SyncImpl) syncIndividualResource(ctx context.Context, buddle *store.Buddle, resFilter k8sversionerv1alpha1.ResourceFilter, gitClient *git.GitClient, item *unstructured.Unstructured, mapping *meta.RESTMapping) error {
	cleanedItem := s.prepareResource(item, resFilter)

	data, err := s.serializeResource(cleanedItem, buddle.Cfg.Spec.OutputType)
	if err != nil {
		return err
	}

	path := generateFilePath(buddle.Cfg.Spec.FolderStructure, cleanedItem)
	if err := gitClient.SaveResource(ctx, path, data); err != nil {
		klog.ErrorS(err, "Error saving the resource to Git", "path", path)
		return err
	}

	klog.InfoS("Resource saved to Git", "resource", mapping.Resource.Resource, "name", item.GetName(), "namespace", item.GetNamespace(), "path", path)
	return nil
}

// prepareResource cleans the resource based on the filter settings
func (s *SyncImpl) prepareResource(item *unstructured.Unstructured, resFilter k8sversionerv1alpha1.ResourceFilter) *unstructured.Unstructured {
	if !resFilter.WithManagedFields {
		item.SetManagedFields(nil)
	}

	if !resFilter.WithStatusField {
		delete(item.Object, "status")
	}

	return item
}

// serializeResource serializes the resource to the desired format
func (s *SyncImpl) serializeResource(item *unstructured.Unstructured, outputType string) ([]byte, error) {
	if outputType == "yaml" {
		return yaml.Marshal(item.Object)
	}
	return json.MarshalIndent(item.Object, "", "  ")
}

// generateFilePath generates the file path based on the folder structure template
func generateFilePath(structure string, item *unstructured.Unstructured) string {
	namespace := item.GetNamespace()
	if namespace == "" {
		namespace = "all"
	}
	resourceType := item.GetKind()
	resourceName := item.GetName()

	templ, err := template.New("path").Parse(structure)
	if err != nil {
		klog.ErrorS(err, "Error parsing folder structure template")
		return ""
	}

	data := struct {
		Namespace    string
		ResourceType string
		ResourceName string
	}{
		Namespace:    namespace,
		ResourceType: resourceType,
		ResourceName: resourceName,
	}

	var b bytes.Buffer
	if err := templ.Execute(&b, data); err != nil {
		klog.ErrorS(err, "Error executing folder structure template")
		return ""
	}

	return b.String()
}

// matchesFilters checks if the resource matches the specified label and annotation filters
func matchesFilters(item *unstructured.Unstructured, labels, annotations map[string]string) bool {
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

// metav1ListOptions creates a default ListOptions object
func metav1ListOptions() metav1.ListOptions {
	return metav1.ListOptions{}
}
