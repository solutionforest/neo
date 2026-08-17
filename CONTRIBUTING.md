# Contributing

## Ideas and features — write them yourself, informally

Coding agents write most of the implementation now, so what we actually need from you is
**intent**, and that part doesn't compress well. Open an issue and describe the change the way
you'd explain it to a colleague: what you were trying to do, what got in the way, what you
expected instead. Three sentences is fine.

**Please don't have an AI expand a one-line idea into a formal proposal.** A long document
generated from a short thought is longer to read and carries less information than the short
thought did. If we agree on the change, we're happy to spend the tokens on building it.

Rough, opinionated, half-formed is welcome. "This should work like X and it doesn't" is a
useful issue.

## Bugs

Open an issue with what you ran and what happened. The three things that make a Neo bug
reproducible:

```bash
neo version                    # CLI version and platform
neo <command> --debug          # logs the SSH commands actually sent
cat /etc/os-release            # on the server, when it's a server-side failure
```

Paste the real output, not a summary of it. Redact domains and IPs if you like — but keep the
error text exact, since that's usually the part that identifies the bug.

If we merge a fix for something you reported, we'll credit you as co-author on the commit.

**Never paste real credentials into an issue.** Neo handles SSH keys, `.env` contents, database
passwords and license keys — if any of those appear in the output you're about to share, replace
them first. If a bug can only be demonstrated with real secrets, treat it as a security report
(see [SECURITY.md](SECURITY.md)).

## Code

Pull requests are welcome, but for anything beyond a small fix, open the issue first — it's
cheaper for both of us than a PR that doesn't fit the design.

### Building

**Docker is the only supported build path.** Don't run `go build`, `go vet` or `go run`
directly; the version stamping, license-bypass flags and cross-compilation all live in the
Makefile.

```bash
make build        # → bin/neo
make test         # go test ./...
make test-race    # race detector — required if you touch anything concurrent
make fmt          # gofmt
```

Before proposing a release-worthy change, `make release-local VERSION=x.y.z` runs the same
pipeline the release CI does, so build failures surface before a tag exists.

### Integration tests

The Docker sandbox spins up containers that behave like real VPSes (Docker-in-Docker over SSH)
across 13 distributions. No cloud account needed:

```bash
make sandbox-distro DISTRO=debian-12   # one distro
make sandbox-supported                 # the full supported matrix
```

### What we look for

- **Comments explain why, not what.** The code says what it does; the comment should say what
  breaks without it. Comments that restate the next line get removed.
- **A test that fails before the fix.** For a bug fix, the test is the argument that the bug was
  real.
- **Conventional Commits** — `fix(deploy):`, `feat(env):`, `docs:`, `chore(release):`.
- **Behaviour changes go in [CHANGELOG.md](CHANGELOG.md)**, written for someone deciding whether
  to upgrade — what broke, what it means for them, not the diff.
- **Say what you didn't do.** A known limitation stated in the PR is worth more than one
  discovered in production.

## Releases

Maintainers only: a release is the CHANGELOG entry plus a `site/version.json` bump, then a
`vX.Y.Z` tag. Pushing the tag builds and publishes every platform binary.
