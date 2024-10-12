/*
Package cmd implements the root command of the application.
*/
package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/component-base/cli"
	"k8s.io/klog/v2"

	"github.com/RafOSS-br/K8sVersioner/config"
	"github.com/RafOSS-br/K8sVersioner/kubernetes"
)

var (
	oneShot bool
)

var rootCmd = &cobra.Command{
	Use:   "K8sVersioner",
	Short: "K8sVersioner is a tool to manage Kubernetes resources versions",
	Long:  `K8sVersioner is a tool to manage Kubernetes resources versions`,
}

// Execute runs the root command
func Execute() {
	exitCode := cli.Run(rootCmd)
	os.Exit(exitCode)
}

func init() {
	// Add subcommands to the root command
	rootCmd.PersistentFlags().BoolVarP(&oneShot, "one-shot", "o", false, "Run the command only once")
	rootCmd.AddCommand(kubeOperatorSubCmd)
}

func run(envConf *config.EnvironmentConfig, f func(*config.EnvironmentConfig)) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle termination signals
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	if envConf == nil {
		klog.Fatal("Invalid environment configuration")
	}
	if err := envConf.Validate(); err != nil {
		klog.Fatal("Invalid environment configuration")
	}

	kubeClientFactory, err := kubernetes.NewKubernetesClientFactory(time.Minute * 5)
	if err != nil {
		klog.ErrorS(err, "Failed to create Kubernetes client factory")
		return
	}
	envConf.KubeClientFactory = kubeClientFactory
	envConf.Context = ctx
	envConf.Cancel = cancel
	f(envConf)

	select {
	case <-ctx.Done():
		klog.Info("Context cancelled, shutting down application")
	case sig := <-sigs:
		klog.Infof("Received signal: %s, shutting down application", sig.String())
	}
}
