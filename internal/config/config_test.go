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

func TestLoadQBittorrentShutdownAfterProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("qbittorrent:\n  url: http://qbt\n  username: user\n  password: secret\n  shutdown_after_process: true\ntmdb:\n  token: token\npaths:\n  tv_source: tv\n  movie_source: movies\n  anime_source: anime\n  tv_target: ptv\n  movie_target: pmovies\n  anime_target: panime\nstate:\n  directory: state\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.QBittorrent.ShutdownAfterProcess {
		t.Fatal("qbittorrent.shutdown_after_process was not loaded")
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

func TestGeminiConfigDefaultsAndEnvironmentKey(t *testing.T) {
	c := Config{QBittorrent: QBittorrent{URL: "http://qbt", Username: "user", Password: "secret"}, TMDB: TMDB{Token: "token"}, AI: AI{Enabled: true, Provider: "gemini", WebSearch: "allow", MinConfidence: .9, Timeout: "45s", MaxOutputTokens: 1200, Gemini: Gemini{BaseURL: "https://example.test/v1beta", Model: "gemini-test", APIKeyEnv: "PLEXLINK_TEST_GEMINI_KEY"}}, Paths: Paths{TVSource: "tv", MovieSource: "movies", AnimeSource: "anime", TVTarget: "ptv", MovieTarget: "pmovies", AnimeTarget: "panime"}, Matching: Matching{MinScore: 80, MinMargin: 15}, State: State{Directory: "state"}}
	t.Setenv("PLEXLINK_TEST_GEMINI_KEY", "gemini-key")
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if key, err := c.AIKey(); err != nil || key != "gemini-key" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	c.AI.Gemini.APIKeyEnv = ""
	if err := c.Validate(); err == nil {
		t.Fatal("missing Gemini key accepted")
	}
	c.AI.Gemini.APIKey = "literal-key"
	if err := c.Validate(); err != nil {
		t.Fatalf("literal Gemini key rejected: %v", err)
	}
	if key, err := c.AIKey(); err != nil || key != "literal-key" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	c.AI.Gemini.APIKeyEnv = "PLEXLINK_TEST_GEMINI_KEY"
	if err := c.Validate(); err == nil {
		t.Fatal("simultaneous literal and environment Gemini key accepted")
	}
}

func TestLoadDoesNotDefaultGeminiAPIKeyEnvWhenLiteralKeyIsSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("qbittorrent:\n  url: http://qbt\n  username: user\n  password: secret\ntmdb:\n  token: token\nai:\n  enabled: true\n  provider: gemini\n  gemini:\n    api_key: literal-key\npaths:\n  tv_source: tv\n  movie_source: movies\n  anime_source: anime\n  tv_target: ptv\n  movie_target: pmovies\n  anime_target: panime\nstate:\n  directory: state\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AI.Gemini.APIKeyEnv != "" || cfg.AI.Gemini.APIKey != "literal-key" {
		t.Fatalf("Gemini config=%+v", cfg.AI.Gemini)
	}
}

func TestOpenRouterDefaultsAndEnvironmentKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("qbittorrent:\n  url: http://qbt\n  username: user\n  password: secret\ntmdb:\n  token: token\nai:\n  enabled: true\npaths:\n  tv_source: tv\n  movie_source: movies\n  anime_source: anime\n  tv_target: ptv\n  movie_target: pmovies\n  anime_target: panime\nstate:\n  directory: state\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLEXLINK_OPENROUTER_API_KEY", "openrouter-key")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AI.Provider != "openrouter" || cfg.AI.WebSearch != "never" || cfg.AI.MaxOutputTokens != 2048 || cfg.AI.OpenRouter.BaseURL != "https://openrouter.ai/api/v1" || cfg.AI.OpenRouter.Model != "openrouter/free" || cfg.AI.OpenRouter.ReasoningEffort != "minimal" {
		t.Fatalf("AI defaults=%+v", cfg.AI)
	}
	if key, err := cfg.AIKey(); err != nil || key != "openrouter-key" {
		t.Fatalf("key=%q err=%v", key, err)
	}
}

func TestOpenRouterReasoningEffortValidation(t *testing.T) {
	c := Config{QBittorrent: QBittorrent{URL: "http://qbt", Username: "user", Password: "secret"}, TMDB: TMDB{Token: "token"}, AI: AI{Provider: "openrouter", WebSearch: "never", MinConfidence: .9, Timeout: "45s", MaxOutputTokens: 2048, OpenRouter: OpenRouter{ReasoningEffort: "extreme"}}, Paths: Paths{TVSource: "tv", MovieSource: "movies", AnimeSource: "anime", TVTarget: "ptv", MovieTarget: "pmovies", AnimeTarget: "panime"}, Matching: Matching{MinScore: 80, MinMargin: 15}, State: State{Directory: "state"}}
	if err := c.Validate(); err == nil {
		t.Fatal("invalid OpenRouter reasoning effort accepted")
	}
	for _, effort := range []string{"none", "minimal", "low", "medium", "high"} {
		c.AI.OpenRouter.ReasoningEffort = effort
		if err := c.Validate(); err != nil {
			t.Fatalf("effort %q rejected: %v", effort, err)
		}
	}
}

func TestOpenRouterLiteralKeyDoesNotGainEnvironmentDefault(t *testing.T) {
	c := Config{QBittorrent: QBittorrent{URL: "http://qbt", Username: "user", Password: "secret"}, TMDB: TMDB{Token: "token"}, AI: AI{Enabled: true, Provider: "openrouter", WebSearch: "never", MinConfidence: .9, Timeout: "45s", MaxOutputTokens: 2048, OpenRouter: OpenRouter{BaseURL: "https://openrouter.ai/api/v1", Model: "openrouter/free", APIKey: "literal-key"}}, Paths: Paths{TVSource: "tv", MovieSource: "movies", AnimeSource: "anime", TVTarget: "ptv", MovieTarget: "pmovies", AnimeTarget: "panime"}, Matching: Matching{MinScore: 80, MinMargin: 15}, State: State{Directory: "state"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if key, err := c.AIKey(); err != nil || key != "literal-key" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	c.AI.OpenRouter.APIKeyEnv = "PLEXLINK_OPENROUTER_API_KEY"
	if err := c.Validate(); err == nil {
		t.Fatal("simultaneous OpenRouter literal and environment key accepted")
	}
}

func TestGroqDefaultsRequiredWebSearchAndSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("qbittorrent:\n  url: http://qbt\n  username: user\n  password: secret\ntmdb:\n  token: token\nai:\n  enabled: true\n  provider: groq\n  web_search: require\npaths:\n  tv_source: tv\n  movie_source: movies\n  anime_source: anime\n  tv_target: ptv\n  movie_target: pmovies\n  anime_target: panime\nstate:\n  directory: state\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLEXLINK_GROQ_API_KEY", "groq-key")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AI.Groq.BaseURL != "https://api.groq.com/openai/v1" || cfg.AI.Groq.Model != "groq/compound-mini" || cfg.AI.Groq.Timeout != "15s" || cfg.GroqTimeout().String() != "15s" {
		t.Fatalf("Groq defaults=%+v", cfg.AI.Groq)
	}
	if key, err := cfg.AIKey(); err != nil || key != "groq-key" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	cfg.AI.WebSearch = "allow"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Groq accepted non-required web search")
	}
}

func TestGroqLiteralKeyDoesNotGainEnvironmentDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("qbittorrent:\n  url: http://qbt\n  username: user\n  password: secret\ntmdb:\n  token: token\nai:\n  enabled: true\n  provider: groq\n  web_search: require\n  groq:\n    api_key: literal-key\npaths:\n  tv_source: tv\n  movie_source: movies\n  anime_source: anime\n  tv_target: ptv\n  movie_target: pmovies\n  anime_target: panime\nstate:\n  directory: state\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AI.Groq.APIKeyEnv != "" || cfg.AI.Groq.APIKey != "literal-key" {
		t.Fatalf("Groq config=%+v", cfg.AI.Groq)
	}
}

func TestResolverDefaultsAndDisabledKeysAreOptional(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("qbittorrent:\n  url: http://qbt\n  username: user\n  password: secret\ntmdb:\n  token: token\npaths:\n  tv_source: tv\n  movie_source: movies\n  anime_source: anime\n  tv_target: ptv\n  movie_target: pmovies\n  anime_target: panime\nstate:\n  directory: state\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Resolvers.Timeout != "10s" || cfg.Resolvers.OpenSubtitles.RepresentativeFiles != 3 || cfg.Resolvers.OpenSubtitles.BaseURL != "https://api.opensubtitles.com/api/v1" || cfg.Resolvers.Kinopoisk.BaseURL != "https://api.poiskkino.dev" || cfg.Resolvers.TVMaze.BaseURL != "https://api.tvmaze.com" {
		t.Fatalf("resolver defaults=%+v", cfg.Resolvers)
	}
}

func TestResolverDirectKeyTakesPrecedenceOverEnvironmentReference(t *testing.T) {
	c := Config{Resolvers: Resolvers{OpenSubtitles: OpenSubtitles{APIKey: "direct", APIKeyEnv: "PLEXLINK_TEST_OS_KEY"}, Kinopoisk: Kinopoisk{APIKey: "direct-kp", APIKeyEnv: "PLEXLINK_TEST_KP_KEY"}}}
	t.Setenv("PLEXLINK_TEST_OS_KEY", "environment")
	t.Setenv("PLEXLINK_TEST_KP_KEY", "environment-kp")
	if key, err := c.OpenSubtitlesKey(); err != nil || key != "direct" {
		t.Fatalf("OpenSubtitles key=%q err=%v", key, err)
	}
	if key, err := c.KinopoiskKey(); err != nil || key != "direct-kp" {
		t.Fatalf("Kinopoisk key=%q err=%v", key, err)
	}
}

func TestLoggingDefaultsDirectoryAndValidation(t *testing.T) {
	base := "qbittorrent:\n  url: http://qbt\n  username: user\n  password: secret\ntmdb:\n  token: token\npaths:\n  tv_source: tv\n  movie_source: movies\n  anime_source: anime\n  tv_target: ptv\n  movie_target: pmovies\n  anime_target: panime\nstate:\n  directory: state\n"
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Logging.Enabled || cfg.Logging.Level != "info" || cfg.Logging.MaxTotalMB != 50 || cfg.LoggingDirectory() != filepath.Join("state", "logs") {
		t.Fatalf("logging defaults=%+v dir=%q", cfg.Logging, cfg.LoggingDirectory())
	}
	for _, value := range []string{"debug", "info", "warn", "error"} {
		body := base + "logging:\n  enabled: false\n  level: " + value + "\n  directory: custom-logs\n  max_total_mb: 1\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(path)
		if err != nil {
			t.Fatalf("level %q rejected: %v", value, err)
		}
		if loaded.Logging.Enabled || loaded.LoggingDirectory() != "custom-logs" {
			t.Fatalf("explicit logging settings ignored: %+v", loaded.Logging)
		}
	}
	for _, value := range []string{"0", "-1"} {
		if err := os.WriteFile(path, []byte(base+"logging:\n  max_total_mb: "+value+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("max_total_mb %s accepted", value)
		}
	}
}
