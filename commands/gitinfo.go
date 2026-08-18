package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"sort"
	"strings"
)

// gitInfo is what the local checkout says about the build being deployed.
type gitInfo struct {
	Commit      string
	ShortCommit string
	Branch      string
	Tag         string
	Dirty       bool
}

// Found reports whether anything identifying was captured. A scaffolded
// project, a shallow CI checkout, or a tarball has no git data — deploy carries
// on without it rather than refusing.
func (g gitInfo) Found() bool {
	return g.Commit != ""
}

// captureGitInfo reads the commit, branch and tag of a project directory.
//
// Every lookup is best effort: `git` may be missing, the directory may not be a
// repository, and CI often checks out a detached shallow clone. Where git can't
// answer, the CI-provided environment is used instead, since that is the one
// place the sha is still known.
func captureGitInfo(projectDir string) gitInfo {
	var info gitInfo

	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = projectDir
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}

	info.Commit = run("rev-parse", "HEAD")
	if info.Commit == "" {
		// No usable repository. CI systems export the sha even for checkouts
		// that git itself can't describe.
		info.Commit = firstNonEmptyEnv("NEO_GIT_COMMIT", "GITHUB_SHA", "CI_COMMIT_SHA", "GIT_COMMIT")
		info.Branch = firstNonEmptyEnv("NEO_GIT_BRANCH", "GITHUB_REF_NAME", "CI_COMMIT_REF_NAME")
		info.Tag = firstNonEmptyEnv("NEO_GIT_TAG", "CI_COMMIT_TAG")
		info.ShortCommit = shortCommit(info.Commit)
		return info
	}

	info.ShortCommit = shortCommit(info.Commit)
	info.Branch = run("rev-parse", "--abbrev-ref", "HEAD")
	if info.Branch == "HEAD" {
		// Detached head (a tag checkout, or CI). The branch name isn't
		// knowable from git here, so prefer whatever CI recorded.
		info.Branch = firstNonEmptyEnv("NEO_GIT_BRANCH", "GITHUB_REF_NAME", "CI_COMMIT_REF_NAME")
	}

	// --exact-match so only a commit that IS tagged reports a tag; without it
	// git describe walks backwards and labels an untagged commit with an
	// ancestor's tag, which reads as "v1.4.2 is deployed" when it isn't.
	info.Tag = run("describe", "--tags", "--exact-match")

	// Any tracked modification, staged or not, means the image contains code
	// that is not in the commit recorded alongside it.
	info.Dirty = run("status", "--porcelain") != ""

	return info
}

func shortCommit(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

func firstNonEmptyEnv(names ...string) string {
	for _, name := range names {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
	}
	return ""
}

// deployedBy identifies the machine a deploy came from, so a shared history
// answers "who shipped this" without any account system.
func deployedBy() string {
	name := "unknown"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		return name
	}
	return name + "@" + host
}

// injectedEnvKeys are the variables deploy adds to describe the build.
//
// Listed exactly rather than matched on a NEO_ prefix: projects legitimately
// use that prefix for their own configuration (NEO_LS_STORE_ID and friends in
// neo-cms), and excluding those from the digest would mean a real config change
// went unnoticed.
var injectedEnvKeys = map[string]bool{
	"NEO_DEPLOYMENT_ID":    true,
	"NEO_GIT_COMMIT":       true,
	"NEO_GIT_SHORT_COMMIT": true,
	"NEO_GIT_BRANCH":       true,
	"NEO_GIT_TAG":          true,
	"NEO_DEPLOYED_AT":      true,
}

// envDigest fingerprints the environment a build ran with, so the same commit
// deployed twice with different config is distinguishable.
//
// The injected variables are excluded deliberately: NEO_DEPLOYMENT_ID changes on
// every deploy, so including them would make every digest unique and destroy the
// only signal this field carries.
func envDigest(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		if injectedEnvKeys[k] {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%s=%s\n", k, env[k])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))[:16]
}
