package traffic

import "testing"

func TestParseActive(t *testing.T) {
	raw := `=== OSH traffic switch status ===
active (by :80): blue  (container: osh-nginx)
state file:
blue`
	if got := ParseActive(raw); got != "blue" {
		t.Fatalf("ParseActive(blue) = %q", got)
	}

	green := "active (by :80): green  (container: osh-g-nginx)"
	if got := ParseActive(green); got != "green" {
		t.Fatalf("ParseActive(green) = %q", got)
	}
}
