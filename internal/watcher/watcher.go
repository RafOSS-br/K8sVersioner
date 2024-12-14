package watcher

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/RafOSS-br/K8sVersioner/api/v1alpha1"
	"github.com/RafOSS-br/K8sVersioner/internal/store"
	synchronizer "github.com/RafOSS-br/K8sVersioner/internal/synchronizator"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// WatcherMgmt is an interface for observing a resource
type WatcherMgmt interface {
	AddListener(ctx context.Context, listen <-chan func() (*store.Bundle, error)) error
}

// Informer represents a single informer instance
type Informer struct {
	BundleFunc func() (*store.Bundle, error)
	Informer   cache.SharedInformer
	StopCh     chan struct{}
	WaitGroup  sync.WaitGroup
}

// Notify is a function that notifies the synchronizer
func (i *Informer) Notify(ctx context.Context, key string, objs *unstructured.UnstructuredList, w *WatcherImpl) {
	logger := log.FromContext(ctx)
	bundle, err := i.BundleFunc()
	if err != nil {
		logger.Error(err, "Failed to get bundle")
		return
	}
	err = w.synchronizer.Synchronize(ctx, i.BundleFunc, objs)
	if err != nil {
		logger.Error(err, "Failed to synchronize", "bundle", bundle.Config.Cfg.Name)
		return
	}
	logger.Info("Notified channel", "bundle", bundle.Config.Cfg.Name)

}

// InformerMap maps a key to a map of GroupVersionKind to Informer
type InformerMap map[string]map[schema.GroupVersionKind]*Informer

// WatcherImpl is a struct that implements the WatcherMgmt interface
type WatcherImpl struct {
	informers       InformerMap
	mu              sync.Mutex
	dynamicClient   dynamic.Interface
	scheme          *runtime.Scheme
	synchronizer    synchronizer.Sync
	mapper          *restmapper.DeferredDiscoveryRESTMapper
	discoveryClient discovery.DiscoveryInterface
}

// NewWatcherImpl returns a new WatcherImpl
func NewWatcherImpl(config *rest.Config, scheme *runtime.Scheme,
	sync synchronizer.Sync, mapper *restmapper.DeferredDiscoveryRESTMapper,
	discovery discovery.DiscoveryInterface) (WatcherMgmt, error) {
	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &WatcherImpl{
		informers:       make(InformerMap),
		dynamicClient:   dynClient,
		scheme:          scheme,
		synchronizer:    sync,
		mapper:          mapper,
		discoveryClient: discovery,
	}, nil
}

// AddListener adds a listener to the WatcherImpl
func (w *WatcherImpl) AddListener(ctx context.Context, listen <-chan func() (*store.Bundle, error)) error {
	logger := log.FromContext(ctx)

	go func() {
		for {
			select {
			case <-ctx.Done():
				logger.Info("AddListener context cancelled")
				return
			case bundleFunc, ok := <-listen:
				if !ok {
					logger.Info("Listener channel closed")
					return
				}

				bundle, err := bundleFunc()
				if err != nil {
					logger.Error(err, "Failed to get bundle")
					continue
				}

				logger.Info("Received bundle to add watcher", "bundle", bundle.Config.Cfg.Name)

				w.mu.Lock()
				if bundle.Del {
					if err := w.StopInformer(ctx, bundleFunc); err != nil {
						logger.Error(err, "Failed to stop informer", "bundle", bundle.Config.Cfg.Name)
					}
					w.mu.Unlock()
					continue
				}

				if err := w.addInformer(ctx, bundleFunc); err != nil {
					logger.Error(err, "Failed to add informer", "bundle", bundle.Config.Cfg.Name)
				}
				w.mu.Unlock()
			}
		}
	}()

	return nil
}

// StopInformer stops the informer for a given bundle
func (w *WatcherImpl) StopInformer(ctx context.Context, bundleFunc func() (*store.Bundle, error)) error {
	logger := log.FromContext(ctx)

	bundle, err := bundleFunc()
	if err != nil {
		logger.Error(err, "Failed to get bundle")
		return err
	}

	key := getKey(bundle)
	oldMap, exists := w.informers[key]
	if !exists {
		logger.Info("No informers found for bundle", "bundle", bundle.Config.Cfg.Name)
		return nil
	}

	if err := w.cleanupStaleInformers(ctx, key, nil, oldMap, bundleFunc); err != nil {
		logger.Error(err, "Failed to delete informer", "bundle", bundle.Config.Cfg.Name)
		return err
	}

	return nil
}

// addInformer adds informers for the resources in the bundle
func (w *WatcherImpl) addInformer(ctx context.Context, bundleFunc func() (*store.Bundle, error)) error {
	logger := log.FromContext(ctx)

	bundle, err := bundleFunc()
	if err != nil {
		logger.Error(err, "Failed to get bundle")
		return err
	}

	key := getKey(bundle)

	// Get the GroupVersionKinds for the resources in the bundle
	gvks, err := getGvks(w.discoveryClient, bundle)
	if err != nil {
		logger.Error(err, "Failed to get gvks", "bundle", bundle.Config.Cfg.Name)
		return err
	}

	// Delete the old informers
	oldMap, exists := w.informers[key]

	if exists {
		if err := w.cleanupStaleInformers(ctx, key, gvks, oldMap, bundleFunc); err != nil {
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
		gvk, ok := gvks[getGvkKey(res.Group, res.Version, res.Kind)]
		if !ok {
			logger.Info("GVK not found", "resource", res.Kind, "apiVersion", res.Version)
			continue
		}

		// Check if informer already exists
		if _, exists := w.informers[key][gvk]; exists {
			logger.Info("Informer already exists for resource", "gvk", gvk)
			continue
		}
		plural, err := getPlural(res.Group, res.Version, res.Kind, w.mapper)
		if err != nil {
			logger.Error(err, "Failed to get plural", "resource", res.Kind)
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
			BundleFunc: bundleFunc,
			Informer:   informer,
			StopCh:     make(chan struct{}),
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
func (w *WatcherImpl) cleanupStaleInformers(ctx context.Context, bundleKey string, gvks map[string]schema.GroupVersionKind, m map[schema.GroupVersionKind]*Informer, bundleFunc func() (*store.Bundle, error)) error {
	logger := log.FromContext(ctx)

	if len(gvks) == 0 {
		logger.Info("No resources found, deleting informers", "bundle", bundleKey)
		w.stopInformers(ctx, m, bundleFunc)
		delete(w.informers, bundleKey)
		return nil
	}

	for kGvk := range m {
		// Check if the kGvk is present in the bundle
		gvk, ok := gvks[getGvkKey(kGvk.Group, kGvk.Version, kGvk.Kind)]
		if ok {
			continue
		}

		// Stop the informer
		close(m[gvk].StopCh)
		m[gvk].WaitGroup.Wait()

		// Delete the informer from the map
		delete(m, gvk)
		logger.Info("Deleted informer", "gvk", gvk)
	}

	// If no more resources are being watched for this key, delete the map entry
	if len(m) == 0 {
		delete(w.informers, bundleKey)
	}

	return nil
}

// Helper function to stop informers
func (w *WatcherImpl) stopInformers(ctx context.Context, m map[schema.GroupVersionKind]*Informer, bundleFunc func() (*store.Bundle, error)) {
	logger := log.FromContext(ctx)

	for _, inf := range m {
		close(inf.StopCh)
		bundle, err := bundleFunc()
		if err != nil {
			logger.Error(err, "Failed to get bundle")
			return
		}
		inf.WaitGroup.Wait()
		logger.Info("Stopped informer", "bundle", bundle.Config.Cfg.Name)
	}
}

// Helper to creates a gvk key
func getGvkKey(group, version, kind string) string {
	return fmt.Sprintf("%s-%s-%s", group, version, kind)
}

// Helper function to get gvks from a bundle
func getGvks(discoveryClient discovery.DiscoveryInterface, bundle *store.Bundle) (map[string]schema.GroupVersionKind, error) {
	newIncludedResources := make([]*v1alpha1.ResourceFilter, 0)
	for _, res := range bundle.Config.Cfg.Spec.IncludeResource {
		if !isGVKComplete(res) {
			newFilters, err := resolveGVKsToResourceFilter(discoveryClient, res)
			if err != nil {
				return nil, err
			}
			newIncludedResources = append(newIncludedResources, newFilters...)
			continue
		}
		newIncludedResources = append(newIncludedResources, res)
	}
	bundle.Config.Cfg.Spec.IncludeResource = newIncludedResources
	gvks := make(map[string]schema.GroupVersionKind, len(bundle.Config.Cfg.Spec.IncludeResource))
	for _, res := range bundle.Config.Cfg.Spec.IncludeResource {
		group := res.Group
		version := res.Version
		gvk := schema.GroupVersionKind{
			Group:   group,
			Version: version,
			Kind:    res.Kind,
		}
		gvks[getGvkKey(res.Group, res.Version, res.Kind)] = gvk
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

// resolveGVKsToResourceFilter resolves incomplete GVKs to complete ResourceFilters.
// Only Kind is mandatory. Users can optionally specify Group or Version.
func resolveGVKsToResourceFilter(discoveryClient discovery.DiscoveryInterface, filter *v1alpha1.ResourceFilter) ([]*v1alpha1.ResourceFilter, error) {
	if filter == nil || strings.TrimSpace(filter.Kind) == "" {
		return nil, fmt.Errorf("filter and filter.Kind must be provided")
	}

	apiResources, err := discoveryClient.ServerPreferredResources()
	if err != nil {
		return nil, fmt.Errorf("failed to get server preferred resources: %w", err)
	}

	newResourceFilters := []*v1alpha1.ResourceFilter{}
	filterFunc, err := makeFilterFunc(filter)
	if err != nil {
		return nil, err
	}

	for _, apiResourceList := range apiResources {
		for _, apiResource := range apiResourceList.APIResources {
			if filterFunc(apiResource) {
				group, err := getGroup(apiResourceList.GroupVersion)
				if err != nil {
					return nil, err
				}
				version, err := getVersion(apiResourceList.GroupVersion)
				if err != nil {
					return nil, err
				}
				newResourceFilters = append(newResourceFilters, &v1alpha1.ResourceFilter{
					Kind:    apiResource.Kind,
					Group:   group,
					Version: version,
				})
			}
		}
	}

	if len(newResourceFilters) == 0 && isGVKComplete(filter) {
		// If no matches found but filter is complete, return the original filter
		newResourceFilters = append(newResourceFilters, filter)
	}

	return newResourceFilters, nil
}

// isGVKComplete checks if the ResourceFilter has all of Kind, Group, and Version.
func isGVKComplete(filter *v1alpha1.ResourceFilter) bool {
	return strings.TrimSpace(filter.Kind) != "" &&
		strings.TrimSpace(filter.Version) != ""
}

// makeFilterFunc creates a filter function based on the provided ResourceFilter.
// It ensures that Kind is mandatory and other fields are optional.
func makeFilterFunc(filter *v1alpha1.ResourceFilter) (func(apiResource metav1.APIResource) bool, error) {
	lowerKind := strings.ToLower(strings.TrimSpace(filter.Kind))
	if lowerKind == "" {
		return nil, fmt.Errorf("filter.Kind must be provided")
	}

	lowerGroup := strings.ToLower(strings.TrimSpace(filter.Group))
	lowerVersion := strings.ToLower(strings.TrimSpace(filter.Version))

	return func(apiResource metav1.APIResource) bool {
		// Match Kind (mandatory)
		if strings.ToLower(apiResource.Kind) != lowerKind {
			return false
		}

		// If Group is specified, match Group
		if lowerGroup != "" && strings.ToLower(apiResource.Group) != lowerGroup {
			return false
		}

		// If Version is specified, match Version
		if lowerVersion != "" && strings.ToLower(apiResource.Version) != lowerVersion {
			return false
		}

		return true
	}, nil
}

// Helper function to get plural resource name
func getPlural(group, version, kind string, mapper *restmapper.DeferredDiscoveryRESTMapper) (string, error) {
	gv := schema.GroupVersion{Group: group, Version: version}
	gvk := gv.WithKind(kind)
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
