#!/bin/sh
# Download verified Backlot releases using stock macOS/Linux tools.
set -eu
umask 077
fail() { printf '%s\n' "backlot installer: $*" >&2; exit 1; }
version=
destination=${HOME:?}/.local/bin/backlot
state=
seen=
while [ "$#" -gt 0 ]; do
    case $1 in
        --version|--destination|--state-dir)
            [ "$#" -ge 2 ] && [ -n "$2" ] || fail "$1 requires a value"
            case " $seen " in *" $1 "*) fail "duplicate $1" ;; esac; seen="$seen $1"
            case $1 in
                --version) version=$2 ;;
                --destination) destination=$2 ;;
                --state-dir) state=$2 ;;
            esac
            shift 2 ;;
        --help) printf '%s\n' 'Usage: sh install.sh [--version v0.X.Y] [--destination /absolute/path/backlot] [--state-dir /absolute/private/state]' 'Exact versions are fresh-install only; existing installs update to latest stable.'; exit 0 ;;
        *) fail 'unknown argument; use --help' ;;
    esac
done
case $destination in /*) ;; *) fail 'destination must be absolute' ;; esac
[ ! -L "$destination" ] || fail 'destination must not be a symlink'
if [ -n "$version" ] && [ -e "$destination" ]; then fail '--version is fresh-install only'; fi
if [ -n "$state" ]; then case $state in /*) ;; *) fail 'state-dir must be absolute' ;; esac; fi
case $(uname -s)/$(uname -m) in
    Darwin/arm64) platform=darwin_arm64; checksum=shasum ;;
    Linux/x86_64) platform=linux_amd64; checksum=sha256sum ;;
    *) fail 'supported targets: macOS ARM64 and Linux amd64' ;;
esac
work=$(mktemp -d "${TMPDIR:-/tmp}/backlot-install.XXXXXXXX")
stage=
cleanup() { rm -rf "$work"; if [ -n "$stage" ]; then rm -rf "$stage"; fi; }
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
fetch() { curl --proto '=https' --tlsv1.2 --fail --location --silent --show-error --connect-timeout 15 --max-time 180 "$1" -o "$2"; }
base=https://github.com/octacian/backlot/releases
if [ -z "$version" ]; then
    selected=$(curl --proto '=https' --tlsv1.2 --fail --location --silent --show-error --connect-timeout 15 --max-time 60 -o /dev/null -w '%{url_effective}' "$base/latest")
    case $selected in "$base"/tag/*) version=${selected##*/} ;; *) fail 'cannot select latest stable release' ;; esac
fi
case $version in *[!v0-9.]*|'') fail 'version must be a canonical stable v0.X.Y tag' ;; esac
printf '%s\n' "$version" | LC_ALL=C grep -Eq '^v0\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || fail 'version must be a canonical stable v0.X.Y tag'
asset=backlot_${version}_${platform}.tar.gz
fetch "$base/download/$version/$asset" "$work/archive.tar.gz"
fetch "$base/download/$version/backlot_${version}_checksums.txt" "$work/checksums"
expected=$(awk -v name="$asset" '$2 == name {count++; hash=$1; if (NF != 2) bad=1} END {if (bad || count != 1 || length(hash) != 64 || hash ~ /[^0-9a-f]/) exit 1; print hash}' "$work/checksums") || fail 'missing or ambiguous checksum'
if [ "$checksum" = shasum ]; then actual=$(shasum -a 256 "$work/archive.tar.gz"); else actual=$(sha256sum "$work/archive.tar.gz"); fi
[ "${actual%% *}" = "$expected" ] || fail 'archive checksum mismatch'
tar -tzf "$work/archive.tar.gz" > "$work/members"
printf '%s\n' LICENSE THIRD_PARTY_NOTICES.md backlot | LC_ALL=C sort > "$work/expected"
LC_ALL=C sort "$work/members" > "$work/sorted"
cmp -s "$work/expected" "$work/sorted" || fail 'unexpected archive members'
tar -tvzf "$work/archive.tar.gz" > "$work/types"
[ "$(awk 'substr($0,1,1) != "-" {bad=1} END {print bad+0}' "$work/types")" = 0 ] || fail 'archive contains non-regular entries'
tar -xzf "$work/archive.tar.gz" -C "$work" backlot
chmod 700 "$work/backlot"
# Old releases cannot safely provide the lock-held replacement capability.
"$work/backlot" install-replace --help > /dev/null 2>&1 || fail 'release lacks installer support; use manual verified archive installation'
parent=$(dirname "$destination")
mkdir -p "$parent"
stage=$(mktemp -d "$parent/.backlot-install.XXXXXXXX")
cp "$work/backlot" "$stage/backlot"
chmod 755 "$stage/backlot"
if [ -n "$state" ]; then
    "$work/backlot" install-replace --stage "$stage/backlot" --destination "$destination" --version "$version" --state-dir "$state"
else
    "$work/backlot" install-replace --stage "$stage/backlot" --destination "$destination" --version "$version"
fi
printf 'Backlot %s installed at %s\n' "$version" "$destination"
selected=$(command -v backlot || :)
if [ "$selected" != "$destination" ]; then printf 'PATH selects %s; add %s to PATH or invoke %s explicitly.\n' "${selected:-no backlot}" "$parent" "$destination"; fi
