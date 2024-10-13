package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/template"

	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/RafOSS-br/K8sVersioner/internal/git"
	"github.com/RafOSS-br/K8sVersioner/internal/store"
)

// Sync defines the interface for resource synchronization
type Sync interface {
	Synchronize(ctx context.Context, buddle *store.Buddle, objs ...*unstructured.UnstructuredList) error
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
func (s *SyncImpl) Synchronize(ctx context.Context, buddle *store.Buddle, objs ...*unstructured.UnstructuredList) error {
	logger := log.FromContext(ctx)
	logger.Info("Starting resource synchronization")

	gitClient, err := s.getGitClient(ctx, buddle)
	if err != nil {
		logger.Error(err, "Skipping config due to Git client error", "config", buddle.Cfg.Name, "namespace", buddle.Cfg.Namespace)
		return err
	}

	logger.Info("Synchronizing resources for config", "config", buddle.Cfg.Name, "namespace", buddle.Cfg.Namespace)

	for _, obj := range objs {
		for _, item := range obj.Items {
			item = *cleanResource(&item)

			if !matchesFilters(&item, buddle.Cfg.Spec.Labels, buddle.Cfg.Spec.Annotations) {
				logger.Info("Resource does not match filters, skipping", "name", item.GetName())
				continue
			}

			if err := s.syncIndividualResource(ctx, buddle, gitClient, &item); err != nil {
				if err == git.ErrAlreadyUpToDate {
					logger.Info("No changes to commit and push", "name", item.GetName())
					continue
				}
				if os.IsNotExist(err) {
					logger.Info("Resource not found in Git, skipping", "name", item.GetName())
					continue
				}
				logger.Error(err, "Error synchronizing resource", "name", item.GetName())
			}
		}
	}

	logger.Info("Resource synchronization completed successfully")
	return nil
}

func cleanResource(resource *unstructured.Unstructured) *unstructured.Unstructured {
	// Remove 'status'
	delete(resource.Object, "status")

	// Remove 'managedFields'
	resource.SetManagedFields(nil)

	// Remove 'finalizers'
	resource.SetFinalizers(nil)

	return resource
}

const (
	// MapKeySeparator is the separator used in the Git client map key
	MapKeySeparator = "/"
)

// getGitClient retrieves or creates a Git client for the given configuration
func (s *SyncImpl) getGitClient(ctx context.Context, buddle *store.Buddle) (*git.GitClient, error) {
	logger := log.FromContext(ctx)
	gitConfigKey := fmt.Sprintf("%s%s%s", buddle.Cfg.Spec.GitRef, MapKeySeparator, buddle.Cfg.Namespace)
	gitClient, exists := s.gitClients[gitConfigKey]
	var err error
	if !exists {
		gitClient, err = git.NewGitClient(ctx, buddle)
		if err != nil {
			logger.Error(err, "Error creating Git client", "config", buddle.Cfg.Name)
			return nil, err
		}
		s.gitClients[gitConfigKey] = gitClient
	}
	return gitClient, nil
}

// syncIndividualResource synchronizes an individual Kubernetes resource
func (s *SyncImpl) syncIndividualResource(ctx context.Context, buddle *store.Buddle, gitClient *git.GitClient, item *unstructured.Unstructured) error {
	logger := log.FromContext(ctx)

	data, err := s.serializeResource(item, buddle.Cfg.Spec.OutputType)
	if err != nil {
		return err
	}

	path := generateFilePath(buddle.Cfg.Spec.FolderStructure, item)

	if isDeletionEvent(item) {
		if err := gitClient.RemoveResource(ctx, path); err != nil {
			if os.IsNotExist(err) {
				logger.Info("Resource not found in Git, skipping", "name", item.GetName())
				return nil
			}
			logger.Error(err, "Error removing the resource from Git", "path", path)
			return err
		}
	} else {
		if err := gitClient.SaveResource(ctx, path, data); err != nil {
			logger.Error(err, "Error saving the resource to Git", "path", path)
			return err
		}
	}

	if err := gitClient.CommitAndPush(ctx, fmt.Sprintf("Add %s %s", item.GetKind(), item.GetName())); err != nil {
		if err == git.ErrAlreadyUpToDate {
			logger.Info("No changes to commit and push", "path", path)
			return nil
		}
		logger.Error(err, "Error committing and pushing changes to Git", "path", path)
		return err
	}

	logger.Info("Resource saved to Git", "name", item.GetName(), "namespace", item.GetNamespace(), "path", path)
	return nil
}

// Helper function to check if is deletion event
func isDeletionEvent(obj *unstructured.Unstructured) bool {
	deletionTimestamp := obj.GetDeletionTimestamp()
	return deletionTimestamp != nil
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
	logger := log.FromContext(context.Background())
	namespace := item.GetNamespace()
	if namespace == "" {
		namespace = "all"
	}
	resourceType := item.GetKind()
	resourceName := item.GetName()

	templ, err := template.New("path").Parse(structure)
	if err != nil {
		logger.Error(err, "Error parsing folder structure template")
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
		logger.Error(err, "Error executing folder structure template")
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
