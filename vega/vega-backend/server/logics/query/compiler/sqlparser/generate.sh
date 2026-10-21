#!/bin/sh
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
set -eu

# ANTLR 4.13.2 generator 与 Go runtime v4.13.1 配套；仅生成阶段需要 Java。
script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
antlr_version=4.13.2
antlr_sha256=eae2dfa119a64327444672aff63e9ec35a20180dc5b8090b7a6ab85125df4d76
antlr_cache=${XDG_CACHE_HOME:-${TMPDIR:-/tmp}/vega-query-tools}/antlr
antlr_jar=${ANTLR_JAR:-${antlr_cache}/antlr-${antlr_version}-complete.jar}
if [ ! -f "$antlr_jar" ]; then
    mkdir -p "$(dirname -- "$antlr_jar")"
    download=$(mktemp "${antlr_jar}.XXXXXX")
    trap 'rm -f "$download"' EXIT HUP INT TERM
    curl --fail --location --retry 2 --output "$download" "https://www.antlr.org/download/antlr-${antlr_version}-complete.jar"
    printf '%s  %s\n' "$antlr_sha256" "$download" | sha256sum --check --status
    mv "$download" "$antlr_jar"
    trap - EXIT HUP INT TERM
fi
printf '%s  %s\n' "$antlr_sha256" "$antlr_jar" | sha256sum --check --status
antlr_jar_dir=$(CDPATH='' cd -- "$(dirname -- "$antlr_jar")" && pwd)
antlr_jar="${antlr_jar_dir}/$(basename -- "$antlr_jar")"
mode=${1:-generate}
case "$mode" in
    generate|--check) ;;
    *) printf '%s\n' 'usage: generate.sh [--check]' >&2; exit 2 ;;
esac
output=$(mktemp -d)
trap 'rm -rf "$output"' EXIT HUP INT TERM
cd "$script_dir"
java -Xmx500M -jar "$antlr_jar" -Dlanguage=Go -visitor -no-listener \
    -package generated -o "$output" SqlBase.g4
gofmt -w "$output"/*.go
if [ "$mode" = --check ]; then
    diff -ru generated "$output"
else
    mkdir -p generated
    cp "$output"/* generated/
fi
