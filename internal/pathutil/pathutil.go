package pathutil

import (
	"errors"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode"
)

var reserved = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\..*)?$`)

func SanitizeWindowsName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, s)
	s = strings.TrimRightFunc(strings.TrimSpace(s), func(r rune) bool { return r == '.' || unicode.IsSpace(r) })
	if s == "" {
		return "_"
	}
	if reserved.MatchString(s) {
		return "_" + s
	}
	return s
}

// Contains compares paths case-insensitively on Windows and rejects siblings
// with a shared string prefix as well as paths escaping through '..'.
func Contains(root, candidate string) bool {
	r, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return false
	}
	c, err := filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(r, c)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		rel = strings.ToLower(rel)
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func JoinInside(root string, parts ...string) (string, error) {
	p := filepath.Join(append([]string{root}, parts...)...)
	if !Contains(root, p) {
		return "", errors.New("path escapes configured root")
	}
	return p, nil
}
