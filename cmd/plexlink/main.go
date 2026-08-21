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
	"regexp"
	"time"

	"github.com/SleepingCat/PlexLink/internal/app"
	"github.com/SleepingCat/PlexLink/internal/config"
	"github.com/SleepingCat/PlexLink/internal/doctor"
	"github.com/SleepingCat/PlexLink/internal/qbt"
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
	r, err := p.Process(ctx, *hash, *dry, *id)
	b, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(b))
	if err != nil {
		slog.Error("processing failed", "torrent_hash", *hash, "error", err)
		switch {
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
		default:
			return 50
		}
	}
	if *dry {
		fmt.Println("DRY RUN: no filesystem changes")
	}
	return 0
}
func usage() { fmt.Fprintln(os.Stderr, "usage: plexlink <doctor|process|inspect|resolve> [options]") }
