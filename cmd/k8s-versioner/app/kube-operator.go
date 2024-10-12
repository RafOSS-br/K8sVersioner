package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"k8s.io/klog/v2"

	"github.com/RafOSS-br/K8sVersioner/config"
	"github.com/RafOSS-br/K8sVersioner/controller"
	"github.com/RafOSS-br/K8sVersioner/kubernetes"
)

var kubeOperatorSubCmd = &cobra.Command{
	Use:   "kube-operator",
	Short: "KubeOperator is a command to manage K8sVersioner using CRDs",
	Run: func(_ *cobra.Command, _ []string) {
		run(
			&config.EnvironmentConfig{
				OneShot:       oneShot,
				ExecutionMode: "kube-controller",
			},
			kubeOperator,
		)
	},
}

func kubeOperator(envConf *config.EnvironmentConfig) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	factory, err := kubernetes.NewFactory()
	if err != nil {
		klog.ErrorS(err, "Failed to create Kubernetes factory")
		return
	}

	cfg, err := config.LoadConfigStore(factory.GetDynamicClient())
	if err != nil {
		if config.HandleValidationErrors(ctx, err) {
			klog.ErrorS(err, "Validation errors in configuration")
			return
		}
		klog.ErrorS(err, "Error loading configuration")
		return
	}

	go func() {
		if err := controller.StartController(ctx, controller.ControllerArgs{
			CfgManager:        config.NewConfigManager(cfg),
			Factory:           factory,
			EnvironmentConfig: envConf,
		}); err != nil {
			klog.ErrorS(err, "Error starting controller")
			// Do not terminate the system here, just log and let the signal channel handle it
			cancel()
		}
	}()

	// Watching for configuration changes
	config.WatchConfig(ctx, config.NewConfigManager(cfg), factory)

	select {
	case <-ctx.Done():
		klog.Info("Context cancelled, shutting down application")
	case sig := <-sigs:
		klog.Infof("Received signal: %s, shutting down application", sig.String())
	}
}
