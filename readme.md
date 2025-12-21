## How it works

### Default behavior (no flags)

Tool git-mj-rebase
- makes sure you have no uncommitted changes (`git status --porcelain` is empty)
- fails if the current branch has no upstream (tracking) branch configured
- fails if `HEAD` is detached
- fails if a rebase is already in progress
- checks that your editor is set to Visual Studio Code (or Insider) (`code` or `code-insiders`) or prints a warning (checks `GIT_SEQUENCE_EDITOR`, `sequence.editor`, `GIT_EDITOR`, `core.editor`, `$EDITOR`)
- uses `origin/HEAD` as the target ref to rebase onto
	- ensures remote 'origin' HEAD name is known (`git remote set-head origin --auto`)
- determines the push remote from the upstream branch of the current branch
	- if the immediate upstream is a local branch, the tool follows the upstream chain until it finds a remote-tracking branch
	- if your current branch is tracking `origin/<branch>`, the tool fails unless `--allow-origin` is set
- fetches changes from the remotes needed for the rebase and push (unless fetched recently or `--fetch` is used)
	- fetches `origin` (because the target is `origin/HEAD`)
	- fetches the push remote (the remote of the upstream branch of the current branch)
- checks that the upstream branch on the push remote was not updated since the last fetch
	- "last fetch" means the last update time of `.git/FETCH_HEAD`
	- implemented by comparing local `refs/remotes/<push-remote>/<branch>` (what your repo last observed) with `git ls-remote <push-remote> <branch>` (what the remote currently has)
	- if the local `refs/remotes/<push-remote>/<branch>` does not exist, the tool fetches (same effect as `--fetch`)
	- if `git ls-remote` fails (offline/auth/remote issues), the tool aborts (fail closed) to avoid an unsafe force push
	- this is a pre-flight check so the later `git push --force-with-lease` has a good chance to succeed
	- this check is only performed if `--push` is enabled
- finds merge base between `origin/HEAD` and your current branch
- runs `git rebase -i --onto origin/HEAD <merge-base>`
- waits for you to finish the rebase in the editor
- if rebase is successful, prints success message

### Details for flags

#### `--push`

- enables pushing to the remote after rebase
- pushes the rebased branch to the upstream branch on the push remote with `--force-with-lease`
- checks if push was successful and prints appropriate message

#### `--onto <ref>`

- changes the target ref to rebase onto
- `--onto` accepts anything that `git rev-parse --verify <ref>` accepts (e.g. `origin/main`, `main`, a tag, or a commit SHA)
- if the target is `origin/HEAD`, the tool still runs `git remote set-head origin --auto`

- fetch behavior with `--onto`
	- if the target is `origin/HEAD`, the tool fetches `origin` (unless fetched recently or `--fetch` is used)
	- if the target is `<remote>/<branch>`, the tool fetches `<remote>` (unless fetched recently or `--fetch` is used)
	- if the target is a local ref / tag / commit SHA, the tool does not fetch for the target

- fetches the target remote when the target ref implies one
	- `--onto <remote>/<branch>` implies `<remote>`
- finds merge base between the target ref and your current branch
- runs `git rebase -i --onto <target> <merge-base>`

#### `--fetch`

- forces fetching the required remotes even if they were fetched recently

#### `--allow-origin`

- allows using `origin` as the push remote when the current branch tracks `origin/<branch>`
- only relevant when `--push` is used
- this is potentially destructive; combined with rebase it will require a force push (`--force-with-lease`)

#### `--git-dir <path>`

- sets the path to the `.git` directory (sets `GIT_DIR` environment variable)

#### `--work-tree <path>`

- sets the path to the working tree (sets `GIT_WORK_TREE` environment variable)

### Fetch recency heuristic

- "recently" means: `.git/FETCH_HEAD` was updated within the last hour
	- this treats any user-initiated fetch as good enough to skip the tool's own fetch
	- when fetch is skipped, the tool proceeds using whatever remote-tracking refs are already present locally
		- this can be conservative: if your local remote-tracking refs are stale, the "remote not updated" check may fail and the tool will abort
		- use `--fetch` to refresh and retry
	- if `.git/FETCH_HEAD` does not exist, the tool fetches
	- `--fetch` forces fetching even if `.git/FETCH_HEAD` is recent

### Prerequisites

- Git remote 'origin' points to the main repository (upstream).
- You have write access to a fork of the main repository. Remote of fork is named whatever you like e.'fork' (all except 'origin' is fine).

## Usage

```bash
git-mj-rebase [--push] [--onto <ref>] [--fetch] [--allow-origin] [--git-dir <path>] [--work-tree <path>] [--help]
```

## Details

- Written in Go
- Single binary, no installation required
- Cross-platform (Windows, macOS, Linux)
- Open source: Apache License 2.0
- Repository: https://github.com/mj41/git-mj-rebase
