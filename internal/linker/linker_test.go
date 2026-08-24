package linker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinkIdempotencyAndConflict(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "source")
	dstRoot := filepath.Join(root, "target")
	_ = os.MkdirAll(srcRoot, 0755)
	src := filepath.Join(srcRoot, "a.mkv")
	_ = os.WriteFile(src, []byte("a"), 0644)
	dst := filepath.Join(dstRoot, "show", "a.mkv")
	a, err := Link(srcRoot, dstRoot, src, dst, false)
	if err != nil || a != Created {
		t.Fatalf("first link: %s %v", a, err)
	}
	a, err = Link(srcRoot, dstRoot, src, dst, false)
	if err != nil || a != Noop {
		t.Fatalf("repeat: %s %v", a, err)
	}
	si, _ := os.Stat(src)
	di, _ := os.Stat(dst)
	if !os.SameFile(si, di) {
		t.Fatal("not a hardlink")
	}
	other := filepath.Join(srcRoot, "b.mkv")
	_ = os.WriteFile(other, []byte("b"), 0644)
	a, err = Link(srcRoot, dstRoot, other, dst, false)
	if err != nil || a != Conflict {
		t.Fatalf("conflict: %s %v", a, err)
	}
}
func TestDryRunDoesNotCreateDirectory(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "source")
	dstRoot := filepath.Join(root, "target")
	_ = os.MkdirAll(srcRoot, 0755)
	src := filepath.Join(srcRoot, "a.mkv")
	_ = os.WriteFile(src, []byte("a"), 0644)
	dst := filepath.Join(dstRoot, "x", "a.mkv")
	a, err := Link(srcRoot, dstRoot, src, dst, true)
	if err != nil || a != Planned {
		t.Fatal(a, err)
	}
	if _, err := os.Stat(dstRoot); !os.IsNotExist(err) {
		t.Fatal("dry-run created target root")
	}
}

func TestWriteSidecarIsDryRunSafeIdempotentAndConflictAware(t *testing.T) {
	root := t.TempDir()
	targetRoot := filepath.Join(root, "target")
	target := filepath.Join(targetRoot, "Show (2020)", ".plexmatch")
	content := []byte("Title: Show\nYear: 2020\nTmdbId: 7\n")
	action, err := WriteSidecar(targetRoot, target, content, true)
	if err != nil || action != Planned {
		t.Fatalf("dry run: action=%s err=%v", action, err)
	}
	if _, err := os.Stat(targetRoot); !os.IsNotExist(err) {
		t.Fatalf("dry run created target root: %v", err)
	}
	action, err = WriteSidecar(targetRoot, target, content, false)
	if err != nil || action != Created {
		t.Fatalf("create: action=%s err=%v", action, err)
	}
	action, err = WriteSidecar(targetRoot, target, content, false)
	if err != nil || action != Noop {
		t.Fatalf("repeat: action=%s err=%v", action, err)
	}
	action, err = WriteSidecar(targetRoot, target, []byte("TmdbId: 99\n"), false)
	if err != nil || action != Conflict {
		t.Fatalf("conflict: action=%s err=%v", action, err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != string(content) {
		t.Fatalf("existing sidecar changed: content=%q err=%v", got, err)
	}
}
