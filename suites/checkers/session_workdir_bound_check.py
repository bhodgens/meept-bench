#!/usr/bin/env python3
"""Checker for the session-workdir-bound task (cwd.txt must hold the BOUND
session working directory).

Why a program and not a suite grep
----------------------------------
The bound working directory has no single literal shape. It is whatever
directory the bench binds the task's worktree to, which differs per layout:

  * scratch rig (scripts/scratch-rig.sh):
      /private/tmp/meept-scratch-rig.XXXXXX/worktrees/<task>
      /tmp/meept-scratch-rig.XXXXXX/worktrees/<task>
    (the rig canonicalises its mktemp dir with `pwd -P`, so /private/tmp is
    what actually arrives; /tmp is accepted because macOS symlinks it)
  * default make / --scratch layout:
      $HOME/.meept-bench/<task>
      /tmp/meept-bench/<task>

The checker this replaces used a file_contains pattern that only knew the two
`.meept-bench` shapes, so a correctly-resolved scratch-rig cwd was REJECTED;
its second checker was an exit_zero grep asserting the cwd was not the bench
repo, with that repo's absolute path hardcoded into the suite — a value that
exists on one developer machine and silently degenerates everywhere else.

What is asserted
----------------
  1. cwd.txt holds ONE line that is an ABSOLUTE, EXISTING directory.
  2. That directory is not the operator's home (`$HOME`). meept used to
     resolve the working dir to $HOME when the session/project binding was
     missing, so a home cwd is the bug this task pins.
  3. That directory is not the bench repo checkout — i.e. the main checkout
     the task worktree forked from. The runner's scratch root must live
     OUTSIDE RepoPath (internal/runner/runner.go New), so a cwd of the
     checkout means the agent ran in the repo itself. The "forked from" root
     is read from git metadata (--git-common-dir lives in the MAIN checkout's
     .git), so no developer path is baked in.

Comparisons run on realpath AND on the literal spelling, so a symlinked /tmp
alias cannot smuggle a rejected path through.

Failure semantics: a checker failure prints its reason to stdout (the runner
captures combined output as the checker detail) and exits non-zero.

This file is duplicated at suites/checkers/ and suites/fragments/checkers/
because a trusted_python `script` path resolves relative to the MANIFEST's
own directory and os.OpenRoot refuses to escape it (no `..`, no symlink
escape) — and the fragment must run standalone via `make memory-fragments`.
Keep the two copies byte-identical; a test pins that.
"""

import os
import subprocess
import sys

FILE = "cwd.txt"


def fail(msg):
    print(msg)
    sys.exit(1)


def git(*args):
    """Run git in the checker's cwd (cmd.Dir = the bound worktree).

    Returns stdout, or "" when git is unavailable / not a repo. In a linked
    worktree git honours GIT_DIR/GIT_WORK_TREE, so this answers correctly
    even though cwd is a scratch path rather than the main checkout.
    """
    try:
        out = subprocess.run(
            ["git", *args], capture_output=True, text=True, timeout=5
        )
    except (OSError, subprocess.SubprocessError):
        return ""
    if out.returncode != 0:
        return ""
    return out.stdout.strip()


def rejected_roots():
    """Directories that would mean the session working dir did NOT bind."""
    roots = {}

    home = os.path.realpath(os.path.expanduser("~"))
    roots[home] = "the operator's home"

    # The checkout this worktree forked from: --git-common-dir resolves to
    # <main-checkout>/.git (a linked worktree has no .git of its own), so its
    # parent is the main checkout. When the checker is NOT run inside a
    # worktree the common dir is <cwd>/.git and the parent is the cwd.
    common = git("rev-parse", "--path-format=absolute", "--git-common-dir")
    if common:
        roots.setdefault(
            os.path.realpath(os.path.dirname(common)), "the bench repo checkout"
        )
    return roots


def main():
    # --- read cwd.txt (the checker runs with cmd.Dir = the task worktree) ---
    try:
        with open(FILE, "r", encoding="utf-8") as fh:
            raw = fh.read()
    except FileNotFoundError:
        fail(f"{FILE} is missing — the agent never wrote it")
    except OSError as exc:
        fail(f"{FILE} is unreadable: {exc}")

    lines = raw.splitlines()
    if len(lines) != 1:
        fail(
            f"{FILE} must contain exactly ONE line, the absolute path "
            f"(got {len(lines) if lines else 0} lines)"
        )
    reported = lines[0].strip()
    if not reported:
        fail(
            f"{FILE} is empty — expected the absolute path of the bound "
            "working directory"
        )
    if not reported.startswith("/"):
        fail(f"{FILE} does not contain an absolute path: {reported!r}")
    if not os.path.isdir(reported):
        fail(f"reported working directory does not exist: {reported}")

    reported_real = os.path.realpath(reported)
    reported_lit = reported.rstrip("/")

    for root, label in rejected_roots().items():
        if reported_real == root or reported_lit == root.rstrip("/"):
            fail(f"reported working directory is {label} ({reported})")

    print(f"cwd bound to scratch worktree: {reported}")
    sys.exit(0)


if __name__ == "__main__":
    main()