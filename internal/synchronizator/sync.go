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
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Sync defines the interface for resource synchronization
type Sync interface {
	Synchronize(ctx context.Context, bundleFunc func() (*store.Bundle, error), objs ...*unstructured.UnstructuredList) error
}

// SyncImpl is the concrete implementation of the Sync interface
type SyncImpl struct {
	dynClient  dynamic.Interface
	restMapper meta.RESTMapper
}

// NewSync creates a new instance of SyncImpl
func NewSync(dyn dynamic.Interface, mapper meta.RESTMapper) Sync {
	return &SyncImpl{
		dynClient:  dyn,
		restMapper: mapper,
	}
}

// Synchronize initiates the synchronization process
func (s *SyncImpl) Synchronize(ctx context.Context, bundleFunc func() (*store.Bundle, error), objs ...*unstructured.UnstructuredList) error {
	logger := log.FromContext(ctx)
	logger.Info("Starting resource synchronization")
	bundle, err := bundleFunc()
	if err != nil {
		logger.Error(err, "Error getting bundle")
		return err
	}
	for {
		select {
		case <-ctx.Done():
			logger.Info("Context cancelled, stopping synchronization")
			return nil
		default:
			if !bundle.Git.TryLock() {
				continue
			}

			defer bundle.Git.Unlock()

			gitClient, err := s.getGitClient(ctx, bundle)
			if err != nil {
				logger.Error(err, "Skipping config due to Git client error", "config", bundle.Config.Cfg.Name, "namespace", bundle.Config.Cfg.Namespace)
				return err
			}

			logger.Info("Synchronizing resources for config", "config", bundle.Config.Cfg.Name, "namespace", bundle.Config.Cfg.Namespace)

			for _, obj := range objs {
				for _, item := range obj.Items {

					if !matchesFilters(&item, bundle.Config.Cfg.Spec.Labels, bundle.Config.Cfg.Spec.Annotations) {
						logger.Info("Resource does not match filters, skipping", "name", item.GetName())
						continue
					}
					if err := s.syncIndividualResource(ctx, bundle, gitClient, &item); err != nil {
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
	}
}

const (
	// MapKeySeparator is the separator used in the Git client map key
	MapKeySeparator = "/"
)

// getGitClient retrieves or creates a Git client for the given configuration
func (s *SyncImpl) getGitClient(ctx context.Context, bundle *store.Bundle) (*git.GitClient, error) {
	gitClient, err := git.NewGitClient(ctx, bundle)
	if err != nil {
		return nil, err
	}

	return gitClient, nil
}

// syncIndividualResource synchronizes an individual Kubernetes resource
func (s *SyncImpl) syncIndividualResource(ctx context.Context, bundle *store.Bundle, gitClient *git.GitClient, item *unstructured.Unstructured) error {
	logger := log.FromContext(ctx)

	data, err := s.serializeResource(bundle, item)
	if err != nil {
		return err
	}

	path := generateFilePath(bundle.Config.Cfg.Spec.FolderStructure, item)

	if isDeletionEvent(item) {
		if err := gitClient.RemoveResource(ctx, path); err != nil {
			if os.IsNotExist(err) {
				logger.Info("Resource not found in Git, skipping", "name", item.GetName())
				return nil
			}
			return err
		}
	} else {
		if err := gitClient.SaveResource(ctx, path, data); err != nil {
			return err
		}
	}

	commitMsg := fmt.Sprintf("Add %s %s", item.GetKind(), item.GetName())
	if err := gitClient.CommitAndPush(ctx, commitMsg); err != nil {
		if err == git.ErrAlreadyUpToDate {
			logger.Info("No changes to commit/push", "path", path)
			return nil
		}
		if err == git.ErrObjectNotFound {
			if err := gitClient.Reclone(ctx); err != nil {
				return err
			}
			// Retry after reclone
			if err := gitClient.CommitAndPush(ctx, commitMsg); err != nil {
				return err
			}
		} else {
			return err
		}
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
func (s *SyncImpl) serializeResource(bundle *store.Bundle, item *unstructured.Unstructured) ([]byte, error) {

	b, err := json.Marshal(item.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize item object: %w", err)
	}
	itemJSON := string(b)

	for _, path := range bundle.Config.Cfg.Spec.ExcludeFieldPaths {
		if path == "" {
			continue
		}
		if res := gjson.Get(string(itemJSON), path); res.Exists() {
			itemJSON, err = sjson.Delete(itemJSON, path)
			if err != nil {
				return nil, fmt.Errorf("failed to delete path %s: %w", path, err)
			}
		}
	}
	item = &unstructured.Unstructured{}
	err = json.Unmarshal([]byte(itemJSON), item)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal item object: %w", err)
	}

	return yaml.Marshal(item.Object)
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
