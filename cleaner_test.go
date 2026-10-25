package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// jellyfinResponder serves the three endpoints the cleaner uses.
func jellyfinResponder(t *testing.T, movies, episodes []string, series map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/Users":
			_ = json.NewEncoder(w).Encode([]map[string]string{{"Id": "u1", "Name": "admin"}})
		case r.URL.Path == "/Items" && q.Has("Ids"):
			out := []map[string]any{}
			for _, id := range strings.Split(q.Get("Ids"), ",") {
				if tvdb, ok := series[id]; ok {
					out = append(out, map[string]any{"Id": id, "ProviderIds": map[string]string{"Tvdb": tvdb}})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Items": out, "TotalRecordCount": len(out)})
		case r.URL.Path == "/Items" && strings.Contains(q.Get("IncludeItemTypes"), "Movie"):
			_ = json.NewEncoder(w).Encode(map[string]any{"Items": rawItems(t, movies), "TotalRecordCount": len(movies)})
		case r.URL.Path == "/Items" && strings.Contains(q.Get("IncludeItemTypes"), "Episode"):
			_ = json.NewEncoder(w).Encode(map[string]any{"Items": rawItems(t, episodes), "TotalRecordCount": len(episodes)})
		default:
			t.Errorf("unexpected jellyfin request: %s %s", r.Method, r.URL)
			http.Error(w, "nope", http.StatusNotFound)
		}
	}
}

func rawItems(t *testing.T, jsonItems []string) []json.RawMessage {
	out := make([]json.RawMessage, len(jsonItems))
	for i, s := range jsonItems {
		out[i] = json.RawMessage(s)
	}
	return out
}

func daysAgo(n int) string {
	return time.Now().Add(-time.Duration(n) * 24 * time.Hour).Format(time.RFC3339)
}

func testConfig(dryRun bool) *Config {
	return &Config{
		Jellyfin:             Service{URL: "http://jellyfin", Username: "admin"},
		Scryer:               Service{URL: "http://scryer"},
		MovieRetentionDays:   2,
		EpisodeRetentionDays: 1,
		UnmonitorOnWatch:     true,
		IntervalMinutes:      60,
		DryRun:               dryRun,
		LogLevel:             "info",
	}
}

// scryerSpy scaffolding removed — the per-test responder closures carry state.

// newCleanerFixture wires a Cleaner against fake jellyfin + scryer servers and
// returns the scryer request log for mutation assertions.
func newCleanerFixture(t *testing.T, cfg *Config, jellyfin http.HandlerFunc, scryerRespond func(query string, vars map[string]any) any) (*Cleaner, *[]recordedRequest) {
	t.Helper()
	jsrv := httptest.NewServer(jellyfin)
	t.Cleanup(jsrv.Close)
	var reqs []recordedRequest
	ssrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body recordedRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("bad scryer body: %v", err)
			return
		}
		reqs = append(reqs, body)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": scryerRespond(body.Query, body.Variables)})
	}))
	t.Cleanup(ssrv.Close)
	cfg.Jellyfin.URL = jsrv.URL
	cfg.Scryer.URL = ssrv.URL
	c, err := NewCleaner(cfg, "jk", "sk")
	if err != nil {
		t.Fatalf("NewCleaner: %v", err)
	}
	return c, &reqs
}

func mutationKinds(reqs []recordedRequest) []string {
	var out []string
	for _, r := range reqs {
		if !strings.Contains(r.Query, "mutation") {
			continue
		}
		switch {
		case strings.Contains(r.Query, "setEpisodeMonitored"):
			in, _ := r.Variables["input"].(map[string]any)
			out = append(out, "unmonE:"+in["episodeId"].(string))
		case strings.Contains(r.Query, "setTitleMonitored"):
			in, _ := r.Variables["input"].(map[string]any)
			out = append(out, "unmonT:"+in["titleId"].(string))
		case strings.Contains(r.Query, "deleteEpisodeFiles"):
			in, _ := r.Variables["input"].(map[string]any)
			out = append(out, "delEps:"+in["titleId"].(string)+":"+strings.Join(itoa(in["episodeIds"].([]any)), ","))
		case strings.Contains(r.Query, "deleteTitle"):
			in, _ := r.Variables["input"].(map[string]any)
			out = append(out, "delTitle:"+in["titleId"].(string))
		}
	}
	return out
}

func itoa(xs []any) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = x.(string)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func daysAgoString(n int) string {
	return time.Now().Add(-time.Duration(n) * 24 * time.Hour).Format(time.RFC3339)
}

func movieItem(name, tmdb, when string) string {
	return `{"Name":"` + name + `","Id":"jm1","Type":"Movie","ProviderIds":{"Tmdb":"` + tmdb + `"},"UserData":{"Played":true,"LastPlayedDate":"` + when + `"}}`
}

func episodeItem(name, id string, season, episode int, when string) string {
	return `{"Name":"` + name + `","Id":"` + id + `","Type":"Episode","SeriesId":"js1","SeriesName":"9-1-1","ParentIndexNumber":` +
		itoaInt(season) + `,"IndexNumber":` + itoaInt(episode) + `,"UserData":{"Played":true,"LastPlayedDate":"` + when + `"}}`
}

func itoaInt(n int) string { return string(rune('0' + n)) }

func watchedMovieFresh() string { return movieItem("Runner", "12345", daysAgoString(0)) }
func watchedMovieOld() string   { return movieItem("Old Movie", "99999", daysAgoString(3)) }
func epFresh() string           { return episodeItem("New Ep", "je1", 1, 1, daysAgoString(0)) }
func epOldWithFile() string     { return episodeItem("Old Ep", "je2", 2, 3, daysAgoString(2)) }
func epOldNoFile() string       { return episodeItem("Older Ep", "je3", 3, 5, daysAgoString(2)) }

func TestMoviesUnmonitorOnWatch(t *testing.T) {
	respond := func(q string, vars map[string]any) any {
		switch {
		case strings.Contains(q, "titlesByExternalIds"):
			return map[string]any{"titlesByExternalIds": []map[string]any{{
				"id": "T1", "name": "Runner", "facet": "MOVIE", "monitored": true,
				"mediaFiles": []map[string]any{{"id": "f1"}},
			}}}
		case strings.Contains(q, "setTitleMonitored"):
			return map[string]any{"setTitleMonitored": map[string]any{"id": "T1", "monitored": false}}
		}
		t.Errorf("unexpected query: %.80s", q)
		return nil
	}
	c, reqs := newCleanerFixture(t, testConfig(false),
		jellyfinResponder(t, []string{watchedMovieFresh()}, nil, nil), respond)
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := mutationKinds(*reqs)
	if !equalStrings(got, []string{"unmonT:T1"}) {
		t.Errorf("mutations = %v", got)
	}
}

func TestMoviesDeletePastRetention(t *testing.T) {
	respond := func(q string, vars map[string]any) any {
		switch {
		case strings.Contains(q, "titlesByExternalIds"):
			return map[string]any{"titlesByExternalIds": []map[string]any{{
				"id": "T9", "name": "Old Movie", "facet": "MOVIE", "monitored": true,
				"mediaFiles": []map[string]any{{"id": "f1"}},
			}}}
		case strings.Contains(q, "setTitleMonitored"):
			return map[string]any{"setTitleMonitored": map[string]any{"id": "T9", "monitored": false}}
		case strings.Contains(q, "deleteTitlePreview"):
			return map[string]any{"deleteTitlePreview": map[string]any{
				"fingerprint": "fp", "requiresTypedConfirmation": false, "totalFileCount": 1, "targetLabel": "1 file"}}
		case strings.Contains(q, "deleteTitle"):
			return map[string]any{"deleteTitle": map[string]any{"fingerprint": "fp"}}
		}
		t.Errorf("unexpected query: %.80s", q)
		return nil
	}
	c, reqs := newCleanerFixture(t, testConfig(false),
		jellyfinResponder(t, []string{watchedMovieOld()}, nil, nil), respond)
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := mutationKinds(*reqs)
	if !equalStrings(got, []string{"unmonT:T9", "delTitle:T9"}) {
		t.Errorf("mutations = %v", got)
	}
}

func TestMovieNotInScryerSkipped(t *testing.T) {
	respond := func(q string, vars map[string]any) any {
		if strings.Contains(q, "titlesByExternalIds") {
			return map[string]any{"titlesByExternalIds": []any{}}
		}
		t.Errorf("unexpected query: %.80s", q)
		return nil
	}
	c, reqs := newCleanerFixture(t, testConfig(false),
		jellyfinResponder(t, []string{watchedMovieFresh()}, nil, nil), respond)
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := mutationKinds(*reqs); len(got) != 0 {
		t.Errorf("mutations = %v", got)
	}
}

// Remove the placeholder: episode fixtures are built by episodeItem() at run time.

func seriesScryerRespond(t *testing.T) func(string, map[string]any) any {
	return func(q string, vars map[string]any) any {
		switch {
		case strings.Contains(q, "titlesByExternalIds"):
			return map[string]any{"titlesByExternalIds": []map[string]any{{
				"id": "S1", "name": "9-1-1", "facet": "SERIES", "monitored": true, "mediaFiles": []any{},
			}}}
		case strings.Contains(q, "collections"):
			return map[string]any{"title": map[string]any{
				"id": "S1", "name": "9-1-1", "monitored": true,
				"collections": []map[string]any{{"episodes": []map[string]any{
					{"id": "E1", "seasonNumber": "1", "episodeNumber": "1", "monitored": true,
						"mediaFiles": []map[string]any{{"id": "f1"}}},
					{"id": "E2", "seasonNumber": "2", "episodeNumber": "3", "monitored": true,
						"mediaFiles": []map[string]any{{"id": "f2"}}},
					{"id": "E3", "seasonNumber": "3", "episodeNumber": "5", "monitored": true,
						"mediaFiles": []any{}},
				}}},
			}}
		case strings.Contains(q, "setEpisodeMonitored"):
			return map[string]any{"setEpisodeMonitored": map[string]any{"id": "x", "monitored": false}}
		case strings.Contains(q, "deleteEpisodeFilesPreview"):
			return map[string]any{"deleteEpisodeFilesPreview": map[string]any{
				"preview": map[string]any{"fingerprint": "fp", "requiresTypedConfirmation": false,
					"totalFileCount": 1, "targetLabel": "1 file"},
				"fileCount": 1, "failedCount": 0}}
		case strings.Contains(q, "deleteEpisodeFiles"):
			return map[string]any{"deleteEpisodeFiles": map[string]any{"fileCount": 1, "failedCount": 0}}
		}
		t.Errorf("unexpected query: %.80s", q)
		return nil
	}
}

func TestEpisodesUnmonitorAndDelete(t *testing.T) {
	c, reqs := newCleanerFixture(t, testConfig(false),
		jellyfinResponder(t, nil, []string{epFresh(), epOldWithFile(), epOldNoFile()},
			map[string]string{"js1": "337907"}),
		seriesScryerRespond(t))
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := mutationKinds(*reqs)
	want := []string{"unmonE:E1", "unmonE:E2", "unmonE:E3", "delEps:S1:E2"}
	if !equalStrings(got, want) {
		t.Errorf("mutations = %v, want %v", got, want)
	}
}

func TestDryRunMakesNoMutations(t *testing.T) {
	c, reqs := newCleanerFixture(t, testConfig(true),
		jellyfinResponder(t, []string{watchedMovieOld()},
			[]string{epFresh(), epOldWithFile(), epOldNoFile()},
			map[string]string{"js1": "337907"}),
		seriesScryerRespond(t))
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := mutationKinds(*reqs); len(got) != 0 {
		t.Errorf("dry-run performed mutations: %v", got)
	}
	// lookups still happen so the dry-run log is informative
	lookups := 0
	for _, r := range *reqs {
		if strings.Contains(r.Query, "titlesByExternalIds") {
			lookups++
		}
	}
	if lookups != 2 {
		t.Errorf("expected 2 lookups (movie+series), got %d", lookups)
	}
}

func TestMovieFacetMismatchSkipped(t *testing.T) {
	// A watched movie's TMDB id must only ever act on a MOVIE-faceted title.
	// TMDB movie and TV number spaces are independent; if the lookup ever
	// resolves to a series, acting on it would delete a whole series' files.
	respond := func(q string, vars map[string]any) any {
		if strings.Contains(q, "titlesByExternalIds") {
			return map[string]any{"titlesByExternalIds": []map[string]any{{
				"id": "S77", "name": "Some Series", "facet": "SERIES", "monitored": true,
				"mediaFiles": []map[string]any{{"id": "f1"}},
			}}}
		}
		t.Errorf("unexpected query: %.80s", q)
		return nil
	}
	c, reqs := newCleanerFixture(t, testConfig(false),
		jellyfinResponder(t, []string{watchedMovieOld()}, nil, nil), respond)
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := mutationKinds(*reqs); len(got) != 0 {
		t.Errorf("facet mismatch produced mutations: %v", got)
	}
}

func TestSeriesFacetMismatchSkipped(t *testing.T) {
	// The series pipeline's TVDB lookup must not act on a MOVIE-faceted
	// title (collision in the other direction); the whole series is skipped
	// before any episodes are even fetched.
	respond := func(q string, vars map[string]any) any {
		if strings.Contains(q, "titlesByExternalIds") {
			return map[string]any{"titlesByExternalIds": []map[string]any{{
				"id": "M5", "name": "Actually a Movie", "facet": "MOVIE", "monitored": true,
				"mediaFiles": []map[string]any{{"id": "f1"}},
			}}}
		}
		t.Errorf("unexpected query (facet guard should have skipped): %.80s", q)
		return nil
	}
	c, reqs := newCleanerFixture(t, testConfig(false),
		jellyfinResponder(t, nil, []string{epOldWithFile()}, map[string]string{"js1": "337907"}), respond)
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := mutationKinds(*reqs); len(got) != 0 {
		t.Errorf("facet mismatch produced mutations: %v", got)
	}
}

func TestAnimeSeriesStillProcessed(t *testing.T) {
	// ANIME is a series-carrying facet in Scryer (MediaFacetValue enum:
	// MOVIE/SERIES/ANIME); the facet guard must accept it or anime would
	// silently never be cleaned.
	respond := func(q string, vars map[string]any) any {
		if strings.Contains(q, "titlesByExternalIds") {
			return map[string]any{"titlesByExternalIds": []map[string]any{{
				"id": "S1", "name": "9-1-1", "facet": "ANIME", "monitored": true, "mediaFiles": []any{},
			}}}
		}
		return seriesScryerRespond(t)(q, vars)
	}
	c, reqs := newCleanerFixture(t, testConfig(false),
		jellyfinResponder(t, nil, []string{epFresh(), epOldWithFile(), epOldNoFile()},
			map[string]string{"js1": "337907"}), respond)
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := mutationKinds(*reqs)
	want := []string{"unmonE:E1", "unmonE:E2", "unmonE:E3", "delEps:S1:E2"}
	if !equalStrings(got, want) {
		t.Errorf("mutations = %v, want %v", got, want)
	}
}

// captureLogs swaps the default slog logger for a buffer while fn runs.
func captureLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	fn()
	return buf.String()
}

func TestDryRunLogsDoNotClaimActions(t *testing.T) {
	// Dry-run logs are the go/no-go evidence for enabling real deletions;
	// they must never claim an action that was not performed.
	c, _ := newCleanerFixture(t, testConfig(true),
		jellyfinResponder(t, []string{watchedMovieOld()},
			[]string{epFresh(), epOldWithFile(), epOldNoFile()},
			map[string]string{"js1": "337907"}),
		seriesScryerRespond(t))
	logs := captureLogs(t, func() {
		if err := c.Run(); err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	for _, phrase := range []string{"unmonitored movies", "unmonitored episodes", "deleted episode files", "deleted movie"} {
		if strings.Contains(logs, phrase) {
			t.Errorf("dry-run log claims action: %q", phrase)
		}
	}
	if !strings.Contains(logs, "[dry-run]") {
		t.Errorf("dry-run log missing [dry-run] lines:\n%s", logs)
	}
}
