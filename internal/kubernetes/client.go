/*
Package kubernetes provides a factory for creating kubernetes clients.
*/
package kubernetes

import (
	"fmt"
	"sync"
	"time"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// KubernetesFactory is a factory for creating kubernetes clients
type KubeClientFactory struct {
	configFlags *genericclioptions.ConfigFlags
	clientset   *kubernetes.Clientset
	dynClient   *dynamic.DynamicClient
	mu          sync.RWMutex
}

// NewFactory creates a new factory for kubernetes clients
func NewKubernetesClientFactory(refreshTick time.Duration) (*KubeClientFactory, error) {
	configFlags := genericclioptions.NewConfigFlags(true)
	factory := &KubeClientFactory{
		configFlags: configFlags,
	}
	err := factory.Refresh()
	if err != nil {
		return nil, err
	}
	go func() {
		for range time.Tick(refreshTick) {
			err := factory.Refresh()
			if err != nil {
				klog.ErrorS(err, "Failed to refresh kubernetes clientset")
			}
		}
	}()
	return factory, nil
}

// GetClientset returns the kubernetes clientset
func (k *KubeClientFactory) GetClientset() *kubernetes.Clientset {
	return k.clientset
}

// GetDynamicClient returns the dynamic client
func (k *KubeClientFactory) GetDynamicClient() *dynamic.DynamicClient {
	return k.dynClient
}

// Refresh refreshes the kubernetes clientset and dynamic client
func (k *KubeClientFactory) Refresh() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.configFlags == nil {
		return fmt.Errorf("config flags are nil")
	}
	config, err := k.configFlags.ToRESTConfig()
	if err != nil {
		return err
	}
	k.clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	k.dynClient, err = dynamic.NewForConfig(config)
	if err != nil {
		return err
	}
	return nil
}
