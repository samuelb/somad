# ADR-0027: The website is one hand-written static page in `site/`, deployed to GitHub Pages, presenting only the software, installation, and usage

- **Status:** Accepted
- **Date:** 2026-09-03
- **Sources:** `site/`, `.github/workflows/website.yml`, `make site`

## Context

The README is the only documentation, and it mixes user-facing material
(features, installation, usage) with contributor material (hooks,
releasing, dependencies). A project page should present the software to
someone deciding whether to install it, stay current without manual
deploys, and not become a second thing to build and maintain.

## Decision

- The website is a single page: `site/index.html`, `site/style.css` and
  `site/favicon.svg`, written by hand. No static-site generator, no
  theme, no build step, no dependencies. The only script fetches the
  latest release tag from the GitHub API so the page never goes stale
  between releases; the link works without it.
- The page presents the software, installation and usage, nothing else:
  no ADRs, no contributor documentation, no changelog. The README stays
  the canonical document; the page's Install and Usage sections mirror
  its Installation and Usage sections and are kept in sync by hand (see
  the "Where facts live" table in `AGENTS.md`).
- The `demo.gif` at the repository root is copied in at deploy time
  (`make site` stages `site/` plus the GIF into `dist/site`), so there is
  one demo.
- The Website workflow deploys `dist/site` with the official GitHub Pages
  actions (`upload-pages-artifact` / `deploy-pages`, Pages source "GitHub
  Actions") on pushes to `main` that touch `site/` or `demo.gif`, and on
  manual dispatch.

## Consequences

- Changing commands, keys, config keys or installation steps means
  updating the README and `site/index.html` in the same commit.
- Nothing to install to work on the site; `make site-serve` previews it
  with Python's HTTP server. `dist/` is already git-ignored.
- Content that would be tedious to hand-write in HTML (long prose, many
  pages) does not belong on the page; it belongs in the README.

## Rejected alternatives

- **Rendering the README (and ADRs) into the site with a purpose-built
  generator** (2026-09-03). It kept everything in sync automatically but
  meant owning a generator, and it would have published contributor
  material and architecture records that do not belong on a product page.
- **Hugo with a small custom layout, three pages** (2026-09-03). Worked,
  but for a single page a generator, a pinned Hugo version in CI and a
  local Hugo install bought nothing over plain HTML.
- **A third-party Hugo theme** and **Jekyll via the built-in Pages
  build**: more surface and toolchain than one page needs.
