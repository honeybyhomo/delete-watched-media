package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type JellyfinClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewJellyfinClient(baseURL, apiKey string) *JellyfinClient {
	return &JellyfinClient{baseURL: baseURL, apiKey: apiKey, http: &http.Client{Timeout: 30 * time.Second}}
}

func (j *JellyfinClient) get(path string, q url.Values, out any) error {
	u, err := url.Parse(j.baseURL)
	if err != nil {
		return err
	}
	u.Path = path
	if q != nil {
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "MediaBrowser Token="+j.apiKey)
	resp, err := j.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("jellyfin GET %s: %s", u.String(), resp.Status)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

type jfUser struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
}

// UserID resolves a username to a Jellyfin user id.
func (j *JellyfinClient) UserID(username string) (string, error) {
	var users []jfUser
	if err := j.get("/Users", nil, &users); err != nil {
		return "", err
	}
	for _, u := range users {
		if u.Name == username {
			return u.ID, nil
		}
	}
	return "", fmt.Errorf("jellyfin user %q not found", username)
}

type jfUserData struct {
	Played         bool   `json:"Played"`
	LastPlayedDate string `json:"LastPlayedDate"`
}

type jfItem struct {
	Name              string            `json:"Name"`
	ID                string            `json:"Id"`
	Type              string            `json:"Type"`
	SeriesID          string            `json:"SeriesId"`
	SeriesName        string            `json:"SeriesName"`
	IndexNumber       *int              `json:"IndexNumber"`       // episode number
	ParentIndexNumber *int              `json:"ParentIndexNumber"` // season number
	ProviderIDs       map[string]string `json:"ProviderIds"`
	UserData          *jfUserData       `json:"UserData"`
}

type jfItemsResp struct {
	Items            []jfItem `json:"Items"`
	TotalRecordCount int      `json:"TotalRecordCount"`
}

// WatchedItems returns watched items of the given types (e.g. "Movie", "Episode")
// for the user, paginating as needed.
func (j *JellyfinClient) WatchedItems(userID string, includeTypes []string) ([]jfItem, error) {
	var all []jfItem
	const limit = 200
	start := 0
	for {
		q := url.Values{}
		q.Set("UserId", userID)
		q.Set("IsPlayed", "true")
		q.Set("Recursive", "true")
		q.Set("Fields", "ProviderIds,UserData")
		q.Set("IncludeItemTypes", strings.Join(includeTypes, ","))
		q.Set("StartIndex", strconv.Itoa(start))
		q.Set("Limit", strconv.Itoa(limit))
		var resp jfItemsResp
		if err := j.get("/Items", q, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Items...)
		if len(resp.Items) < limit || len(all) >= resp.TotalRecordCount {
			break
		}
		start += len(resp.Items)
	}
	return all, nil
}

// SeriesTVDBIDs maps each Jellyfin series id to its TVDB id (needed to look the
// series up in Scryer).
func (j *JellyfinClient) SeriesTVDBIDs(seriesIDs []string) (map[string]string, error) {
	out := map[string]string{}
	const batch = 50
	for i := 0; i < len(seriesIDs); i += batch {
		end := i + batch
		if end > len(seriesIDs) {
			end = len(seriesIDs)
		}
		q := url.Values{}
		q.Set("Ids", strings.Join(seriesIDs[i:end], ","))
		q.Set("Fields", "ProviderIds")
		var resp jfItemsResp
		if err := j.get("/Items", q, &resp); err != nil {
			return nil, err
		}
		for _, s := range resp.Items {
			// ProviderIds key capitalisation drifts across Jellyfin
			// versions; match case-insensitively like providerID does.
			if tvdb := providerID(s.ProviderIDs, "Tvdb"); tvdb != "" {
				out[s.ID] = tvdb
			}
		}
	}
	return out, nil
}
