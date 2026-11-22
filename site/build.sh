#!/bin/sh
# Rebuild site/pages/architecture/index.html from the repo's own sources:
# README.md, docs/quickstart.md, docs/principles.md and site/build/data.
# Needs python3 only. Run it from anywhere; commit the result.
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cp "$root/site/build/data/base.json" "$work/final.json"
cp "$root/site/build/data/template.html" "$work/tpl.html"
cp "$root/site/build/data/feature_details.json" "$root/site/build/data/conductor-tab.html" "$work/"
export SITE_ROOT="$root" SITE_WORK="$work"
cd "$root/site/build"
# Content steps first (they edit final.json), then the page steps (they edit the template), in this order.
for step in model protocols newcomer layered proposed features \
            patch_tpl model_tpl newcomer_tpl layered_tpl proposed_tpl features_tpl \
            restructure regroup quickstart_tpl principles_tpl reorder readme_bits \
            keep_arch_tab slim public_gwnet; do
  python3 -I -c "import sys; sys.path.insert(0, '.'); exec(open('$step.py').read())" >/dev/null
done
python3 -I inject.py
