package cmd

import (
	"github.com/spf13/cobra"
	"k8s.io/klog/v2"

	"github.com/RafOSS-br/K8sVersioner/config"
	"github.com/RafOSS-br/K8sVersioner/controller"
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
	var (
		ctx    = envConf.Context
		cancel = envConf.Cancel
	)
	cfg, err := config.LoadConfigStore(envConf.GetDynamicClient())
	if err != nil {
		if config.HandleValidationErrors(ctx, err) {
			klog.ErrorS(err, "Validation errors in configuration")
			return
		}
		klog.ErrorS(err, "Error loading configuration")
		return
	}

	go func() {
		if err := controller.StartController(controller.ControllerArgs{
			CfgManager:        config.NewConfigManager(cfg),
			EnvironmentConfig: envConf,
		}); err != nil {
			klog.ErrorS(err, "Error starting controller")
			// Do not terminate the system here, just log and let the signal channel handle it
			cancel()
		}
	}()

	// Watching for configuration changes
	config.WatchConfig(ctx, config.NewConfigManager(cfg), envConf.KubeClientFactory)
}
