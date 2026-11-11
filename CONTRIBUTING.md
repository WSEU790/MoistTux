# Contributing to NEXUS

Welcome! NEXUS controls real servers, so correctness and safety come before
features. Please read [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md) before
your first change. It explains the layout, how to run everything locally,
and where each kind of change goes.

## Before you start

```bash
make ui && make test       # must pass before every push
```

You need Go 1.26+, Node 20+, and NSIS if you build the installer locally.
For anything that touches systemd, sudo or SSH deployment, test against a
disposable Linux server (see *Disposable Linux servers* in the handbook),
never a real one.

## Rules every change must keep

1. No fabricated data, mock-ups or placeholder buttons. If something is
   unavailable, show that and the reason.
2. No free-form command execution, for the AI or for people. Operations are
   typed tools with validated arguments. Root access goes only through the
   allowlisted helper, which validates again as root.
3. No plaintext secrets at rest, and no credentials in AI prompts or tool
   results.
4. No destructive or risky change without authorization: approvals with an
   impact preview, and typed confirmation for high-risk operations. SSH and
   firewall configuration is never changed automatically.
5. Verify the real state after every change, and report what was verified.
6. Never swallow errors.
7. Keep the agent lightweight; heavy scans are on demand and bounded.
8. The AI (Ollama) stays optional and local.

The full list, with reasons, is in section 2 of the handbook.

## Workflow

* Work on a branch and open a pull request against `nexus-foundation` (the
  current development line) or `main`.
* **Pushing to `main` or `nexus-foundation` publishes a release** that every
  installed copy of NEXUS downloads automatically. Only merge code that has
  passed CI.
* Keep commits focused, and say *why* in the message.
* Add or update tests with the code: unit tests next to the package, and
  `internal/integration` for behavior that crosses the agent/core boundary.
* Update the docs in the same pull request: `README.md` for user-facing
  features, `docs/STATUS.md` for what is verified and how, and
  `docs/PROTOCOL.md` / `docs/ARCHITECTURE.md` for protocol or security
  changes.
* Never edit an existing database migration; append a new one.
* Never commit secrets. The update signing key lives only in the
  `NEXUS_UPDATE_SIGNING_KEY` repository secret and the maintainer's password
  manager.

## Reporting security problems

Do not open a public issue for a vulnerability. Contact the repository owner
privately through GitHub (Security → Report a vulnerability, if enabled).
