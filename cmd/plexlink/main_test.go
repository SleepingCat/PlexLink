package main

import (
	"context"
	"errors"
	"testing"
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
