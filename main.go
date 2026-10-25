package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	logger := setupLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	jellyfinKey := os.Getenv("JELLYFIN_API_KEY")
	scryerKey := os.Getenv("SCRYER_API_KEY")
	if jellyfinKey == "" || scryerKey == "" {
		slog.Error("missing required environment variable(s): JELLYFIN_API_KEY, SCRYER_API_KEY must both be set")
		os.Exit(1)
	}

	cleaner, err := NewCleaner(cfg, jellyfinKey, scryerKey)
	if err != nil {
		slog.Error("failed to initialize", "error", err)
		os.Exit(1)
	}

	interval := time.Duration(cfg.IntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = time.Hour
	}
	retryInterval := 5 * time.Minute
	if retryInterval > interval {
		retryInterval = interval
	}

	slog.Info("delete-watched-media starting",
		"movie_retention_days", cfg.MovieRetentionDays,
		"episode_retention_days", cfg.EpisodeRetentionDays,
		"interval", interval,
		"dry_run", cfg.DryRun,
		"unmonitor_on_watch", cfg.UnmonitorOnWatch,
		"jellyfin_user", cfg.Jellyfin.Username,
	)

	for {
		start := time.Now()
		err := cleaner.Run()
		elapsed := time.Since(start)
		if err != nil {
			// Transient errors (e.g. DNS not ready right after start, a service
			// restarting mid-poll) shouldn't block the whole interval.
			slog.Error("run failed", "error", err, "elapsed", elapsed)
			slog.Info("retrying", "in", retryInterval)
			time.Sleep(retryInterval)
			continue
		}
		slog.Info("run complete", "elapsed", elapsed, "next_run_in", interval)
		time.Sleep(interval)
	}
}

func setupLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
