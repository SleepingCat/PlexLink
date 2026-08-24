package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/SleepingCat/PlexLink/internal/app"
	"github.com/SleepingCat/PlexLink/internal/linker"
	"github.com/SleepingCat/PlexLink/internal/model"
)

type fakeIdleShutdownClient struct {
	requested bool
	err       error
	calls     int
}

func (f *fakeIdleShutdownClient) ShutdownIfIdle(context.Context) (bool, error) {
	f.calls++
	return f.requested, f.err
}

func TestShutdownAfterProcess(t *testing.T) {
	for _, test := range []struct {
		name      string
		client    fakeIdleShutdownClient
		requested bool
		skipped   string
		wantError bool
	}{
		{name: "shutdown requested", client: fakeIdleShutdownClient{requested: true}, requested: true},
		{name: "incomplete downloads", client: fakeIdleShutdownClient{}, skipped: "incomplete downloads remain"},
		{name: "API failure", client: fakeIdleShutdownClient{err: errors.New("offline")}, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			diagnostic, err := shutdownAfterProcess(context.Background(), &test.client, 0)
			if (err != nil) != test.wantError || diagnostic == nil || !diagnostic.Enabled || !diagnostic.Attempted || diagnostic.Requested != test.requested || diagnostic.SkippedReason != test.skipped {
				t.Fatalf("diagnostic=%+v err=%v", diagnostic, err)
			}
			if test.wantError && diagnostic.Error == "" {
				t.Fatal("shutdown error was not recorded")
			}
			if test.client.calls != 1 {
				t.Fatalf("calls=%d", test.client.calls)
			}
		})
	}
}

func TestShutdownGateExcludesReadOnlyCommands(t *testing.T) {
	for _, test := range []struct {
		enabled bool
		command string
		dryRun  bool
		want    bool
	}{
		{enabled: true, command: "process", want: true},
		{enabled: false, command: "process"},
		{enabled: true, command: "process", dryRun: true},
		{enabled: true, command: "inspect"},
		{enabled: true, command: "resolve"},
		{enabled: true, command: "doctor"},
	} {
		if got := shouldShutdownAfterProcess(test.enabled, test.command, test.dryRun); got != test.want {
			t.Errorf("enabled=%t command=%s dryRun=%t: got %t want %t", test.enabled, test.command, test.dryRun, got, test.want)
		}
	}
}

func TestShutdownAfterProcessHonorsCanceledGracePeriod(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &fakeIdleShutdownClient{requested: true}
	diagnostic, err := shutdownAfterProcess(ctx, client, 1)
	if err == nil || diagnostic == nil || diagnostic.Attempted || diagnostic.Error == "" || client.calls != 0 {
		t.Fatalf("diagnostic=%+v err=%v calls=%d", diagnostic, err, client.calls)
	}
}

func TestProcessSummaryShowsRecognitionAndCreatedStructure(t *testing.T) {
	result := app.Result{
		Kind:       model.KindTV,
		Match:      model.Match{ID: 1104, Name: "Mad Men", Year: 2007},
		Candidates: []model.Match{{ID: 999, Name: "debug-only"}},
		Plan:       []model.LinkPlan{{Source: `K:\video\serials\source.mkv`, Target: `K:\plex\serials\Mad Men (2007)\Season 01\Mad Men (2007) - S01E01.mkv`}},
		Actions:    []linker.Action{linker.Created},
		PlexMatch:  &app.PlexMatchDiagnostics{Target: `K:\plex\serials\Mad Men (2007)\.plexmatch`, Content: "Title: Mad Men\nYear: 2007\nTmdbId: 1104\n", Action: linker.Created},
		QBittorrentShutdown: &app.QBittorrentShutdownDiagnostics{
			Enabled: true, Attempted: true, SkippedReason: "incomplete downloads remain",
		},
	}
	var output bytes.Buffer
	writeProcessResult(&output, result, nil, false, false)
	got := output.String()
	for _, want := range []string{
		"Recognition: SUCCESS",
		"Media: Mad Men (2007) [tv, TMDB 1104]",
		"Processing: SUCCESS",
		`[CREATED] K:\plex\serials\Mad Men (2007)\.plexmatch`,
		`[CREATED] K:\plex\serials\Mad Men (2007)\Season 01\Mad Men (2007) - S01E01.mkv`,
		"qBittorrent: kept running (incomplete downloads remain)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary does not contain %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{`"candidates"`, `"ai_used"`, `K:\video\serials\source.mkv`, "debug-only"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("summary leaked diagnostic %q:\n%s", unwanted, got)
		}
	}
}

func TestDebugOutputKeepsFullDiagnosticJSON(t *testing.T) {
	result := app.Result{Kind: model.KindMovie, Match: model.Match{ID: 5, Name: "Film", Year: 2024}, Candidates: []model.Match{{ID: 5, Name: "Film", Score: 600}}}
	var output bytes.Buffer
	writeProcessResult(&output, result, nil, false, true)
	got := output.String()
	for _, want := range []string{`"candidates"`, `"ai_used"`, `"Score": 600`} {
		if !strings.Contains(got, want) {
			t.Fatalf("debug output does not contain %q:\n%s", want, got)
		}
	}
}

func TestSummaryReportsUnresolvedWithoutDiagnosticJSON(t *testing.T) {
	var output bytes.Buffer
	err := fmt.Errorf("%w: confidence too low", app.ErrUnresolved)
	writeProcessResult(&output, app.Result{}, err, false, false)
	got := output.String()
	if !strings.Contains(got, "Recognition: UNRESOLVED") || !strings.Contains(got, "Reason: unresolved: confidence too low") || strings.Contains(got, "{") {
		t.Fatalf("unexpected unresolved summary:\n%s", got)
	}
}

func TestSummaryDistinguishesOperationalErrorFromUnresolved(t *testing.T) {
	var output bytes.Buffer
	err := fmt.Errorf("%w: API unavailable", app.ErrTorrent)
	writeProcessResult(&output, app.Result{}, err, false, false)
	if got := output.String(); !strings.Contains(got, "Recognition: ERROR") || strings.Contains(got, "Recognition: UNRESOLVED") {
		t.Fatalf("unexpected operational error summary:\n%s", got)
	}
}

func TestDryRunSummaryUsesPlannedActions(t *testing.T) {
	result := app.Result{Kind: model.KindMovie, Match: model.Match{ID: 5, Name: "Film", Year: 2024}, Plan: []model.LinkPlan{{Target: `K:\plex\films\Film (2024) {tmdb-5}\Film (2024) {tmdb-5}.mkv`}}}
	var output bytes.Buffer
	writeProcessResult(&output, result, nil, true, false)
	got := output.String()
	if !strings.Contains(got, "Planned structure:") || !strings.Contains(got, "[PLANNED]") || strings.Contains(got, "Created structure:") {
		t.Fatalf("unexpected dry-run summary:\n%s", got)
	}
}

func TestDetailedOutputIsExplicitExceptForInspect(t *testing.T) {
	if detailedOutput("process", false) || !detailedOutput("process", true) || !detailedOutput("inspect", false) {
		t.Fatal("unexpected detailed output gate")
	}
}
