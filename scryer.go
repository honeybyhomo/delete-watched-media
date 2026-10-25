package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ScryerClient talks to Scryer's GraphQL API (Bearer API-key auth).
type ScryerClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewScryerClient(baseURL, apiKey string) *ScryerClient {
	return &ScryerClient{baseURL: baseURL, apiKey: apiKey, http: &http.Client{Timeout: 30 * time.Second}}
}

// gql runs one GraphQL operation. Scryer returns HTTP 200 with an "errors"
// array on failure, so both the status code and the errors field are checked.
func (s *ScryerClient) gql(query string, variables map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, s.baseURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("scryer graphql: %s", resp.Status)
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("scryer graphql decode: %w", err)
	}
	if len(envelope.Errors) > 0 {
		msgs := make([]string, len(envelope.Errors))
		for i, e := range envelope.Errors {
			msgs[i] = e.Message
		}
		return fmt.Errorf("scryer graphql errors: %s", strings.Join(msgs, "; "))
	}
	if out != nil && len(envelope.Data) > 0 {
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			return fmt.Errorf("scryer graphql unmarshal: %w", err)
		}
	}
	return nil
}

// scTitle is a Scryer title (movie or series) as seen from the catalog lookup.
type scTitle struct {
	ID        string
	Name      string
	Facet     string
	Monitored bool
	FileCount int // len(mediaFiles)
}

// TitleByExternalID resolves a TMDB/TVDB id to a Scryer title, or nil when the
// id is not tracked. Source is "TMDB" (movies) or "TVDB" (series); the API
// matches source names case-insensitively.
func (s *ScryerClient) TitleByExternalID(source, externalID string) (*scTitle, error) {
	const q = `query($src: String!, $val: String!) {
		titlesByExternalIds(source: $src, values: [$val]) {
			id name facet monitored mediaFiles { id }
		}
	}`
	var resp struct {
		TitlesByExternalIds []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			Facet      string `json:"facet"`
			Monitored  bool   `json:"monitored"`
			MediaFiles []struct {
				ID string `json:"id"`
			} `json:"mediaFiles"`
		} `json:"titlesByExternalIds"`
	}
	if err := s.gql(q, map[string]any{"src": source, "val": externalID}, &resp); err != nil {
		return nil, fmt.Errorf("lookup %s %s: %w", source, externalID, err)
	}
	if len(resp.TitlesByExternalIds) == 0 {
		return nil, nil
	}
	t := resp.TitlesByExternalIds[0]
	return &scTitle{
		ID:        t.ID,
		Name:      t.Name,
		Facet:     t.Facet,
		Monitored: t.Monitored,
		FileCount: len(t.MediaFiles),
	}, nil
}

// scEpisode is one episode of a series. Scryer's GraphQL returns season and
// episode numbers as STRINGS — keep them raw; callers coerce.
type scEpisode struct {
	ID            string
	SeasonNumber  string
	EpisodeNumber string
	Monitored     bool
	HasFile       bool
}

type scSeries struct {
	ID        string
	Name      string
	Monitored bool
	Episodes  []scEpisode
}

// SeriesEpisodes returns all episodes of a series title, flattened across
// collections (seasons, specials).
func (s *ScryerClient) SeriesEpisodes(titleID string) (*scSeries, error) {
	const q = `query($id: ID!) {
		title(id: $id) {
			id name monitored
			collections { episodes { id seasonNumber episodeNumber monitored mediaFiles { id } } }
		}
	}`
	var resp struct {
		Title struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Monitored   bool   `json:"monitored"`
			Collections []struct {
				Episodes []struct {
					ID            string `json:"id"`
					SeasonNumber  string `json:"seasonNumber"`
					EpisodeNumber string `json:"episodeNumber"`
					Monitored     bool   `json:"monitored"`
					MediaFiles    []struct {
						ID string `json:"id"`
					} `json:"mediaFiles"`
				} `json:"episodes"`
			} `json:"collections"`
		} `json:"title"`
	}
	if err := s.gql(q, map[string]any{"id": titleID}, &resp); err != nil {
		return nil, fmt.Errorf("series %s: %w", titleID, err)
	}
	if resp.Title.ID == "" {
		return nil, fmt.Errorf("series %s: not found", titleID)
	}
	out := &scSeries{ID: resp.Title.ID, Name: resp.Title.Name, Monitored: resp.Title.Monitored}
	for _, col := range resp.Title.Collections {
		for _, e := range col.Episodes {
			if e.ID == "" {
				continue
			}
			out.Episodes = append(out.Episodes, scEpisode{
				ID:            e.ID,
				SeasonNumber:  e.SeasonNumber,
				EpisodeNumber: e.EpisodeNumber,
				Monitored:     e.Monitored,
				HasFile:       len(e.MediaFiles) > 0,
			})
		}
	}
	return out, nil
}
