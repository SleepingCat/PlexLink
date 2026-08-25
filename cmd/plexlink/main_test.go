package main

import (
	"errors"
	"testing"

	"github.com/SleepingCat/PlexLink/internal/app"
	"github.com/SleepingCat/PlexLink/internal/model"
)

func TestProcessLogOutcome(t *testing.T) {
	tests := []struct {
		name      string
		result    app.Result
		err       error
		status    string
		attention bool
	}{
		{"success", app.Result{MappingStatus: model.MappingResolved}, nil, "SUCCESS", false},
		{"provisional success", app.Result{MappingStatus: model.MappingResolvedWithWarnings, AI: app.AIDiagnostics{Error: "optional provider failed"}}, nil, "SUCCESS", false},
		{"partial", app.Result{MappingStatus: model.MappingPartial}, nil, "PARTIAL", true},
		{"ignored", app.Result{}, app.ErrIgnored, "IGNORED", false},
		{"unresolved", app.Result{}, app.ErrUnresolved, "UNRESOLVED", true},
		{"conflict", app.Result{}, app.ErrConflict, "CONFLICT", true},
		{"filesystem", app.Result{}, errors.New("hardlink failed"), "FILESYSTEM_ERROR", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, attention := processLogOutcome(test.result, test.err)
			if status != test.status || attention != test.attention {
				t.Fatalf("status=%q attention=%v, want %q/%v", status, attention, test.status, test.attention)
			}
		})
	}
}

func TestProcessExitCodesRemainStable(t *testing.T) {
	tests := []struct {
		err  error
		code int
	}{{nil, 0}, {app.ErrIgnored, 10}, {app.ErrUnresolved, 20}, {app.ErrAnimeNumbering, 21}, {app.ErrConflict, 30}, {app.ErrTorrent, 41}, {app.ErrMetadata, 42}, {app.ErrAI, 43}, {errors.New("hardlink"), 50}}
	for _, test := range tests {
		if got := processExitCode(test.err); got != test.code {
			t.Errorf("error %v: code=%d want %d", test.err, got, test.code)
		}
	}
}
