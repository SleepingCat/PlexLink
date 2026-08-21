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
