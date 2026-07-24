package domain

import "testing"

// FuzzParsers drives the digest and ID parsers with arbitrary strings:
// no panics, and anything accepted must round-trip through its own
// string form.
func FuzzParsers(f *testing.F) {
	f.Add("sha256:" + "ab12" + "cd34ef567890abcdef1234567890abcdef1234567890abcdef1234567890")
	f.Add("sha256:short")
	f.Add("md5:abcd")
	f.Add("SHA256:ABCDEF")
	f.Add("proj-id-01")
	f.Add("../escape")
	f.Add("")

	f.Fuzz(func(t *testing.T, s string) {
		if d, err := ParseDigest(s); err == nil {
			back, err := ParseDigest(d.String())
			if err != nil || back != d {
				t.Fatalf("digest round-trip broke: %q → %v (%v)", s, back, err)
			}
		}
		if id, err := ParseProjectID(s); err == nil {
			if _, err := ParseProjectID(string(id)); err != nil {
				t.Fatalf("project ID round-trip broke: %q", s)
			}
		}
		if id, err := ParseRequestID(s); err == nil {
			if _, err := ParseRequestID(string(id)); err != nil {
				t.Fatalf("request ID round-trip broke: %q", s)
			}
		}
	})
}
