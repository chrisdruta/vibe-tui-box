package payload

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPopupScript locks the popup helper's contract: display-popup is
// exec'd with the explicit client and a shell-command whose engine
// path and verb words are single-quoted (a path with spaces or an
// embedded quote cannot break out).
func TestPopupScript(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "..", "payload", "host", "scripts", "popup.sh"))
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	rec := filepath.Join(t.TempDir(), "argv")
	fake := "#!/bin/sh\nprintf '%s\\n' \"$@\" >" + rec + "\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"bash", "printf"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("test needs %s", tool)
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil && !os.IsExist(err) {
			t.Fatal(err)
		}
	}

	exe := "/store/path with space/vi'be"
	cmd := exec.Command("bash", script, "client-7", exe, "request", "list")
	cmd.Env = []string{"PATH=" + bin}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("popup.sh: %v\n%s", err, out)
	}

	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	join := strings.Join(argv, " ")
	if argv[0] != "display-popup" {
		t.Fatalf("tmux verb %q, argv %q", argv[0], argv)
	}
	if !strings.Contains(join, "-c client-7") {
		t.Fatalf("client missing: %q", argv)
	}
	shell := argv[len(argv)-1]
	wantExe := `'/store/path with space/vi'\''be'`
	if !strings.Contains(shell, wantExe+" 'request' 'list';") {
		t.Fatalf("shell-command quoting wrong:\n%s\nwant fragment:\n%s", shell, wantExe)
	}
	if !strings.Contains(shell, "[enter to close]") {
		t.Fatalf("chrome missing: %s", shell)
	}
}
