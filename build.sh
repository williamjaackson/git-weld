#!/bin/sh
set -e
bun build src/index.ts --compile --outfile git-weld --minify
codesign -f -s - ./git-weld
