package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/RafOSS-br/K8sVersioner/config"
	"github.com/RafOSS-br/K8sVersioner/controllers"
	"github.com/RafOSS-br/K8sVersioner/kubernetes"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var kubeControllerSubCmd = &cobra.Command{
	Use:   "kube-controller",
	Short: "KubeController is a tool to manage Kubernetes resources versions",
	Run: func(cmd *cobra.Command, args []string) {
		run(
			&config.EnvironmentConfig{
				OneShot:       oneShot,
				ExecutionMode: "kube-controller",
			},
			kubeController,
		)
	},
}

func kubeController(envConf *config.EnvironmentConfig) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	k8sClient, err := kubernetes.GetKubernetesConfig()
	if err != nil {
		panic(err)
	}

	cfg, err := config.LoadConfigStore(k8sClient.GetDynamicClient())
	if err != nil {
		if config.HandleValidationErrors(ctx, err) {
			os.Exit(1)
		}
		log.Fatal().Err(err).Msg("Error loading configuration")
		os.Exit(1)
	}

	go func() {
		if err := controllers.StartController(ctx, controllers.ControllerArgs{
			CfgManager:        config.NewConfigManager(cfg),
			K8sClient:         k8sClient,
			EnvironmentConfig: envConf,
		}); err != nil {
			log.Error().Err(err).Msg("Error starting controller")
			os.Exit(1)
		}
		os.Exit(0)
	}()

	// go func() {
	// 	if err := config.WatchConfig(ctx, cfgManager, "config.json"); err != nil {
	// 		log.Error().Err(err).Msg("Error monitoring configuration")
	// 	}
	// }()

	// Waiting for signal to terminate
	<-sigs
	log.Info().Msg("Shutting down application")
}
