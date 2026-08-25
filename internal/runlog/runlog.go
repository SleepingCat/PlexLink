package runlog

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SleepingCat/PlexLink/internal/app"
	"github.com/SleepingCat/PlexLink/internal/linker"
	"github.com/SleepingCat/PlexLink/internal/model"
)

type Options struct {
	Directory  string
	Level      string
	Hash       string
	ConfigPath string
	Executable string
	MaxBytes   int64
	Now        func() time.Time
}

type Run struct {
	logger     *slog.Logger
	file       *os.File
	tmpPath    string
	finalPath  string
	directory  string
	hash       string
	configPath string
	executable string
	maxBytes   int64
	started    time.Time
	now        func() time.Time
}

func Start(options Options) (*Run, error) {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.MaxBytes <= 0 {
		return nil, errors.New("run log size budget must be positive")
	}
	runs := filepath.Join(options.Directory, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		return nil, fmt.Errorf("create run log directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(options.Directory, "failed"), 0o755); err != nil {
		return nil, fmt.Errorf("create failed log directory: %w", err)
	}
	started := options.Now()
	name := filename(started, options.Hash)
	tmpPath := filepath.Join(runs, name+".tmp")
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create run log: %w", err)
	}
	level, err := parseLevel(options.Level)
	if err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return nil, err
	}
	handler := slog.NewTextHandler(file, &slog.HandlerOptions{Level: level, ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
		if attr.Key == slog.TimeKey {
			attr.Value = slog.StringValue(attr.Value.Time().Format(time.RFC3339Nano))
		}
		return attr
	}})
	r := &Run{logger: slog.New(handler), file: file, tmpPath: tmpPath, finalPath: filepath.Join(runs, name+".log"), directory: options.Directory, hash: options.Hash, configPath: options.ConfigPath, executable: options.Executable, maxBytes: options.MaxBytes, started: started, now: options.Now}
	r.logger.Info("process.start", "event", "process.start", "hash", options.Hash)
	return r, nil
}

func (r *Run) Record(result app.Result, status string, exitCode int, processErr error) {
	if result.Torrent.Name != "" {
		r.logger.Info("torrent.loaded", "event", "torrent.loaded", "name", result.Torrent.Name, "kind", result.Kind)
	}
	if len(result.Evidence.Titles) > 0 {
		r.logger.Info("evidence.parsed", "event", "evidence.parsed", "title", result.Evidence.Titles[0].Title, "year", result.Evidence.Year, "episodes", len(result.Evidence.Episodes))
	}
	for _, resolver := range result.Ensemble.ResolverResults {
		args := []any{"event", "resolver.finished", "resolver", resolver.Name, "status", strings.ToUpper(string(resolver.Status)), "duration", resolver.Duration}
		if resolver.Error != nil {
			args = append(args, "error_kind", resolver.Error.Kind, "http_status", resolver.Error.StatusCode, "error", resolver.Error.Message)
		}
		if resolver.Status == "error" {
			r.logger.Warn("resolver.finished", args...)
		} else {
			r.logger.Info("resolver.finished", args...)
		}
	}
	if result.Ensemble.FirstPass != nil {
		r.logger.Info("ensemble.first_pass", "event", "ensemble.first_pass", "decision", result.Ensemble.FirstPass.Type, "reason", result.Ensemble.FirstPass.Reason)
	}
	if result.AI.Used {
		args := []any{"event", "ai.finished", "provider", result.AI.Provider, "model", result.AI.ActualModel, "http_status", result.AI.HTTPStatus, "duration", result.AI.Duration}
		if result.AI.Error != "" || result.AI.ProviderError != "" {
			r.logger.Warn("ai.finished", append(args, "error", first(result.AI.Error, result.AI.ProviderError))...)
		} else {
			r.logger.Info("ai.finished", append(args, "status", "OK")...)
		}
	}
	if result.Ensemble.FinalDecision != nil {
		r.logger.Info("ensemble.final", "event", "ensemble.final", "decision", result.Ensemble.FinalDecision.Type, "reason", result.Ensemble.FinalDecision.Reason)
	}
	if result.Match.ID > 0 {
		r.logger.Info("media.resolved", "event", "media.resolved", "tmdb_id", result.Match.ID, "title", result.Match.Name, "year", result.Match.Year)
	}
	resolved, provisional, unresolved, ignored := mappingCounts(result.EpisodeValidation)
	r.logger.Info("mapping.result", "event", "mapping.result", "status", result.MappingStatus, "resolved", resolved, "provisional", provisional, "unresolved", unresolved, "ignored", ignored)
	for i, action := range result.Actions {
		args := []any{"event", "hardlink.action", "action", action}
		if i < len(result.Plan) {
			args = append(args, "source", result.Plan[i].Source, "target", result.Plan[i].Target)
		}
		if action == linker.Conflict {
			r.logger.Warn("hardlink.action", args...)
		} else {
			r.logger.Info("hardlink.action", args...)
		}
	}
	if result.PlexMatch != nil {
		args := []any{"event", "plexmatch.action", "action", result.PlexMatch.Action, "target", result.PlexMatch.Target}
		if result.PlexMatch.Action == linker.Conflict {
			r.logger.Warn("plexmatch.action", args...)
		} else {
			r.logger.Info("plexmatch.action", args...)
		}
	}
	if shutdown := result.QBittorrentShutdown; shutdown != nil {
		r.logger.Info("qbittorrent.shutdown", "event", "qbittorrent.shutdown", "enabled", shutdown.Enabled, "attempted", shutdown.Attempted, "requested", shutdown.Requested, "skipped_reason", shutdown.SkippedReason, "error", shutdown.Error)
	}
	args := []any{"event", "process.result", "status", status, "exit_code", exitCode}
	if processErr != nil {
		args = append(args, "error", processErr.Error())
	}
	if exitCode == 0 && status != string(model.MappingPartial) {
		r.logger.Info("process.result", args...)
	} else {
		r.logger.Warn("process.result", args...)
	}
}

func (r *Run) Finalize(result app.Result, status string, exitCode int, attention bool, processErr error) (string, string, error) {
	r.logger.Info("process.end", "event", "process.end", "duration", r.now().Sub(r.started))
	if err := r.file.Close(); err != nil {
		return "", "", fmt.Errorf("close run log: %w", err)
	}
	if err := os.Rename(r.tmpPath, r.finalPath); err != nil {
		return "", "", fmt.Errorf("finalize run log: %w", err)
	}
	failedPath := ""
	if attention {
		failedPath = filepath.Join(r.directory, "failed", strings.TrimSuffix(filepath.Base(r.finalPath), ".log")+".log")
		if err := writeFailed(failedPath, r, result, status, exitCode, processErr); err != nil {
			return r.finalPath, "", err
		}
	}
	if err := Cleanup(r.directory, r.maxBytes); err != nil {
		// Cleanup is deliberately best-effort and cannot change processing semantics.
		return r.finalPath, failedPath, nil
	}
	return r.finalPath, failedPath, nil
}

func Cleanup(directory string, maxBytes int64) error {
	type entry struct {
		path string
		size int64
		mod  time.Time
	}
	var files []entry
	var total int64
	for _, sub := range []string{"runs", "failed"} {
		rows, err := os.ReadDir(filepath.Join(directory, sub))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		for _, row := range rows {
			if row.IsDir() || strings.ToLower(filepath.Ext(row.Name())) != ".log" {
				continue
			}
			info, err := row.Info()
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return err
			}
			files = append(files, entry{filepath.Join(directory, sub, row.Name()), info.Size(), info.ModTime()})
			total += info.Size()
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].mod.Equal(files[j].mod) {
			return files[i].path < files[j].path
		}
		return files[i].mod.Before(files[j].mod)
	})
	for _, file := range files {
		if total <= maxBytes {
			break
		}
		if err := os.Remove(file.path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		total -= file.size
	}
	return nil
}

func writeFailed(path string, run *Run, result app.Result, status string, exitCode int, processErr error) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create failed log: %w", err)
	}
	defer f.Close()
	reason := "The torrent was not fully processed."
	if processErr != nil {
		reason = processErr.Error()
	}
	fmt.Fprintf(f, "PlexLink failed run\n\nTime:       %s\nStatus:     %s\nTorrent:    %s\nHash:       %s\nKind:       %s\nExit code:  %d\n\nReason:\n%s\n", run.started.Format("2006-01-02 15:04:05"), status, result.Torrent.Name, run.hash, result.Kind, exitCode, reason)
	var failed []model.EpisodeValidation
	for _, mapping := range result.EpisodeValidation {
		if mapping.State == model.EpisodeUnresolved {
			failed = append(failed, mapping)
		}
	}
	if len(failed) > 0 {
		fmt.Fprintf(f, "\nResult:\n%d/%d media files processed.\n%d file(s) require attention.\n\nFailed files:\n", len(result.EpisodeValidation)-len(failed), len(result.EpisodeValidation), len(failed))
		for _, mapping := range failed {
			fmt.Fprintf(f, "- %s\n  reason: %s\n", mapping.File, mapping.Reason)
		}
	}
	fmt.Fprintf(f, "\nReplay:\n& %s process `\n  --config %s `\n  --hash %s `\n  --debug\n\nRun log:\n%s\n", psQuote(run.executable), psQuote(run.configPath), psQuote(run.hash), relativeRunPath(path, run.finalPath))
	return nil
}

func filename(now time.Time, hash string) string {
	short := hash
	if len(short) > 8 {
		short = short[:8]
	}
	var random [4]byte
	_, _ = io.ReadFull(rand.Reader, random[:])
	return fmt.Sprintf("%s-%s-%d-%s", now.Format("20060102-150405.000"), short, os.Getpid(), hex.EncodeToString(random[:]))
}

func parseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(value) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("invalid logging level %q", value)
}

func mappingCounts(values []model.EpisodeValidation) (resolved, provisional, unresolved, ignored int) {
	for _, value := range values {
		switch value.State {
		case model.EpisodeResolved:
			resolved++
		case model.EpisodeProvisional:
			provisional++
		case model.EpisodeUnresolved:
			unresolved++
		case model.EpisodeIgnored:
			ignored++
		}
	}
	return
}

func psQuote(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }
func relativeRunPath(failed, run string) string {
	value, err := filepath.Rel(filepath.Dir(failed), run)
	if err != nil {
		return run
	}
	return value
}
func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
