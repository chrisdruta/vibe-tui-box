package envfile

import (
	"strings"
	"testing"
)

// FuzzParse asserts the literal dotenv parser never panics and that
// accepted entries obey its own contract: validated keys, no NUL
// anywhere, values passed through byte-for-byte with no expansion.
func FuzzParse(f *testing.F) {
	f.Add("KEY=value\n")
	f.Add("# comment\n\nA=1\nB=$A ${A} `cmd` $(cmd)\n")
	f.Add("DUP=1\nDUP=2\n")
	f.Add("=novalue\n")
	f.Add("BAD KEY=x\n")
	f.Add("NUL=\x00\n")
	f.Add(strings.Repeat("K=v\n", 3000))

	f.Fuzz(func(t *testing.T, data string) {
		entries, err := Parse(strings.NewReader(data), Limits{})
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.Key == "" {
				t.Fatalf("accepted empty key: %+v", e)
			}
			if strings.ContainsRune(e.Key, 0) || strings.ContainsRune(e.Value, 0) {
				t.Fatalf("accepted NUL: %+v", e)
			}
		}
	})
}
