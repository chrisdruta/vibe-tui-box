package dockerapi

import (
	"strings"
	"testing"
)

// The classifier must turn the classic builder's stream into step
// transitions, cache verdicts, and raw lines — and drop the builder's
// bookkeeping chatter — even when the daemon splits lines across
// stream payloads.
func TestForwardBuildProgressClassifies(t *testing.T) {
	stream := strings.Join([]string{
		`{"stream":"Step 1/3 : FROM ${VIBE_BASE_IMAGE}\n"}`,
		`{"stream":" ---> abc123\n"}`,
		`{"stream":"Step 2/3 : RUN make install\n"}`,
		`{"stream":" ---> Running in c0ffee\n"}`,
		// One payload carrying two lines, then a line split across two.
		`{"stream":"compiling...\nlinking"}`,
		`{"stream":" objects\n"}`,
		`{"stream":"Removing intermediate container c0ffee\n"}`,
		`{"stream":"Step 3/3 : RUN echo done\n"}`,
		`{"stream":" ---> Using cache\n"}`,
		`{"stream":"Successfully built abc123\n"}`,
		`{"stream":"Successfully tagged t:latest\n"}`,
	}, "")
	var got []Progress
	sink := ProgressSink(func(p Progress) { got = append(got, p) })
	if err := forwardBuildProgress(strings.NewReader(stream), sink); err != nil {
		t.Fatal(err)
	}
	want := []Progress{
		{Stage: StageBuild, Message: "FROM ${VIBE_BASE_IMAGE}", Step: 1, Steps: 3, StepStart: true},
		{Stage: StageBuild, Message: "RUN make install", Step: 2, Steps: 3, StepStart: true},
		{Stage: StageBuild, Message: "compiling...", Step: 2, Steps: 3},
		{Stage: StageBuild, Message: "linking objects", Step: 2, Steps: 3},
		{Stage: StageBuild, Message: "RUN echo done", Step: 3, Steps: 3, StepStart: true},
		{Stage: StageBuild, Step: 3, Steps: 3, Cached: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d: got %+v want %+v", i, got[i], want[i])
		}
	}
}

// A daemon error surfaces both as a Failed event (so a renderer can
// replay its tail) and as the returned error (so the build aborts).
func TestForwardBuildProgressError(t *testing.T) {
	stream := `{"stream":"Step 1/2 : RUN false\n"}` +
		`{"stream":"boom context line\n"}` +
		`{"errorDetail":{"message":"The command '/bin/sh -c false' returned a non-zero code: 1"}}`
	var got []Progress
	err := forwardBuildProgress(strings.NewReader(stream), func(p Progress) { got = append(got, p) })
	if err == nil || !strings.Contains(err.Error(), "non-zero code") {
		t.Fatalf("want daemon error, got %v", err)
	}
	last := got[len(got)-1]
	if !last.Failed || last.Step != 1 || !strings.Contains(last.Message, "non-zero code") {
		t.Fatalf("want Failed event for step 1, got %+v", last)
	}
}
