package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type releaseFixture struct {
	t      *testing.T
	root   string
	script string
	runs   []map[string]any
}

func newReleaseFixture(t *testing.T) *releaseFixture {
	t.Helper()
	script, err := filepath.Abs("../../.github/scripts/release.sh")
	if err != nil {
		t.Fatal(err)
	}
	f := &releaseFixture{t: t, root: t.TempDir(), script: script}
	f.write("mode", "success")
	f.write("bin/git", `#!/bin/sh
case $1 in
rev-parse) echo source;;
status) if [ "$(cat mode)" = dirty ]; then echo dirty; fi;;
ls-remote) printf 'source\trefs/tags/v0.1.1\n';;
esac
`)
	// All remote boundaries are fixtures; no test may contact GitHub or publish.
	f.write("bin/gh", `#!/bin/sh
set -eu
printf '%s\n' "$*" >> calls
mode=$(cat mode)
case "$*" in
  *actions/runs\?*)
    [ "$2" = --paginate ] && [ "$3" = --slurp ]
    [ "$mode" != lookup-error ] || exit 1
    cat pages.json;;
  api*actions/runs/*)
    [ "$mode" != refresh-error ] || exit 1
    cat "run-${2##*/}.json";;
  api*releases*)
    [ "$mode" != release-lookup-error ] || exit 1
    if [ "$mode" = existing ]; then echo 42; fi;;
  'release create '*)
    touch created
    [ "$mode" != upload-error ];;
  'release view '*) cat uploaded.json;;
  'release download '*)
    while [ "$1" != --dir ]; do shift; done
    cp dist/* "$2/"
    if [ "$mode" = corrupt ]; then echo corrupt >> "$2/backlot_v0.1.1_linux_amd64.tar.gz"; fi;;
  *) echo 'unexpected remote operation' >&2; exit 99;;
esac
`)
	f.write("bin/sha256sum", "#!/bin/sh\nexit 0\n")
	var assets []map[string]string
	for _, target := range []string{"darwin_arm64.tar.gz", "linux_amd64.tar.gz", "checksums.txt"} {
		name := "backlot_v0.1.1_" + target
		f.write("dist/"+name, "fixture")
		assets = append(assets, map[string]string{"name": name})
	}
	f.json("uploaded.json", map[string]any{"assets": assets, "isDraft": true, "tagName": "v0.1.1"})
	for i, name := range []string{"check", "docs", "scenes", "project-inputs", "release"} {
		f.runs = append(f.runs, map[string]any{
			"id": i + 1, "workflow_id": i + 10, "run_number": 5, "run_attempt": 1,
			"path": ".github/workflows/" + name + ".yml", "event": "push",
			"head_branch": "main", "head_sha": "source", "status": "completed", "conclusion": "success",
			"repository": map[string]string{"full_name": "owner/repo"}, "head_repository": map[string]string{"full_name": "owner/repo"},
		})
	}
	f.saveRuns()
	return f
}

func (f *releaseFixture) write(name, body string) {
	f.t.Helper()
	path := filepath.Join(f.root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		f.t.Fatal(err)
	}
}

func (f *releaseFixture) json(name string, value any) {
	f.t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		f.t.Fatal(err)
	}
	f.write(name, string(data))
}

func (f *releaseFixture) saveRuns() {
	f.t.Helper()
	// Exercise slurped pagination, including a required workflow on the second page.
	f.json("pages.json", []any{
		map[string]any{"total_count": len(f.runs), "workflow_runs": f.runs[:2]},
		map[string]any{"total_count": len(f.runs), "workflow_runs": f.runs[2:]},
	})
	for _, run := range f.runs {
		f.json(fmt.Sprintf("run-%v.json", run["id"]), run)
	}
}

func (f *releaseFixture) run(command, tag string) (string, error) {
	f.t.Helper()
	cmd := exec.Command("bash", f.script, command)
	cmd.Dir = f.root
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(f.root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"), "RELEASE_TAG="+tag, "SOURCE_SHA=source", "GITHUB_REPOSITORY=owner/repo")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestReleaseMainCI(t *testing.T) {
	for _, test := range []struct {
		name  string
		field string
		value any
	}{
		{"success including existing job skips", "", nil},
		{"pending", "status", "in_progress"}, {"failed", "conclusion", "failure"},
		{"cancelled", "conclusion", "cancelled"}, {"skipped workflow", "conclusion", "skipped"},
		{"unknown", "conclusion", nil}, {"PR", "event", "pull_request"},
		{"tag", "head_branch", "v0.1.1"}, {"other SHA", "head_sha", "other"},
		{"other workflow", "path", ".github/workflows/other.yml"},
		{"fork", "head_repository", map[string]string{"full_name": "fork/repo"}},
		{"other repository", "repository", map[string]string{"full_name": "other/repo"}},
		{"malformed run", "run_number", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newReleaseFixture(t)
			if test.field != "" {
				f.runs[0][test.field] = test.value
			}
			f.saveRuns()
			out, err := f.run("preflight", "v0.1.1")
			if (err == nil) != (test.field == "") {
				t.Fatalf("result %v: %s", err, out)
			}
			if err != nil && !strings.Contains(out, "rerun the tag workflow") {
				t.Fatal("missing recovery instruction", out)
			}
		})
	}
	for _, test := range []string{"newer failed run", "newer pending attempt", "missing workflow", "incomplete pages", "search cap", "malformed pages", "lookup-error", "refresh-error", "refresh identity"} {
		t.Run(test, func(t *testing.T) {
			f := newReleaseFixture(t)
			switch test {
			case "newer failed run":
				newer := make(map[string]any)
				for k, v := range f.runs[0] {
					newer[k] = v
				}
				newer["id"], newer["run_number"], newer["conclusion"] = 20, 6, "failure"
				f.runs = append(f.runs, newer)
				f.saveRuns()
			case "newer pending attempt":
				f.runs[0]["run_attempt"], f.runs[0]["status"] = 2, "queued"
				f.json("run-1.json", f.runs[0])
			case "missing workflow":
				f.runs = f.runs[:4]
				f.saveRuns()
			case "incomplete pages", "search cap":
				total := 6
				if test == "search cap" {
					total = 1000
				}
				f.json("pages.json", []any{map[string]any{"total_count": total, "workflow_runs": f.runs}})
			case "malformed pages":
				f.write("pages.json", "{}")
			case "refresh identity":
				f.runs[0]["head_sha"] = "other"
				f.json("run-1.json", f.runs[0])
			default:
				f.write("mode", test)
			}
			if out, err := f.run("preflight", "v0.1.1"); err == nil {
				t.Fatal("unsafe evidence accepted", out)
			}
		})
	}
}

func TestReleaseVerifiedDraft(t *testing.T) {
	for _, mode := range []string{"success", "existing", "release-lookup-error", "upload-error", "corrupt", "not draft", "missing asset", "changed source", "failed recheck", "dirty"} {
		t.Run(mode, func(t *testing.T) {
			f := newReleaseFixture(t)
			f.write("mode", mode)
			switch mode {
			case "not draft":
				body, err := os.ReadFile(filepath.Join(f.root, "uploaded.json"))
				if err != nil {
					t.Fatal(err)
				}
				f.write("uploaded.json", strings.Replace(string(body), `"isDraft":true`, `"isDraft":false`, 1))
			case "missing asset":
				f.json("uploaded.json", map[string]any{"isDraft": true, "tagName": "v0.1.1", "assets": []any{}})
			case "changed source":
				f.write("bin/git", "#!/bin/sh\nif [ -e created ]; then exit 1; fi\ncase $1 in\nrev-parse) echo source;;\nls-remote) printf 'source\\trefs/tags/v0.1.1\\n';;\nesac\n")
			case "failed recheck":
				f.runs[0]["conclusion"] = "failure"
				f.json("run-1.json", f.runs[0])
			}
			out, err := f.run("draft", "v0.1.1")
			if (err == nil) != (mode == "success") {
				t.Fatalf("result %v: %s", err, out)
			}
			calls, _ := os.ReadFile(filepath.Join(f.root, "calls"))
			if strings.Contains(string(calls), "release edit") || strings.Contains(string(calls), "--draft=false") {
				t.Fatal("attempted publication", string(calls))
			}
			if mode == "success" && (!strings.Contains(string(calls), "--verify-tag --draft --generate-notes") || !strings.Contains(string(calls), "/v0.1.1/docs/operations.md")) {
				t.Fatal("missing generated draft notes and tagged guidance", string(calls))
			}
			if mode == "failed recheck" || mode == "existing" || mode == "release-lookup-error" {
				if _, err := os.Stat(filepath.Join(f.root, "created")); !os.IsNotExist(err) {
					t.Fatal("draft created despite failed prerequisite")
				}
			}
		})
	}
	for _, tag := range []string{"v0.1.0-rc.1", "v0.01.0", "v1.0.0", "v0.1.0+build"} {
		f := newReleaseFixture(t)
		if out, err := f.run("draft", tag); err == nil || !strings.Contains(out, "invalid stable release tag") {
			t.Fatalf("unsafe tag %s: %v %s", tag, err, out)
		}
	}
}

func TestReleaseJobEligibility(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			If string `yaml:"if"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []bool{false, true} {
		for _, cancelled := range []bool{false, true} {
			for _, status := range []string{"success", "failure", "cancelled", "skipped"} {
				for _, dependency := range []string{"preflight", "package", "smoke"} {
					for _, job := range []string{"package", "smoke", "draft"} {
						expr := workflow.Jobs[job].If
						if !strings.Contains(expr, "!cancelled()") {
							t.Fatalf("%s lacks skipped-ancestor status override", job)
						}
						values := map[string]string{"preflight": "success", "package": "success", "smoke": "success", "changes": "skipped"}
						values[dependency] = status
						// These workflow predicates use only Boolean operators/comparisons;
						// substitute the contexts and exercise the actual expression in Bash.
						expr = strings.TrimSuffix(strings.TrimPrefix(expr, "${{ "), " }}")
						expr = strings.ReplaceAll(expr, "!cancelled()", fmt.Sprint(!cancelled))
						expr = strings.ReplaceAll(expr, "!startsWith(github.ref, 'refs/tags/')", fmt.Sprint(!tag))
						expr = strings.ReplaceAll(expr, "startsWith(github.ref, 'refs/tags/')", fmt.Sprint(tag))
						expr = strings.ReplaceAll(expr, "needs.changes.outputs.check", "'true'")
						for name, result := range values {
							expr = strings.ReplaceAll(expr, "needs."+name+".result", "'"+result+"'")
						}
						// Bash Boolean literals need comparisons inside [[ ... ]].
						expr = strings.NewReplacer("true", "1 == 1", "false", "1 == 0").Replace(expr)
						cmd := exec.Command("bash", "-c", "if [[ "+expr+" ]]; then exit 0; else exit 1; fi")
						out, err := cmd.CombinedOutput()
						want := !cancelled
						switch job {
						case "package":
							want = want && (!tag || values["preflight"] == "success")
						case "smoke":
							want = want && values["package"] == "success"
						case "draft":
							want = want && tag && values["preflight"] == "success" && values["package"] == "success" && values["smoke"] == "success"
						}
						if (err == nil) != want || len(out) != 0 {
							t.Fatalf("%s tag=%v cancelled=%v %s=%s: want %v, %v %s", job, tag, cancelled, dependency, status, want, err, out)
						}
					}
				}
			}
		}
	}
}
