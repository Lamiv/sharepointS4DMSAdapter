# Design of the zaks-drive command

For contributors. The user guide is [docs/cli.md](../cli.md).

## Two binaries

| Binary | Source | Runs | Why separate |
|---|---|---|---|
| `zaks-drive` | `cmd/zaks-drive`, logic in `internal/ctl` | on the host, as the operator | It manages Docker, files and releases. It must not be able to serve documents or hold Graph tokens. |
| `zaks-drive-server` | `cmd/adapter` | in the container | It serves SAP and REST. It must not be able to start containers or touch the host. |

Keeping them apart removes the ambiguity between "start the server" and "start the deployment", and lets each have the permissions it needs. The management command imports the server's `internal/config` and `internal/apperr` so that `config validate` and `setup` use **the same loader and error catalogue** as the server, instead of a second, drifting parser.

## Standard library, not Cobra

Cobra was evaluated. It would give generated help and shell completion, at the cost of a dependency tree in a tool that runs with elevated rights and in a repository that otherwise has four small dependencies. The command set is small and flat (about a dozen verbs); `flag` per command, a table-driven router and a hand-written help text cover it, and the output contract (exit codes, `--json`) is explicit. Shell completion is not provided. Reconsider if the command tree grows nested or if completion becomes a requirement; the `App.commands()` table is the single place that would change.

## Testability: nothing runs for real in unit tests

`ctl.App` carries every outside dependency: process runner, HTTP clients, environment, clock, terminal functions, disk and port probes. The tests replace the runner with `fakeRunner`, which **records the exact argument list of every process the command would start**. That is what lets the tests assert:

- `stop` and `restart` never issue `down`, `rm`, `-v`, `prune`, `volume`, `image rm`;
- every Compose call carries the deployment's compose file;
- `docker inspect` receives only the container IDs that Compose returned;
- no `ss`, `lsof` or `netstat` is ever run;
- the support bundle never runs `exec`, `config`, `cp` or an environment query.

A fake admin endpoint, a fake Content Server port and a local release server (real SHA256SUMS, real archives, hostile archives) drive the diagnostics, the update and the rollback paths. Because fakes can hide real problems, the milestones were also exercised against real Docker Compose with the Graph emulator (setup, start, status, logs, doctor, check, bundle, stop, restart, update to a good release, update to a broken release with automatic rollback, tampered download refused). Those runs are manual; CI runs the unit tests and `scripts/test_install.sh`.

## Safe process execution

`Runner.Run(ctx, name, args, opts)` takes the program and its arguments separately; there is no shell and no string interpolation. Values that come from files or flags and end up in an argument (versions, tags, revisions) are validated first (`validVersion`, `revisionRe`). Interactive secret input uses `stty -echo` with fixed arguments.

## Sanitizing, allowlisting, failing closed

Three layers, because each can fail:

1. **Prevention in the server:** the logging policy ([security](../operations/security.md#logging-policy)).
2. **Sanitizer in the command** (`Sanitizer`): masks `.env` secret values (plain and URL-encoded) and credential-shaped patterns. Used for logs, `check`, `doctor` details and `status` notes.
3. **Allowlist plus a final scan in the support bundle:** files are generated from typed fields; before anything is written the content is scanned for every secret value of the deployment and the bundle is refused if one is found.

These tests were verified by mutation: disabling the sanitizer, widening a file mode, putting a site URL into the config summary, skipping the checksum comparison, accepting links in archives, or removing the automatic rollback each make a test fail.

## Exit codes

They are a contract (see [cli.md](../cli.md#exit-codes)); tests assert them. New failure classes get a new code only when scripts could reasonably react differently.

## Release flow

`scripts/build-release.sh` builds both binaries per architecture into a deterministic archive. `scripts/install.sh` and `zaks-drive update` verify the archive against `SHA256SUMS` **before** opening it, then extract with an allowlist. The release workflow builds twice and compares the checksums, runs the offline installer tests, attests provenance, and creates a **draft** release only. The same bundle layout is read by `internal/ctl/release.go` (update) and `scripts/install.sh` (first install); `scripts/test_install.sh` and `internal/ctl/update_test.go` cover both.

## Adding a command or a diagnostic

- A command: add it to `App.commands()`, write its tests (arguments, exit codes, secret handling), document it in `docs/cli.md`.
- A diagnostic: add the check, **and** its entry in `Diagnostics` (how it works, when it fails or warns). `TestDiagnosticsCatalogueIsComplete` fails if a finding is undocumented or a documented one is never emitted, and `TestCLIGuideDocumentsEverything` fails if `docs/cli.md` does not mention it.
- Never print or log a value that came from `.env`. If a new field is shown to the user, ask whether it could contain a secret.
