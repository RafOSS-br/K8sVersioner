package kubernetes

import (
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type K8sClient struct {
	clientset     *kubernetes.Clientset
	dynamicClient *dynamic.DynamicClient
}

func GetKubernetesConfig() (*K8sClient, error) {
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

	return &K8sClient{
		clientset:     clientset,
		dynamicClient: dynamicClient,
	}, nil
}

func (k *K8sClient) GetClientset() *kubernetes.Clientset {
	return k.clientset
}

func (k *K8sClient) GetDynamicClient() *dynamic.DynamicClient {
	return k.dynamicClient
}
func GetClientConfig(cfg *rest.Config) (*kubernetes.Clientset, error) {
	return kubernetes.NewForConfig(cfg)
}
