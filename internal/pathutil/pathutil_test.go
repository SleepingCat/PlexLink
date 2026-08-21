package pathutil

import "testing"

func TestSanitizeWindowsName(t *testing.T) {
	cases := map[string]string{"A:B?": "A_B_", "CON": "_CON", "name. ": "name", "": "_", "Мышь": "Мышь"}
	for in, want := range cases {
		if got := SanitizeWindowsName(in); got != want {
			t.Errorf("SanitizeWindowsName(%q)=%q, want %q", in, got, want)
		}
	}
}
func TestContains(t *testing.T) {
	if !Contains("/media/tv", "/media/tv/show/a.mkv") {
		t.Fatal("descendant rejected")
	}
	if Contains("/media/tv", "/media/tv-old/a.mkv") {
		t.Fatal("prefix sibling accepted")
	}
	if Contains("/media/tv", "/media/tv/../films/a.mkv") {
		t.Fatal("escape accepted")
	}
}
