package commands

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/vxero/neo/internal/state"
)

// initTestRepo builds a throwaway git repo so the capture is tested against
// real git output rather than a mock of what git might print.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable or failed (%v): %s", err, out)
		}
	}

	run("init", "-q", "-b", "main")
	writeFile(t, dir+"/README.md", "hello")
	run("add", ".")
	run("commit", "-q", "-m", "initial")
	return dir
}

func TestCaptureGitInfoCleanRepo(t *testing.T) {
	dir := initTestRepo(t)

	got := captureGitInfo(dir)
	if !got.Found() {
		t.Fatal("no commit captured from a real repo")
	}
	if len(got.Commit) != 40 {
		t.Errorf("commit = %q, want a full sha", got.Commit)
	}
	if got.ShortCommit != got.Commit[:7] {
		t.Errorf("short commit = %q", got.ShortCommit)
	}
	if got.Branch != "main" {
		t.Errorf("branch = %q, want main", got.Branch)
	}
	if got.Dirty {
		t.Error("a freshly committed tree is not dirty")
	}
	if got.Tag != "" {
		t.Errorf("tag = %q, want empty on an untagged commit", got.Tag)
	}
}

func TestCaptureGitInfoDetectsDirty(t *testing.T) {
	dir := initTestRepo(t)
	writeFile(t, dir+"/README.md", "changed after commit")

	if got := captureGitInfo(dir); !got.Dirty {
		t.Error("uncommitted changes were not detected — the recorded commit would misdescribe the build")
	}
}

func TestCaptureGitInfoOnlyReportsAnExactTag(t *testing.T) {
	dir := initTestRepo(t)

	tag := func(name string) {
		cmd := exec.Command("git", "tag", name)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Skipf("git tag failed: %v", err)
		}
	}
	tag("v1.0.0")

	if got := captureGitInfo(dir); got.Tag != "v1.0.0" {
		t.Errorf("tag = %q, want v1.0.0", got.Tag)
	}

	// A later, untagged commit must NOT inherit the ancestor's tag — otherwise
	// the UI claims v1.0.0 is deployed when it isn't.
	writeFile(t, dir+"/second.txt", "second")
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", "second"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if err := cmd.Run(); err != nil {
			t.Skipf("git commit failed: %v", err)
		}
	}

	if got := captureGitInfo(dir); got.Tag != "" {
		t.Errorf("tag = %q — an untagged commit must not inherit an ancestor's tag", got.Tag)
	}
}

func TestCaptureGitInfoFallsBackToCIEnv(t *testing.T) {
	// A shallow CI checkout often has no usable repository, but the sha is in
	// the environment. Losing it there would leave exactly the builds you most
	// want identified unlabelled.
	t.Setenv("GITHUB_SHA", "abcdef1234567890abcdef1234567890abcdef12")
	t.Setenv("GITHUB_REF_NAME", "release/2026-08")

	got := captureGitInfo(t.TempDir()) // not a repo
	if got.Commit != "abcdef1234567890abcdef1234567890abcdef12" {
		t.Errorf("commit = %q, want the CI-provided sha", got.Commit)
	}
	if got.ShortCommit != "abcdef1" {
		t.Errorf("short commit = %q", got.ShortCommit)
	}
	if got.Branch != "release/2026-08" {
		t.Errorf("branch = %q", got.Branch)
	}
}

func TestCaptureGitInfoNoRepoNoEnv(t *testing.T) {
	for _, k := range []string{"NEO_GIT_COMMIT", "GITHUB_SHA", "CI_COMMIT_SHA", "GIT_COMMIT"} {
		t.Setenv(k, "")
	}

	got := captureGitInfo(t.TempDir())
	if got.Found() {
		t.Errorf("expected nothing captured, got %+v", got)
	}
	// Deploy must still work — this is a scaffolded project or a tarball.
	if id := deploymentIdentifier("20260818-045536", got); id != "20260818-045536" {
		t.Errorf("identifier = %q, want the bare timestamp", id)
	}
}

func TestDeploymentIdentifier(t *testing.T) {
	withSha := deploymentIdentifier("20260818-045536", gitInfo{ShortCommit: "a1b2c3d"})
	if withSha != "20260818-045536-a1b2c3d" {
		t.Errorf("got %q", withSha)
	}

	// The timestamp must stay: the same commit redeployed after an env change
	// is a distinct deployment and cannot reuse an image tag.
	other := deploymentIdentifier("20260818-051500", gitInfo{ShortCommit: "a1b2c3d"})
	if other == withSha {
		t.Error("two deploys of the same commit produced identical identifiers")
	}
}

func TestEnvDigestIgnoresNeoVars(t *testing.T) {
	base := map[string]string{"APP_ENV": "production", "DB_HOST": "db"}

	withNeo := map[string]string{
		"APP_ENV": "production", "DB_HOST": "db",
		"NEO_DEPLOYMENT_ID": "20260818-045536-a1b2c3d",
		"NEO_GIT_COMMIT":    "a1b2c3d",
	}

	// NEO_* change every deploy. Including them would make every digest unique
	// and destroy the only thing it measures: whether config changed.
	if envDigest(base) != envDigest(withNeo) {
		t.Error("NEO_* variables changed the digest")
	}

	// A project's own NEO_-prefixed config must still count. neo-cms uses
	// NEO_LS_STORE_ID; a prefix filter would have silently ignored it.
	withOwnNeoVar := map[string]string{"APP_ENV": "production", "DB_HOST": "db", "NEO_LS_STORE_ID": "924820"}
	if envDigest(base) == envDigest(withOwnNeoVar) {
		t.Error("a project's own NEO_-prefixed variable was excluded from the digest")
	}

	changed := map[string]string{"APP_ENV": "staging", "DB_HOST": "db"}
	if envDigest(base) == envDigest(changed) {
		t.Error("a real config change did not change the digest")
	}

	if envDigest(map[string]string{}) != "" {
		t.Error("an empty environment should have no digest")
	}
}

func TestInjectDeploymentEnv(t *testing.T) {
	env := map[string]string{"APP_ENV": "production"}
	git := gitInfo{
		Commit: "a1b2c3d4e5f6", ShortCommit: "a1b2c3d",
		Branch: "main", Tag: "v1.4.2",
	}

	injectDeploymentEnv(env, "20260818-045536-a1b2c3d", git)

	for k, want := range map[string]string{
		"NEO_DEPLOYMENT_ID":    "20260818-045536-a1b2c3d",
		"NEO_GIT_COMMIT":       "a1b2c3d4e5f6",
		"NEO_GIT_SHORT_COMMIT": "a1b2c3d",
		"NEO_GIT_BRANCH":       "main",
		"NEO_GIT_TAG":          "v1.4.2",
	} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
	if env["APP_ENV"] != "production" {
		t.Error("existing variables were disturbed")
	}
	if env["NEO_DEPLOYED_AT"] == "" {
		t.Error("NEO_DEPLOYED_AT missing")
	}
}

func TestInjectDeploymentEnvDoesNotOverrideExplicitValues(t *testing.T) {
	// Someone pinning NEO_GIT_COMMIT by hand means it deliberately.
	env := map[string]string{"NEO_GIT_COMMIT": "pinned-by-user"}
	injectDeploymentEnv(env, "id", gitInfo{Commit: "captured"})

	if env["NEO_GIT_COMMIT"] != "pinned-by-user" {
		t.Errorf("overwrote an explicitly set value: %q", env["NEO_GIT_COMMIT"])
	}
}

func TestInjectDeploymentEnvOmitsUnknowns(t *testing.T) {
	// Outside git there is no commit; empty variables would be worse than
	// absent ones, since an app can't tell "unset" from "empty string".
	env := map[string]string{}
	injectDeploymentEnv(env, "20260818-045536", gitInfo{})

	if _, ok := env["NEO_GIT_COMMIT"]; ok {
		t.Error("NEO_GIT_COMMIT set with no commit available")
	}
	if env["NEO_DEPLOYMENT_ID"] != "20260818-045536" {
		t.Error("deployment id should always be set")
	}
}

func TestDeploymentDescribe(t *testing.T) {
	cases := []struct {
		name string
		dep  *state.Deployment
		want string
	}{
		{"tag and sha", &state.Deployment{Tag: "v1.4.2", ShortCommit: "a1b2c3d"}, "v1.4.2 (a1b2c3d)"},
		{"sha only", &state.Deployment{ShortCommit: "a1b2c3d"}, "a1b2c3d"},
		{"dirty", &state.Deployment{ShortCommit: "a1b2c3d", Dirty: true}, "a1b2c3d *"},
		{"no git info", &state.Deployment{ID: "20260818-045536"}, "20260818-045536"},
		{"nil", nil, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.dep.Describe(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOCILabelArgs(t *testing.T) {
	labels := ociLabelArgs(gitInfo{
		Commit: "a1b2c3d4", Tag: "v1.4.2", Branch: "main", Dirty: true,
	}, "20260818-045536-a1b2c3d")

	joined := strings.Join(labels, " ")
	for _, want := range []string{
		"org.opencontainers.image.revision=a1b2c3d4",
		"org.opencontainers.image.version=v1.4.2",
		"dev.vxero.neo.deployment=20260818-045536-a1b2c3d",
		"dev.vxero.neo.dirty=true",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing label %q in %v", want, labels)
		}
	}

	// Nothing to say means no labels, not empty ones.
	if got := ociLabelArgs(gitInfo{}, ""); len(got) != 0 {
		t.Errorf("expected no labels, got %v", got)
	}
}
