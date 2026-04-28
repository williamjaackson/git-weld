# git-weld

`git-weld` is a Git plugin for managing branch graphs with one or more direct parents.

It is designed for workflows where a branch can depend on multiple sibling branches at once, while still allowing you to view and rebase only the branch-specific work.

## Current Scope

This repository currently implements the local-only workflow:

- local metadata stored in Git config under `weld.*`
- cycle validation and dependency graph traversal
- local synthetic `_weld/*` branches for multi-parent effective bases
- branch-local diffing against the effective base
- automatic pruning of stale weld metadata for deleted branches/parents

Current commands:

- `git weld new <branch>`
- `git weld stack <branch> [<base>] [-c|--create]`
- `git weld unstack <branch> [<base>]`
- `git weld show [<branch>] [--tree]`
- `git weld status`
- `git weld diff [<branch>]`
- `git weld sync [<branch>] [--tree]`

Not implemented yet:

- `git weld ship`
- `git weld pr`
- GitHub integration
- remote synthetic `_weld/*` refs

## Command Behavior

### `git weld new <branch>`

- creates `<branch>` from `master`
- checks out the new branch
- records the branch as weld-managed
- uses implicit `master` as the root, so `master` is not stored as an explicit parent

### `git weld stack <branch> [<base>]`

- adds `[<base>]` as a parent of an existing managed branch
- if `[<base>]` is omitted, it defaults to the current branch
- if the target branch currently has no explicit parents, stacking replaces the implicit `master` root with the new explicit parent

### `git weld stack -c <branch> [<base>]`

- creates `<branch>` from `[<base>]`
- checks out the new branch
- if `[<base>]` is omitted, it defaults to the current branch

### `git weld unstack <branch> [<base>]`

- removes `[<base>]` from the branch's explicit parents
- if the last explicit parent is removed, the branch falls back to implicit `master`

### `git weld show [<branch>]`

- shows the branch and its parent tree
- omits `master` because it is treated as the implicit root

### `git weld show --tree [<branch>]`

- shows both:
  - `(parents)` tree
  - `(children)` tree
- descendants are rooted at the target branch only, so sibling branches under `master` are not included

### `git weld status`

- lists all weld-managed branches
- shows only explicit parents
- branches rooted directly on `master` display as an empty block

### `git weld diff [<branch>]`

- shows only the changes introduced by the branch relative to its current effective base

### `git weld sync [<branch>] [--tree]`

- updates the local branch graph
- rebuilds synthetic `_weld/*` branches as needed
- rebases managed branches onto their current effective base
- auto-prunes stale metadata for deleted branches and deleted parents
- `--tree` also syncs descendants

## Build

```bash
make build
./bin/git-weld help
```

## Install

Build directly into a directory on your `PATH`:

```bash
make install INSTALL_DIR="$HOME/.local/bin"
```

That produces:

```bash
$HOME/.local/bin/git-weld
```

If `~/.local/bin` is not on your `PATH`, add this to `~/.zshrc`:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Then verify:

```bash
git weld help
git weld status
```

## Development

Run tests:

```bash
go test ./...
```

Build:

```bash
go build ./...
```

## Next Steps

Planned Phase 2 work:

- `ship` support for remote review correctness
- `pr` support with GitHub integration via `gh`
- remote synthetic `_weld/*` refs and PR refresh behavior
