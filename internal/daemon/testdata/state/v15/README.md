# Genuine closed v15 predecessor

Produced by the actual v15 #125 binary before #128 release producers. The producer
was `6178482084f076fc71efadf24e41091db1a42cb6` plus its recorded dirty source;
no clean revision is claimed. Source archive SHA-256:
`97f982b9132f9134e98878cb1748abc6fe7e5c5de97413c74f76041998c152c1`.
Producer binary SHA-256:
`1749d9d0aa2a988309342ccdb7003a9140fb7a511fb14202e5df8646e4a374df`.
Closed database SHA-256:
`5c69f40ab9ed2337f85f2df4598c447dd2da7edcb3fc82f9a96c19b3aa6e9ef6`.

Go 1.27.3 darwin/arm64 built the binary with
`go build -o /tmp/backlot-125-128/backlot-v15-predecessor ./cmd/backlot`.
The capture prepared one persistent scene and ran/stopped a native job in another.
Authenticated daemon shutdown joined exit 0 and verified socket absence before
copying this database. The owned runtime directory was then removed. No Docker,
SQL or Caddy effects and no #128 consumer release facts were captured. Optional
settlement types then present in the producer were unused; fixture bytes remain
unchanged after the corrected #125/#128 boundary.

Raw immutable provenance, producer.json, commands, cleanup, source archive/diff and
binary are retained in `/tmp/backlot-125-128/evidence/v15` and its documented binary
path for this implementation session. To regenerate, use that exact producer source
and the included capture.py with only task-owned binary/evidence paths adapted;
new paths, IDs and hashes will differ. Preserve these historical bytes. Tests use
passive temporary copies, do not recover historical effects or contact their removed
source locations, and never manufacture current proof or observation time.
