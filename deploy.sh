#!/usr/bin/env bash
# The ONLY supported way to deploy VaultReader. Builds the image once and
# recreates every container that runs it, so all instances stay on the
# same code. Stamps the git SHA (suffix -dirty for uncommitted changes).
set -euo pipefail
cd "$(dirname "$0")"
sha=$(git rev-parse --short HEAD)
git diff --quiet HEAD -- . || sha="${sha}-dirty"
services="vaultreader"
if [ -f "$HOME/docker/stacks/office/compose/vaultreader-jll.yml" ]; then
  services="vaultreader vaultreader-jll"
fi
VR_GIT_SHA="$sha" bash -lic "cd ~/docker && dc-office-up -d --build $services"
echo "deployed $sha → $services"
