#!/bin/sh
# Syntax-check the SPA's inline script (the last <script> block of
# static/index.html). There is no JS build step, so this is the only gate.
set -eu
out="$(mktemp -d)/inline.js"
awk '/<script>/{buf="";on=1;next} /<\/script>/{if(on){last=buf};on=0;next} on{buf=buf $0 "\n"} END{printf "%s", last}' \
  "$(dirname "$0")/../static/index.html" > "$out"
[ -s "$out" ] || { echo "no inline script found" >&2; exit 1; }
node --check "$out"
