# Sourced only while producing the initial signed checkpoint.
: "${MACOS_CERTIFICATE_P12:?}" "${MACOS_CERTIFICATE_PASSWORD:?}" "${MYGO_UPDATER_PRIVATE_KEY:?}"
umask 077
keychain="$RUNNER_TEMP/mujicagent-signing.keychain-db"
password="$(openssl rand -hex 32)"
echo "::add-mask::$password"
security create-keychain -p "$password" "$keychain"
security set-keychain-settings -lut 21600 "$keychain"
security unlock-keychain -p "$password" "$keychain"
printf '%s' "$MACOS_CERTIFICATE_P12" | base64 --decode > "$RUNNER_TEMP/certificate.p12"
security import "$RUNNER_TEMP/certificate.p12" -k "$keychain" -P "$MACOS_CERTIFICATE_PASSWORD" -T /usr/bin/codesign
security set-key-partition-list -S apple-tool:,apple: -s -k "$password" "$keychain"
security list-keychains -d user -s "$keychain" login.keychain-db
MACOS_SIGNING_IDENTITY="$(security find-identity -v -p codesigning "$keychain" | awk '/"Developer ID Application:/ {print $2}')"
if [[ ! "$MACOS_SIGNING_IDENTITY" =~ ^[0-9A-Fa-f]{40}$ ]]; then
  echo '::error::The P12 must contain exactly one valid Developer ID Application identity.'
  exit 1
fi
umask 022
