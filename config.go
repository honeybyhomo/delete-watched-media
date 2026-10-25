package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds all non-secret settings. API keys are read from the environment
// (JELLYFIN_API_KEY, SCRYER_API_KEY) so this file can be safely committed to
// git and shared.
type Config struct {
	Jellyfin             Service `yaml:"jellyfin"`
	Scryer               Service `yaml:"scryer"`
	MovieRetentionDays   int     `yaml:"movie_retention_days"`
	EpisodeRetentionDays int     `yaml:"episode_retention_days"`
	UnmonitorOnWatch     bool    `yaml:"unmonitor_on_watch"`
	IntervalMinutes      int     `yaml:"interval_minutes"`
	DryRun               bool    `yaml:"dry_run"`
	LogLevel             string  `yaml:"log_level"`
}

// Service is a single *arr / media-server endpoint. Username is Jellyfin-only.
type Service struct {
	URL      string `yaml:"url"`
	Username string `yaml:"username"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if c.IntervalMinutes <= 0 {
		c.IntervalMinutes = 60
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	// Retention of 0 is valid and means "delete immediately on the next poll".
	// Guard only against nonsensical negatives.
	if c.MovieRetentionDays < 0 {
		c.MovieRetentionDays = 0
	}
	if c.EpisodeRetentionDays < 0 {
		c.EpisodeRetentionDays = 0
	}
	if c.Jellyfin.URL == "" || c.Scryer.URL == "" {
		return nil, fmt.Errorf("jellyfin/scryer urls must both be set")
	}
	if c.Jellyfin.Username == "" {
		return nil, fmt.Errorf("jellyfin.username must be set")
	}
	return &c, nil
}
