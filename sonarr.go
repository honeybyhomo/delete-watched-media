package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type SonarrClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewSonarrClient(baseURL, apiKey string) *SonarrClient {
	return &SonarrClient{baseURL: baseURL, apiKey: apiKey, http: &http.Client{Timeout: 30 * time.Second}}
}

func (s *SonarrClient) req(method, path string, q url.Values, body any) error {
	u, err := url.Parse(s.baseURL)
	if err != nil {
		return err
	}
	u.Path = path
	if q != nil {
		u.RawQuery = q.Encode()
	}
	// Untyped nil io.Reader when there's no body — a typed-nil (*bytes.Reader)(nil)
	// is a non-nil interface and makes http.NewRequest's internal *bytes.Reader
	// type-switch dereference a nil pointer.
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, u.String(), bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", s.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("sonarr %s %s: %s", method, u.String(), resp.Status)
	}
	return nil
}

type sonarrSeries struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// SeriesByTVDB returns the Sonarr series for a TVDB id, or nil if not tracked.
func (s *SonarrClient) SeriesByTVDB(tvdbID string) (*sonarrSeries, error) {
	u, _ := url.Parse(s.baseURL)
	u.Path = "/api/v3/series"
	u.RawQuery = url.Values{"tvdbId": {tvdbID}}.Encode()
	req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
	req.Header.Set("X-Api-Key", s.apiKey)
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("sonarr GET series: %s", resp.Status)
	}
	var series []sonarrSeries
	if err := json.NewDecoder(resp.Body).Decode(&series); err != nil {
		return nil, err
	}
	if len(series) == 0 {
		return nil, nil
	}
	return &series[0], nil
}

type sonarrEpisode struct {
	ID            int64  `json:"id"`
	SeasonNumber  int    `json:"seasonNumber"`
	EpisodeNumber int    `json:"episodeNumber"`
	EpisodeFileID int64  `json:"episodeFileId"` // 0 when no file on disk
	Monitored     bool   `json:"monitored"`
	HasFile       bool   `json:"hasFile"`
	Title         string `json:"title"`
}

// EpisodesBySeries returns all episodes Sonarr knows for a series.
func (s *SonarrClient) EpisodesBySeries(seriesID int64) ([]sonarrEpisode, error) {
	u, _ := url.Parse(s.baseURL)
	u.Path = "/api/v3/episode"
	u.RawQuery = url.Values{"seriesId": {fmt.Sprintf("%d", seriesID)}}.Encode()
	req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
	req.Header.Set("X-Api-Key", s.apiKey)
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("sonarr GET episode: %s", resp.Status)
	}
	var eps []sonarrEpisode
	if err := json.NewDecoder(resp.Body).Decode(&eps); err != nil {
		return nil, err
	}
	return eps, nil
}

// UnmonitorEpisodes sets monitored=false for the given episode ids (idempotent).
func (s *SonarrClient) UnmonitorEpisodes(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	return s.req(http.MethodPut, "/api/v3/episode/monitor", nil,
		map[string]any{"episodeIds": ids, "monitored": false})
}

// DeleteEpisodeFile deletes one episode file from disk (keeps the episode record).
func (s *SonarrClient) DeleteEpisodeFile(id int64) error {
	return s.req(http.MethodDelete, fmt.Sprintf("/api/v3/episodeFile/%d", id), nil, nil)
}
