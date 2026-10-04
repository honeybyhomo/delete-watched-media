package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Regression: req() with a nil body must not panic. Previously it passed a
// typed-nil *bytes.Reader to http.NewRequest, whose internal *bytes.Reader
// type-switch then dereferenced the nil pointer (crashed on the first live
// DELETE, which the dry-run path never exercised).
func TestReqNilBodyNoPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	for _, c := range []struct {
		name string
		fn   func() error
	}{
		{"sonarr delete nil body", func() error {
			return NewSonarrClient(srv.URL, "k").req(http.MethodDelete, "/api/v3/episodeFile/1", nil, nil)
		}},
		{"sonarr put with body", func() error {
			return NewSonarrClient(srv.URL, "k").req(http.MethodPut, "/api/v3/episode/monitor", nil,
				map[string]any{"episodeIds": []int64{1}, "monitored": false})
		}},
		{"radarr delete nil body", func() error {
			return NewRadarrClient(srv.URL, "k").req(http.MethodDelete, "/api/v3/movie/1",
				nil, nil)
		}},
		{"radarr put with body", func() error {
			return NewRadarrClient(srv.URL, "k").req(http.MethodPut, "/api/v3/movie/editor", nil,
				map[string]any{"movieIds": []int64{1}, "monitored": false})
		}},
	} {
		if err := c.fn(); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
	}
}
