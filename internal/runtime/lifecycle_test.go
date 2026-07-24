package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	dockerfake "github.com/chrisdruta/vibe-tui-box/internal/dockerapi/fake"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
)

// withArtifact publishes a minimal engine artifact into the fixture's
// store and compiles the plan against it, so the dev container mounts
// the payload and the lifecycle hooks apply.
func withArtifact(t *testing.T) func(*store.Store, *model.CompileInput) {
	return func(st *store.Store, in *model.CompileInput) {
		t.Helper()
		staging, err := st.NewStaging("artifact")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(staging, "payload"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(staging, "payload", "marker"), []byte("p"), 0o600); err != nil {
			t.Fatal(err)
		}
		digest, _, err := store.DigestTree(staging)
		if err != nil {
			t.Fatal(err)
		}
		obj, err := st.Publish(context.Background(), store.ArtifactObject, staging, digest)
		if err != nil {
			t.Fatal(err)
		}
		in.Artifact = store.Artifact{
			Record: store.ArtifactRecord{Digest: digest, Version: "test", PayloadDigest: digest},
			Path:   obj.Path,
		}
	}
}

func lifecycleArgv(mode string) []string {
	return []string{"bash", model.PayloadLifecycle, mode}
}

var probeArgv = []string{"test", "-r", model.PayloadLifecycle}

// execArgvs lists the argv of every recorded Exec call.
func execArgvs(docker *dockerfake.Client) [][]string {
	var out [][]string
	for _, call := range docker.CallsTo("Exec") {
		out = append(out, call.Request.(dockerapi.ExecRequest).Argv)
	}
	return out
}

func assertArgvs(t *testing.T, got [][]string, want [][]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("exec calls:\ngot  %v\nwant %v", got, want)
	}
	for i := range got {
		if !equalSlices(got[i], want[i]) {
			t.Fatalf("exec call %d:\ngot  %v\nwant %v", i, got[i], want[i])
		}
	}
}

func TestLifecycleHooksRunOnCreateAndStart(t *testing.T) {
	f := newFixture(t, testManifest, withArtifact(t))
	ctx := context.Background()

	// Fresh create: probe, post-create, post-start.
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	assertArgvs(t, execArgvs(f.docker), [][]string{
		probeArgv, lifecycleArgv("post-create"), lifecycleArgv("post-start"),
	})

	// The hook exec runs as the container user in the workspace with the
	// project identity and nothing else.
	execs := f.docker.CallsTo("Exec")
	hook := execs[1].Request.(dockerapi.ExecRequest)
	if hook.User != "vscode" || hook.WorkDir != model.WorkspaceTarget {
		t.Fatalf("hook exec context: %+v", hook)
	}
	wantEnv := []string{"VIBE_PROJECT=" + string(testProjectID), "VIBE_PROJECT_NAME=project"}
	if !equalSlices(hook.Env, wantEnv) {
		t.Fatalf("hook env %v, want %v", hook.Env, wantEnv)
	}

	// Idempotent up (running, unchanged): post-create re-probes (marker
	// makes it a container-side no-op) but post-start must not run.
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	assertArgvs(t, execArgvs(f.docker), [][]string{
		probeArgv, lifecycleArgv("post-create"), lifecycleArgv("post-start"),
		probeArgv, lifecycleArgv("post-create"),
	})

	// A stopped dev container restarts: post-start runs again.
	devName := dockerapi.ContainerName(model.DevContainerName(testProjectID))
	f.docker.Containers[devName].Running = false
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	argvs := execArgvs(f.docker)
	last := argvs[len(argvs)-1]
	if !equalSlices(last, lifecycleArgv("post-start")) {
		t.Fatalf("restart should re-run post-start, last exec %v", last)
	}
}

func TestLifecycleHookFailureFailsUp(t *testing.T) {
	f := newFixture(t, testManifest, withArtifact(t))
	ctx := context.Background()
	f.docker.ExecResults[dockerfake.ExecKey(lifecycleArgv("post-create"))] = dockerapi.ExecResult{ExitCode: 7}

	_, err := f.svc.Up(ctx, f.cand, UpOptions{})
	if err == nil || !strings.Contains(err.Error(), "post-create hook failed with exit code 7") {
		t.Fatalf("hook failure not surfaced: %v", err)
	}
}

func TestLifecycleSkipsOldPayloads(t *testing.T) {
	f := newFixture(t, testManifest, withArtifact(t))
	ctx := context.Background()
	// A pinned payload predating lifecycle.sh fails the probe.
	f.docker.ExecResults[dockerfake.ExecKey(probeArgv)] = dockerapi.ExecResult{ExitCode: 1}

	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	assertArgvs(t, execArgvs(f.docker), [][]string{probeArgv})
}

func TestLifecycleSkipsWithoutPayload(t *testing.T) {
	f := newFixture(t, testManifest) // no artifact → no payload mount
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	if execs := f.docker.CallsTo("Exec"); len(execs) != 0 {
		t.Fatalf("payload-less plan must not exec hooks: %d calls", len(execs))
	}
}
