#!/usr/bin/env bash

set -euo pipefail

artifactsFile=~/Downloads/artifact.zip

if [[ ! -f "${artifactsFile}" ]]; then
  echo "Artifacts file not found at ${artifactsFile}, download it"
  exit 1
fi

mkdir -p testdata/tmp
unzip -o "${artifactsFile}" -d testdata/tmp
ts=$(stat -c %Y testdata/tmp/news.json)
actualDir="testdata/$(date -d "@${ts}" +%FT%T)"
mv testdata/tmp "${actualDir}"
rm "${artifactsFile}"
