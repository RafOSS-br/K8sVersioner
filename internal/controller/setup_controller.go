package controller

import (
	"context"

	"github.com/RafOSS-br/K8sVersioner/internal/store"
	sync "github.com/RafOSS-br/K8sVersioner/internal/synchronizator"
	"github.com/RafOSS-br/K8sVersioner/internal/watcher"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// SetupController is a structure that implements the Runnable interface
type SetupController struct {
	config *rest.Config
	scheme *runtime.Scheme
}

// NewSetupController creates a new SetupController
func NewSetupController(config *rest.Config, scheme *runtime.Scheme) *SetupController {
	return &SetupController{
		config: config,
		scheme: scheme,
	}
}

// Start executes the runner logic. This method blocks until the context is canceled.
func (mc *SetupController) Start(ctx context.Context) error {
	logger := log.FromContext(ctx)
	go func() {
		for {
			select {
			case <-ctx.Done():
				logger.Info("SetupController received cancel signal")
				return
			default:
				logger.Info("Starting SetupController")
				storeConfig := store.StoreSingleton
				ch := storeConfig.ConfigProducer()

				dynClient, err := dynamic.NewForConfig(mc.config)
				if err != nil {
					logger.Error(err, "Failed to create dynamic client")
					return
				}

				clientSet, err := kubernetes.NewForConfig(mc.config)
				if err != nil {
					logger.Error(err, "Failed to create clientset")
					return
				}

				memory := memory.NewMemCacheClient(clientSet.Discovery())

				mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory)

				sync := sync.NewSync(dynClient, mapper)

				watcher, err := watcher.NewWatcherImpl(mc.config, mc.scheme, sync, mapper, clientSet.Discovery())
				if err != nil {
					logger.Error(err, "Failed to create watcher")
					return
				}
				err = watcher.AddListener(ctx, ch)
				if err != nil {
					logger.Error(err, "Failed to add listener")
					return
				}

				// Block until the context is canceled
				<-ctx.Done()
				logger.Info("SetupController received cancel signal")
				return
			}
		}
	}()

	// Block until the context is canceled
	<-ctx.Done()
	return nil
}
