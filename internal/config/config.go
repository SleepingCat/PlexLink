package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SleepingCat/PlexLink/internal/model"
	"gopkg.in/yaml.v3"
)

type Config struct {
	QBittorrent QBittorrent `yaml:"qbittorrent"`
	TMDB        TMDB        `yaml:"tmdb"`
	Paths       Paths       `yaml:"paths"`
	Matching    Matching    `yaml:"matching"`
	State       State       `yaml:"state"`
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
	return nil
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
