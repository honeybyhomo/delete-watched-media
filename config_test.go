package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const fullConfig = `jellyfin:
  url: http://jellyfin:8096
  username: admin
scryer:
  url: http://scryer:8080
movie_retention_days: 2
episode_retention_days: 1
unmonitor_on_watch: true
interval_minutes: 30
log_level: debug
`

func TestLoadConfigFullParse(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, fullConfig))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.Jellyfin.URL != "http://jellyfin:8096" || c.Jellyfin.Username != "admin" {
		t.Errorf("jellyfin = %+v", c.Jellyfin)
	}
	if c.Scryer.URL != "http://scryer:8080" {
		t.Errorf("scryer = %+v", c.Scryer)
	}
	if c.MovieRetentionDays != 2 || c.EpisodeRetentionDays != 1 || !c.UnmonitorOnWatch ||
		c.IntervalMinutes != 30 || c.LogLevel != "debug" {
		t.Errorf("config = %+v", c)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, `jellyfin:
  url: http://jellyfin:8096
  username: admin
scryer:
  url: http://scryer:8080
`))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.IntervalMinutes != 60 {
		t.Errorf("interval default = %d", c.IntervalMinutes)
	}
	if c.LogLevel != "info" {
		t.Errorf("log level default = %q", c.LogLevel)
	}
}

func TestLoadConfigMissingScryerURL(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, `jellyfin:
  url: http://jellyfin:8096
  username: admin
`))
	if err == nil {
		t.Fatal("expected error for missing scryer.url")
	}
}

func TestLoadConfigMissingJellyfinUsername(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, `jellyfin:
  url: http://jellyfin:8096
scryer:
  url: http://scryer:8080
`))
	if err == nil {
		t.Fatal("expected error for missing jellyfin.username")
	}
}
