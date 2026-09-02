package solver

import "testing"

// Splitting the challenge FQDN into a KAS zone plus a relative record name is
// the single most error-prone part of this webhook, so it is pinned here.
func TestRelativeName(t *testing.T) {
	cases := []struct {
		fqdn, zone, want string
		wantErr          bool
	}{
		{"_acme-challenge.lan.example.com.", "example.com.", "_acme-challenge.lan", false},
		{"_acme-challenge.example.com.", "example.com.", "_acme-challenge", false},
		{"_acme-challenge.a.b.example.com.", "example.com.", "_acme-challenge.a.b", false},
		// Trailing dots are optional on both sides.
		{"_acme-challenge.lan.example.com", "example.com", "_acme-challenge.lan", false},
		// Case must not matter: DNS is case-insensitive and cert-manager does
		// not normalise for us.
		{"_acme-challenge.LAN.Example.COM.", "example.com.", "_acme-challenge.LAN", false},
		// A name outside the zone must fail loudly rather than write into the
		// wrong zone.
		{"_acme-challenge.example.org.", "example.com.", "", true},
		{"", "example.com.", "", true},
	}
	for _, c := range cases {
		got, err := relativeName(c.fqdn, c.zone)
		if c.wantErr {
			if err == nil {
				t.Errorf("relativeName(%q, %q) = %q, want error", c.fqdn, c.zone, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("relativeName(%q, %q) unexpected error: %v", c.fqdn, c.zone, err)
			continue
		}
		if got != c.want {
			t.Errorf("relativeName(%q, %q) = %q, want %q", c.fqdn, c.zone, got, c.want)
		}
	}
}

func TestNormalizeName(t *testing.T) {
	cases := []struct{ record, zone, want string }{
		{"_acme-challenge.lan", "example.com.", "_acme-challenge.lan"},
		{"_acme-challenge.lan.example.com.", "example.com.", "_acme-challenge.lan"},
		{"example.com.", "example.com.", ""},
	}
	for _, c := range cases {
		if got := normalizeName(c.record, c.zone); got != c.want {
			t.Errorf("normalizeName(%q, %q) = %q, want %q", c.record, c.zone, got, c.want)
		}
	}
}

func TestUnquote(t *testing.T) {
	for in, want := range map[string]string{
		`"abc"`: "abc",
		"abc":   "abc",
		`"`:     `"`,
		"":      "",
	} {
		if got := unquote(in); got != want {
			t.Errorf("unquote(%q) = %q, want %q", in, got, want)
		}
	}
}
