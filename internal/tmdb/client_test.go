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
		case "/tv/7/external_ids":
			fmt.Fprint(w, `{"imdb_id":"tt123"}`)
		case "/movie/752/release_dates":
			fmt.Fprint(w, `{"results":[{"iso_3166_1":"US","release_dates":[{"type":1,"release_date":"2005-12-11T00:00:00.000Z"},{"type":3,"release_date":"2006-03-17T00:00:00.000Z"}]}]}`)
		case "/find/tt0119167":
			if r.URL.Query().Get("external_source") != "imdb_id" {
				t.Errorf("query=%s", r.URL.RawQuery)
			}
			fmt.Fprint(w, `{"movie_results":[{"id":10,"title":"Funny Games","release_date":"1997-01-01"}],"tv_results":[]}`)
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
	external, err := c.GetTVExternalIDs(context.Background(), 7)
	if err != nil || external.IMDbID != "tt123" {
		t.Fatalf("external=%+v err=%v", external, err)
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	dates, err := c.GetMovieReleaseDates(context.Background(), 752)
	if err != nil || len(dates.Results) != 1 || dates.Results[0].ReleaseDates[0].Type != 1 {
		t.Fatalf("release dates %+v %v", dates, err)
	}
	found, err := c.FindByIMDb(context.Background(), "tt0119167")
	if err != nil || len(found.MovieResults) != 1 || found.MovieResults[0].ID != 10 {
		t.Fatalf("find=%+v err=%v", found, err)
	}
}
