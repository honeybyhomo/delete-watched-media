package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
	return &ScryerClient{baseURL: graphqlEndpoint(baseURL), apiKey: apiKey, http: &http.Client{Timeout: 30 * time.Second}}
}

// graphqlEndpoint accepts either a base URL ("http://scryer:8080") or the full
// GraphQL endpoint ("http://scryer:8080/graphql") and returns the latter.
func graphqlEndpoint(baseURL string) string {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || u.Path == "" {
		return strings.TrimRight(baseURL, "/") + "/graphql"
	}
	return strings.TrimRight(baseURL, "/")
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

// SetEpisodeMonitored flips an episode's monitored flag (idempotent).
func (s *ScryerClient) SetEpisodeMonitored(episodeID string, monitored bool) error {
	const q = `mutation($input: SetEpisodeMonitoredInput!) {
		setEpisodeMonitored(input: $input) { id monitored }
	}`
	var resp struct {
		SetEpisodeMonitored struct {
			ID        string `json:"id"`
			Monitored bool   `json:"monitored"`
		} `json:"setEpisodeMonitored"`
	}
	if err := s.gql(q, map[string]any{"input": map[string]any{
		"episodeId": episodeID, "monitored": monitored,
	}}, &resp); err != nil {
		return fmt.Errorf("set episode monitored %s=%v: %w", episodeID, monitored, err)
	}
	return nil
}

// SetTitleMonitored flips a title's (movie's) monitored flag (idempotent).
func (s *ScryerClient) SetTitleMonitored(titleID string, monitored bool) error {
	const q = `mutation($input: SetTitleMonitoredInput!) {
		setTitleMonitored(input: $input) { id monitored }
	}`
	var resp struct {
		SetTitleMonitored struct {
			ID        string `json:"id"`
			Monitored bool   `json:"monitored"`
		} `json:"setTitleMonitored"`
	}
	if err := s.gql(q, map[string]any{"input": map[string]any{
		"titleId": titleID, "monitored": monitored,
	}}, &resp); err != nil {
		return fmt.Errorf("set title monitored %s=%v: %w", titleID, monitored, err)
	}
	return nil
}

// scDeletePreview is the shared preview-fingerprint envelope.
type scDeletePreview struct {
	Fingerprint          string `json:"fingerprint"`
	RequiresTypedConfirm bool   `json:"requiresTypedConfirmation"`
	TotalFileCount       int    `json:"totalFileCount"`
	TargetLabel          string `json:"targetLabel"`
}

// refuseIfTypedConfirmation guards destructive calls: if Scryer demands a typed
// confirmation for this delete, refuse it rather than bypass the guard.
func refuseIfTypedConfirmation(p scDeletePreview) error {
	if p.RequiresTypedConfirm {
		return fmt.Errorf("scryer requires typed confirmation for %q — refusing to delete automatically", p.TargetLabel)
	}
	return nil
}

// DeleteEpisodeFiles deletes the given episodes' files from disk (one
// preview+fingerprinted mutation). Returns the previewed file count.
func (s *ScryerClient) DeleteEpisodeFiles(titleID string, episodeIDs []string) (int, error) {
	if len(episodeIDs) == 0 {
		return 0, nil
	}
	const previewQ = `query($input: DeleteEpisodeFilesPreviewInput!) {
		deleteEpisodeFilesPreview(input: $input) {
			preview { fingerprint requiresTypedConfirmation totalFileCount targetLabel }
			fileCount failedCount
		}
	}`
	var preview struct {
		DeleteEpisodeFilesPreview struct {
			Preview scDeletePreview `json:"preview"`
		} `json:"deleteEpisodeFilesPreview"`
	}
	if err := s.gql(previewQ, map[string]any{"input": map[string]any{
		"titleId": titleID, "episodeIds": episodeIDs,
	}}, &preview); err != nil {
		return 0, fmt.Errorf("preview episode files %s: %w", titleID, err)
	}
	p := preview.DeleteEpisodeFilesPreview.Preview
	if err := refuseIfTypedConfirmation(p); err != nil {
		return 0, err
	}
	const mutQ = `mutation($input: DeleteEpisodeFilesInput!) {
		deleteEpisodeFiles(input: $input) { acceptedFileIds }
	}`
	// Scryer's delete is an async job: the mutation answers with the file
	// ids it accepted (introspected live 2026-10-28 — there are no sync
	// fileCount/failedCount fields on this payload type).
	var out struct {
		DeleteEpisodeFiles struct {
			AcceptedFileIds []string `json:"acceptedFileIds"`
		} `json:"deleteEpisodeFiles"`
	}
	if err := s.gql(mutQ, map[string]any{"input": map[string]any{
		"titleId": titleID, "episodeIds": episodeIDs,
		"deleteFromDisk": true, "previewFingerprint": p.Fingerprint,
	}}, &out); err != nil {
		return 0, fmt.Errorf("delete episode files %s: %w", titleID, err)
	}
	n := len(out.DeleteEpisodeFiles.AcceptedFileIds)
	if n < p.TotalFileCount {
		// Fewer files accepted than the preview projected: some deletes
		// did not happen. The affected files still exist, so the next
		// cycle retries them.
		return n, fmt.Errorf("delete episode files %s: only %d of %d previewed file(s) accepted — will retry next cycle",
			titleID, n, p.TotalFileCount)
	}
	return n, nil
}

// DeleteTitle removes the title and its files from disk (preview+fingerprinted).
// Returns the previewed file count.
func (s *ScryerClient) DeleteTitle(titleID string) (int, error) {
	const previewQ = `query($id: ID!) {
		deleteTitlePreview(titleId: $id) {
			fingerprint requiresTypedConfirmation totalFileCount targetLabel
		}
	}`
	var preview struct {
		DeleteTitlePreview scDeletePreview `json:"deleteTitlePreview"`
	}
	if err := s.gql(previewQ, map[string]any{"id": titleID}, &preview); err != nil {
		return 0, fmt.Errorf("preview title %s: %w", titleID, err)
	}
	p := preview.DeleteTitlePreview
	if err := refuseIfTypedConfirmation(p); err != nil {
		return 0, err
	}
	const mutQ = `mutation($input: DeleteTitleInput!) {
		deleteTitle(input: $input) { id recyclePurged recyclePreserved recycleBusy recycleFailed }
	}`
	// Scryer's title delete runs files through a recycle pipeline
	// (introspected live 2026-10-28): purged = permanently removed,
	// preserved = kept in the recycle bin, busy/failed = not cleanly
	// handled. Busy or failed files remain and are retried next cycle.
	var out struct {
		DeleteTitle struct {
			ID               string `json:"id"`
			RecyclePurged    int    `json:"recyclePurged"`
			RecyclePreserved int    `json:"recyclePreserved"`
			RecycleBusy      int    `json:"recycleBusy"`
			RecycleFailed    int    `json:"recycleFailed"`
		} `json:"deleteTitle"`
	}
	if err := s.gql(mutQ, map[string]any{"input": map[string]any{
		"titleId": titleID, "deleteFilesOnDisk": true, "previewFingerprint": p.Fingerprint,
	}}, &out); err != nil {
		return 0, fmt.Errorf("delete title %s: %w", titleID, err)
	}
	r := out.DeleteTitle
	if r.RecycleBusy > 0 || r.RecycleFailed > 0 {
		return r.RecyclePurged + r.RecyclePreserved, fmt.Errorf("delete title %s: recycle busy=%d failed=%d — will retry next cycle",
			titleID, r.RecycleBusy, r.RecycleFailed)
	}
	return r.RecyclePurged + r.RecyclePreserved, nil
}
