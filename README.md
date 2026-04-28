# git-weld

`git-weld` is a Git plugin for managing branch graphs with one or more direct parents.

This repository currently contains the initial Go CLI scaffold for Phase 1.

## Current Status

- Go module scaffolded
- `git-weld` binary entrypoint added
- basic command dispatcher and help output added

## Planned Next Steps

- implement local metadata storage in `git config`
- implement graph traversal and validation
- add local synthetic `_weld/*` refs
- implement `new`, `stack`, `unstack`, `show`, `status`, `diff`, and `sync`

## Build

```bash
make build
./bin/git-weld
```
