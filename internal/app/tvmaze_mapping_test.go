package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SleepingCat/PlexLink/internal/model"
	"github.com/SleepingCat/PlexLink/internal/resolvers/tvmaze"
)

type externalIDsStub struct{}

func (externalIDsStub) GetTVExternalIDs(context.Context, int) (model.TVExternalIDs, error) {
	return model.TVExternalIDs{IMDbID: "tt3398228"}, nil
}

func TestTVMazeEpisodeCatalogMapsTitledSpecial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/lookup/shows":
			_, _ = w.Write([]byte(`{"id":1,"name":"BoJack Horseman","externals":{"imdb":"tt3398228"}}`))
		case "/shows/1/episodes":
			_, _ = w.Write([]byte(`[{"id":2,"name":"Sabrina's Christmas Wish","season":0,"number":1}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	catalog := NewTVMazeEpisodeCatalog(externalIDsStub{}, tvmaze.New(server.URL, server.Client()))
	season, episode, evidence, ok, err := catalog.MapEpisode(context.Background(), 61222, model.MediaFile{Name: "BoJack.S01E13.mkv", EpisodeTitle: "Sabrinas Christmas Wish"})
	if err != nil || !ok || season != 0 || episode != 1 || evidence != "tvmaze_episode_title_exact" {
		t.Fatalf("season=%d episode=%d evidence=%q ok=%v err=%v", season, episode, evidence, ok, err)
	}
}
