package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type options struct {
	onto        string
	fetch       bool
	push        bool
	allowOrigin bool
	gitDir      string
	workTree    string
}

func main() {
	var opts options

	flag.StringVar(&opts.onto, "onto", "", "Target ref to rebase onto (default: origin/HEAD)")
	flag.BoolVar(&opts.fetch, "fetch", false, "Force fetching required remotes even if fetched recently")
	flag.BoolVar(&opts.push, "push", false, "Push to remote after rebase")
	flag.BoolVar(&opts.allowOrigin, "allow-origin", false, "Allow using origin as push remote when current branch tracks origin/<branch>")
	flag.StringVar(&opts.gitDir, "git-dir", "", "Path to the .git directory")
	flag.StringVar(&opts.workTree, "work-tree", "", "Path to the working tree")
	flag.Parse()

	if opts.gitDir != "" {
		os.Setenv("GIT_DIR", opts.gitDir)
	}
	if opts.workTree != "" {
		os.Setenv("GIT_WORK_TREE", opts.workTree)
	}

	if err := run(opts); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run(opts options) error {
	if _, err := gitOutput("rev-parse", "--show-toplevel"); err != nil {
		return fmt.Errorf("not a git repository (or any parent directory): %w", err)
	}

	if err := ensureCleanWorktree(); err != nil {
		return err
	}

	if err := ensureHeadAttached(); err != nil {
		return err
	}

	if err := ensureNoRebaseInProgress(); err != nil {
		return err
	}

	curr := "HEAD"
	var fullUpstream string
	for {
		var err error
		fullUpstream, err = gitOutput("rev-parse", "--symbolic-full-name", curr+"@{u}")
		if err != nil {
			return fmt.Errorf("branch %q has no upstream (tracking) branch configured", curr)
		}
		fullUpstream = strings.TrimSpace(fullUpstream)
		if strings.HasPrefix(fullUpstream, "refs/remotes/") {
			break
		}
		if !strings.HasPrefix(fullUpstream, "refs/heads/") {
			return fmt.Errorf("unexpected upstream ref for %q: %q", curr, fullUpstream)
		}
		curr = strings.TrimPrefix(fullUpstream, "refs/heads/")
	}

	upstreamShort, err := gitOutput("rev-parse", "--abbrev-ref", fullUpstream)
	if err != nil {
		return fmt.Errorf("failed to get short name for %q: %w", fullUpstream, err)
	}
	pushRemote, pushBranch, err := splitRemoteRef(strings.TrimSpace(upstreamShort))
	if err != nil {
		return fmt.Errorf("cannot determine push remote from upstream ref %q: %w", strings.TrimSpace(upstreamShort), err)
	}

	if opts.push && pushRemote == "origin" && !opts.allowOrigin {
		return errors.New("current branch tracks origin/<branch>; pass --allow-origin to allow pushing to origin")
	}

	warnIfEditorNotVSCode()

	targetRef := "origin/HEAD"
	if strings.TrimSpace(opts.onto) != "" {
		targetRef = strings.TrimSpace(opts.onto)
	}

	if targetRef == "origin/HEAD" {
		if err := gitInteractive("remote", "set-head", "origin", "--auto"); err != nil {
			return fmt.Errorf("failed to resolve origin/HEAD (git remote set-head origin --auto): %w", err)
		}
	}

	remotes, err := gitRemotes()
	if err != nil {
		return err
	}

	remotesToFetch := map[string]struct{}{}
	remotesToFetch[pushRemote] = struct{}{}
	if targetRef == "origin/HEAD" {
		remotesToFetch["origin"] = struct{}{}
	} else if targetRemote := impliedRemote(remotes, targetRef); targetRemote != "" {
		remotesToFetch[targetRemote] = struct{}{}
	}

	shouldFetch := opts.fetch
	if !shouldFetch {
		recent, err := fetchHeadIsRecent(time.Hour)
		if err != nil {
			return err
		}
		shouldFetch = !recent
	}

	if ok, _ := gitRefExists(fmt.Sprintf("refs/remotes/%s/%s", pushRemote, pushBranch)); !ok {
		// Spec: if the local tracking ref does not exist, we fetch (same effect as --fetch).
		shouldFetch = true
	}

	if !shouldFetch {
		// If the target cannot be resolved locally, try fetching when it implies a remote.
		if ok, _ := gitRevParseVerify(targetRef); !ok {
			if targetRef == "origin/HEAD" || impliedRemote(remotes, targetRef) != "" {
				shouldFetch = true
			}
		}
	}

	if shouldFetch {
		for remote := range remotesToFetch {
			if err := gitInteractive("fetch", remote); err != nil {
				return fmt.Errorf("git fetch %s failed: %w", remote, err)
			}
		}
	}

	if opts.push {
		if err := ensurePushRemoteNotUpdatedSinceFetch(pushRemote, pushBranch); err != nil {
			return err
		}
	}

	mergeBase, err := gitOutput("merge-base", targetRef, "HEAD")
	if err != nil {
		return fmt.Errorf("failed to compute merge base between %q and HEAD: %w", targetRef, err)
	}
	mergeBase = strings.TrimSpace(mergeBase)

	if err := gitInteractive("rebase", "-i", "--onto", targetRef, mergeBase); err != nil {
		return fmt.Errorf("rebase failed: %w", err)
	}

	if opts.push {
		pushSpec := fmt.Sprintf("HEAD:refs/heads/%s", pushBranch)
		if err := gitInteractive("push", "--force-with-lease", pushRemote, pushSpec); err != nil {
			return fmt.Errorf("push failed: %w", err)
		}

		fmt.Printf("Rebase successful. Pushed %s to %s/%s with --force-with-lease.\n", currentBranchName(), pushRemote, pushBranch)
	} else {
		fmt.Println("Rebase successful.")
	}
	return nil
}

func ensureCleanWorktree() error {
	status, err := gitOutput("status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		return errors.New("working tree is not clean (git status --porcelain is not empty)")
	}
	return nil
}

func ensureHeadAttached() error {
	if _, err := gitOutput("symbolic-ref", "-q", "HEAD"); err != nil {
		return errors.New("HEAD is detached")
	}
	return nil
}

func ensureNoRebaseInProgress() error {
	paths := []string{"rebase-apply", "rebase-merge"}
	for _, p := range paths {
		gitPath, err := gitOutput("rev-parse", "--git-path", p)
		if err != nil {
			return err
		}
		gitPath = strings.TrimSpace(gitPath)
		if gitPath == "" {
			continue
		}
		if st, err := os.Stat(gitPath); err == nil && st.IsDir() {
			return errors.New("a rebase is already in progress")
		}
	}
	return nil
}

func ensurePushRemoteNotUpdatedSinceFetch(pushRemote, pushBranch string) error {
	remoteBranchRef := fmt.Sprintf("refs/remotes/%s/%s", pushRemote, pushBranch)
	localSHA, err := gitOutput("rev-parse", remoteBranchRef)
	if err != nil {
		return fmt.Errorf("cannot read local tracking ref %s: %w", remoteBranchRef, err)
	}
	localSHA = strings.TrimSpace(localSHA)

	remoteSHA, err := gitLsRemoteHead(pushRemote, pushBranch)
	if err != nil {
		return err
	}
	if remoteSHA != localSHA {
		return fmt.Errorf("upstream branch %s/%s was updated since last fetch; aborting to avoid unsafe force push (retry with --fetch)", pushRemote, pushBranch)
	}

	return nil
}

func warnIfEditorNotVSCode() {
	editor := strings.TrimSpace(os.Getenv("GIT_SEQUENCE_EDITOR"))
	if editor == "" {
		if v, _ := gitOutput("config", "--get", "sequence.editor"); strings.TrimSpace(v) != "" {
			editor = strings.TrimSpace(v)
		}
	}
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("GIT_EDITOR"))
	}
	if editor == "" {
		if v, _ := gitOutput("config", "--get", "core.editor"); strings.TrimSpace(v) != "" {
			editor = strings.TrimSpace(v)
		}
	}
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}

	if editor == "" {
		fmt.Fprintln(os.Stderr, "Warning: no editor configured via GIT_SEQUENCE_EDITOR/sequence.editor/GIT_EDITOR/core.editor/EDITOR")
		return
	}

	e := strings.ToLower(editor)
	if !strings.Contains(e, "code") {
		fmt.Fprintf(os.Stderr, "Warning: editor does not look like VS Code: %q\n", editor)
	}
}

func gitRemotes() (map[string]struct{}, error) {
	out, err := gitOutput("remote")
	if err != nil {
		return nil, err
	}
	res := map[string]struct{}{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		res[line] = struct{}{}
	}
	return res, nil
}

func impliedRemote(remotes map[string]struct{}, ref string) string {
	idx := strings.IndexByte(ref, '/')
	if idx <= 0 {
		return ""
	}
	remote := ref[:idx]
	if _, ok := remotes[remote]; ok {
		return remote
	}
	return ""
}

func splitRemoteRef(upstream string) (remote string, branch string, err error) {
	idx := strings.IndexByte(upstream, '/')
	if idx <= 0 || idx == len(upstream)-1 {
		return "", "", errors.New("expected <remote>/<branch>")
	}
	return upstream[:idx], upstream[idx+1:], nil
}

func fetchHeadIsRecent(window time.Duration) (bool, error) {
	p, err := gitOutput("rev-parse", "--git-path", "FETCH_HEAD")
	if err != nil {
		return false, err
	}
	p = strings.TrimSpace(p)
	if p == "" {
		return false, nil
	}
	st, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return time.Since(st.ModTime()) <= window, nil
}

func gitRefExists(fullRef string) (bool, error) {
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", fullRef)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 0 {
			return false, nil
		}
	}
	return false, err
}

func gitRevParseVerify(ref string) (bool, error) {
	cmd := exec.Command("git", "rev-parse", "--verify", ref)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 0 {
			return false, nil
		}
	}
	return false, err
}

func gitLsRemoteHead(remote, branch string) (string, error) {
	ref := fmt.Sprintf("refs/heads/%s", branch)
	out, err := gitOutput("ls-remote", remote, ref)
	if err != nil {
		return "", errors.New("git ls-remote failed (offline/auth/remote issue): aborting")
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", fmt.Errorf("remote branch not found: %s %s", remote, ref)
	}
	scanner := bufio.NewScanner(strings.NewReader(out))
	if !scanner.Scan() {
		return "", fmt.Errorf("unexpected git ls-remote output: %q", out)
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 1 {
		return "", fmt.Errorf("unexpected git ls-remote output line: %q", scanner.Text())
	}
	return fields[0], nil
}

func gitOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Stdin = nil
	b, err := cmd.CombinedOutput()
	out := strings.TrimSpace(string(b))
	if err != nil {
		if out == "" {
			return "", err
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), out)
	}
	return out, nil
}

func gitInteractive(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func currentBranchName() string {
	out, err := gitOutput("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "HEAD"
	}
	return strings.TrimSpace(out)
}
