#!/usr/bin/env bash
#
# Prevents: a mutable third-party action tag changing CI behavior without any
# commit in this repository.
#
set -euo pipefail

cd "$(dirname "$0")/../.."
status=0
roots=(.github/workflows)
[[ -d .github/actions ]] && roots+=(.github/actions)

set +e
matches="$(rg -n --no-heading 'uses[[:space:]]*:[[:space:]]+' "${roots[@]}" -g '*.yml' -g '*.yaml')"
rg_status=$?
set -e
if [[ "$rg_status" -gt 1 ]]; then
  echo "✗ action-pins: could not inspect workflow and composite-action manifests" >&2
  exit 1
fi

if [[ -n "$matches" ]]; then
  while IFS=: read -r file line body; do
    trimmed="${body#"${body%%[![:space:]]*}"}"
    [[ "$trimmed" == \#* ]] && continue
    value="$(sed -E 's/^.*uses[[:space:]]*:[[:space:]]*//' <<<"$body")"
    value="${value#"${value%%[![:space:]]*}"}"
    if [[ "$value" == \"* ]]; then
      value="${value#\"}"
      value="${value%%\"*}"
    elif [[ "$value" == \'* ]]; then
      value="${value#\'}"
      value="${value%%\'*}"
    else
      value="${value%%[[:space:]#]*}"
    fi

    [[ "$value" == ./* ]] && continue
    if [[ "$value" == docker://* ]]; then
      if [[ ! "$value" =~ @sha256:[0-9a-f]{64}$ ]]; then
        echo "✗ action-pins: $file:$line uses a mutable Docker action reference" >&2
        status=1
      fi
      continue
    fi

    ref="${value##*@}"
    if [[ "$value" != *@* || ! "$ref" =~ ^[0-9a-f]{40}$ ]]; then
      echo "✗ action-pins: $file:$line uses mutable external ref $value" >&2
      status=1
    fi
  done <<<"$matches"
fi

[[ "$status" -eq 0 ]] || exit 1
echo "✓ action-pins: external actions use immutable commit SHAs."
