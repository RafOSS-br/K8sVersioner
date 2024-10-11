package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/RafOSS-br/K8sVersioner/config"
	"github.com/RafOSS-br/K8sVersioner/controller"
	"github.com/RafOSS-br/K8sVersioner/kubernetes"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var kubeOperatorSubCmd = &cobra.Command{
	Use:   "kube-operator",
	Short: "KubeOperator is a command to manage K8sVersioner using CRDs",
	Run: func(cmd *cobra.Command, args []string) {
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
		panic(err)
	}

	cfg, err := config.LoadConfigStore(factory.GetDynamicClient())
	if err != nil {
		if config.HandleValidationErrors(ctx, err) {
			os.Exit(1)
		}
		log.Fatal().Err(err).Msg("Error loading configuration")
		os.Exit(1)
	}

	go func() {
		if err := controller.StartController(ctx, controller.ControllerArgs{
			CfgManager:        config.NewConfigManager(cfg),
			Factory:           factory,
			EnvironmentConfig: envConf,
		}); err != nil {
			log.Error().Err(err).Msg("Error starting controller")
			os.Exit(1)
		}
	}()

	// Watching for configuration changes
	config.WatchConfig(ctx, config.NewConfigManager(cfg), factory)

	// Waiting for signal to terminate
	<-sigs
	log.Info().Msg("Shutting down application")
}
