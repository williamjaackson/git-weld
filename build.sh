#!/bin/sh
set -e
bun build src/index.ts --compile --outfile git-weld --minify
codesign --remove-signature ./git-weld 2>/dev/null || true
codesign -f -s - ./git-weld
