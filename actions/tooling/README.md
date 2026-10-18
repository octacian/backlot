# Optional native action SDK build

Contributor `make check` remains Go-only. Node/npm are needed only when changing
SDK dependencies or regenerating `actions/setup/sdk.mjs`. Native action entry
points import this committed bundle, not application `node_modules`.

With Node 26.8.1 / npm 11.19.0 (the example's current tooling):

```sh
cd actions/tooling
npm ci --ignore-scripts
npm run build
```

The lockfile pins official `@actions/cache` and `@actions/artifact` clients and
esbuild. `sdk.mjs` is the source entry point; `../setup/sdk.mjs` is generated.
Review dependency and generated changes together. esbuild preserves bundled legal
notices. No custom cache/artifact service protocol is maintained here.

The archive auditor is independent Go source in `../setup/audit`; setup compiles
`audit/main.go` from the selected ACTION tree with its required Go 1.27.1, even
when `backlot-ref` selects a different runtime tree. To validate it directly:

```sh
go test -race ./actions/setup/audit # from repository root
# Optional: go build -o /private/owned/audit ./actions/setup/audit
# /private/owned/audit /private/owned/retained-evidence
```

Future release-based installation (#25) must account for packaging this helper
before removing the consumer Go prerequisite. Python is not used by these actions
or the action-check fixture generation.
