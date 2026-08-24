package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SleepingCat/PlexLink/internal/model"
	"gopkg.in/yaml.v3"
)

type Config struct {
	QBittorrent QBittorrent `yaml:"qbittorrent"`
	TMDB        TMDB        `yaml:"tmdb"`
	AI          AI          `yaml:"ai"`
	Resolvers   Resolvers   `yaml:"resolvers"`
	Paths       Paths       `yaml:"paths"`
	Matching    Matching    `yaml:"matching"`
	State       State       `yaml:"state"`
}

type Resolvers struct {
	Timeout       string        `yaml:"timeout"`
	OpenSubtitles OpenSubtitles `yaml:"opensubtitles"`
	Kinopoisk     Kinopoisk     `yaml:"kinopoisk"`
	TVMaze        TVMaze        `yaml:"tvmaze"`
}

type OpenSubtitles struct {
	Enabled             bool   `yaml:"enabled"`
	BaseURL             string `yaml:"base_url"`
	APIKey              string `yaml:"api_key"`
	APIKeyEnv           string `yaml:"api_key_env"`
	RepresentativeFiles int    `yaml:"representative_files"`
}

type Kinopoisk struct {
	Enabled   bool   `yaml:"enabled"`
	BaseURL   string `yaml:"base_url"`
	APIKey    string `yaml:"api_key"`
	APIKeyEnv string `yaml:"api_key_env"`
}

type TVMaze struct {
	Enabled bool   `yaml:"enabled"`
	BaseURL string `yaml:"base_url"`
}

type QBittorrent struct {
	URL         string `yaml:"url"`
	Username    string `yaml:"username"`
	PasswordEnv string `yaml:"password_env"`
	Password    string `yaml:"password"`
}

type TMDB struct {
	URL      string `yaml:"url"`
	TokenEnv string `yaml:"token_env"`
	Token    string `yaml:"token"`
	Language string `yaml:"language"`
}

type AI struct {
	Enabled         bool       `yaml:"enabled"`
	Provider        string     `yaml:"provider"`
	WebSearch       string     `yaml:"web_search"`
	MinConfidence   float64    `yaml:"min_confidence"`
	Timeout         string     `yaml:"timeout"`
	MaxOutputTokens int        `yaml:"max_output_tokens"`
	Cache           bool       `yaml:"cache"`
	XAI             XAI        `yaml:"xai"`
	Gemini          Gemini     `yaml:"gemini"`
	OpenRouter      OpenRouter `yaml:"openrouter"`
	Groq            Groq       `yaml:"groq"`
}

type XAI struct {
	BaseURL         string `yaml:"base_url"`
	APIKeyEnv       string `yaml:"api_key_env"`
	APIKey          string `yaml:"api_key"`
	Model           string `yaml:"model"`
	ReasoningEffort string `yaml:"reasoning_effort"`
}

type Gemini struct {
	BaseURL   string `yaml:"base_url"`
	APIKeyEnv string `yaml:"api_key_env"`
	APIKey    string `yaml:"api_key"`
	Model     string `yaml:"model"`
}

type OpenRouter struct {
	BaseURL         string `yaml:"base_url"`
	APIKeyEnv       string `yaml:"api_key_env"`
	APIKey          string `yaml:"api_key"`
	Model           string `yaml:"model"`
	ReasoningEffort string `yaml:"reasoning_effort"`
}

type Groq struct {
	BaseURL   string `yaml:"base_url"`
	APIKeyEnv string `yaml:"api_key_env"`
	APIKey    string `yaml:"api_key"`
	Model     string `yaml:"model"`
	Timeout   string `yaml:"timeout"`
}

type Paths struct {
	TVSource    string `yaml:"tv_source"`
	MovieSource string `yaml:"movie_source"`
	AnimeSource string `yaml:"anime_source"`
	TVTarget    string `yaml:"tv_target"`
	MovieTarget string `yaml:"movie_target"`
	AnimeTarget string `yaml:"anime_target"`
}

type Matching struct {
	MinScore  int `yaml:"min_score"`
	MinMargin int `yaml:"min_margin"`
}

type State struct {
	Directory string `yaml:"directory"`
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	c.defaults()
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c *Config) defaults() {
	if c.TMDB.URL == "" {
		c.TMDB.URL = "https://api.themoviedb.org/3"
	}
	if c.TMDB.Language == "" {
		c.TMDB.Language = "en-US"
	}
	if c.Matching.MinScore == 0 {
		c.Matching.MinScore = 80
	}
	if c.Matching.MinMargin == 0 {
		c.Matching.MinMargin = 15
	}
	if c.AI.Provider == "" {
		c.AI.Provider = "openrouter"
	}
	if c.AI.WebSearch == "" {
		c.AI.WebSearch = "never"
	}
	if c.AI.MinConfidence == 0 {
		c.AI.MinConfidence = 0.90
	}
	if c.AI.Timeout == "" {
		c.AI.Timeout = "45s"
	}
	if c.AI.MaxOutputTokens == 0 {
		c.AI.MaxOutputTokens = 2048
	}
	if c.AI.OpenRouter.BaseURL == "" {
		c.AI.OpenRouter.BaseURL = "https://openrouter.ai/api/v1"
	}
	if c.AI.OpenRouter.APIKeyEnv == "" && c.AI.OpenRouter.APIKey == "" {
		c.AI.OpenRouter.APIKeyEnv = "PLEXLINK_OPENROUTER_API_KEY"
	}
	if c.AI.OpenRouter.Model == "" {
		c.AI.OpenRouter.Model = "openrouter/free"
	}
	if c.AI.OpenRouter.ReasoningEffort == "" {
		c.AI.OpenRouter.ReasoningEffort = "minimal"
	}
	if c.AI.Groq.BaseURL == "" {
		c.AI.Groq.BaseURL = "https://api.groq.com/openai/v1"
	}
	if c.AI.Groq.APIKeyEnv == "" && c.AI.Groq.APIKey == "" {
		c.AI.Groq.APIKeyEnv = "PLEXLINK_GROQ_API_KEY"
	}
	if c.AI.Groq.Model == "" {
		c.AI.Groq.Model = "groq/compound-mini"
	}
	if c.AI.Groq.Timeout == "" {
		c.AI.Groq.Timeout = "15s"
	}
	if c.AI.XAI.BaseURL == "" {
		c.AI.XAI.BaseURL = "https://api.x.ai/v1"
	}
	if c.AI.XAI.APIKeyEnv == "" && c.AI.XAI.APIKey == "" {
		c.AI.XAI.APIKeyEnv = "PLEXLINK_XAI_API_KEY"
	}
	if c.AI.XAI.Model == "" {
		c.AI.XAI.Model = "grok-4.3"
	}
	if c.AI.XAI.ReasoningEffort == "" {
		c.AI.XAI.ReasoningEffort = "low"
	}
	if c.AI.Gemini.BaseURL == "" {
		c.AI.Gemini.BaseURL = "https://generativelanguage.googleapis.com/v1beta"
	}
	if c.AI.Gemini.APIKeyEnv == "" && c.AI.Gemini.APIKey == "" {
		c.AI.Gemini.APIKeyEnv = "PLEXLINK_GEMINI_API_KEY"
	}
	if c.AI.Gemini.Model == "" {
		c.AI.Gemini.Model = "gemini-2.5-flash"
	}
	if c.Resolvers.Timeout == "" {
		c.Resolvers.Timeout = "10s"
	}
	if c.Resolvers.OpenSubtitles.BaseURL == "" {
		c.Resolvers.OpenSubtitles.BaseURL = "https://api.opensubtitles.com/api/v1"
	}
	if c.Resolvers.OpenSubtitles.APIKey == "" && c.Resolvers.OpenSubtitles.APIKeyEnv == "" {
		c.Resolvers.OpenSubtitles.APIKeyEnv = "PLEXLINK_OPENSUBTITLES_API_KEY"
	}
	if c.Resolvers.OpenSubtitles.RepresentativeFiles == 0 {
		c.Resolvers.OpenSubtitles.RepresentativeFiles = 3
	}
	if c.Resolvers.Kinopoisk.BaseURL == "" {
		c.Resolvers.Kinopoisk.BaseURL = "https://api.poiskkino.dev"
	}
	if c.Resolvers.Kinopoisk.APIKey == "" && c.Resolvers.Kinopoisk.APIKeyEnv == "" {
		c.Resolvers.Kinopoisk.APIKeyEnv = "PLEXLINK_KINOPOISK_API_KEY"
	}
	if c.Resolvers.TVMaze.BaseURL == "" {
		c.Resolvers.TVMaze.BaseURL = "https://api.tvmaze.com"
	}
}

func (c Config) Validate() error {
	var missing []string
	fields := map[string]string{
		"qbittorrent.url": c.QBittorrent.URL, "qbittorrent.username": c.QBittorrent.Username,
		"paths.tv_source": c.Paths.TVSource, "paths.movie_source": c.Paths.MovieSource,
		"paths.anime_source": c.Paths.AnimeSource, "paths.tv_target": c.Paths.TVTarget,
		"paths.movie_target": c.Paths.MovieTarget, "paths.anime_target": c.Paths.AnimeTarget,
		"state.directory": c.State.Directory,
	}
	for k, v := range fields {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("invalid config: missing %s", strings.Join(missing, ", "))
	}
	if (c.QBittorrent.PasswordEnv == "") == (c.QBittorrent.Password == "") {
		return errors.New("invalid config: set exactly one of qbittorrent.password_env or qbittorrent.password")
	}
	if (c.TMDB.TokenEnv == "") == (c.TMDB.Token == "") {
		return errors.New("invalid config: set exactly one of tmdb.token_env or tmdb.token")
	}
	if c.Matching.MinScore < 1 || c.Matching.MinMargin < 0 {
		return errors.New("invalid config: matching thresholds")
	}
	if c.AI.Provider != "" && c.AI.Provider != "xai" && c.AI.Provider != "gemini" && c.AI.Provider != "openrouter" && c.AI.Provider != "groq" {
		return errors.New("invalid config: ai.provider must be openrouter, groq, xai, or gemini")
	}
	if c.AI.WebSearch != "" && c.AI.WebSearch != "never" && c.AI.WebSearch != "allow" && c.AI.WebSearch != "require" {
		return errors.New("invalid config: ai.web_search must be never, allow, or require")
	}
	if (c.AI.MinConfidence != 0 && (c.AI.MinConfidence <= 0 || c.AI.MinConfidence > 1)) || c.AI.MaxOutputTokens < 0 {
		return errors.New("invalid config: AI confidence/output limits")
	}
	if c.AI.Timeout != "" {
		if _, err := time.ParseDuration(c.AI.Timeout); err != nil {
			return fmt.Errorf("invalid config: ai.timeout: %w", err)
		}
	}
	if c.AI.Groq.Timeout != "" {
		if _, err := time.ParseDuration(c.AI.Groq.Timeout); err != nil {
			return fmt.Errorf("invalid config: ai.groq.timeout: %w", err)
		}
	}
	if c.Resolvers.Timeout != "" {
		if _, err := time.ParseDuration(c.Resolvers.Timeout); err != nil {
			return fmt.Errorf("invalid config: resolvers.timeout: %w", err)
		}
	}
	if c.Resolvers.OpenSubtitles.RepresentativeFiles < 0 || (c.Resolvers.OpenSubtitles.Enabled && c.Resolvers.OpenSubtitles.RepresentativeFiles < 1) {
		return errors.New("invalid config: resolvers.opensubtitles.representative_files must be positive")
	}
	if c.Resolvers.OpenSubtitles.Enabled {
		if strings.TrimSpace(c.Resolvers.OpenSubtitles.BaseURL) == "" {
			return errors.New("invalid config: resolvers.opensubtitles.base_url is required when enabled")
		}
		if strings.TrimSpace(c.Resolvers.OpenSubtitles.APIKey) == "" && strings.TrimSpace(c.Resolvers.OpenSubtitles.APIKeyEnv) == "" {
			return errors.New("invalid config: OpenSubtitles API key is required when enabled")
		}
	}
	if c.Resolvers.Kinopoisk.Enabled {
		if strings.TrimSpace(c.Resolvers.Kinopoisk.BaseURL) == "" {
			return errors.New("invalid config: resolvers.kinopoisk.base_url is required when enabled")
		}
		if strings.TrimSpace(c.Resolvers.Kinopoisk.APIKey) == "" && strings.TrimSpace(c.Resolvers.Kinopoisk.APIKeyEnv) == "" {
			return errors.New("invalid config: Kinopoisk API key is required when enabled")
		}
	}
	if c.Resolvers.TVMaze.Enabled && strings.TrimSpace(c.Resolvers.TVMaze.BaseURL) == "" {
		return errors.New("invalid config: resolvers.tvmaze.base_url is required when enabled")
	}
	if c.AI.Enabled && c.AI.Provider == "xai" && (strings.TrimSpace(c.AI.XAI.BaseURL) == "" || strings.TrimSpace(c.AI.XAI.Model) == "") {
		return errors.New("invalid config: incomplete ai.xai configuration")
	}
	if c.AI.Enabled && c.AI.Provider == "xai" && (strings.TrimSpace(c.AI.XAI.APIKeyEnv) == "") == (strings.TrimSpace(c.AI.XAI.APIKey) == "") {
		return errors.New("invalid config: set exactly one of ai.xai.api_key_env or ai.xai.api_key")
	}
	if c.AI.Enabled && c.AI.Provider == "gemini" && (strings.TrimSpace(c.AI.Gemini.BaseURL) == "" || strings.TrimSpace(c.AI.Gemini.Model) == "") {
		return errors.New("invalid config: incomplete ai.gemini configuration")
	}
	if c.AI.Enabled && c.AI.Provider == "gemini" && (strings.TrimSpace(c.AI.Gemini.APIKeyEnv) == "") == (strings.TrimSpace(c.AI.Gemini.APIKey) == "") {
		return errors.New("invalid config: set exactly one of ai.gemini.api_key_env or ai.gemini.api_key")
	}
	if c.AI.Enabled && c.AI.Provider == "openrouter" && (strings.TrimSpace(c.AI.OpenRouter.BaseURL) == "" || strings.TrimSpace(c.AI.OpenRouter.Model) == "") {
		return errors.New("invalid config: incomplete ai.openrouter configuration")
	}
	if c.AI.Enabled && c.AI.Provider == "openrouter" && (strings.TrimSpace(c.AI.OpenRouter.APIKeyEnv) == "") == (strings.TrimSpace(c.AI.OpenRouter.APIKey) == "") {
		return errors.New("invalid config: set exactly one of ai.openrouter.api_key_env or ai.openrouter.api_key")
	}
	if c.AI.Enabled && c.AI.Provider == "groq" && (strings.TrimSpace(c.AI.Groq.BaseURL) == "" || strings.TrimSpace(c.AI.Groq.Model) == "") {
		return errors.New("invalid config: incomplete ai.groq configuration")
	}
	if c.AI.Enabled && c.AI.Provider == "groq" && c.AI.WebSearch != "require" {
		return errors.New("invalid config: ai.web_search must be require for Groq")
	}
	if c.AI.Enabled && c.AI.Provider == "groq" && (strings.TrimSpace(c.AI.Groq.APIKeyEnv) == "") == (strings.TrimSpace(c.AI.Groq.APIKey) == "") {
		return errors.New("invalid config: set exactly one of ai.groq.api_key_env or ai.groq.api_key")
	}
	if !validOpenRouterReasoningEffort(c.AI.OpenRouter.ReasoningEffort) {
		return errors.New("invalid config: ai.openrouter.reasoning_effort must be none, minimal, low, medium, or high")
	}
	return nil
}

func validOpenRouterReasoningEffort(value string) bool {
	switch value {
	case "", "none", "minimal", "low", "medium", "high":
		return true
	default:
		return false
	}
}

func (c Config) Password() (string, error) {
	if c.QBittorrent.Password != "" {
		return c.QBittorrent.Password, nil
	}
	return secret(c.QBittorrent.PasswordEnv, "qBittorrent password")
}
func (c Config) Token() (string, error) {
	if c.TMDB.Token != "" {
		return c.TMDB.Token, nil
	}
	return secret(c.TMDB.TokenEnv, "TMDB token")
}
func (c Config) AIKey() (string, error) {
	if c.AI.Provider == "groq" {
		if c.AI.Groq.APIKey != "" {
			return c.AI.Groq.APIKey, nil
		}
		return secret(c.AI.Groq.APIKeyEnv, "Groq API key")
	}
	if c.AI.Provider == "openrouter" {
		if c.AI.OpenRouter.APIKey != "" {
			return c.AI.OpenRouter.APIKey, nil
		}
		return secret(c.AI.OpenRouter.APIKeyEnv, "OpenRouter API key")
	}
	if c.AI.Provider == "gemini" {
		if c.AI.Gemini.APIKey != "" {
			return c.AI.Gemini.APIKey, nil
		}
		return secret(c.AI.Gemini.APIKeyEnv, "Gemini API key")
	}
	if c.AI.XAI.APIKey != "" {
		return c.AI.XAI.APIKey, nil
	}
	return secret(c.AI.XAI.APIKeyEnv, "xAI API key")
}
func (c Config) AITimeout() time.Duration { d, _ := time.ParseDuration(c.AI.Timeout); return d }
func (c Config) GroqTimeout() time.Duration {
	d, _ := time.ParseDuration(c.AI.Groq.Timeout)
	return d
}
func (c Config) ResolverTimeout() time.Duration {
	d, _ := time.ParseDuration(c.Resolvers.Timeout)
	return d
}
func (c Config) OpenSubtitlesKey() (string, error) {
	if c.Resolvers.OpenSubtitles.APIKey != "" {
		return c.Resolvers.OpenSubtitles.APIKey, nil
	}
	return secret(c.Resolvers.OpenSubtitles.APIKeyEnv, "OpenSubtitles API key")
}
func (c Config) KinopoiskKey() (string, error) {
	if c.Resolvers.Kinopoisk.APIKey != "" {
		return c.Resolvers.Kinopoisk.APIKey, nil
	}
	return secret(c.Resolvers.Kinopoisk.APIKeyEnv, "Kinopoisk API key")
}
func secret(name, label string) (string, error) {
	v := os.Getenv(name)
	if v == "" {
		return "", fmt.Errorf("%s environment variable %q is empty", label, name)
	}
	return v, nil
}

func (p Paths) Roots(kind model.Kind) (source, target string) {
	switch kind {
	case model.KindTV:
		return p.TVSource, p.TVTarget
	case model.KindMovie:
		return p.MovieSource, p.MovieTarget
	case model.KindAnime:
		return p.AnimeSource, p.AnimeTarget
	default:
		return "", ""
	}
}

func DefaultPath() string {
	if v := os.Getenv("PLEXLINK_CONFIG"); v != "" {
		return v
	}
	return filepath.Join(".", "config.yaml")
}
