package runlog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SleepingCat/PlexLink/internal/app"
	"github.com/SleepingCat/PlexLink/internal/linker"
	"github.com/SleepingCat/PlexLink/internal/model"
)

const testHash = "da795bc6b28f53fb92cd367b74ca9e8fd658e5fe"

func TestRunCreatesUniqueFinalLogsAndContainsSummaryWithoutSecrets(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	paths := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, err := Start(Options{Directory: dir, Level: "info", Hash: testHash, ConfigPath: `K:\config.yaml`, Executable: `K:\plexlink.exe`, MaxBytes: 1 << 20})
			if err != nil {
				t.Error(err)
				return
			}
			result := app.Result{Torrent: model.Torrent{Name: "Show", Hash: testHash}, Kind: model.KindTV, MappingStatus: model.MappingResolved, Actions: []linker.Action{linker.Noop}, AI: app.AIDiagnostics{ProviderRequest: `{"Authorization":"Bearer super-secret"}`}}
			run.Record(result, "SUCCESS", 0, nil)
			path, failed, err := run.Finalize(result, "SUCCESS", 0, false, nil)
			if err != nil {
				t.Error(err)
				return
			}
			if failed != "" {
				t.Errorf("unexpected failed log %q", failed)
			}
			paths <- path
		}()
	}
	wg.Wait()
	close(paths)
	seen := map[string]bool{}
	for path := range paths {
		if seen[path] {
			t.Fatalf("duplicate path %s", path)
		}
		seen[path] = true
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if !strings.Contains(text, testHash) || !strings.Contains(text, "Show") || !strings.Contains(text, "SUCCESS") {
			t.Fatalf("incomplete log: %s", text)
		}
		if strings.Contains(text, "super-secret") {
			t.Fatal("secret leaked")
		}
	}
	if tmp, _ := filepath.Glob(filepath.Join(dir, "runs", "*.tmp")); len(tmp) != 0 {
		t.Fatalf("temporary logs remain: %v", tmp)
	}
}

func TestFailedSummaryListsPartialFileAndReplay(t *testing.T) {
	dir := t.TempDir()
	run, err := Start(Options{Directory: dir, Level: "info", Hash: testHash, ConfigPath: `K:\PlexLink\config.yaml`, Executable: `K:\PlexLink\plexlink.exe`, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	result := app.Result{Torrent: model.Torrent{Name: "Some Show"}, Kind: model.KindTV, MappingStatus: model.MappingPartial, EpisodeValidation: []model.EpisodeValidation{{File: "S01E01.mkv", State: model.EpisodeResolved}, {File: "S01E02.mkv", State: model.EpisodeUnresolved, Reason: "episode could not be mapped"}}}
	run.Record(result, "PARTIAL", 0, nil)
	full, failed, err := run.Finalize(result, "PARTIAL", 0, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(failed)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, wanted := range []string{"Status:     PARTIAL", "S01E02.mkv", "episode could not be mapped", "--debug", testHash, filepath.Base(full)} {
		if !strings.Contains(text, wanted) {
			t.Errorf("summary missing %q:\n%s", wanted, text)
		}
	}
}

func TestCleanupUsesCombinedOldestFirstPoolAndKeepsTmp(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"runs", "failed"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := filepath.Join(dir, "failed", "old.log")
	newer := filepath.Join(dir, "runs", "new.log")
	tmp := filepath.Join(dir, "runs", "active.tmp")
	for _, path := range []string{old, newer, tmp} {
		if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	_ = os.Chtimes(old, now.Add(-time.Hour), now.Add(-time.Hour))
	_ = os.Chtimes(newer, now, now)
	if err := Cleanup(dir, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("oldest file not removed: %v", err)
	}
	if _, err := os.Stat(newer); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Fatal("active tmp removed")
	}
	if err := Cleanup(dir, 100); err != nil {
		t.Fatal(err)
	}
}

func TestAttentionDecisionsDoNotDependOnProviderDegradation(t *testing.T) {
	cases := []struct {
		name      string
		status    model.MappingStatus
		err       error
		attention bool
	}{
		{"success", model.MappingResolved, nil, false},
		{"noop", model.MappingResolved, nil, false},
		{"partial", model.MappingPartial, nil, true},
		{"conflict", model.MappingConflict, app.ErrConflict, true},
		{"unresolved", "", app.ErrUnresolved, true},
		{"filesystem", "", errors.New("link failed"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, got := outcomeForTest(app.Result{MappingStatus: tc.status}, tc.err)
			if got != tc.attention {
				t.Fatalf("attention=%v want %v", got, tc.attention)
			}
		})
	}
}

func outcomeForTest(result app.Result, err error) (string, bool) {
	if err == nil {
		if result.MappingStatus == model.MappingPartial || result.MappingStatus == model.MappingConflict {
			return string(result.MappingStatus), true
		}
		return "SUCCESS", false
	}
	if errors.Is(err, app.ErrIgnored) {
		return "IGNORED", false
	}
	return "FAILED", true
}
