package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"k8s-git-operator/config"
	"k8s-git-operator/controllers"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	cfg, err := config.LoadConfig("config.json")
	if err != nil {
		log.Fatal().Err(err).Msg("Error loading configuration")
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
