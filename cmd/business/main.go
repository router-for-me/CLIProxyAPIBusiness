package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	_ "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator/builtin"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/app"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/config"

	log "github.com/sirupsen/logrus"
)

// main runs the CLI entrypoint and exits on unrecoverable command errors.
func main() {
	if errRun := run(context.Background(), os.Args[1:]); errRun != nil {
		log.WithError(errRun).Error("command failed")
		os.Exit(1)
	}
}

// run parses flags, loads config, and starts the init or main server.
func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "config file path (or env CONFIG_PATH)")
	port := fs.Int("port", 8318, "server port (used for init server and initial config)")
	if errParse := fs.Parse(args); errParse != nil {
		return errParse
	}

	if errValidate := validatePort(*port); errValidate != nil {
		return errValidate
	}

	appCfg, err := config.LoadFromEnv()
	if err != nil {
		return err
	}
	if strings.TrimSpace(*cfgPath) != "" {
		appCfg.ConfigPath = config.ResolveConfigPath(*cfgPath)
	}

	configPath := config.ResolveConfigPath(appCfg.ConfigPath)
	if !app.ConfigExists(configPath) && strings.TrimSpace(os.Getenv(config.EnvDBConnection)) == "" {
		log.Info("config.yaml not found, starting init server...")
		errInit := app.RunInitServer(ctx, appCfg, *port)
		if errors.Is(errInit, app.ErrInitCompleted) {
			log.Info("initialization completed, starting main server...")
			return app.RunServer(ctx, appCfg, *port)
		}
		return errInit
	}

	return app.RunServer(ctx, appCfg, *port)
}

func validatePort(port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("invalid port: %d", port)
	}
	return nil
}
