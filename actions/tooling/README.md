# Optional native action SDK build

Contributor `make check` remains Go-only. Node/npm are needed only when changing
SDK dependencies or regenerating the SDK and its notices. Native action entry
points import this committed bundle, not application `node_modules`.

With Node 26.8.1 / npm 11.19.0 (the example's current tooling):

```sh
cd actions/tooling
npm ci --ignore-scripts
npm run build
npm run build:check # rebuild in memory and reject drift in any generated artifact
```

The lockfile pins official `@actions/cache` and `@actions/artifact` clients and
esbuild. `sdk.mjs` is the source entry point; `build.mjs` generates
`../setup/sdk.mjs`, `../setup/SDK_NOTICES.md` and `../setup/sdk-packages.json`
together. Commit and distribute all three. The inventory uses esbuild's output
metafile to select package installations that contribute bytes, including nested
versions, and binds each to the lockfile version and integrity. Build-only
dependencies such as esbuild are excluded. The notices retain package-level and
nested LICENSE/NOTICE/COPYING/COPYRIGHT files, the isarray README license section,
and the exact-version supplements described in [licenses/README.md](licenses/README.md).
Generation fails if an included package has no notice text or lock binding.
Preserved source comments alone are insufficient for package-level licenses.
Review dependency and generated changes together, including supplement provenance
when versions change. The root `THIRD_PARTY_NOTICES.md` remains the independent Go
inventory. No npm step is added to contributor checks or the consumer path.
No custom cache/artifact service protocol is maintained here.

Portable evidence is created by the selected export-capable Backlot runtime through
its typed public CLI. Generic audit lives in `internal/evidenceaudit`; application
report validation lives in `examples/ci-scenes`. No action-side auditor is built.
