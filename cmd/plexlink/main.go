package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/SleepingCat/PlexLink/internal/ai/providers/gemini"
	"github.com/SleepingCat/PlexLink/internal/ai/providers/groq"
	"github.com/SleepingCat/PlexLink/internal/ai/providers/openrouter"
	"github.com/SleepingCat/PlexLink/internal/ai/providers/xai"
	"github.com/SleepingCat/PlexLink/internal/app"
	"github.com/SleepingCat/PlexLink/internal/config"
	"github.com/SleepingCat/PlexLink/internal/doctor"
	"github.com/SleepingCat/PlexLink/internal/ensemble/opensubtitles"
	"github.com/SleepingCat/PlexLink/internal/kinopoisk"
	"github.com/SleepingCat/PlexLink/internal/qbt"
	tmdbresolver "github.com/SleepingCat/PlexLink/internal/resolvers/tmdb"
	"github.com/SleepingCat/PlexLink/internal/resolvers/tvmaze"
	"github.com/SleepingCat/PlexLink/internal/runlog"
	"github.com/SleepingCat/PlexLink/internal/tmdb"
)

var hashRE = regexp.MustCompile(`(?i)^[a-f0-9]{40}$`)

func main() { os.Exit(run()) }
func run() int {
	if len(os.Args) < 2 {
		usage()
		return 40
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	cfgPath := fs.String("config", config.DefaultPath(), "configuration file")
	hash := fs.String("hash", "", "torrent infohash")
	dry := fs.Bool("dry-run", false, "plan without filesystem changes")
	noAI := fs.Bool("no-ai", false, "disable AI fallback for this run")
	debug := fs.Bool("debug", false, "include debug diagnostics")
	id := fs.Int("tmdb-id", 0, "explicit TMDB ID")
	remember := fs.Bool("remember-alias", false, "reserved for a future release")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return 40
	}
	_ = remember
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 40
	}
	password, err := cfg.Password()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 40
	}
	token, err := cfg.Token()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 40
	}
	h := &http.Client{Timeout: 20 * time.Second}
	qc, _ := qbt.New(cfg.QBittorrent.URL, cfg.QBittorrent.Username, password, h)
	tc := tmdb.New(cfg.TMDB.URL, token, cfg.TMDB.Language, h)
	p := &app.Processor{Torrents: qc, Metadata: tc, Config: cfg}
	p.Resolvers = append(p.Resolvers, tmdbresolver.New(tc))
	resolverHTTP := &http.Client{Timeout: cfg.ResolverTimeout()}
	if cfg.Resolvers.OpenSubtitles.Enabled {
		key, keyErr := cfg.OpenSubtitlesKey()
		if keyErr != nil {
			fmt.Fprintln(os.Stderr, keyErr)
			return 40
		}
		client := opensubtitles.NewClient(cfg.Resolvers.OpenSubtitles.BaseURL, key, "PlexLink/0.1", resolverHTTP)
		p.Resolvers = append(p.Resolvers, opensubtitles.New(client, cfg.Resolvers.OpenSubtitles.RepresentativeFiles))
	}
	var kinopoiskClient *kinopoisk.Client
	if cfg.Resolvers.Kinopoisk.Enabled {
		key, keyErr := cfg.KinopoiskKey()
		if keyErr != nil {
			fmt.Fprintln(os.Stderr, keyErr)
			return 40
		}
		kinopoiskClient = kinopoisk.NewClient(cfg.Resolvers.Kinopoisk.BaseURL, key, resolverHTTP)
		p.Resolvers = append(p.Resolvers, kinopoisk.NewResolver(kinopoiskClient))
	}
	if cfg.Resolvers.TVMaze.Enabled {
		tvmazeClient := tvmaze.New(cfg.Resolvers.TVMaze.BaseURL, resolverHTTP)
		p.Resolvers = append(p.Resolvers, tvmaze.NewResolver(tvmazeClient))
		p.EpisodeCatalog = app.NewTVMazeEpisodeCatalog(tc, tvmazeClient)
	}
	if cfg.AI.Enabled {
		key, keyErr := cfg.AIKey()
		if keyErr != nil {
			fmt.Fprintln(os.Stderr, keyErr)
			return 40
		}
		aiHTTP := &http.Client{Timeout: cfg.AITimeout()}
		var resolverErr error
		switch cfg.AI.Provider {
		case "openrouter":
			p.AI, resolverErr = openrouter.New(openrouter.Config{BaseURL: cfg.AI.OpenRouter.BaseURL, APIKey: key, Model: cfg.AI.OpenRouter.Model, ReasoningEffort: cfg.AI.OpenRouter.ReasoningEffort, MaxOutputTokens: cfg.AI.MaxOutputTokens}, aiHTTP)
			p.AIProvider, p.AIModel = "openrouter", cfg.AI.OpenRouter.Model
		case "groq":
			p.AI, resolverErr = groq.New(groq.Config{BaseURL: cfg.AI.Groq.BaseURL, APIKey: key, Model: cfg.AI.Groq.Model, MinConfidence: cfg.AI.MinConfidence}, &http.Client{Timeout: cfg.GroqTimeout()})
			p.AIProvider, p.AIModel = "groq", cfg.AI.Groq.Model
		case "gemini":
			p.AI, resolverErr = gemini.New(gemini.Config{BaseURL: cfg.AI.Gemini.BaseURL, APIKey: key, Model: cfg.AI.Gemini.Model, MaxOutputTokens: cfg.AI.MaxOutputTokens}, aiHTTP)
			p.AIProvider, p.AIModel = "gemini", cfg.AI.Gemini.Model
		case "xai":
			p.AI, resolverErr = xai.New(xai.Config{BaseURL: cfg.AI.XAI.BaseURL, APIKey: key, Model: cfg.AI.XAI.Model, ReasoningEffort: cfg.AI.XAI.ReasoningEffort, MaxOutputTokens: cfg.AI.MaxOutputTokens}, aiHTTP)
			p.AIProvider, p.AIModel = "xai", cfg.AI.XAI.Model
		}
		if resolverErr != nil {
			fmt.Fprintln(os.Stderr, resolverErr)
			return 40
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if cmd == "doctor" {
		if err := qc.Login(ctx); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 41
		}
		if err := tc.Ping(ctx); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 42
		}
		if err := doctor.Filesystems(cfg.Paths); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 50
		}
		for _, diagnostic := range doctor.ResolverConfiguration(cfg.Resolvers) {
			fmt.Println(diagnostic)
		}
		if cfg.Resolvers.Kinopoisk.Enabled {
			fmt.Println(doctor.PoiskKinoStatus(ctx, kinopoiskClient))
		}
		fmt.Println("OK: configuration, qBittorrent, TMDB and hardlink probes")
		return 0
	}
	if cmd != "process" && cmd != "inspect" && cmd != "resolve" {
		usage()
		return 40
	}
	if !hashRE.MatchString(*hash) {
		fmt.Fprintln(os.Stderr, "--hash must be a 40-character hexadecimal infohash")
		return 40
	}
	if cmd == "resolve" && *id <= 0 {
		fmt.Fprintln(os.Stderr, "resolve requires --tmdb-id")
		return 40
	}
	if cmd == "inspect" {
		*dry = true
	}
	var persistent *runlog.Run
	if cmd == "process" && cfg.Logging.Enabled {
		executable, executableErr := os.Executable()
		if executableErr != nil {
			executable = os.Args[0]
		}
		executable, _ = filepath.Abs(executable)
		actualConfig, _ := filepath.Abs(*cfgPath)
		level := cfg.Logging.Level
		if *debug {
			level = "debug"
		}
		persistent, err = runlog.Start(runlog.Options{Directory: cfg.LoggingDirectory(), Level: level, Hash: *hash, ConfigPath: actualConfig, Executable: executable, MaxBytes: int64(cfg.Logging.MaxTotalMB) * 1024 * 1024})
		if err != nil {
			fmt.Fprintln(os.Stderr, "persistent logging disabled:", err)
		}
	}
	r, err := p.ProcessWithOptions(ctx, *hash, app.ProcessOptions{DryRun: *dry, ManualID: *id, NoAI: *noAI})
	b, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(b))
	exitCode := processExitCode(err)
	status, attention := processLogOutcome(r, err)
	if persistent != nil {
		persistent.Record(r, status, exitCode, err)
		if _, _, logErr := persistent.Finalize(r, status, exitCode, attention, err); logErr != nil {
			fmt.Fprintln(os.Stderr, "finalize persistent log:", logErr)
		}
	}
	if err != nil {
		slog.Error("processing failed", "torrent_hash", *hash, "error", err)
		return exitCode
	}
	if *dry {
		fmt.Println("DRY RUN: no filesystem changes")
	}
	return 0
}
func usage() { fmt.Fprintln(os.Stderr, "usage: plexlink <doctor|process|inspect|resolve> [options]") }

func processExitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, app.ErrIgnored):
		return 10
	case errors.Is(err, app.ErrAnimeNumbering):
		return 21
	case errors.Is(err, app.ErrUnresolved):
		return 20
	case errors.Is(err, app.ErrConflict):
		return 30
	case errors.Is(err, app.ErrTorrent):
		return 41
	case errors.Is(err, app.ErrMetadata):
		return 42
	case errors.Is(err, app.ErrAI):
		return 43
	default:
		return 50
	}
}

func processLogOutcome(result app.Result, err error) (string, bool) {
	if err == nil {
		if result.MappingStatus == "PARTIAL" || result.MappingStatus == "CONFLICT" {
			return string(result.MappingStatus), true
		}
		return "SUCCESS", false
	}
	if errors.Is(err, app.ErrIgnored) {
		return "IGNORED", false
	}
	switch {
	case errors.Is(err, app.ErrAnimeNumbering):
		return "UNRESOLVED_ANIME_NUMBERING", true
	case errors.Is(err, app.ErrUnresolved):
		return "UNRESOLVED", true
	case errors.Is(err, app.ErrConflict):
		return "CONFLICT", true
	case errors.Is(err, app.ErrTorrent):
		return "QBITTORRENT_ERROR", true
	case errors.Is(err, app.ErrMetadata):
		return "METADATA_ERROR", true
	case errors.Is(err, app.ErrAI):
		return "AI_ERROR", true
	default:
		return "FILESYSTEM_ERROR", true
	}
}
