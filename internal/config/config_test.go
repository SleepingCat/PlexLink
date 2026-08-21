package config

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestXAIKeyCanBeLiteralOrEnvironment(t *testing.T) {
	base := Config{QBittorrent: QBittorrent{URL: "http://qbt", Username: "user", Password: "secret"}, TMDB: TMDB{Token: "token"}, AI: AI{Enabled: true, Provider: "xai", WebSearch: "allow", MinConfidence: .9, Timeout: "45s", MaxOutputTokens: 1200, XAI: XAI{BaseURL: "https://api.x.ai/v1", Model: "model", APIKey: "literal-key"}}, Paths: Paths{TVSource: "tv", MovieSource: "movies", AnimeSource: "anime", TVTarget: "ptv", MovieTarget: "pmovies", AnimeTarget: "panime"}, Matching: Matching{MinScore: 80, MinMargin: 15}, State: State{Directory: "state"}}
	if err := base.Validate(); err != nil {
		t.Fatalf("literal xAI key rejected: %v", err)
	}
	if key, err := base.AIKey(); err != nil || key != "literal-key" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	base.AI.XAI.APIKeyEnv = "PLEXLINK_TEST_XAI_KEY"
	if err := base.Validate(); err == nil {
		t.Fatal("simultaneous literal and environment xAI key accepted")
	}
	base.AI.XAI.APIKey = ""
	t.Setenv("PLEXLINK_TEST_XAI_KEY", "environment-key")
	if err := base.Validate(); err != nil {
		t.Fatalf("environment xAI key rejected: %v", err)
	}
	if key, err := base.AIKey(); err != nil || key != "environment-key" {
		t.Fatalf("key=%q err=%v", key, err)
	}
}

func TestLoadDoesNotDefaultAPIKeyEnvWhenLiteralKeyIsSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("qbittorrent:\n  url: http://qbt\n  username: user\n  password: secret\ntmdb:\n  token: token\nai:\n  enabled: true\n  xai:\n    api_key: literal-key\npaths:\n  tv_source: tv\n  movie_source: movies\n  anime_source: anime\n  tv_target: ptv\n  movie_target: pmovies\n  anime_target: panime\nstate:\n  directory: state\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AI.XAI.APIKeyEnv != "" || cfg.AI.XAI.APIKey != "literal-key" {
		t.Fatalf("xAI config=%+v", cfg.AI.XAI)
	}
}
