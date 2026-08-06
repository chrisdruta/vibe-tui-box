package envfile

import (
	"errors"
	"strings"
	"testing"

	"vibe/internal/domain"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []Entry
		fail  bool
	}{
		{
			name:  "basic",
			input: "FOO=bar\nBAZ=qux\n",
			want:  []Entry{{"FOO", "bar"}, {"BAZ", "qux"}},
		},
		{
			name:  "comments and blanks",
			input: "# comment\n\n  # indented comment\nKEY=value\n",
			want:  []Entry{{"KEY", "value"}},
		},
		{
			name:  "literal value with hash and quotes",
			input: `KEY="quoted # not a comment" $HOME`,
			want:  []Entry{{"KEY", `"quoted # not a comment" $HOME`}},
		},
		{
			name:  "crlf",
			input: "A=1\r\nB=2\r\n",
			want:  []Entry{{"A", "1"}, {"B", "2"}},
		},
		{
			name:  "empty value",
			input: "EMPTY=\n",
			want:  []Entry{{"EMPTY", ""}},
		},
		{
			name:  "value with equals",
			input: "URL=http://x/?a=b\n",
			want:  []Entry{{"URL", "http://x/?a=b"}},
		},
		{name: "missing equals", input: "NOEQUALS\n", fail: true},
		{name: "empty key", input: "=v\n", fail: true},
		{name: "bad key char", input: "MY-KEY=v\n", fail: true},
		{name: "digit-leading key", input: "1KEY=v\n", fail: true},
		{name: "duplicate key", input: "A=1\nA=2\n", fail: true},
		{name: "nul byte", input: "A=a\x00b\n", fail: true},
		{name: "invalid utf8", input: "A=\xff\xfe\n", fail: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(strings.NewReader(tc.input), Limits{})
			if tc.fail {
				if err == nil {
					t.Fatalf("expected failure, got %v", got)
				}
				if !errors.Is(err, domain.ErrInvalid) {
					t.Fatalf("error %v is not ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("entry %d: got %v, want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseLimits(t *testing.T) {
	if _, err := Parse(strings.NewReader("A=1\nB=2\n"), Limits{MaxEntries: 1}); err == nil {
		t.Fatal("entry limit not enforced")
	}
	if _, err := Parse(strings.NewReader("A="+strings.Repeat("v", 100)+"\n"), Limits{MaxValueBytes: 10}); err == nil {
		t.Fatal("value limit not enforced")
	}
	if _, err := Parse(strings.NewReader(strings.Repeat("K", 300)+"=v\n"), Limits{}); err == nil {
		t.Fatal("key limit not enforced")
	}
	if _, err := Parse(strings.NewReader("A=1\n"), Limits{MaxTotalBytes: 2}); err == nil {
		t.Fatal("total size limit not enforced")
	}
}
