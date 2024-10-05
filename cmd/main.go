package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/RafOSS-br/K8sVersioner/config"
	"github.com/RafOSS-br/K8sVersioner/controllers"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	cfg, err := config.LoadConfig("config.json")
	if err != nil {
		if config.HandleValidationErrors(ctx, err) {
			os.Exit(1)
		}
		log.Fatal().Err(err).Msg("Error loading configuration")
		os.Exit(1)
	}

	cfgManager := config.NewConfigManager(cfg)

	go func() {
		if err := controllers.StartController(ctx, cfgManager); err != nil {
			log.Fatal().Err(err).Msg("Error starting controller")
		}
	}()

	go func() {
		if err := config.WatchConfig(ctx, cfgManager, "config.json"); err != nil {
			log.Error().Err(err).Msg("Error monitoring configuration")
		}
	}()

	// Waiting for signal to terminate
	<-sigs
	log.Info().Msg("Shutting down application")
}
