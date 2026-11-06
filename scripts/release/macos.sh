#!/bin/bash
set -euo pipefail
source "scripts/release/retry-apple.sh"

# Checkpoints belong to this run and its immutable source commit.
restore() {
  local name="$1" destination="$2" found
  found="$(gh api "repos/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID/artifacts?per_page=100" --jq ".artifacts[] | select(.name == \"$name\") | .expired")" || exit $?
  [[ -n "$found" ]] || return 3
  [[ "$found" == false ]] || { echo "::error::Checkpoint expired: $name"; exit 1; }
  gh run download "$GITHUB_RUN_ID" --repo "$GITHUB_REPOSITORY" --name "$name" --dir "$destination" || exit $?
}

verify_build() {
  (cd build-state && shasum -a 256 -c SHA256SUMS)
  jq -e --arg sha "$GITHUB_SHA" --arg run "$GITHUB_RUN_ID" \
    '.source == $sha and .run == $run' "build-state/state.json" >/dev/null
  cp "build-state/mygo.json" "mygo.json"
  cp "build-state/CHANGELOG.md" "CHANGELOG.md"
  VERSION="$(jq -r .version "mygo.json")"
}

load_build() {
  restore release-build build-state
  verify_build
}

notary_auth() {
  : "${APPLE_API_KEY_ID:?}" "${APPLE_API_ISSUER_ID:?}" "${APPLE_API_PRIVATE_KEY:?}"
  umask 077
  printf '%s' "$APPLE_API_PRIVATE_KEY" > "$RUNNER_TEMP/notary-key.p8"
  umask 022
  NOTARY_AUTH=(--key "$RUNNER_TEMP/notary-key.p8" --key-id "$APPLE_API_KEY_ID" --issuer "$APPLE_API_ISSUER_ID")
}

unpack_arch() {
  mkdir -p "work/$ARCH"
  tar -xzf "build-state/$ARCH.tar.gz" -C "work/$ARCH"
  PLATFORM="work/$ARCH/darwin-$ARCH"
  DMG="$PLATFORM/mujicagent-$VERSION-$ARCH.dmg"
  APP="$PLATFORM/MujicAgent.app"
  DMG_SHA="$(shasum -a 256 "$DMG" | awk '{print $1}')"
}

verify_submission() {
  jq -e --arg sha "$GITHUB_SHA" --arg version "$VERSION" --arg arch "$ARCH" --arg digest "$DMG_SHA" \
    '.source == $sha and .version == $version and .arch == $arch and .sha256 == $digest
     and (.id | test("^[0-9a-fA-F-]{36}$"))' "submit-state/submission.json" >/dev/null
}

case "$1" in
build)
  if restore release-build build-state; then
    verify_build
    echo "cached=true" >> "$GITHUB_OUTPUT"
    exit 0
  else
    [[ $? == 3 ]] || exit 1
  fi
  # A re-run before this checkpoint exists still builds the original commit.
  version="$(go run ./scripts/release next "$BUMP")"
  previous="$(gh api "repos/$GITHUB_REPOSITORY/releases" --jq '[.[] | select(.draft == false and .prerelease == false)][0].tag_name // empty')"
  args=(-f "tag_name=v$version" -f "target_commitish=$GITHUB_SHA")
  range=HEAD
  if [[ -n "$previous" ]]; then
    args+=(-f "previous_tag_name=$previous")
    range="$previous..HEAD"
  fi
  mkdir -p build-state
  gh api --method POST "repos/$GITHUB_REPOSITORY/releases/generate-notes" "${args[@]}" --jq .body > "build-state/release-notes.md"
  printf '\n### Commits\n\n' >> "build-state/release-notes.md"
  git log "$range" --pretty=format:'- %s (%h)' --no-merges --invert-grep --grep='^chore(release):' >> "build-state/release-notes.md"
  printf '\n' >> "build-state/release-notes.md"
  go run ./scripts/release prepare "$version" "build-state/release-notes.md"
  jq -e '.updates.deltas == 0' "mygo.json" >/dev/null
  source "scripts/release/signing.sh"
  for arch in arm64 amd64; do
    go tool mygo build -skip-notarize -sign "$MACOS_SIGNING_IDENTITY" -platform "darwin/$arch"
    codesign --verify --deep --strict "build/darwin-$arch/MujicAgent.app"
    codesign --verify --strict "build/darwin-$arch/mujicagent-$version-$arch.dmg"
    test -s "build/darwin-$arch/update-darwin-$arch.json"
    tar -czf "build-state/$arch.tar.gz" -C build "darwin-$arch"
  done
  cp "mygo.json" "CHANGELOG.md" build-state/
  jq -n --arg source "$GITHUB_SHA" --arg run "$GITHUB_RUN_ID" --arg version "$version" \
    '{source: $source, run: $run, version: $version}' > "build-state/state.json"
  (cd build-state && shasum -a 256 *.tar.gz mygo.json CHANGELOG.md release-notes.md state.json > SHA256SUMS)
  ;;
submit)
  load_build
  unpack_arch
  if restore "release-submission-$ARCH" submit-state; then
    verify_submission
    echo "cached=true" >> "$GITHUB_OUTPUT"
    exit 0
  else
    [[ $? == 3 ]] || exit 1
  fi
  notary_auth
  mkdir -p submit-state
  # Do not wait here: persist the ID before any polling or ticket operations.
  status=0
  xcrun notarytool submit "$DMG" "${NOTARY_AUTH[@]}" --output-format json > "$RUNNER_TEMP/submission-response.json" || status=$?
  id="$(jq -er '.id | select(test("^[0-9a-fA-F-]{36}$"))' "$RUNNER_TEMP/submission-response.json")" || {
    echo "::error::Submission returned no ID (exit $status). Check Apple submission history before retrying."
    exit 1
  }
  jq -n --arg id "$id" --arg source "$GITHUB_SHA" --arg version "$VERSION" --arg arch "$ARCH" --arg digest "$DMG_SHA" \
    '{id: $id, source: $source, version: $version, arch: $arch, sha256: $digest}' > "submit-state/submission.json"
  echo "Apple submission: $id"
  ;;
finalize)
  load_build
  if restore "release-final-$ARCH" final-state; then
    (cd final-state && shasum -a 256 -c SHA256SUMS)
    echo "cached=true" >> "$GITHUB_OUTPUT"
    exit 0
  else
    [[ $? == 3 ]] || exit 1
  fi
  unpack_arch
  restore "release-submission-$ARCH" submit-state
  verify_submission
  notary_auth
  id="$(jq -r .id "submit-state/submission.json")"
  mkdir -p notary-logs final-state
  # A timeout leaves the same submission processing; a re-run waits on this ID.
  status=0
  xcrun notarytool wait "$id" "${NOTARY_AUTH[@]}" --timeout 20m --output-format json > "notary-logs/status.json" || status=$?
  result="$(jq -r '.status // empty' "notary-logs/status.json" 2>/dev/null || true)"
  if [[ "$result" != Accepted ]]; then
    xcrun notarytool log "$id" "${NOTARY_AUTH[@]}" "notary-logs/apple.json" || true
    echo "::error::Submission $id: ${result:-unknown} (exit $status). In Progress/network failure: re-run waits on this ID. Invalid/Rejected: inspect the Apple log and fix the build."
    exit 1
  fi
  xcrun notarytool log "$id" "${NOTARY_AUTH[@]}" "notary-logs/apple.json" || true
  for target in "$APP" "$DMG"; do
    retry_apple_ticket 'The staple and validate action failed!' xcrun stapler staple "$target"
    retry_apple_ticket 'The validate action failed!' xcrun stapler validate "$target"
    codesign --verify --deep --strict "$target"
  done
  archive="mujicagent-$VERSION-darwin-$ARCH.tar.gz"
  # Avoid AppleDouble entries; the app ticket lives in Contents/CodeResources.
  COPYFILE_DISABLE=1 tar --format pax -czf "final-state/$archive" -C "$PLATFORM" MujicAgent.app
  cp "$PLATFORM/update-darwin-$ARCH.json" final-state/
  go run ./scripts/release sign-update "final-state/update-darwin-$ARCH.json" "final-state/$archive"
  mkdir -p "work/verify-$ARCH"
  tar -xzf "final-state/$archive" -C "work/verify-$ARCH"
  codesign --verify --deep --strict "work/verify-$ARCH/MujicAgent.app"
  retry_apple_ticket 'The validate action failed!' xcrun stapler validate "work/verify-$ARCH/MujicAgent.app"
  cp "$DMG" final-state/
  (cd final-state && shasum -a 256 *.dmg *.tar.gz *.json > SHA256SUMS)
  ;;
publish)
  load_build
  for arch in arm64 amd64; do
    restore "release-final-$arch" "final-$arch"
    (cd "final-$arch" && shasum -a 256 -c SHA256SUMS)
  done
  tag="v$VERSION"
  git config user.name 'github-actions[bot]'
  git config user.email '41898282+github-actions[bot]@users.noreply.github.com'
  git add mygo.json CHANGELOG.md
  tree="$(git write-tree)"
  git fetch origin main --tags
  if git rev-parse --verify "refs/tags/$tag" >/dev/null 2>&1; then
    # Resume only our exact release tree with our original source as its parent.
    [[ "$(git rev-parse "$tag^{tree}")" == "$tree" ]]
    [[ "$(git rev-parse "$tag^")" == "$GITHUB_SHA" ]]
  else
    [[ "$(git rev-parse origin/main)" == "$GITHUB_SHA" ]] || {
      echo "::error::main advanced. Start a new release run; this run cannot change its source."
      exit 1
    }
    git commit -m "chore(release): $tag"
    git tag -a "$tag" -m "MujicAgent $VERSION"
    git push --atomic origin HEAD:main "refs/tags/$tag"
  fi
  # Query the collection so permission/network errors cannot look like absence.
  releases="$(gh api --paginate "repos/$GITHUB_REPOSITORY/releases" --jq ".[] | select(.tag_name == \"$tag\") | .draft")"
  if [[ "$releases" == false ]]; then
    echo "Release $tag is already published; leaving its assets unchanged."
    exit 0
  fi
  git fetch origin main
  git merge-base --is-ancestor "$tag" origin/main
  [[ "$(git show origin/main:mygo.json | jq -r .version)" == "$VERSION" ]] || {
    echo "::error::main has a different release version; refusing to publish this old checkpoint as latest."
    exit 1
  }
  if [[ -z "$releases" ]]; then
    gh release create "$tag" --verify-tag --draft --title "MujicAgent $VERSION" --notes-file "build-state/release-notes.md"
  fi
  gh release upload "$tag" final-*/*.dmg final-*/*.tar.gz final-*/update-*.json --clobber
  gh release edit "$tag" --draft=false --latest
  ;;
*)
  echo "usage: macos.sh build|submit|finalize|publish" >&2
  exit 2
  ;;
esac
