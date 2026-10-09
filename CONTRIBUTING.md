# Contributing to VaultReader

Thanks for considering a contribution. VaultReader is a small, maintained-on-spare-time project: issues and pull requests are welcome, but there is **no SLA** on replies, reviews or releases.

Before starting anything larger than a bug fix, open an issue to discuss it. [AGENTS.md](AGENTS.md) maps the code (which file owns what) and lists the traps; read it first.

## Setup

You need Go 1.24 and Node 20 (Node is only used to syntax-check the SPA). `officecli` is optional; without it DOCX export is simply disabled.

```bash
git clone https://github.com/joaompfp/vaultreader
cd vaultreader
make test        # go vet + go test + inline-script syntax check
make build       # static binary ./vaultreader
make run-demo    # run against the bundled demo vault on http://localhost:8080
make image       # multi-arch image build (needs Docker with BuildKit; no push)
```

`make test` is exactly what CI runs; it must pass before you open a pull request.

## Rules of the repo

- **No build step for the frontend.** `static/index.html` is hand-written Alpine.js plus bundled libraries loaded with `<script src>`. Do not add a bundler, transpiler, npm install or generated files.
- **Syntax-check the inline script.** A typo in the SPA's inline script blanks the whole app. After every change to `static/index.html` run `./scripts/check-inline-js.sh` (it is part of `make test`; it needs Node).
- **Tests use real temporary directories**, never filesystem mocks (`t.TempDir()` and the helpers in `testutil_test.go`). Write the failing test first for bug fixes and new behaviour.
- **Never run repo-wide formatters** (`go fmt ./...`, `gofmt -w .`, prettier). They rewrite files outside your change and bury the real diff. Format only the lines you touch.
- **Keep it flat and small.** One concern per `package main` file; a new dependency needs a justification in the pull request.
- **New write endpoints** must call `safePath` and `isWritable` and record `s.audit(...)`; new optional routes go behind `s.feature(...)`. See AGENTS.md.
- **Update `CHANGELOG.md`** for user-visible changes, and the relevant page in `docs/` when you change behaviour or configuration.
- Every `.go` file starts with `// SPDX-License-Identifier: AGPL-3.0-only`.
- Keep operator specifics (real hostnames, personal paths, names) out of code, docs, tests and examples; use `example.com`, `team`, `alice`.

## Commits and pull requests

- Conventional-style subjects in the imperative: `fix: ...`, `feat: ...`, `docs: ...`, `ci: ...`, `refactor: ...`. One logical change per commit.
- Branch from `main`, keep the pull request focused, fill in the template, and make sure CI is green.
- Describe how you verified UI changes (a local binary driven by a browser is fine).

## Licence

VaultReader is licensed under the [GNU AGPL-3.0-only](LICENSE). By submitting a contribution you agree that it is licensed under the same terms. Bundled third-party libraries keep their own licences ([THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)).

## Security issues

Do not file them as public issues; see [SECURITY.md](SECURITY.md).
