package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"server/src/core"

	"github.com/joho/godotenv"
)

func loadEnvVariable(key string, required bool) (value string) {
	value = os.Getenv(key)
	if value == "" && required {
		panic(fmt.Sprintf("Error: required env variable %s not set", key))
	}
	return
}

func parseLogLevel(level string) (slog.Level, error) {
	switch level {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("invalid log level: %s", level)
	}
}

func main() {
	port := flag.Uint("port", 8080, "port to listen on")
	profile := flag.Bool("profile", false, "enable profiling")
	lobbies := flag.Int("lobbies", 50, "number of lobbies to run")
	logfileLocation := flag.String("logfile", "", "log file to write to")
	logLevelArg := flag.String("log-level", "info", "log level")
	flag.Parse()

	if *profile {
		go func() {
			fmt.Println("Profiling enabled, listening at http://localhost:6060")

			if err := http.ListenAndServe("localhost:6060", nil); err != nil {
				panic(fmt.Sprintf("Pprof server stopped error: %s", err))
			}
		}()
	}

	var logWriter io.Writer
	if *logfileLocation != "" {
		f, err := os.OpenFile(*logfileLocation, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			panic(fmt.Sprintf("Error: unable to open log file %s: %s", *logfileLocation, err))
		}
		logWriter = f
	} else {
		logWriter = os.Stdout
	}

	logLevel, err := parseLogLevel(*logLevelArg)
	if err != nil {
		panic(fmt.Sprintf("Error: unable to parse log level %s: %s", *logLevelArg, err))
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{
		Level:     logLevel,
		AddSource: true,
	})))

	if err := godotenv.Load(); err != nil {
		slog.Warn("unable to load .env", "error", err)
	}
	slog.Info("loaded .env")

	dbUrl := fmt.Sprintf(
		"postgresql://%s:%s@%s:%s/%s",
		loadEnvVariable("DB_USER", true),
		loadEnvVariable("DB_PASSWORD", true),
		loadEnvVariable("DB_HOST", true),
		loadEnvVariable("DB_PORT", true),
		loadEnvVariable("DB", true),
	)

	config := core.Config{
		Port:               uint16(*port),
		LobbiesCount:       *lobbies,
		DbUrl:              dbUrl,
		ClientRedirectUrl:  loadEnvVariable("CLIENT_REDIRECT_URL", true),
		Domain:             loadEnvVariable("DOMAIN", true),
		Prod:               loadEnvVariable("ENV", false) == "prod",
		GoogleClientId:     loadEnvVariable("GOOGLE_CLIENT_ID", true),
		GoogleClientSecret: loadEnvVariable("GOOGLE_CLIENT_SECRET", true),
	}
	if err := core.Run(&config); err != nil {
		slog.Error("unable to run server", "error", err)
		os.Exit(1)
	}
}
