package main

import (
	"os"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/rs/zerolog"

	"github.com/RafOSS-br/K8sVersioner/cmd"
)

func main() {
	zerolog.TimeFieldFormat = time.RFC3339
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	cmd.Execute()
}
