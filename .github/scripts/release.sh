#!/usr/bin/env bash
set -euo pipefail

# Only this script's publish command writes remotely; packaging never does.
tag=${RELEASE_TAG:?RELEASE_TAG is required}
sha=${SOURCE_SHA:?SOURCE_SHA is required}
[[ $tag =~ ^v0\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || { echo "invalid stable release tag: $tag" >&2; exit 1; }
[[ $(git rev-parse HEAD) == "$sha" ]]
[[ $(git rev-parse "refs/tags/$tag^{commit}") == "$sha" ]]
[[ -z $(git status --porcelain) ]] || { echo 'release source is dirty' >&2; exit 1; }
remote=$(git ls-remote origin "refs/tags/$tag" "refs/tags/$tag^{}")
resolved=$(printf '%s\n' "$remote" | tail -1 | cut -f1)
[[ $resolved == "$sha" ]] || { echo 'remote tag differs from event commit' >&2; exit 1; }

case ${1:-} in
  verify-tag) exit 0 ;;
  publish) ;;
  *) echo 'usage: release.sh verify-tag|publish' >&2; exit 1 ;;
esac

repo=${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}
assets=("backlot_${tag}_darwin_arm64.tar.gz" "backlot_${tag}_linux_amd64.tar.gz" "backlot_${tag}_checksums.txt")
for asset in "${assets[@]}"; do test -s "dist/$asset"; done
test -s "docs/releases/$tag.md"
(cd dist && sha256sum --strict -c "backlot_${tag}_checksums.txt")

# A successful paginated listing distinguishes absence from a failed lookup.
releases=$(gh api --paginate "repos/$repo/releases" --jq ".[] | select(.tag_name == \"$tag\") | .id")
[[ -z $releases ]] || {
  echo 'release already exists (including drafts); inspect manually' >&2; exit 1;
}
paths=()
for asset in "${assets[@]}"; do paths+=("dist/$asset"); done
gh release create "$tag" --repo "$repo" --verify-tag --draft --title "$tag" --notes-file "docs/releases/$tag.md" "${paths[@]}"

# Verify the complete uploaded set and bytes before exposing the draft.
uploaded=$(gh release view "$tag" --repo "$repo" --json assets,isDraft,tagName)
jq -e --arg tag "$tag" --argjson expected "$(printf '%s\n' "${assets[@]}" | jq -R . | jq -s .)" \
  '.isDraft and .tagName == $tag and ([.assets[].name] | sort) == ($expected | sort)' <<<"$uploaded" >/dev/null
download=$(mktemp -d)
trap 'rm -rf "$download"' EXIT
gh release download "$tag" --repo "$repo" --dir "$download"
for asset in "${assets[@]}"; do cmp "dist/$asset" "$download/$asset"; done
bash "$0" verify-tag
gh release edit "$tag" --repo "$repo" --draft=false
