# ADR-0027: The website is a Hugo site in `site/`, deployed to GitHub Pages, presenting only the software, installation, and usage

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

- The website lives in `site/` as a Hugo project with its own small
  layouts and stylesheet. No theme dependency (no submodule, no Hugo
  module), no JavaScript, no generator of our own.
- It contains a landing page, an Install page and a Usage page, nothing
  else: no ADRs, no contributor documentation, no changelog. The README
  stays the canonical document; `site/content/install.md` and
  `site/content/usage.md` mirror its Installation and Usage sections and
  are kept in sync by hand (see the "Where facts live" table in
  `AGENTS.md`).
- The `demo.gif` at the repository root is mounted into the site rather
  than copied, so there is one demo.
- The Website workflow builds with a pinned Hugo version and deploys with
  the official GitHub Pages actions (`upload-pages-artifact` /
  `deploy-pages`, Pages source "GitHub Actions"). It runs on pushes to
  `main` that touch `site/` or `demo.gif`, on every published release, and
  on manual dispatch. The latest git tag is passed in as
  `HUGO_PARAMS_VERSION` so the landing page shows the current release.

## Consequences

- Changing commands, keys, config keys or installation steps means
  updating the README and the two site pages in the same commit.
- The site never needs a deploy step from a developer machine; `make site`
  and `make site-serve` exist for local preview only. Hugo output
  (`site/public/`, `site/resources/`) is git-ignored.
- Rendering is limited to what Hugo's Goldmark offers; the pages use raw
  `<kbd>` HTML, so `unsafe` rendering stays enabled.

## Rejected alternatives

- **Rendering the README (and ADRs) into the site with a purpose-built
  generator** (2026-09-03). It kept everything in sync automatically but
  meant owning a generator, and it would have published contributor
  material and architecture records that do not belong on a product page.
- **A third-party Hugo theme.** Themes bring a submodule or module fetch
  into every build and far more surface than three pages need.
- **Jekyll via the built-in Pages build.** Slower builds and a Ruby
  toolchain for local preview, with no benefit over Hugo for a
  hand-written site.
