---
name: PR description
description: >-
  Draft a concise pull request description for the current branch, following
  this repository's PR template. Use when the user asks to write, draft, or
  generate a PR description or pull request body.
---

# Draft a PR description

Write a short, plain-language description that a non-technical reviewer can
understand. Follow the section headings in `.github/pull_request_template.md`,
in order, but keep each section to one or two short sentences or bullets.
Explain **why** the change matters before **how** it works.

## Formatting

Write the body as full-width Markdown: one line per paragraph or bullet,
however long that line runs in your terminal. GitHub reflows text and does not
need hard wrapping — short, ragged ~80-column lines read strangely in the
rendered issue or PR body. Reserve line breaks for real structure (paragraphs,
bullets, headings, tables). This applies to issue bodies too.

## Workflow

1. Find the base branch (default `origin/main`) and gather evidence:
   `git merge-base`, `git log --oneline <base>..HEAD`, `git diff <base>...HEAD`.
   Read the changed files, not just the diffstat.
2. Build **absolute links** from the origin remote. Normalize
   `git remote get-url origin` to `OWNER/REPO` (strip a trailing `.git` and any
   `git@github.com:` or `https://github.com/` prefix). Link files as
   `https://github.com/OWNER/REPO/blob/<ref>/<path>`, where `<ref>` is the base
   branch; for a file added on this branch, use the current branch name so the
   link resolves immediately. Do **not** copy the template's relative links
   such as `../docs/...` verbatim — rewrite every one as an absolute URL, so it
   works when pasted into the PR body on GitHub.
3. Fill each section in plain language:
   - **Summary** — two or three sentences: what changed and why it matters.
     Avoid code identifiers unless essential, and gloss any unavoidable term in
     a few words.
   - **Decision and spec** — link the ADR(s) and SPEC section(s) touched, or
     state "none needed" and why.
   - **Scope check** — tick a box only when the diff supports it; otherwise
     leave it unticked and append `TODO`.
   - **Tests** — one or two sentences: what proves it works, and whether a test
     fails without the change. Name the integration tier if one is affected.
   - **Docs** — what was updated, or "none".
   - **Risk and rollback**, **Reviewer notes** — one short sentence each, or
     `TODO`.
4. Never invent facts. Where the diff does not tell you something, keep the
   template's placeholder and write `TODO`.
5. Write the body to `${TMPDIR:-/tmp}/atelier-pr.md`. Then print, in order:
   1. a suggested PR title in the repository's conventional-commit style
      (`feat:`, `fix:`, `docs:`, `refactor:`, `chore:`, `test:`), matching the
      actual change and kept under about 72 characters;
   2. a ready-to-run command that uses that title and the body file:
      `gh pr create --title "<title>" --body-file <path>`;
   3. the raw Markdown body, with no surrounding prose and no code fence.

   Keep the body file to the Markdown body only — the title and the command are
   terminal output, not part of the body.

Keep the whole description as short as it can be while still complete; cut
anything a non-technical reviewer does not need.
