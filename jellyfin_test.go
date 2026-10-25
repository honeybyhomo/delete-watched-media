package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSeriesTVDBIDsCaseInsensitive(t *testing.T) {
	// Jellyfin's ProviderIds key capitalisation drifts across versions
	// ("Tvdb" vs "TVDB"); series resolution must match case-insensitively,
	// like the movie path's Tmdb lookup.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Items" || !r.URL.Query().Has("Ids") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "nope", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Items": []map[string]any{
			{"Id": "js1", "ProviderIds": map[string]string{"TVDB": "111"}},
			{"Id": "js2", "ProviderIds": map[string]string{"Tvdb": "222"}},
			{"Id": "js3", "ProviderIds": map[string]string{"Imdb": "tt0000001"}},
		}, "TotalRecordCount": 3})
	}))
	defer srv.Close()
	j := NewJellyfinClient(srv.URL, "k")
	got, err := j.SeriesTVDBIDs([]string{"js1", "js2", "js3"})
	if err != nil {
		t.Fatalf("SeriesTVDBIDs: %v", err)
	}
	if got["js1"] != "111" {
		t.Errorf("TVDB (upper-case) key not matched: %v", got)
	}
	if got["js2"] != "222" {
		t.Errorf("Tvdb (mixed-case) key not matched: %v", got)
	}
	if _, ok := got["js3"]; ok {
		t.Errorf("series without a tvdb id unexpectedly present: %v", got)
	}
}
