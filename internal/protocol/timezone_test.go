package protocol

import "testing"

func TestNormalizeTimeZone(t *testing.T) {
	for _, in := range []string{"", "  ", "auto", "AUTO", " Auto "} {
		if got, err := NormalizeTimeZone(in); err != nil || got != TimeZoneAuto {
			t.Errorf("%q: got %q, %v; want auto", in, got, err)
		}
	}
	if got, err := NormalizeTimeZone(" Europe/Paris "); err != nil || got != "Europe/Paris" {
		t.Errorf("Europe/Paris: got %q, %v", got, err)
	}
	if got, err := NormalizeTimeZone("America/New_York"); err != nil || got != "America/New_York" {
		t.Errorf("America/New_York: got %q, %v", got, err)
	}
	for _, bad := range []string{"Mars/Olympus", "not a zone", "../etc/passwd"} {
		if got, err := NormalizeTimeZone(bad); err == nil {
			t.Errorf("%q was accepted as %q", bad, got)
		}
	}
}
