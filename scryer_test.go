package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeGraphQL returns a server that records each request body (decoded) and
// replies with canned responses chosen by inspecting the query string.
type recordedRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
	Auth      string
}

func fakeGraphQL(t *testing.T, respond func(query string) any) (*httptest.Server, *[]recordedRequest) {
	t.Helper()
	var reqs []recordedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body recordedRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("bad request body: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body.Auth = r.Header.Get("Authorization")
		reqs = append(reqs, body)
		_ = json.NewEncoder(w).Encode(respond(body.Query))
	}))
	t.Cleanup(srv.Close)
	return srv, &reqs
}

func TestGqlSurfacesGraphQLErrors(t *testing.T) {
	srv, _ := fakeGraphQL(t, func(string) any {
		return map[string]any{"errors": []map[string]string{{"message": "boom"}}}
	})
	c := NewScryerClient(srv.URL, "k")
	err := c.gql("query { x }", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected error containing 'boom', got %v", err)
	}
}

func TestGqlSurfacesHTTPStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewScryerClient(srv.URL, "k")
	if err := c.gql("query { x }", nil, nil); err == nil {
		t.Fatal("expected error on HTTP 401")
	}
}

func TestGqlSendsBearerAndBody(t *testing.T) {
	srv, reqs := fakeGraphQL(t, func(string) any {
		return map[string]any{"data": map[string]any{}}
	})
	c := NewScryerClient(srv.URL, "sekret")
	if err := c.gql("query Q", map[string]any{"a": "b"}, nil); err != nil {
		t.Fatalf("gql: %v", err)
	}
	if len(*reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*reqs))
	}
	got := (*reqs)[0]
	if got.Auth != "Bearer sekret" {
		t.Errorf("auth header = %q", got.Auth)
	}
	if got.Query != "query Q" {
		t.Errorf("query = %q", got.Query)
	}
	if got.Variables["a"] != "b" {
		t.Errorf("variables = %v", got.Variables)
	}
}

func TestGqlAppendsGraphqlPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()
	for _, base := range []string{srv.URL, srv.URL + "/"} {
		c := NewScryerClient(base, "k")
		if err := c.gql("query { x }", nil, nil); err != nil {
			t.Fatalf("gql(%s): %v", base, err)
		}
		if gotPath != "/graphql" {
			t.Errorf("base %s → path %s, want /graphql", base, gotPath)
		}
	}
	// A caller who already passes the full endpoint is not double-suffixed.
	full := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer full.Close()
	c := NewScryerClient(full.URL+"/graphql", "k")
	if err := c.gql("query { x }", nil, nil); err != nil {
		t.Fatalf("gql(full endpoint): %v", err)
	}
	if gotPath != "/graphql" {
		t.Errorf("full endpoint → path %s, want /graphql", gotPath)
	}
}

func TestTitleByExternalIDFound(t *testing.T) {
	srv, reqs := fakeGraphQL(t, func(q string) any {
		if strings.Contains(q, "titlesByExternalIds") {
			return map[string]any{"data": map[string]any{"titlesByExternalIds": []map[string]any{{
				"id": "T1", "name": "Nine One One", "facet": "SERIES", "monitored": true,
				"mediaFiles": []map[string]any{{"id": "f1"}, {"id": "f2"}},
			}}}}
		}
		t.Errorf("unexpected query: %s", q)
		return nil
	})
	c := NewScryerClient(srv.URL, "k")
	title, err := c.TitleByExternalID("TVDB", "337907")
	if err != nil {
		t.Fatalf("TitleByExternalID: %v", err)
	}
	if title == nil {
		t.Fatal("expected title, got nil")
	}
	if title.ID != "T1" || title.Name != "Nine One One" || title.Facet != "SERIES" || !title.Monitored || title.FileCount != 2 {
		t.Errorf("title = %+v", title)
	}
	vars := (*reqs)[0].Variables
	if vars["src"] != "TVDB" || vars["val"] != "337907" {
		t.Errorf("lookup vars = %v", vars)
	}
}

func TestTitleByExternalIDNotFound(t *testing.T) {
	srv, _ := fakeGraphQL(t, func(string) any {
		return map[string]any{"data": map[string]any{"titlesByExternalIds": []any{}}}
	})
	c := NewScryerClient(srv.URL, "k")
	title, err := c.TitleByExternalID("TMDB", "999")
	if err != nil {
		t.Fatalf("TitleByExternalID: %v", err)
	}
	if title != nil {
		t.Errorf("expected nil title, got %+v", title)
	}
}

func TestSetEpisodeMonitoredSendsInput(t *testing.T) {
	srv, reqs := fakeGraphQL(t, func(q string) any {
		if strings.Contains(q, "setEpisodeMonitored") {
			return map[string]any{"data": map[string]any{"setEpisodeMonitored": map[string]any{"id": "E1", "monitored": false}}}
		}
		t.Errorf("unexpected query: %s", q)
		return nil
	})
	c := NewScryerClient(srv.URL, "k")
	if err := c.SetEpisodeMonitored("E1", false); err != nil {
		t.Fatalf("SetEpisodeMonitored: %v", err)
	}
	vars := (*reqs)[0].Variables
	input, _ := vars["input"].(map[string]any)
	if input["episodeId"] != "E1" || input["monitored"] != false {
		t.Errorf("input = %v", input)
	}
}

func TestSetTitleMonitoredSendsInput(t *testing.T) {
	srv, reqs := fakeGraphQL(t, func(q string) any {
		if strings.Contains(q, "setTitleMonitored") {
			return map[string]any{"data": map[string]any{"setTitleMonitored": map[string]any{"id": "T1", "monitored": false}}}
		}
		t.Errorf("unexpected query: %s", q)
		return nil
	})
	c := NewScryerClient(srv.URL, "k")
	if err := c.SetTitleMonitored("T1", false); err != nil {
		t.Fatalf("SetTitleMonitored: %v", err)
	}
	input, _ := (*reqs)[0].Variables["input"].(map[string]any)
	if input["titleId"] != "T1" || input["monitored"] != false {
		t.Errorf("input = %v", input)
	}
}

// fakeFileIDs returns n synthetic media-file ids ("f1".."fn").
func fakeFileIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("f%d", i+1)
	}
	return ids
}

// deleteRecorder answers previews and mutations, tracking them separately.
// Mutation shapes match Scryer's live schema (introspected 2026-10-28):
// deleteEpisodeFiles returns acceptedFileIds (async job); deleteTitle
// returns recycle-bin counts.
type deleteRecorder struct {
	mutations []map[string]any
}

func (d *deleteRecorder) handler(t *testing.T, previewFingerprint string, requiresTyped bool, fileCount int) func(string) any {
	return d.handlerPartial(t, previewFingerprint, requiresTyped, fileCount, fileCount, 0, 0)
}

// handlerPartial lets tests vary the episode-accept count and the title
// recycle counts independently of the preview.
func (d *deleteRecorder) handlerPartial(t *testing.T, previewFingerprint string, requiresTyped bool, previewFileCount, acceptedCount, recycleBusy, recycleFailed int) func(string) any {
	return func(q string) any {
		switch {
		case strings.Contains(q, "deleteEpisodeFilesPreview"):
			return map[string]any{"data": map[string]any{"deleteEpisodeFilesPreview": map[string]any{
				"preview": map[string]any{
					"fingerprint": previewFingerprint, "requiresTypedConfirmation": requiresTyped,
					"totalFileCount": previewFileCount, "targetLabel": "2 selected episode files",
				},
				"fileCount": previewFileCount, "failedCount": 0,
			}}}
		case strings.Contains(q, "deleteEpisodeFiles("):
			return map[string]any{"data": map[string]any{"deleteEpisodeFiles": map[string]any{
				"acceptedFileIds": fakeFileIDs(acceptedCount),
			}}}
		case strings.Contains(q, "deleteTitlePreview"):
			return map[string]any{"data": map[string]any{"deleteTitlePreview": map[string]any{
				"fingerprint": previewFingerprint, "requiresTypedConfirmation": requiresTyped,
				"totalFileCount": previewFileCount, "targetLabel": "1 file",
			}}}
		case strings.Contains(q, "deleteTitle("):
			return map[string]any{"data": map[string]any{"deleteTitle": map[string]any{
				"id": "T1",
				// recycle: previewFileCount purged, nothing preserved,
				// plus whatever busy/failed the test asked for.
				"recyclePurged":    previewFileCount,
				"recyclePreserved": 0,
				"recycleBusy":      recycleBusy,
				"recycleFailed":    recycleFailed,
			}}}
		}
		t.Errorf("unexpected query: %s", q)
		return nil
	}
}

func TestDeleteEpisodeFilesHandshake(t *testing.T) {
	rec := &deleteRecorder{}
	srv, reqs := fakeGraphQL(t, func(q string) any {
		if strings.Contains(q, "deleteEpisodeFiles") && strings.Contains(q, "mutation") {
			rec.mutations = append(rec.mutations, map[string]any{"q": q})
		}
		return rec.handler(t, "fp-123", false, 3)(q)
	})
	c := NewScryerClient(srv.URL, "k")
	n, err := c.DeleteEpisodeFiles("T1", []string{"E1", "E2"})
	if err != nil {
		t.Fatalf("DeleteEpisodeFiles: %v", err)
	}
	if n != 3 {
		t.Errorf("accepted file count = %d, want 3", n)
	}
	if len(*reqs) != 2 {
		t.Fatalf("expected preview+mutation, got %d requests", len(*reqs))
	}
	preview, mut := (*reqs)[0], (*reqs)[1]
	if !strings.Contains(preview.Query, "deleteEpisodeFilesPreview") {
		t.Errorf("first call not preview: %s", preview.Query)
	}
	pin, _ := preview.Variables["input"].(map[string]any)
	if pin["titleId"] != "T1" {
		t.Errorf("preview input = %v", pin)
	}
	if ids, ok := pin["episodeIds"].([]any); !ok || len(ids) != 2 || ids[0] != "E1" {
		t.Errorf("preview episodeIds = %v", pin["episodeIds"])
	}
	// The mutation must select the live schema's fields — the async job
	// payload answers with acceptedFileIds, not sync counts (the v0.2.0
	// selection of fileCount/failedCount failed validation on the real
	// server and every delete was rejected).
	if !strings.Contains(mut.Query, "acceptedFileIds") || strings.Contains(mut.Query, "fileCount") {
		t.Errorf("mutation selects wrong fields: %s", mut.Query)
	}
	min, _ := mut.Variables["input"].(map[string]any)
	if min["titleId"] != "T1" || min["previewFingerprint"] != "fp-123" || min["deleteFromDisk"] != true {
		t.Errorf("mutation input = %v", min)
	}
	if ids, ok := min["episodeIds"].([]any); !ok || len(ids) != 2 {
		t.Errorf("mutation episodeIds = %v", min["episodeIds"])
	}
}

func TestDeleteRefusesTypedConfirmation(t *testing.T) {
	rec := &deleteRecorder{}
	srv, reqs := fakeGraphQL(t, func(q string) any {
		if strings.Contains(q, "mutation") {
			rec.mutations = append(rec.mutations, map[string]any{"q": q})
		}
		return rec.handler(t, "fp-x", true, 5)(q)
	})
	c := NewScryerClient(srv.URL, "k")
	if _, err := c.DeleteEpisodeFiles("T1", []string{"E1"}); err == nil {
		t.Fatal("expected typed-confirmation refusal error")
	} else if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("error = %v", err)
	}
	if _, err := c.DeleteTitle("T1"); err == nil {
		t.Fatal("expected typed-confirmation refusal error for title")
	}
	for _, r := range *reqs {
		if strings.Contains(r.Query, "mutation") {
			t.Errorf("mutation reached server despite typed confirmation: %s", r.Query)
		}
	}
}

func TestDeleteTitleHandshake(t *testing.T) {
	rec := &deleteRecorder{}
	srv, reqs := fakeGraphQL(t, func(q string) any {
		if strings.Contains(q, "mutation") {
			rec.mutations = append(rec.mutations, map[string]any{"q": q})
		}
		return rec.handler(t, "fp-t", false, 4)(q)
	})
	c := NewScryerClient(srv.URL, "k")
	n, err := c.DeleteTitle("T1")
	if err != nil {
		t.Fatalf("DeleteTitle: %v", err)
	}
	if n != 4 {
		t.Errorf("removed files = %d, want 4", n)
	}
	if len(*reqs) != 2 {
		t.Fatalf("expected preview+mutation, got %d", len(*reqs))
	}
	if !strings.Contains((*reqs)[0].Query, "deleteTitlePreview") {
		t.Errorf("first call not preview: %s", (*reqs)[0].Query)
	}
	// The mutation must select the live schema's recycle-bin fields (the
	// v0.2.0 selection of `fingerprint` failed validation on the real
	// server and the delete was rejected).
	mutQ := (*reqs)[1].Query
	if !strings.Contains(mutQ, "recyclePurged") || strings.Contains(mutQ, "fingerprint") {
		t.Errorf("mutation selects wrong fields: %s", mutQ)
	}
	min, _ := (*reqs)[1].Variables["input"].(map[string]any)
	if min["titleId"] != "T1" || min["previewFingerprint"] != "fp-t" || min["deleteFilesOnDisk"] != true {
		t.Errorf("mutation input = %v", min)
	}
}

func TestDeleteTitleReportsRecycleProblems(t *testing.T) {
	// Files that end up busy or failed in the recycle pipeline were not
	// cleanly removed — surface as an error (files remain and are retried
	// next cycle).
	rec := &deleteRecorder{}
	srv, _ := fakeGraphQL(t, func(q string) any {
		return rec.handlerPartial(t, "fp-r", false, 2, 2, 1, 2)(q)
	})
	c := NewScryerClient(srv.URL, "k")
	n, err := c.DeleteTitle("T1")
	if err == nil || !strings.Contains(err.Error(), "busy=1") || !strings.Contains(err.Error(), "failed=2") {
		t.Fatalf("expected recycle error naming busy=1 failed=2, got n=%d err=%v", n, err)
	}
}

func TestDeleteEpisodeFilesEmptyInput(t *testing.T) {
	srv, reqs := fakeGraphQL(t, func(q string) any {
		t.Errorf("no requests expected, got: %s", q)
		return nil
	})
	c := NewScryerClient(srv.URL, "k")
	if _, err := c.DeleteEpisodeFiles("T1", nil); err != nil {
		t.Fatalf("empty input: %v", err)
	}
	if len(*reqs) != 0 {
		t.Errorf("expected 0 requests, got %d", len(*reqs))
	}
}

func TestDeleteEpisodeFilesReportsPartialAcceptance(t *testing.T) {
	// The async job accepts file ids; if fewer files are accepted than the
	// preview projected, some deletes did not happen — surface it (the
	// affected files keep existing and are retried next cycle).
	rec := &deleteRecorder{}
	srv, reqs := fakeGraphQL(t, func(q string) any {
		if strings.Contains(q, "mutation") {
			rec.mutations = append(rec.mutations, map[string]any{"q": q})
		}
		return rec.handlerPartial(t, "fp-p", false, 3, 1, 0, 0)(q)
	})
	c := NewScryerClient(srv.URL, "k")
	n, err := c.DeleteEpisodeFiles("T1", []string{"E1", "E2", "E3"})
	if err == nil || !strings.Contains(err.Error(), "1 of 3") {
		t.Fatalf("expected partial-acceptance error naming '1 of 3', got n=%d err=%v", n, err)
	}
	if len(*reqs) != 2 {
		t.Fatalf("expected preview+mutation, got %d requests", len(*reqs))
	}
}

func TestDeleteEpisodeFilesReturnsAcceptedCount(t *testing.T) {
	// The success count is the mutation's acceptedFileIds length — what the
	// server actually took, not the preview's projection.
	rec := &deleteRecorder{}
	srv, _ := fakeGraphQL(t, func(q string) any {
		return rec.handlerPartial(t, "fp-m", false, 2, 5, 0, 0)(q)
	})
	c := NewScryerClient(srv.URL, "k")
	n, err := c.DeleteEpisodeFiles("T1", []string{"E1"})
	if err != nil {
		t.Fatalf("DeleteEpisodeFiles: %v", err)
	}
	if n != 5 {
		t.Errorf("accepted = %d, want 5", n)
	}
}

func TestSeriesEpisodesFlattensCollections(t *testing.T) {
	srv, reqs := fakeGraphQL(t, func(q string) any {
		if strings.Contains(q, "collections") {
			return map[string]any{"data": map[string]any{"title": map[string]any{
				"id": "T1", "name": "Nine One One", "monitored": true,
				"collections": []map[string]any{
					{"episodes": []any{}}, // specials, empty
					{"episodes": []map[string]any{
						{"id": "E1", "seasonNumber": "1", "episodeNumber": "1", "monitored": true,
							"mediaFiles": []map[string]any{{"id": "f1"}}},
						{"id": "E2", "seasonNumber": "2", "episodeNumber": "3", "monitored": false,
							"mediaFiles": []any{}},
					}},
				},
			}}}
		}
		t.Errorf("unexpected query: %s", q)
		return nil
	})
	c := NewScryerClient(srv.URL, "k")
	s, err := c.SeriesEpisodes("T1")
	if err != nil {
		t.Fatalf("SeriesEpisodes: %v", err)
	}
	if s.ID != "T1" || s.Name != "Nine One One" || !s.Monitored {
		t.Errorf("series = %+v", s)
	}
	if len(s.Episodes) != 2 {
		t.Fatalf("expected 2 episodes, got %d", len(s.Episodes))
	}
	e1, e2 := s.Episodes[0], s.Episodes[1]
	if e1.ID != "E1" || e1.SeasonNumber != "1" || e1.EpisodeNumber != "1" || !e1.Monitored || !e1.HasFile {
		t.Errorf("e1 = %+v", e1)
	}
	if e2.ID != "E2" || e2.SeasonNumber != "2" || e2.EpisodeNumber != "3" || e2.Monitored || e2.HasFile {
		t.Errorf("e2 = %+v", e2)
	}
	if vars := (*reqs)[0].Variables; vars["id"] != "T1" {
		t.Errorf("vars = %v", vars)
	}
}
