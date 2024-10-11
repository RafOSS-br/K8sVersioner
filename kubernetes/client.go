/*
Package kubernetes provides a factory for creating kubernetes clients.
*/
package kubernetes

import (
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Factory is a struct that contains the kubernetes clientset and dynamic client
type Factory struct {
	clientset     *kubernetes.Clientset
	dynamicClient *dynamic.DynamicClient
}

// NewFactory creates a new factory for kubernetes clients
func NewFactory() (*Factory, error) {
	var config *rest.Config
	var err error

	config, err = rest.InClusterConfig()
	if err != nil {
		config, err = clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
		if err != nil {
			return nil, err
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &Factory{
		clientset:     clientset,
		dynamicClient: dynamicClient,
	}, nil
}

// GetClientset returns the kubernetes clientset
func (k *Factory) GetClientset() *kubernetes.Clientset {
	return k.clientset
}

// GetDynamicClient returns the dynamic client
func (k *Factory) GetDynamicClient() *dynamic.DynamicClient {
	return k.dynamicClient
}
