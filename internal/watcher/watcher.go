package watcher

import (
	"context"
	"fmt"
	"sync"

	"github.com/RafOSS-br/K8sVersioner/internal/store"
	synchronizer "github.com/RafOSS-br/K8sVersioner/internal/synchronizator"
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
	AddListener(ctx context.Context, listen <-chan *store.Buddle) error
}

// Informer represents a single informer instance
type Informer struct {
	Buddle    *store.Buddle
	Informer  cache.SharedInformer
	StopCh    chan struct{}
	WaitGroup sync.WaitGroup
}

// Notify is a function that notifies the synchronizer
func (i *Informer) Notify(ctx context.Context, key string, objs *unstructured.UnstructuredList, w *WatcherImpl) {
	logger := log.FromContext(ctx)
	err := w.synchronizer.Synchronize(ctx, i.Buddle, objs)
	if err != nil {
		logger.Error(err, "Failed to synchronize", "buddle", i.Buddle.Config.Cfg.Name)
		return
	}
	logger.Info("Notified channel", "buddle", i.Buddle.Config.Cfg.Name)

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
func (w *WatcherImpl) AddListener(ctx context.Context, listen <-chan *store.Buddle) error {
	logger := log.FromContext(ctx)
	w.mu.Lock()
	defer w.mu.Unlock()

	go func() {
		for {
			select {
			case <-ctx.Done():
				logger.Info("AddListener context cancelled")
				return
			case buddle, ok := <-listen:
				if !ok {
					logger.Info("Listener channel closed")
					return
				}
				logger.Info("Received buddle to add watcher", "buddle", buddle.Config.Cfg.Name)
				if err := w.addInformer(ctx, buddle); err != nil {
					logger.Error(err, "Failed to add informer", "buddle", buddle.Config.Cfg.Name)
				}
			}
		}
	}()

	return nil
}

// addInformer adds informers for the resources in the buddle
func (w *WatcherImpl) addInformer(ctx context.Context, buddle *store.Buddle) error {
	logger := log.FromContext(ctx)
	key := getKey(buddle)

	// Get the GroupVersionKinds for the resources in the buddle
	gvks, err := getGvks(buddle)
	if err != nil {
		logger.Error(err, "Failed to get gvks", "buddle", buddle.Config.Cfg.Name)
		return err
	}

	// Delete the old informers
	oldMap, exists := w.informers[key]

	if exists {
		if err := w.deleteInformer(ctx, key, gvks, oldMap); err != nil {
			logger.Error(err, "Failed to delete informer", "buddle", buddle.Config.Cfg.Name)
			return err
		}
	}

	// Create a new map for the informers
	if _, exists := w.informers[key]; !exists {
		w.informers[key] = make(map[schema.GroupVersionKind]*Informer)
	}

	for _, res := range buddle.Config.Cfg.Spec.IncludeResource {
		// Check if the GVK is present in the buddle
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
		// Create GroupVersionResource
		gvr := schema.GroupVersionResource{
			Group:    gvk.Group,
			Version:  gvk.Version,
			Resource: getPlural(res.Name),
		}

		// Create a new SharedInformer
		informer := cache.NewSharedInformer(
			&cache.ListWatch{
				ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
					return w.dynamicClient.Resource(gvr).Namespace(buddle.Cfg.Spec.Namespace).List(context.Background(), options)
				},
				WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
					return w.dynamicClient.Resource(gvr).Namespace(buddle.Cfg.Spec.Namespace).Watch(context.Background(), options)
				},
			},
			&unstructured.Unstructured{},
			0, // Resync period (0 means no resync)
		)

		// Create Informer instance
		inf := &Informer{
			Buddle:   buddle,
			Informer: informer,
			StopCh:   make(chan struct{}),
		}

		// Add event handlers
		_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
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
func (w *WatcherImpl) deleteInformer(ctx context.Context, buddleKey string, gvks map[string]schema.GroupVersionKind, oldMap map[schema.GroupVersionKind]*Informer) error {
	logger := log.FromContext(ctx)

	if len(gvks) == 0 {
		logger.Info("No resources found, deleting informers", "buddle", buddleKey)
		w.stopInformers(ctx, oldMap)
		return nil
	}

	for kGvk := range oldMap {
		// Check if the kGvk is present in the buddle
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
		delete(w.informers, buddleKey)
	}

	return nil
}

// Helper function to stop informers
func (w *WatcherImpl) stopInformers(ctx context.Context, m map[schema.GroupVersionKind]*Informer) {
	logger := log.FromContext(ctx)

	for _, inf := range m {
		close(inf.StopCh)
		inf.WaitGroup.Wait()
		logger.Info("Stopped informer", "buddle", inf.Buddle.Config.Cfg.Name)
	}
}

// Helper to creates a gvk key
func getGvkKey(kind, apiVersion string) string {
	return fmt.Sprintf("%s-%s", kind, apiVersion)
}

// Helper function to get gvks from a buddle
func getGvks(buddle *store.Buddle) (map[string]schema.GroupVersionKind, error) {
	gvks := make(map[string]schema.GroupVersionKind, len(buddle.Config.Cfg.Spec.IncludeResource))
	for _, res := range buddle.Config.Cfg.Spec.IncludeResource {
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
func getPlural(kind string) string {
	// Simple pluralization logic, can be enhanced
	return fmt.Sprintf("%ss", kind)
}

// getKey generates a unique key for a buddle
func getKey(buddle *store.Buddle) string {
	return buddle.Config.Cfg.Name
}
