# HOWTO: Mirror this project's GitHub setup (docs site + CI) on a new repo

This document describes how to set up a new GitHub repository so its **documentation website** and **GitHub Actions** match the layout used by `iansmith/mazarin`. Every command assumes `gh` is on `PATH` at `/opt/homebrew/bin/gh` (Homebrew's default on macOS); on Linux it's typically `/usr/bin/gh`. Adjust to taste.

The instructions are written so another AI agent with `gh` access can follow them mechanically.

## Prereqs (verify before starting)

```bash
# Confirm gh is authenticated to the right account.
gh auth status

# Required token scopes: repo, workflow, admin:public_key (if you need to add
# a deploy key). The output of `gh auth status` lists the scopes.
```

If `gh auth status` shows a different account or missing scopes, log in:

```bash
gh auth login --git-protocol ssh --web
# or refresh scopes:
gh auth refresh --hostname github.com -s repo,workflow
```

## What this setup gives you

- **`/site/`** is a Jekyll source tree at the repo root. Markdown files there become pages on `https://<owner>.github.io/<repo>/`.
- **`.github/workflows/pages.yml`** auto-builds and deploys the site on every push to `master` that touches `site/**` or the workflow file itself, plus on manual dispatch.
- **A `github-pages` environment** with a branch-policy protection rule (only `master` can deploy).
- **GitHub Pages** is configured with `build_type=workflow`, `source.branch=master`, `source.path=/`, HTTPS enforced, no custom 404, no custom domain.
- **`/site/_config.yml`** picks the Jekyll theme and sets the site title/description.
- **A `deploy-site` operator command** (a one-page Markdown command file in `.claude/commands/`) that triggers the workflow manually and watches it run, for cases where the path-filter doesn't match but you still want a deploy.

There's also a separate **Windows build-verification workflow** (`.github/workflows/windows-test.yml`); that's specific to mazarin's "no POSIX shell on the build path" rule and is **not** part of the docs-site setup. Skip it unless your project has the same constraint.

## Step 1 — Create the repository

Skip this if you already have a repo. Otherwise:

```bash
# Create a public repo (mazarin is public so its Pages can be served).
gh repo create <owner>/<name> --public --description "<short description>" --clone

# OR, if you want to push from an existing local clone:
gh repo create <owner>/<name> --public --description "<short description>" --source=. --remote=origin --push
```

Pages on **private** repos requires a paid plan (Team or Enterprise). For OSS use `--public`.

## Step 2 — Lay out the `site/` directory

Create the source tree at the repo root:

```
site/
├── _config.yml      # Jekyll site config: theme, title, description.
├── index.md         # Landing page. Front-matter: layout, title, author.
└── <other>.md       # Each Markdown file becomes a page.
```

`site/_config.yml` (one of the simplest forms — copy this):

```yaml
theme: jekyll-theme-slate
title: <Project Name>
description: <One-line tagline>
```

`jekyll-theme-slate` is a [supported GitHub Pages theme](https://pages.github.com/themes/) — no theme `Gemfile` needed. To pick a different built-in, swap the value (e.g. `minima`, `jekyll-theme-cayman`, `jekyll-theme-architect`).

Every page gets YAML front-matter at the top:

```markdown
---
layout: default
title: My Page
author: <handle>
---

# Real markdown content begins here
```

Internal links use plain Markdown, e.g. `[News](news.md)`. Jekyll resolves them.

Subdirectories (e.g. `site/mancini/`) work fine — they show up as `https://<owner>.github.io/<repo>/mancini/`.

## Step 3 — Add the deploy workflow

Write `.github/workflows/pages.yml` exactly like this (mazarin's working version):

```yaml
name: Deploy to GitHub Pages

on:
  push:
    branches: [master]
    paths:
      - 'site/**'
      - '.github/workflows/pages.yml'
  workflow_dispatch:

permissions:
  contents: read
  pages: write
  id-token: write

concurrency:
  group: "pages"
  cancel-in-progress: false

jobs:
  deploy:
    environment:
      name: github-pages
      url: ${{ steps.deployment.outputs.page_url }}
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Setup Pages
        uses: actions/configure-pages@v4

      - name: Build with Jekyll
        uses: actions/jekyll-build-pages@v1
        with:
          source: ./site
          destination: ./_site

      - name: Upload artifact
        uses: actions/upload-pages-artifact@v3
        with:
          path: './_site'

      - name: Deploy to GitHub Pages
        id: deployment
        uses: actions/deploy-pages@v4
```

**Notes:**

- The path-filter on `site/**` means edits to *just* the workflow YAML deploy too (because `paths:` is OR-ed across the listed globs).
- If the repo's default branch is `main` (not `master`), change `branches: [master]` to `branches: [main]`.
- The `concurrency: pages` block is what prevents two concurrent deploys from racing — keep it.
- The `id-token: write` permission is what lets `actions/deploy-pages@v4` mint the OIDC token for the deploy. Don't omit it.

## Step 4 — Configure GitHub Pages on the repo

The workflow won't deploy until Pages is enabled in `build_type=workflow` mode. Use `gh api`:

```bash
# Enable Pages with workflow-based build (replaces older "deploy from branch" flow).
gh api -X POST repos/<owner>/<name>/pages \
  -f build_type=workflow \
  -f 'source[branch]=master' \
  -f 'source[path]=/'
```

If Pages is already enabled in the legacy "deploy from branch" mode, switch it:

```bash
gh api -X PUT repos/<owner>/<name>/pages \
  -f build_type=workflow
```

Verify:

```bash
gh api repos/<owner>/<name>/pages
# Expect: "build_type":"workflow", "https_enforced":true, "html_url":"https://<owner>.github.io/<name>/"
```

The first deploy fails if you didn't push the workflow file yet — push it, then retry.

## Step 5 — (Optional) Lock the `github-pages` environment to `master`

GitHub auto-creates the `github-pages` environment on first deploy. mazarin's environment has a branch-policy protection rule restricting deploys to `master`. To replicate:

```bash
# Confirm the environment exists (will return 404 until first deploy).
gh api repos/<owner>/<name>/environments

# Restrict deploys to master via custom branch policies.
gh api -X PUT repos/<owner>/<name>/environments/github-pages \
  -F deployment_branch_policy[protected_branches]=false \
  -F deployment_branch_policy[custom_branch_policies]=true

# Then add 'master' as an allowed branch.
gh api -X POST repos/<owner>/<name>/environments/github-pages/deployment-branch-policies \
  -f name=master
```

If your default branch is `main`, substitute `main` for `master` in the last call.

## Step 6 — Trigger the first deploy

Two options:

```bash
# Option A: just push site/ content and watch the path-filter fire the workflow.
git add site/ .github/workflows/pages.yml
git commit -m "Add docs site + Pages workflow"
git push origin master

# Option B: dispatch the workflow manually (good for re-deploys without site changes).
gh workflow run "Deploy to GitHub Pages" --ref master
```

Watch it complete:

```bash
# List recent runs of this workflow.
gh run list --workflow="Deploy to GitHub Pages" --limit 5

# Stream logs of the most recent run (use the run ID from the list).
gh run watch <run-id>
```

When the run reports success, the site is live at `https://<owner>.github.io/<name>/`.

## Step 7 — (Optional) Add a one-shot deploy command

If you use Claude Code or a similar agent, drop this at `.claude/commands/deploy-site.md`:

````markdown
Trigger the GitHub Pages deployment workflow and wait for it to complete.

## Steps

1. Run the deployment:
```
gh workflow run "Deploy to GitHub Pages" --ref master
```

2. Wait a few seconds, then check status:
```
gh run list --workflow="Deploy to GitHub Pages" --limit 1
```

3. If the run is `in_progress`, wait for it to complete:
```
gh run watch <run-id>
```

4. Report the result: success or failure, with the run URL.

The site is published at https://<owner>.github.io/<name>
````

mazarin's actual file uses absolute paths (`/opt/homebrew/bin/gh`) and includes the homepage URL hardcoded — adapt to your repo.

## Verification checklist

Run these to confirm the new repo matches mazarin's setup:

```bash
# 1. Pages config matches.
gh api repos/<owner>/<name>/pages | grep -E '"build_type"|"html_url"|"https_enforced"'

# Expected:
#   "build_type":"workflow"
#   "html_url":"https://<owner>.github.io/<name>/"
#   "https_enforced":true

# 2. Workflow file is on the default branch.
gh api repos/<owner>/<name>/contents/.github/workflows/pages.yml --jq .name

# 3. Environment exists with protection rule.
gh api repos/<owner>/<name>/environments | grep -E '"name":"github-pages"|"protection_rules"'

# 4. Most recent deploy succeeded.
gh run list --workflow="Deploy to GitHub Pages" --limit 1 --json status,conclusion,url

# Expected: "status":"completed", "conclusion":"success"

# 5. Hit the live URL.
curl -sI https://<owner>.github.io/<name>/ | head -1
# Expected: HTTP/2 200
```

## What's intentionally NOT included

The mazarin repo also has `.github/workflows/windows-test.yml` (Windows build verification). That's **not** part of the docs-site setup — it's a project-specific CI check enforcing "the build runs without POSIX shell." Don't copy it unless your project has the same constraint.

mazarin does NOT use:
- A `Gemfile` (relies on supported themes baked into `actions/jekyll-build-pages`).
- A custom domain / `CNAME` file.
- Branch protection on `master` (only the environment-level policy).
- Required reviewers on the `github-pages` environment (admins can bypass).

If your project needs any of those, layer them on after Steps 1–7 are working.

## Troubleshooting

- **"Pages site not found" 404 from `gh api .../pages`** — Pages isn't enabled yet. Run the `gh api -X POST .../pages -f build_type=workflow ...` command from Step 4.
- **First workflow run fails at "Setup Pages"** — usually means Pages was enabled in the legacy "deploy from branch" mode. Switch with the `PUT` call from Step 4.
- **Path-filter not firing** — confirm the file you edited is under `site/**` (not just inside `site/` at the root) and the commit landed on the default branch. Use Option B (manual dispatch) to verify the workflow itself works.
- **404 on a page that exists** — check the file has YAML front-matter (Jekyll won't process Markdown without it).
- **Stale content after deploy** — GitHub Pages CDN caches aggressively. Force-reload (Cmd-Shift-R) or wait ~10 minutes.
