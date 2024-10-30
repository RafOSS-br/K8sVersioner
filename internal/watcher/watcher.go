package watcher

import (
	"context"
	"fmt"
	"sync"

	"github.com/RafOSS-br/K8sVersioner/internal/store"
	synchronizer "github.com/RafOSS-br/K8sVersioner/internal/synchronizator"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// WatcherMgmt is an interface for observing a resource
type WatcherMgmt interface {
	AddListener(ctx context.Context, listen <-chan *store.Bundle) error
}

// Informer represents a single informer instance
type Informer struct {
	Bundle    *store.Bundle
	Informer  cache.SharedInformer
	StopCh    chan struct{}
	WaitGroup sync.WaitGroup
}

// Notify is a function that notifies the synchronizer
func (i *Informer) Notify(ctx context.Context, key string, objs *unstructured.UnstructuredList, w *WatcherImpl) {
	logger := log.FromContext(ctx)
	err := w.synchronizer.Synchronize(ctx, i.Bundle, objs)
	if err != nil {
		logger.Error(err, "Failed to synchronize", "bundle", i.Bundle.Config.Cfg.Name)
		return
	}
	logger.Info("Notified channel", "bundle", i.Bundle.Config.Cfg.Name)

}

// InformerMap maps a key to a map of GroupVersionKind to Informer
type InformerMap map[string]map[schema.GroupVersionKind]*Informer

// WatcherImpl is a struct that implements the WatcherMgmt interface
type WatcherImpl struct {
	informers     InformerMap
	mu            sync.Mutex
	dynamicClient dynamic.Interface
	scheme        *runtime.Scheme
	synchronizer  synchronizer.Sync
}

// NewWatcherImpl returns a new WatcherImpl
func NewWatcherImpl(config *rest.Config, scheme *runtime.Scheme, sync synchronizer.Sync) (WatcherMgmt, error) {
	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &WatcherImpl{
		informers:     make(InformerMap),
		dynamicClient: dynClient,
		scheme:        scheme,
		synchronizer:  sync,
	}, nil
}

// AddListener adds a listener to the WatcherImpl
func (w *WatcherImpl) AddListener(ctx context.Context, listen <-chan *store.Bundle) error {
	logger := log.FromContext(ctx)
	w.mu.Lock()
	defer w.mu.Unlock()

	go func() {
		for {
			select {
			case <-ctx.Done():
				logger.Info("AddListener context cancelled")
				return
			case bundle, ok := <-listen:
				if !ok {
					logger.Info("Listener channel closed")
					return
				}
				logger.Info("Received bundle to add watcher", "bundle", bundle.Config.Cfg.Name)
				if bundle.Del {
					if err := w.StopInformer(ctx, bundle); err != nil {
						logger.Error(err, "Failed to stop informer", "bundle", bundle.Config.Cfg.Name)
					}
					continue
				}
				if err := w.addInformer(ctx, bundle); err != nil {
					logger.Error(err, "Failed to add informer", "bundle", bundle.Config.Cfg.Name)
				}
			}
		}
	}()

	return nil
}

// StopInformer stops the informer for a given bundle
func (w *WatcherImpl) StopInformer(ctx context.Context, bundle *store.Bundle) error {
	logger := log.FromContext(ctx)
	w.mu.Lock()
	defer w.mu.Unlock()

	key := getKey(bundle)
	oldMap, exists := w.informers[key]
	if !exists {
		logger.Info("No informers found for bundle", "bundle", bundle.Config.Cfg.Name)
		return nil
	}

	if err := w.cleanupStaleInformers(ctx, key, nil, oldMap); err != nil {
		logger.Error(err, "Failed to delete informer", "bundle", bundle.Config.Cfg.Name)
		return err
	}

	return nil
}

// addInformer adds informers for the resources in the bundle
func (w *WatcherImpl) addInformer(ctx context.Context, bundle *store.Bundle) error {
	logger := log.FromContext(ctx)
	key := getKey(bundle)

	// Get the GroupVersionKinds for the resources in the bundle
	gvks, err := getGvks(bundle)
	if err != nil {
		logger.Error(err, "Failed to get gvks", "bundle", bundle.Config.Cfg.Name)
		return err
	}

	// Delete the old informers
	oldMap, exists := w.informers[key]

	if exists {
		if err := w.cleanupStaleInformers(ctx, key, gvks, oldMap); err != nil {
			logger.Error(err, "Failed to delete informer", "bundle", bundle.Config.Cfg.Name)
			return err
		}
	}

	// Create a new map for the informers
	if _, exists := w.informers[key]; !exists {
		w.informers[key] = make(map[schema.GroupVersionKind]*Informer)
	}

	for _, res := range bundle.Config.Cfg.Spec.IncludeResource {
		// Check if the GVK is present in the bundle
		gvk, ok := gvks[getGvkKey(res.Name, res.APIVersion)]
		if !ok {
			logger.Info("GVK not found", "resource", res.Name, "apiVersion", res.APIVersion)
			continue
		}

		// Check if informer already exists
		if _, exists := w.informers[key][gvk]; exists {
			logger.Info("Informer already exists for resource", "gvk", gvk)
			continue
		}
		plural, err := getPlural(res.Name, res.APIVersion)
		if err != nil {
			logger.Error(err, "Failed to get plural", "resource", res.Name)
			return err
		}

		// Create GroupVersionResource
		gvr := schema.GroupVersionResource{
			Group:    gvk.Group,
			Version:  gvk.Version,
			Resource: plural,
		}

		// Create a new SharedInformer
		informer := cache.NewSharedInformer(
			&cache.ListWatch{
				ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
					return w.dynamicClient.Resource(gvr).Namespace(bundle.Config.Cfg.Spec.Namespace).List(context.Background(), options)
				},
				WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
					return w.dynamicClient.Resource(gvr).Namespace(bundle.Config.Cfg.Spec.Namespace).Watch(context.Background(), options)
				},
			},
			&unstructured.Unstructured{},
			0, // Resync period (0 means no resync)
		)

		// Create Informer instance
		inf := &Informer{
			Bundle:   bundle,
			Informer: informer,
			StopCh:   make(chan struct{}),
		}

		// Add event handlers
		_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				u, err := assertUnstructuredList(obj)
				if err != nil {
					logger.Info("Failed to cast object to unstructured", "object", obj)
					return
				}
				logger.Info("Resource added", "gvk", gvk, "name", u.GetName())
				inf.Notify(ctx, key, &unstructured.UnstructuredList{
					Items: []unstructured.Unstructured{*obj.(*unstructured.Unstructured)},
				}, w)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				new, err := assertUnstructuredList(newObj)
				if err != nil {
					logger.Info("Failed to cast object to unstructured", "object", newObj)
					return
				}
				old, err := assertUnstructuredList(oldObj)
				if err != nil {
					logger.Info("Failed to cast object to unstructured", "object", oldObj)
					return
				}
				if new.GetResourceVersion() == old.GetResourceVersion() {
					return
				}
				logger.Info("Resource updated", "gvk", gvk, "name", new.GetName())
				inf.Notify(ctx, key, &unstructured.UnstructuredList{
					Items: []unstructured.Unstructured{*new},
				}, w)
			},
			DeleteFunc: func(obj interface{}) {
				u, err := assertUnstructuredList(obj)
				if err != nil {
					logger.Info("Failed to cast object to unstructured", "object", obj)
					return
				}
				logger.Info("Resource deleted", "gvk", gvk, "name", u.GetName())
				inf.Notify(ctx, key, &unstructured.UnstructuredList{
					Items: []unstructured.Unstructured{*obj.(*unstructured.Unstructured)},
				}, w)
			},
		})
		if err != nil {
			logger.Error(err, "Failed to add event handlers", "gvk", gvk)
			return err
		}
		// Start the informer in a separate goroutine
		inf.WaitGroup.Add(1)
		go func() {
			defer inf.WaitGroup.Done()
			logger.Info("Starting informer", "gvk", gvk)
			informer.Run(inf.StopCh)
			logger.Info("Informer stopped", "gvk", gvk)
		}()

		// Store the informer in the map
		w.informers[key][gvk] = inf
		logger.Info("Added informer", "gvk", gvk)
	}

	return nil
}

// Helper function to assert the type of an object
func assertUnstructuredList(obj interface{}) (*unstructured.Unstructured, error) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("failed to cast object to unstructured")
	}
	return u, nil
}

// Helper function to deletes a stoped used informer and stop the informer
func (w *WatcherImpl) cleanupStaleInformers(ctx context.Context, bundleKey string, gvks map[string]schema.GroupVersionKind, oldMap map[schema.GroupVersionKind]*Informer) error {
	logger := log.FromContext(ctx)

	if len(gvks) == 0 {
		logger.Info("No resources found, deleting informers", "bundle", bundleKey)
		w.stopInformers(ctx, oldMap)
		return nil
	}

	for kGvk := range oldMap {
		// Check if the kGvk is present in the bundle
		gvk, ok := gvks[getGvkKey(kGvk.Kind, kGvk.Version)]
		if ok {
			continue
		}

		// Stop the informer
		close(oldMap[gvk].StopCh)
		oldMap[gvk].WaitGroup.Wait()

		// Delete the informer from the map
		delete(oldMap, gvk)
		logger.Info("Deleted informer", "gvk", gvk)
	}

	// If no more resources are being watched for this key, delete the map entry
	if len(oldMap) == 0 {
		delete(w.informers, bundleKey)
	}

	return nil
}

// Helper function to stop informers
func (w *WatcherImpl) stopInformers(ctx context.Context, m map[schema.GroupVersionKind]*Informer) {
	logger := log.FromContext(ctx)

	for _, inf := range m {
		close(inf.StopCh)
		inf.WaitGroup.Wait()
		logger.Info("Stopped informer", "bundle", inf.Bundle.Config.Cfg.Name)
	}
}

// Helper to creates a gvk key
func getGvkKey(kind, apiVersion string) string {
	return fmt.Sprintf("%s-%s", kind, apiVersion)
}

// Helper function to get gvks from a bundle
func getGvks(bundle *store.Bundle) (map[string]schema.GroupVersionKind, error) {
	gvks := make(map[string]schema.GroupVersionKind, len(bundle.Config.Cfg.Spec.IncludeResource))
	for _, res := range bundle.Config.Cfg.Spec.IncludeResource {
		group, err := getGroup(res.APIVersion)
		if err != nil {
			return nil, err
		}
		version, err := getVersion(res.APIVersion)
		if err != nil {
			return nil, err
		}
		gvk := schema.GroupVersionKind{
			Group:   group,
			Version: version,
			Kind:    res.Name,
		}
		gvks[getGvkKey(res.Name, res.APIVersion)] = gvk
	}
	return gvks, nil
}

// Helper function to extract group from APIVersion
func getGroup(apiVersion string) (string, error) {
	parts, err := schema.ParseGroupVersion(apiVersion)
	return parts.Group, err
}

// Helper function to extract version from APIVersion
func getVersion(apiVersion string) (string, error) {
	parts, err := schema.ParseGroupVersion(apiVersion)
	return parts.Version, err
}

// Helper function to get plural resource name
func getPlural(kind, version string) (string, error) {
	gv := schema.GroupVersion{Group: "", Version: version}
	gvk := gv.WithKind(kind)
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{gv})
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return "", err
	}
	return mapping.Resource.Resource, nil
}

// getKey generates a unique key for a bundle
func getKey(bundle *store.Bundle) string {
	return bundle.Config.Cfg.Name
}
