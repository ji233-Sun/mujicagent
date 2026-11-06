# Retry transient Apple ticket network failures, not signing failures.
# The marker identifies the failing operation in the command's output.
retry_apple_ticket() (
  set -o pipefail
  local marker="$1" attempt status output delay
  local log="$RUNNER_TEMP/apple-ticket.log"
  shift
  for attempt in 1 2 3 4 5; do
    if "$@" 2>&1 | tee "$log"; then
      return 0
    else
      status=$?
    fi
    output="$(cat "$log")"
    if [[ "$attempt" == 5 || "$output" != *"$marker"* || ! "$output" =~ NSURLErrorDomain\ Code=-(1001|1004|1005|1009)([^0-9]|$) ]]; then
      return "$status"
    fi
    delay=$((10 << (attempt - 1)))
    (( delay <= 60 )) || delay=60
    echo "::warning::Apple ticket network error; retrying $* ($attempt/5) in ${delay}s."
    sleep "$delay"
  done
)
