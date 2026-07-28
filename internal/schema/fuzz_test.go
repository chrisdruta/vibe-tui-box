package schema

import (
	"bytes"
	"testing"
)

// FuzzLoad drives arbitrary bytes through the bounded YAML pipeline:
// whatever the input, Load must return within its limits without
// panicking, and a document that loads must also validate without
// panicking.
func FuzzLoad(f *testing.F) {
	f.Add([]byte("schema: 1\nimage: {base: x, agents: [claude]}\nagent: {cmd: claude}\n"))
	f.Add([]byte("schema: [1, 2]\n"))
	f.Add([]byte("a: &x [*x]\n"))                     // alias cycle: rejected, not expanded
	f.Add([]byte("<<: {a: 1}\n"))                     // merge key
	f.Add([]byte("? [1, 2]\n: v\n"))                  // non-string key
	f.Add([]byte("schema: 1\nschema: 2\n"))           // duplicate key
	f.Add([]byte("\xff\xfe"))                         // invalid UTF-8
	f.Add([]byte("a: \"\x00\"\n"))                    // NUL
	f.Add(bytes.Repeat([]byte("- [\n"), 40))          // deep nesting
	f.Add([]byte("env_file: /etc/passwd\nschema: 1")) // unknown-ish shapes

	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := Load(bytes.NewReader(data), Limits{})
		if err != nil {
			return
		}
		_ = doc.Validate()
	})
}
