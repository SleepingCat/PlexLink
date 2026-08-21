package config

import "testing"

func TestLiteralAndEnvironmentSecretsAreMutuallyExclusive(t *testing.T) {
	c := Config{QBittorrent: QBittorrent{URL: "http://qbt", Username: "user", Password: "secret"}, TMDB: TMDB{Token: "token"}, Paths: Paths{TVSource: "tv", MovieSource: "movies", AnimeSource: "anime", TVTarget: "ptv", MovieTarget: "pmovies", AnimeTarget: "panime"}, Matching: Matching{MinScore: 80, MinMargin: 15}, State: State{Directory: "state"}}
	if err := c.Validate(); err != nil {
		t.Fatalf("literal secrets rejected: %v", err)
	}
	c.TMDB.TokenEnv = "PLEXLINK_TMDB_TOKEN"
	if err := c.Validate(); err == nil {
		t.Fatal("simultaneous literal and environment token accepted")
	}
}
