package broker

import (
	"strings"
	"testing"
)

// FuzzParseRequest asserts the request parser never panics and that
// accepted requests carry a valid ID, the supported kind and format,
// and bounded text.
func FuzzParseRequest(f *testing.F) {
	f.Add([]byte(`{"format":1,"id":"req-1","kind":"rebuild","reason":"r","summary":"s"}`))
	f.Add([]byte(`{"format":2,"id":"req-1","kind":"rebuild"}`))
	f.Add([]byte(`{"format":1,"id":"../up","kind":"rebuild"}`))
	f.Add([]byte(`{"format":1,"id":"x","kind":"rm -rf"}`))
	f.Add([]byte(`{"format":1,"id":"x","kind":"rebuild","extra":true}`))
	f.Add([]byte(`{`))
	f.Add([]byte(`{"format":1,"id":"x","kind":"rebuild","reason":"` + strings.Repeat("a", 5000) + `"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		req, err := ParseRequest(data)
		if err != nil {
			return
		}
		if req.Format != 1 || req.Kind != RebuildKind || req.ID == "" {
			t.Fatalf("accepted malformed request: %+v", req)
		}
		if len([]rune(req.Reason)) > 4096 || len([]rune(req.Summary)) > 4096 {
			t.Fatalf("accepted oversized text: %d/%d runes", len([]rune(req.Reason)), len([]rune(req.Summary)))
		}
	})
}
