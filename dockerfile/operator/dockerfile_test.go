package operator

// These tests check that the operator image build uses only pinned and
// verified external inputs. They read the Dockerfile as text. They do not
// build an image.

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// instruction is one Dockerfile instruction after line continuations are joined.
type instruction struct {
	line  int    // first source line of the instruction
	cmd   string // upper case instruction name, for example RUN
	args  string // text after the instruction name
	stage int    // index of the build stage, -1 before the first FROM
}

// parseDockerfile splits src into instructions. It joins lines that end
// with a backslash and skips comments and empty lines.
func parseDockerfile(src string) []instruction {
	var result []instruction
	var current strings.Builder
	start := 0
	stage := -1
	for i, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if current.Len() == 0 && (line == "" || strings.HasPrefix(line, "#")) {
			continue
		}
		if current.Len() == 0 {
			start = i + 1
		}
		continued := strings.HasSuffix(line, "\\")
		current.WriteString(strings.TrimSuffix(line, "\\"))
		current.WriteString(" ")
		if continued {
			continue
		}
		text := strings.TrimSpace(current.String())
		current.Reset()
		cmd, args, _ := strings.Cut(text, " ")
		cmd = strings.ToUpper(cmd)
		if cmd == "FROM" {
			stage++
		}
		result = append(result, instruction{line: start, cmd: cmd, args: strings.TrimSpace(args), stage: stage})
	}
	return result
}

// fromParts returns the image reference and the stage name of a FROM instruction.
func fromParts(args string) (image, name string) {
	var fields []string
	for _, f := range strings.Fields(args) {
		if !strings.HasPrefix(f, "--") {
			fields = append(fields, f)
		}
	}
	if len(fields) == 0 {
		return "", ""
	}
	if len(fields) >= 3 && strings.EqualFold(fields[1], "AS") {
		name = fields[2]
	}
	return fields[0], name
}

var supportedArchitectures = []string{"amd64", "arm64"}

var digestPattern = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)

// checkImagesPinnedByDigest reports each FROM that uses an external image
// without a sha256 digest. A FROM that names an earlier stage for every
// supported architecture is not external.
func checkImagesPinnedByDigest(ins []instruction) []string {
	var violations []string
	stages := map[string]bool{}
	for _, in := range ins {
		if in.cmd != "FROM" {
			continue
		}
		image, name := fromParts(in.args)
		internal := true
		for _, arch := range supportedArchitectures {
			if !stages[strings.ReplaceAll(image, "${TARGETARCH}", arch)] {
				internal = false
			}
		}
		if !internal && !digestPattern.MatchString(image) {
			violations = append(violations, fmt.Sprintf("line %d: image %q has no sha256 digest", in.line, image))
		}
		if name != "" {
			stages[name] = true
		}
	}
	return violations
}

var (
	latestReleasePattern = regexp.MustCompile(`/releases/latest/`)
	goInstallPattern     = regexp.MustCompile(`go install\b[^;&|]*?\s(\S+)@(\S+)`)
	releaseVersion       = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
)

// checkNoMutableReleases reports each "latest" release download and each
// go install that does not name an exact release version.
func checkNoMutableReleases(ins []instruction) []string {
	var violations []string
	for _, in := range ins {
		if latestReleasePattern.MatchString(in.args) {
			violations = append(violations, fmt.Sprintf("line %d: download uses a latest release URL", in.line))
		}
		for _, m := range goInstallPattern.FindAllStringSubmatch(in.args, -1) {
			if !releaseVersion.MatchString(m[2]) {
				violations = append(violations, fmt.Sprintf("line %d: go install %s@%s does not use an exact release version", in.line, m[1], m[2]))
			}
		}
	}
	return violations
}

var (
	downloadPattern = regexp.MustCompile(`(?:^|[\s;&|(])(?:wget|curl)\s`)
	verifyPattern   = regexp.MustCompile(`\bsha256sum\s+(?:-c|--check)\b`)
	remoteAdd       = regexp.MustCompile(`(?i)\bhttps?://`)
)

// checkDownloadsVerified reports each RUN that downloads more files than
// it verifies with "sha256sum -c", and each ADD of a URL without --checksum.
func checkDownloadsVerified(ins []instruction) []string {
	var violations []string
	for _, in := range ins {
		switch in.cmd {
		case "RUN":
			downloads := len(downloadPattern.FindAllString(in.args, -1))
			checks := len(verifyPattern.FindAllString(in.args, -1))
			if downloads > checks {
				violations = append(violations, fmt.Sprintf("line %d: %d download(s) but %d sha256 check(s)", in.line, downloads, checks))
			}
		case "ADD":
			if remoteAdd.MatchString(in.args) && !strings.Contains(in.args, "--checksum=sha256:") {
				violations = append(violations, fmt.Sprintf("line %d: ADD of a URL without --checksum", in.line))
			}
		}
	}
	return violations
}

var platformArgs = []string{"BUILDPLATFORM", "BUILDOS", "BUILDARCH", "TARGETPLATFORM", "TARGETOS", "TARGETARCH"}

// checkPlatformArgsDeclared reports each use of a platform build argument
// inside a stage that does not declare it with ARG first. Without the ARG,
// the value is empty and the stage builds for the wrong platform.
func checkPlatformArgsDeclared(ins []instruction) []string {
	var violations []string
	declared := map[int]map[string]bool{}
	for _, in := range ins {
		if in.stage < 0 || in.cmd == "FROM" {
			continue
		}
		if declared[in.stage] == nil {
			declared[in.stage] = map[string]bool{}
		}
		if in.cmd == "ARG" {
			name, _, _ := strings.Cut(in.args, "=")
			declared[in.stage][strings.TrimSpace(name)] = true
			continue
		}
		for _, arg := range platformArgs {
			used := strings.Contains(in.args, "${"+arg+"}") || regexp.MustCompile(`\$`+arg+`\b`).MatchString(in.args)
			if used && !declared[in.stage][arg] {
				violations = append(violations, fmt.Sprintf("line %d: %s is used but not declared with ARG in this stage", in.line, arg))
			}
		}
	}
	return violations
}

var checks = map[string]func([]instruction) []string{
	"images pinned by digest": checkImagesPinnedByDigest,
	"no mutable releases":     checkNoMutableReleases,
	"downloads verified":      checkDownloadsVerified,
	"platform args declared":  checkPlatformArgsDeclared,
}

// TestOperatorDockerfile_Inputs runs every check on the real Dockerfile.
func TestOperatorDockerfile_Inputs(t *testing.T) {
	src, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	ins := parseDockerfile(string(src))
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			for _, v := range check(ins) {
				t.Error(v)
			}
		})
	}
}

// TestOperatorDockerfile_ChecksRejectUnsafeInputs makes sure that each
// check finds the unsafe pattern that it exists for. The snippets come
// from the Dockerfile before the inputs were pinned.
func TestOperatorDockerfile_ChecksRejectUnsafeInputs(t *testing.T) {
	tests := []struct {
		name  string
		check string
		src   string
		want  int // number of violations
	}{
		{
			name:  "runtime image per architecture without digest",
			check: "images pinned by digest",
			src: "FROM gcr.io/distroless/static-debian11:latest AS image-base-amd64\n" +
				"FROM gcr.io/distroless/static-debian11:latest-arm64@sha256:" + strings.Repeat("a", 64) + " AS image-base-arm64\n" +
				"FROM image-base-${TARGETARCH} AS image-base\n",
			want: 1,
		},
		{
			name:  "latest release download",
			check: "no mutable releases",
			src:   "FROM scratch\nRUN wget https://github.com/mikefarah/yq/releases/latest/download/yq_linux_amd64 -O /usr/bin/yq\n",
			want:  1,
		},
		{
			name:  "go install of latest",
			check: "no mutable releases",
			src:   "FROM scratch\nRUN CGO_ENABLED=0 go install -ldflags \"-s -w\" github.com/go-delve/delve/cmd/dlv@latest && \\\n    rm -rf /go/pkg/mod/\n",
			want:  1,
		},
		{
			name:  "second download without checksum",
			check: "downloads verified",
			src:   "FROM scratch\nRUN wget -O /tmp/a https://example.com/a && echo \"x  /tmp/a\" | sha256sum -c - && \\\n    curl -o /tmp/b https://example.com/b\n",
			want:  1,
		},
		{
			name:  "remote ADD without checksum",
			check: "downloads verified",
			src:   "FROM scratch\nADD https://example.com/tool /tool\n",
			want:  1,
		},
		{
			name:  "argument declared only in an earlier stage",
			check: "platform args declared",
			src:   "FROM golang AS a\nARG TARGETARCH\nFROM golang AS b\nRUN echo $TARGETARCH\n",
			want:  1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checks[tt.check](parseDockerfile(tt.src))
			if len(got) != tt.want {
				t.Errorf("check %q: got %d violation(s) %q, want %d, in:\n%s", tt.check, len(got), got, tt.want, tt.src)
			}
		})
	}
}
