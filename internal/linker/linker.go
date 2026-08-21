package linker

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/SleepingCat/PlexLink/internal/pathutil"
)

type Action string

const (
	Created  Action = "CREATED"
	Noop     Action = "NOOP"
	Conflict Action = "CONFLICT"
	Planned  Action = "PLANNED"
)

func Link(sourceRoot, targetRoot, source, target string, dryRun bool) (Action, error) {
	if !pathutil.Contains(sourceRoot, source) {
		return "", fmt.Errorf("source outside configured root: %s", source)
	}
	if !pathutil.Contains(targetRoot, target) {
		return "", fmt.Errorf("target outside configured root: %s", target)
	}
	si, err := os.Stat(source)
	if err != nil {
		return "", fmt.Errorf("stat source: %w", err)
	}
	if !si.Mode().IsRegular() {
		return "", fmt.Errorf("source is not a regular file: %s", source)
	}
	if ti, err := os.Stat(target); err == nil {
		if os.SameFile(si, ti) {
			return Noop, nil
		}
		return Conflict, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat target: %w", err)
	}
	if dryRun {
		return Planned, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("create target directory: %w", err)
	}
	if err := os.Link(source, target); err != nil {
		return "", fmt.Errorf("create hardlink: %w", err)
	}
	return Created, nil
}
