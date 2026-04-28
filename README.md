# git-weld

`git-weld` is a Git plugin for managing branch graphs with one or more direct parents.

It is designed for workflows where a branch can depend on multiple sibling branches at once, while still allowing you to view and rebase only the branch-specific work.

## Current Scope

This repository implements:

- local metadata stored in Git config under `weld.*`
- configurable main branch and remote settings via `git weld init`
- cycle validation and dependency graph traversal
- local welded bases, materialized internally as `_weld/*` branches when needed
- branch-local diffing against the effective base
- automatic pruning of stale weld metadata for deleted branches/parents
- remote shipping for review correctness
- GitHub PR creation and base refresh through `gh`

Current commands:

- `git weld new <branch>`
- `git weld init [--main <branch>] [--remote <remote>] [--no-remote]`
- `git weld stack <branch> [<base>] [-c|--create]`
- `git weld beside <target> <source>`
- `git weld prepend [-c|--create] <branch> [--beside <branch>]`
- `git weld unstack <branch> [<base>]`
- `git weld drop <branch> [--promote | --cascade | --reparent <branch>] [--remote]`
- `git weld show [<branch>] [--tree]`
- `git weld status [<branch>] [--tree]`
- `git weld diff [<branch>]`
- `git weld sync [<branch>] [--tree] [--local|--remote]`
- `git weld ship [<branch>] [--tree] [--local|--remote]`
- `git weld pr [<branch>] [--title <title>] [--body <body>] [--draft] [--web]`

## Command Behavior

### `git weld new <branch>`

- creates `<branch>` from the configured main branch
- checks out the new branch
- records the branch as weld-managed
- uses the configured main branch as the implicit root, so it is not stored as an explicit parent
- preserves staged, unstaged, and untracked changes across the branch switch

### `git weld init [--main <branch>] [--remote <remote>] [--no-remote]`

- with no flags, prompts for:
  - the main branch
  - the remote name, or `none` to disable remote features
- with flags, configures settings non-interactively
  - `--main <branch>` sets the implicit root branch
  - `--remote <remote>` sets the remote used for sync/ship/pr
  - `--no-remote` disables remote-aware features
- stores those settings in local Git config under `weld.*`
- if remote features are disabled, `git weld ship` and `git weld pr` will error until a remote is configured again

### `git weld stack <branch> [<base>]`

- adds `[<base>]` as a parent of an existing managed branch
- if `[<base>]` is omitted, it defaults to the current branch
- if the target branch currently has no explicit parents, stacking replaces the implicit root with the new explicit parent

### `git weld beside <target> <source>`

- adds the direct parents of `<source>` to `<target>`
- copies direct parents only, not the full ancestor tree
- syncs `<target>` onto its new effective base after updating parents

### `git weld prepend [-c|--create] <branch> [--beside <branch>]`

- creates a new branch and inserts it above the current branch
- without `--beside`:
  - the new branch inherits the current branch's direct parents
  - the current branch is reset to depend only on the new branch
- with `--beside <branch>`:
  - `<branch>` must already be upstream from the current branch
  - the new branch inherits `<branch>`'s direct parents
  - the current branch keeps its existing parents and also adds the new branch
- `-c` / `--create` switches to the new branch after creation

### `git weld stack -c <branch> [<base>]`

- creates `<branch>` from `[<base>]`
- checks out the new branch
- if `[<base>]` is omitted, it defaults to the current branch
- preserves staged, unstaged, and untracked changes across the branch switch

### `git weld unstack <branch> [<base>]`

- removes `[<base>]` from the branch's explicit parents
- if the last explicit parent is removed, the branch falls back to the implicit root branch

### `git weld drop <branch> [--promote | --cascade | --reparent <branch>] [--remote]`

- deletes a weld-managed branch
- if the branch has no downstream branches, no policy flag is required
- if the branch has downstream branches, one of these policies is required:
  - `--promote`
    - downstream branches inherit the dropped branch's direct parents
  - `--cascade`
    - deletes the full downstream weld tree
  - `--reparent <branch>`
    - downstream branches replace the dropped branch with the given direct parent
- after graph changes, affected surviving descendants are rebased onto their new effective bases
- `--remote` also:
  - closes any GitHub PR for the dropped branch
  - deletes the remote branch
  - publishes affected surviving descendants and refreshes their PR bases/bodies if needed

### `git weld show [<branch>]`

- shows the branch and its parent tree
- omits the configured main branch because it is treated as the implicit root

### `git weld show --tree [<branch>]`

- shows both:
  - `(upstream)` tree
  - `(downstream)` tree
- descendants are rooted at the target branch only, so sibling branches under `master` are not included

### `git weld status [<branch>] [--tree]`

- shows status for one managed branch
- defaults to the current branch
- fetches the configured remote quietly before computing status, so remote-only updates are reflected without extra noise
- `--tree` shows the target branch plus its upstream and downstream weld tree
- shows:
  - `upstream`
  - `sync`
  - `ship`
  - `affects`
- `upstream` lists the branch's direct parents, or the configured main branch when the root is implicit
- `sync` shows what `git weld sync` would do for that branch, including transitive upstream changes:
  - `none`
  - `update`
  - `rebase`
  - `update+rebase`
  - `conflicted`
- `ship` shows what `git weld ship` would do for that branch, including upstream branches that would be pushed as part of shipping that branch:
  - `none`
  - `push`
  - `force-push`
  - `sync-first`
  - `conflicted`
- `affects` lists downstream branches that depend on the target branch

### `git weld diff [<branch>]`

- shows only the changes introduced by the branch relative to its current effective base

### `git weld sync [<branch>] [--tree] [--local|--remote]`

- refreshes tracked branches in scope, excluding the configured main branch
- if a tracked branch has remote-only commits, rebases the local branch onto its tracking branch first
- updates the local branch graph
- rebuilds welded bases as needed
- rebases managed branches onto their current effective base
- sync behavior is transitive through upstream branches, so a parent update will mark descendants for rebase
- auto-prunes stale metadata for deleted branches and deleted parents
- `--tree` also syncs descendants
- `--local` skips all remote refresh and syncs against local refs only
- `--remote` also refreshes the configured main branch
- requires a clean working tree before it runs
- if a tracking rebase or weld rebase conflicts, the command exits, aborts the failed rebase, and restores the repo to its starting state

### `git weld ship [<branch>] [--tree] [--local|--remote]`

- requires a configured remote
- runs `sync` first
- pushes the minimum required real branches for the requested branch or tree
- pushes welded bases when a multi-parent review base is required
- refreshes existing GitHub PR bases when `gh` is available
- ship behavior is transitive through upstream branches, so shipping a child branch may also push required parent branches first
- deletes stale remote welded bases after PR bases have been retargeted
- `--local` ships after a local-only sync
- `--remote` ships after a sync that also refreshes the configured main branch

### `git weld pr [<branch>] [--title <title>] [--body <body>] [--draft] [--web]`

- requires a configured remote and `gh`
- runs `ship` first
- uses the real parent as the PR base for single-parent branches
- uses a welded base for multi-parent branches
- supports explicit title/body overrides
- prepends a collapsible `Branch Tree` section to the PR body
  - the section is updated in place on repeated `git weld pr` runs
  - it renders a combined linked branch tree rooted on the PR branch with both `upstream` and `downstream`
  - it includes a `generated by git-weld` footnote
- `--draft` creates a draft PR when a new PR is opened
- `--web` opens the PR in the browser after create/edit

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

## Notes

- Remote features currently assume the configured remote points at a GitHub repository when `gh` is used.
- Existing repositories default to:
  - main branch: `master`
  - remote: `origin`
- Run `git weld init` to override those defaults.
- If a tracking rebase or weld rebase conflicts, Weld exits safely and restores the repo to its starting state.
