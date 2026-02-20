#!/bin/sh
# Remove log files and local DBs from git tracking (they stay on disk).
# Run from repo root: ./scripts/untrack-logs-and-dbs.sh

cd "$(dirname "$0")/.." || exit 1

TO_REMOVE=""
for f in $(git ls-files); do
  case "$f" in
    *.log) TO_REMOVE="$TO_REMOVE $f" ;;
    *.bolt.db) TO_REMOVE="$TO_REMOVE $f" ;;
    .cursor/debug.log) TO_REMOVE="$TO_REMOVE $f" ;;
    .cursor/*.log) TO_REMOVE="$TO_REMOVE $f" ;;
    omnicloud/omnicloud.log) TO_REMOVE="$TO_REMOVE $f" ;;
    omnicloud/*.db) TO_REMOVE="$TO_REMOVE $f" ;;
  esac
done

if [ -z "$TO_REMOVE" ]; then
  echo "No log or DB files are tracked. Nothing to do."
  exit 0
fi

echo "Removing from git (files stay on disk):"
for f in $TO_REMOVE; do
  [ -n "$f" ] && echo "  $f" && git rm --cached "$f" 2>/dev/null
done
echo "Done. Commit the change with: git commit -m 'chore: stop tracking log/DB files'"
echo "These paths are in .gitignore so they will not be added again."
