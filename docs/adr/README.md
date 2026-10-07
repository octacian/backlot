# Architecture decision records

ADRs preserve consequential choices and rationale: system boundaries, data
contracts, dependencies, security, and durable conventions. Routine implementation
details belong in code or PR descriptions. Acceptance of a decision does not mean
its implementation is complete; track progress in the implementation plan/issues.

## Consult decisions

Before architectural or cross-cutting work, search filenames and frontmatter:

```sh
rg -n '^(title|summary|tags|status):' docs/adr
```

Read relevant records' opening overviews, then supporting sections as needed.
Apply accepted decisions. Proposed and rejected records are context, not mandates;
deprecated records no longer apply. Follow `superseded_by` to the current decision.
Check code and implementation tracking rather than treating metadata as evidence
that work has shipped.

## Create and verify

Use the ADR commands in [Testing and tooling](../testing.md#commands). Tooling is
implemented in Go and uses this directory's [template](template.md). Commands run
from the repository root; the tool's `--dir` option supports another explicit
directory. Creation reads that directory's template and never overwrites a file.

Records live directly here and use `YYYYMMDDTHHmmssSSSZ-short-title.md` filenames
(UTC milliseconds). There is no shared sequence or manually maintained index.
Timestamp collisions retry with the next millisecond. Keep referenced filenames
stable. Complete all template prompts before committing, even for proposals.

Keep the overview immediately after the title sufficient to communicate the
choice, scope, reason, and practical instruction. Supporting sections remain
required even when their answer is explicitly "None".

## Metadata and lifecycle

| Field | Contract |
| --- | --- |
| `title` | Nonempty single-line string matching the Markdown heading |
| `date` | UTC creation date matching the filename, `YYYY-MM-DD` |
| `status` | `proposed`, `accepted`, `rejected`, `deprecated`, or `superseded` |
| `summary` | One sentence naming the choice and primary reason |
| `tags` | Nonempty YAML list of searchable areas |
| `supersedes` | List of older ADR filenames, or `[]` |
| `superseded_by` | List of replacements, or `[]` |

Create as proposed; accept when the decision is agreed through review. Reject a
declined proposal. Deprecate a decision that ceases to apply without a replacement.
For a material change to an accepted choice, create a superseding ADR and preserve
the old rationale. Once the replacement is accepted, mark the old record superseded
and update reciprocal links in the same change. Only superseded records have
nonempty `superseded_by`; a proposal cannot supersede an existing decision.

Validation checks filenames/timestamps, typed frontmatter, required prose sections,
unfinished prompts, reciprocal existing references, and supersession cycles.
Markdown examples cannot satisfy required headings. Validation cannot determine
decision quality, approval, or implementation completion; those require review.

GitHub Actions runs ADR tests and verification with the repository checks on every
PR and push to `main`, including record deletions.
