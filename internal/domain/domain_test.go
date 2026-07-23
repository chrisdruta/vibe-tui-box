package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestParseDigest(t *testing.T) {
	valid := "sha256:" + strings.Repeat("ab", 32)
	cases := []struct {
		name  string
		input string
		ok    bool
	}{
		{"valid", valid, true},
		{"empty", "", false},
		{"no algorithm", strings.Repeat("ab", 32), false},
		{"wrong algorithm", "sha512:" + strings.Repeat("ab", 32), false},
		{"short hex", "sha256:abcd", false},
		{"uppercase hex", "sha256:" + strings.Repeat("AB", 32), false},
		{"non-hex", "sha256:" + strings.Repeat("zz", 32), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := ParseDigest(tc.input)
			if tc.ok != (err == nil) {
				t.Fatalf("ParseDigest(%q) error = %v, want ok=%v", tc.input, err, tc.ok)
			}
			if err != nil {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("error %v is not ErrInvalid", err)
				}
				return
			}
			if d.String() != tc.input {
				t.Fatalf("round-trip %q != %q", d.String(), tc.input)
			}
		})
	}
}

func TestSHA256RoundTrip(t *testing.T) {
	d := SHA256([]byte("hello"))
	parsed, err := ParseDigest(d.String())
	if err != nil {
		t.Fatal(err)
	}
	if parsed != d {
		t.Fatalf("parsed %v != computed %v", parsed, d)
	}
	if d.IsZero() {
		t.Fatal("computed digest is zero")
	}
	var zero Digest
	if !zero.IsZero() || zero.String() != "" {
		t.Fatal("zero digest misbehaves")
	}
}

func TestDigestTextMarshalling(t *testing.T) {
	d := SHA256([]byte("x"))
	text, err := d.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	var back Digest
	if err := back.UnmarshalText(text); err != nil {
		t.Fatal(err)
	}
	if back != d {
		t.Fatalf("round-trip mismatch")
	}
	var zero Digest
	if err := zero.UnmarshalText(nil); err != nil || !zero.IsZero() {
		t.Fatalf("empty text should decode to zero digest, got %v, %v", zero, err)
	}
}

func TestProjectIDs(t *testing.T) {
	id, err := NewProjectID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseProjectID(string(id)); err != nil {
		t.Fatalf("generated id %q does not parse: %v", id, err)
	}
	other, err := NewProjectID()
	if err != nil {
		t.Fatal(err)
	}
	if id == other {
		t.Fatal("two generated IDs collide")
	}
	for _, bad := range []string{"", "short", strings.Repeat("A", 26), strings.Repeat("a", 27), strings.Repeat("1", 26)} {
		if _, err := ParseProjectID(bad); err == nil {
			t.Fatalf("ParseProjectID(%q) unexpectedly succeeded", bad)
		}
	}
}

func TestParseRequestID(t *testing.T) {
	for _, good := range []string{"a", "req-42", "0abc"} {
		if _, err := ParseRequestID(good); err != nil {
			t.Fatalf("ParseRequestID(%q): %v", good, err)
		}
	}
	for _, bad := range []string{"", "-lead", "UPPER", "has space", strings.Repeat("a", 65), "dot.dot"} {
		if _, err := ParseRequestID(bad); err == nil {
			t.Fatalf("ParseRequestID(%q) unexpectedly succeeded", bad)
		}
	}
}

func TestPlatform(t *testing.T) {
	p, err := ParsePlatform("linux-amd64")
	if err != nil || p.String() != "linux-amd64" {
		t.Fatalf("linux-amd64: %v %v", p, err)
	}
	if _, err := ParsePlatform("windows-amd64"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("windows should be unsupported, got %v", err)
	}
	if _, err := ParsePlatform("linux"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed platform should be invalid, got %v", err)
	}
}

func TestErrorWrapping(t *testing.T) {
	fe := FieldError{Path: "image.base", Line: 3, Column: 5, Message: "required"}
	if !errors.Is(fe, ErrInvalid) {
		t.Fatal("FieldError should classify as ErrInvalid")
	}
	if want := "image.base (line 3, column 5): required"; fe.Error() != want {
		t.Fatalf("FieldError text %q, want %q", fe.Error(), want)
	}
	op := &OpError{Op: "up", Project: "p", Err: ErrConflict}
	if !errors.Is(op, ErrConflict) {
		t.Fatal("OpError should unwrap to its cause")
	}
}
