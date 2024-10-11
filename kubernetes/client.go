package kubernetes

import (
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Factory struct {
	clientset     *kubernetes.Clientset
	dynamicClient *dynamic.DynamicClient
}

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

func (k *Factory) GetClientset() *kubernetes.Clientset {
	return k.clientset
}

func (k *Factory) GetDynamicClient() *dynamic.DynamicClient {
	return k.dynamicClient
}
