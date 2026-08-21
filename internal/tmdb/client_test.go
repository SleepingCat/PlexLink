package tmdb

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			http.Error(w, "auth", 401)
			return
		}
		switch r.URL.Path {
		case "/configuration":
			fmt.Fprint(w, `{"images":{"base_url":"https://image.tmdb.org/t/p/"}}`)
		case "/search/tv":
			fmt.Fprint(w, `{"results":[{"id":7,"name":"Show","original_name":"Show","first_air_date":"2020-01-01"}]}`)
		case "/tv/7/season/1":
			fmt.Fprint(w, `{"id":8,"season_number":1,"episodes":[{"id":9,"episode_number":1}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c := New(s.URL, "token", "en-US", s.Client())
	rows, err := c.SearchTV(context.Background(), "Show")
	if err != nil || len(rows) != 1 || rows[0].ID != 7 {
		t.Fatal(rows, err)
	}
	season, err := c.GetSeason(context.Background(), 7, 1)
	if err != nil || len(season.Episodes) != 1 {
		t.Fatal(season, err)
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}
