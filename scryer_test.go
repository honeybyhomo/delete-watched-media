package main

import (
	"encoding/json"
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
