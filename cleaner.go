package main

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

type Cleaner struct {
	cfg      *Config
	jellyfin *JellyfinClient
	scryer   *ScryerClient
}

func NewCleaner(cfg *Config, jellyfinKey, scryerKey string) (*Cleaner, error) {
	return &Cleaner{
		cfg:      cfg,
		jellyfin: NewJellyfinClient(cfg.Jellyfin.URL, jellyfinKey),
		scryer:   NewScryerClient(cfg.Scryer.URL, scryerKey),
	}, nil
}

// Run performs one full pass. A failure to resolve the Jellyfin user aborts the
// run (returned error → sooner retry); per-phase failures are logged and do not
// abort the other phase.
func (c *Cleaner) Run() error {
	uid, err := c.jellyfin.UserID(c.cfg.Jellyfin.Username)
	if err != nil {
		return fmt.Errorf("resolve jellyfin user: %w", err)
	}
	if err := c.processMovies(uid); err != nil {
		slog.Error("movie processing failed", "error", err)
	}
	if err := c.processEpisodes(uid); err != nil {
		slog.Error("episode processing failed", "error", err)
	}
	return nil
}

func (c *Cleaner) processMovies(uid string) error {
	items, err := c.jellyfin.WatchedItems(uid, []string{"Movie"})
	if err != nil {
		return fmt.Errorf("jellyfin watched movies: %w", err)
	}
	slog.Info("watched movies", "count", len(items))
	if len(items) == 0 {
		return nil
	}
	cutoff := time.Now().Add(-time.Duration(c.cfg.MovieRetentionDays) * 24 * time.Hour)

	type pendingMovie struct {
		id    string
		title string
	}
	var unmonitor []pendingMovie
	var deletes []pendingMovie

	for _, it := range items {
		tmdb := providerID(it.ProviderIDs, "Tmdb")
		if tmdb == "" {
			slog.Debug("movie has no Tmdb id, skipping", "name", it.Name)
			continue
		}
		m, err := c.scryer.TitleByExternalID("TMDB", tmdb)
		if err != nil {
			slog.Error("scryer lookup failed", "name", it.Name, "error", err)
			continue
		}
		if m == nil {
			slog.Debug("movie not in Scryer, skipping", "name", it.Name)
			continue
		}
		if c.cfg.UnmonitorOnWatch && m.Monitored {
			unmonitor = append(unmonitor, pendingMovie{id: m.ID, title: it.Name})
		}
		if pastRetention(it.UserData, cutoff) && m.FileCount > 0 {
			deletes = append(deletes, pendingMovie{id: m.ID, title: it.Name})
		}
	}

	for _, m := range unmonitor {
		if c.cfg.DryRun {
			slog.Info("[dry-run] would unmonitor movie", "title", m.title)
			continue
		}
		if err := c.scryer.SetTitleMonitored(m.id, false); err != nil {
			slog.Error("unmonitor movie failed", "title", m.title, "error", err)
		}
	}
	if len(unmonitor) > 0 {
		slog.Info("unmonitored movies", "count", len(unmonitor))
	}

	for _, d := range deletes {
		if c.cfg.DryRun {
			slog.Info("[dry-run] would delete movie + files and remove from Scryer", "title", d.title)
			continue
		}
		n, err := c.scryer.DeleteTitle(d.id)
		if err != nil {
			slog.Error("delete movie failed", "title", d.title, "error", err)
		} else {
			slog.Info("deleted movie (files + Scryer entry)", "title", d.title, "files", n)
		}
	}
	return nil
}

func (c *Cleaner) processEpisodes(uid string) error {
	items, err := c.jellyfin.WatchedItems(uid, []string{"Episode"})
	if err != nil {
		return fmt.Errorf("jellyfin watched episodes: %w", err)
	}
	slog.Info("watched episodes", "count", len(items))
	if len(items) == 0 {
		return nil
	}
	cutoff := time.Now().Add(-time.Duration(c.cfg.EpisodeRetentionDays) * 24 * time.Hour)

	bySeries := map[string][]jfItem{}
	for _, it := range items {
		if it.SeriesID == "" {
			slog.Debug("episode has no SeriesId, skipping", "name", it.Name)
			continue
		}
		bySeries[it.SeriesID] = append(bySeries[it.SeriesID], it)
	}

	seriesIDs := make([]string, 0, len(bySeries))
	for id := range bySeries {
		seriesIDs = append(seriesIDs, id)
	}
	tvdbMap, err := c.jellyfin.SeriesTVDBIDs(seriesIDs)
	if err != nil {
		return fmt.Errorf("jellyfin series tvdb: %w", err)
	}

	for seriesID, watchedEps := range bySeries {
		tvdb := tvdbMap[seriesID]
		if tvdb == "" {
			slog.Warn("series has no Tvdb id, skipping", "series_id", seriesID)
			continue
		}
		ss, err := c.scryer.TitleByExternalID("TVDB", tvdb)
		if err != nil {
			slog.Error("scryer series lookup failed", "tvdb", tvdb, "error", err)
			continue
		}
		if ss == nil {
			slog.Debug("series not in Scryer, skipping", "tvdb", tvdb)
			continue
		}
		detail, err := c.scryer.SeriesEpisodes(ss.ID)
		if err != nil {
			slog.Error("scryer episodes fetch failed", "series", ss.Name, "error", err)
			continue
		}
		idx := make(map[string]scEpisode, len(detail.Episodes))
		for _, se := range detail.Episodes {
			season, errS := strconv.Atoi(strings.TrimSpace(se.SeasonNumber))
			episode, errE := strconv.Atoi(strings.TrimSpace(se.EpisodeNumber))
			if errS != nil || errE != nil {
				slog.Debug("scryer episode has unparseable numbers, skipping",
					"series", ss.Name, "season", se.SeasonNumber, "episode", se.EpisodeNumber)
				continue
			}
			idx[epKey(season, episode)] = se
		}

		type pendingEpisode struct {
			id    string
			label string
		}
		var unmonitor []pendingEpisode
		var deletes []pendingEpisode

		for _, je := range watchedEps {
			if je.ParentIndexNumber == nil || je.IndexNumber == nil {
				continue
			}
			se, ok := idx[epKey(*je.ParentIndexNumber, *je.IndexNumber)]
			if !ok {
				continue
			}
			label := fmt.Sprintf("%s S%02dE%02d", ss.Name, *je.ParentIndexNumber, *je.IndexNumber)
			if c.cfg.UnmonitorOnWatch && se.Monitored {
				unmonitor = append(unmonitor, pendingEpisode{id: se.ID, label: label})
			}
			if pastRetention(je.UserData, cutoff) && se.HasFile {
				deletes = append(deletes, pendingEpisode{id: se.ID, label: label})
			}
		}

		for _, e := range unmonitor {
			if c.cfg.DryRun {
				slog.Info("[dry-run] would unmonitor episode", "episode", e.label)
				continue
			}
			if err := c.scryer.SetEpisodeMonitored(e.id, false); err != nil {
				slog.Error("unmonitor episode failed", "episode", e.label, "error", err)
			}
		}
		if len(unmonitor) > 0 {
			slog.Info("unmonitored episodes", "series", ss.Name, "count", len(unmonitor))
		}

		if len(deletes) == 0 {
			continue
		}
		labels := make([]string, len(deletes))
		ids := make([]string, len(deletes))
		for i, d := range deletes {
			labels[i] = d.label
			ids[i] = d.id
		}
		if c.cfg.DryRun {
			for _, l := range labels {
				slog.Info("[dry-run] would delete episode file", "episode", l)
			}
			continue
		}
		n, err := c.scryer.DeleteEpisodeFiles(ss.ID, ids)
		if err != nil {
			slog.Error("delete episode files failed", "series", ss.Name, "episodes", labels, "error", err)
		} else {
			slog.Info("deleted episode files", "series", ss.Name, "episodes", labels, "files", n)
		}
	}
	return nil
}

func providerID(ids map[string]string, key string) string {
	if ids == nil {
		return ""
	}
	// ProviderIds keys vary in capitalisation across Jellyfin versions
	// ("Tmdb" vs "TMDB"); match case-insensitively.
	for k, v := range ids {
		if strings.EqualFold(k, key) && v != "" {
			return v
		}
	}
	return ""
}

func epKey(season, episode int) string {
	return fmt.Sprintf("%d-%d", season, episode)
}

// pastRetention reports whether the item was last played before the cutoff.
// Items with no watch date are treated as not eligible (conservative: we won't
// delete something we can't age).
func pastRetention(ud *jfUserData, cutoff time.Time) bool {
	if ud == nil || ud.LastPlayedDate == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, ud.LastPlayedDate)
	if err != nil {
		return false
	}
	return t.Before(cutoff)
}
