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
	Notify    func()
}

// InformerMap maps a key to a map of GroupVersionKind to Informer
type InformerMap map[string]map[schema.GroupVersionKind]*Informer

// WatcherImpl is a struct that implements the WatcherMgmt interface
type WatcherImpl struct {
	informers     InformerMap
	notify        chan *store.Buddle
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
		notify:        make(chan *store.Buddle, 100), // Adjust buffer size as needed
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
					return w.dynamicClient.Resource(gvr).Namespace("").List(context.Background(), options)
				},
				WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
					return w.dynamicClient.Resource(gvr).Namespace("").Watch(context.Background(), options)
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
		informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				u := obj.(*unstructured.Unstructured)
				logger.Info("Resource added", "gvk", gvk, "name", u.GetName())
				inf.Notify()
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				u := newObj.(*unstructured.Unstructured)
				logger.Info("Resource updated", "gvk", gvk, "name", u.GetName())
				inf.Notify()
			},
			DeleteFunc: func(obj interface{}) {
				u := obj.(*unstructured.Unstructured)
				logger.Info("Resource deleted", "gvk", gvk, "name", u.GetName())
				inf.Notify()
			},
		})

		// Assign Notify function
		inf.Notify = func() {
			select {
			case w.notify <- buddle:
			default:
				logger.Info("Notify channel is full, dropping event", "buddle", buddle.Config.Cfg.Name)
				w.synchronizer.Synchronize(ctx, buddle)
			}
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
