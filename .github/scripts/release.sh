#!/usr/bin/env bash
set -euo pipefail

# Only the draft command writes remotely; publication stays manual.
tag=${RELEASE_TAG:?RELEASE_TAG is required}
sha=${SOURCE_SHA:?SOURCE_SHA is required}
[[ $tag =~ ^v0\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || { echo "invalid stable release tag: $tag" >&2; exit 1; }
[[ $(git rev-parse HEAD) == "$sha" ]]
[[ $(git rev-parse "refs/tags/$tag^{commit}") == "$sha" ]]
[[ -z $(git status --porcelain) ]] || { echo 'release source is dirty' >&2; exit 1; }
remote=$(git ls-remote origin "refs/tags/$tag" "refs/tags/$tag^{}")
resolved=$(printf '%s\n' "$remote" | tail -1 | cut -f1)
[[ $resolved == "$sha" ]] || { echo 'remote tag differs from event commit' >&2; exit 1; }

ci_error() {
  echo "Main CI evidence blocked: $*. Complete/rerun main CI for $sha, then rerun the tag workflow." >&2
  exit 1
}

verify_main_ci() {
  local pages selected run current
  pages=$(gh api --paginate --slurp "repos/$repo/actions/runs?event=push&branch=main&head_sha=$sha&per_page=100") || ci_error 'run lookup failed'
  # Filtered run searches are capped at 1,000 by GitHub; never accept truncated evidence.
  selected=$(jq -ce --arg repo "$repo" --arg sha "$sha" '
    def trusted:
      .repository.full_name == $repo and .head_repository.full_name == $repo
      and .event == "push" and .head_branch == "main" and .head_sha == $sha;
    if type != "array" or length == 0
      or any(.[]; (.workflow_runs | type) != "array" or (.total_count | type) != "number")
      then error("invalid run pages") else . end
    | . as $pages | [.[].workflow_runs[]] as $runs
    | if $pages[0].total_count >= 1000
      or any($pages[]; .total_count != $pages[0].total_count)
      or ($runs | map(.id) | unique | length) != $pages[0].total_count
      then error("incomplete run discovery") else $runs end
    | . as $runs
    | ["check.yml", "docs.yml", "scenes.yml", "project-inputs.yml", "release.yml"]
    | map(".github/workflows/" + . | . as $path
      | [$runs[] | select(.path == $path and trusted)]
      | if length == 0 then error("missing " + $path) else . end
      | if any(.[]; (.id | type) != "number" or (.run_number | type) != "number"
          or (.run_attempt | type) != "number" or (.workflow_id | type) != "number")
        then error("invalid run identity for " + $path) else max_by(.run_number) end)
    | .[]' <<<"$pages") || ci_error 'missing, malformed or incomplete workflow runs'
  while IFS= read -r run; do
    current=$(gh api "repos/$repo/actions/runs/$(jq -r .id <<<"$run")") || ci_error "refresh failed for run $(jq -r .id <<<"$run")"
    jq -e --argjson run "$run" '
      .id == $run.id and .workflow_id == $run.workflow_id and .path == $run.path
      and .run_number == $run.run_number
      and (.run_attempt | type) == "number" and .run_attempt >= $run.run_attempt
      and .repository.full_name == $run.repository.full_name
      and .head_repository.full_name == $run.head_repository.full_name
      and .event == "push" and .head_branch == "main" and .head_sha == $run.head_sha
      and .status == "completed" and .conclusion == "success"
    ' <<<"$current" >/dev/null || ci_error "latest run/attempt is not verified successful: $(jq -r '"\(.path) run \(.id) attempt \(.run_attempt): \(.status)/\(.conclusion)"' <<<"$current")"
    jq -r '"Verified main CI: \(.path) run \(.id) attempt \(.run_attempt)"' <<<"$current"
  done <<<"$selected"
}

case ${1:-} in
  verify-tag) exit 0 ;;
  preflight|draft) ;;
  *) echo 'usage: release.sh verify-tag|preflight|draft' >&2; exit 1 ;;
esac
repo=${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}
if [[ $1 == preflight ]]; then verify_main_ci; exit 0; fi

assets=("backlot_${tag}_darwin_arm64.tar.gz" "backlot_${tag}_linux_amd64.tar.gz" "backlot_${tag}_checksums.txt")
for asset in "${assets[@]}"; do test -s "dist/$asset"; done
(cd dist && sha256sum --strict -c "backlot_${tag}_checksums.txt")

# A successful paginated listing distinguishes absence from a failed lookup.
releases=$(gh api --paginate "repos/$repo/releases" --jq ".[] | select(.tag_name == \"$tag\") | .id")
[[ -z $releases ]] || {
  echo 'release already exists (including drafts); inspect manually' >&2; exit 1;
}
paths=()
for asset in "${assets[@]}"; do paths+=("dist/$asset"); done
verify_main_ci
notes="Installation, upgrades and compatibility: https://github.com/$repo/blob/$tag/docs/operations.md
State migration policy: https://github.com/$repo/blob/$tag/docs/containers.md#state-compatibility
Schemas: https://github.com/$repo/tree/$tag/schemas"
gh release create "$tag" --repo "$repo" --verify-tag --draft --generate-notes --title "$tag" --notes "$notes" "${paths[@]}"

# Verify the complete uploaded set and bytes before handing off the unpublished draft.
uploaded=$(gh release view "$tag" --repo "$repo" --json assets,isDraft,tagName)
jq -e --arg tag "$tag" --argjson expected "$(printf '%s\n' "${assets[@]}" | jq -R . | jq -s .)" \
  '.isDraft and .tagName == $tag and ([.assets[].name] | sort) == ($expected | sort)' <<<"$uploaded" >/dev/null
download=$(mktemp -d)
trap 'rm -rf "$download"' EXIT
gh release download "$tag" --repo "$repo" --dir "$download"
for asset in "${assets[@]}"; do cmp "dist/$asset" "$download/$asset"; done
bash "$0" verify-tag
echo "Verified draft $tag; curate the generated notes and publish manually."
