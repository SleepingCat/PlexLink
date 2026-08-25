package linker

import (
	"bytes"
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

func WriteSidecar(targetRoot, target string, content []byte, dryRun bool) (Action, error) {
	if !pathutil.Contains(targetRoot, target) {
		return "", fmt.Errorf("sidecar target outside configured root: %s", target)
	}
	action, exists, err := inspectSidecar(target, content)
	if err != nil || exists {
		return action, err
	}
	if dryRun {
		return Planned, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("create sidecar directory: %w", err)
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if os.IsExist(err) {
		action, exists, inspectErr := inspectSidecar(target, content)
		if inspectErr != nil || exists {
			return action, inspectErr
		}
		return "", fmt.Errorf("create sidecar: target changed concurrently")
	}
	if err != nil {
		return "", fmt.Errorf("create sidecar: %w", err)
	}
	created := true
	defer func() {
		_ = f.Close()
		if created {
			_ = os.Remove(target)
		}
	}()
	if _, err := f.Write(content); err != nil {
		return "", fmt.Errorf("write sidecar: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close sidecar: %w", err)
	}
	created = false
	return Created, nil
}

func inspectSidecar(target string, content []byte) (Action, bool, error) {
	info, err := os.Stat(target)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("stat sidecar: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Conflict, true, nil
	}
	existing, err := os.ReadFile(target)
	if err != nil {
		return "", true, fmt.Errorf("read sidecar: %w", err)
	}
	if bytes.Equal(existing, content) {
		return Noop, true, nil
	}
	return Conflict, true, nil
}
