package cmd

import (
	"github.com/RafOSS-br/K8sVersioner/config"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var (
	oneShot bool
)

var rootCmd = &cobra.Command{
	Use:   "K8sVersioner",
	Short: "K8sVersioner is a tool to manage Kubernetes resources versions",
	Long:  `K8sVersioner is a tool to manage Kubernetes resources versions`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatal().Err(err).Msg("Error executing the command")
	}
}

func init() {
	// Add subcommands to the root command
	rootCmd.PersistentFlags().BoolVarP(&oneShot, "one-shot", "o", false, "Run the command only once")
	rootCmd.AddCommand(kubeControllerSubCmd)
}

func run(envConf *config.EnvironmentConfig, f func(*config.EnvironmentConfig)) {
	if envConf == nil {
		log.Fatal().Msg("Invalid environment configuration")
	}
	if err := envConf.Validate(); err != nil {
		log.Fatal().Err(err).Msg("Invalid environment configuration")
	}

	f(envConf)
}
