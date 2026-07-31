package payload

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEgressSampleScript locks the sampler's output contract against a
// fixture /proc tree: the version line, the /proc hex byte order (the
// classic little-endian-per-word gotcha, including v4-mapped v6), the
// tcp ESTABLISHED / udp connected filters, fd-readlink attribution,
// and the in-script comm allowlist (defense in depth — the engine
// re-sanitizes).
func TestEgressSampleScript(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("test needs bash")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "payload", "container", "egress-sample.sh"))
	if err != nil {
		t.Fatal(err)
	}

	proc := t.TempDir()
	writeFixture := func(rel, content string) {
		t.Helper()
		p := filepath.Join(proc, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFixture("1234/comm", "node\n")
	writeFixture("5678/comm", "evil\tcomm\x1b[31m!\n")
	for pid, sock := range map[string]string{"1234/fd/5": "socket:[999]", "5678/fd/3": "socket:[888]"} {
		if err := os.MkdirAll(filepath.Join(proc, filepath.Dir(pid)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(sock, filepath.Join(proc, pid)); err != nil {
			t.Fatal(err)
		}
	}
	writeFixture("net/tcp", ""+
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"+
		"   0: 0100007F:1F90 0200007F:01BB 01 00000000:00000000 00:00000000 00000000  1000        0 999 1 0000000000000000 20 4 30 10 -1\n"+
		"   1: 0100007F:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 777 1 0000000000000000 20 4 30 10 -1\n")
	writeFixture("net/tcp6", ""+
		"  sl  local_address                         rem_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"+
		"   0: 00000000000000000000000001000000:0050 00000000000000000000000001000000:1F90 01 00000000:00000000 00:00000000 00000000  1000        0 888 1\n"+
		"   1: 0000000000000000FFFF00000100007F:0050 0000000000000000FFFF0000664B5FA8:01BB 01 00000000:00000000 00:00000000 00000000  1000        0 555 1\n")
	writeFixture("net/udp", ""+
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops\n"+
		"   0: 0100007F:D431 3500A80A:0035 07 00000000:00000000 00:00000000 00000000  1000        0 444 2 0000000000000000 0\n"+
		"   1: 00000000:0044 00000000:0000 07 00000000:00000000 00:00000000 00000000  1000        0 333 2 0000000000000000 0\n")

	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(), "VIBE_EGRESS_PROC="+proc)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("egress-sample.sh: %v\n%s", err, out)
	}

	want := []string{
		"egress-sample\t1",
		"tcp\t127.0.0.1:8080\t127.0.0.2:443\t1234\tnode",
		"tcp6\t[0000:0000:0000:0000:0000:0000:0000:0001]:80\t[0000:0000:0000:0000:0000:0000:0000:0001]:8080\t5678\tevilcomm31m",
		"tcp6\t127.0.0.1:80\t168.95.75.102:443\t-\t-", // v4-mapped renders as IPv4
		"udp\t127.0.0.1:54321\t10.168.0.53:53\t-\t-",
	}
	got := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("row count = %d, want %d\n%s", len(got), len(want), out)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %q, want %q", i, got[i], want[i])
		}
	}
}
