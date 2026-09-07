# ADR-0032: The website is a single-page Zola site

- **Status:** Accepted
- **Date:** 2026-09-07
- **Sources:** `site/`, the `site` and `site-serve` Makefile targets,
  `.github/workflows/website.yml`

## Context

The project website was one hand-written HTML page plus a stylesheet,
"built" by copying `site/` and `demo.gif` into `dist/site`. Every
documentation change meant editing tables and paragraphs as raw HTML, while
the same commands, keys, and config keys are maintained as Markdown in the
README. An earlier Hugo version had been dropped on 2026-09-03 because its
multi-page structure was more than a product page needs and plain HTML was
simpler at the time. The page must stay a single product-facing page.

## Decision

- The website is a [Zola](https://www.getzola.org/) project in `site/`,
  still rendering exactly one page. No theme, no Sass, no search index, no
  feeds, no syntax highlighting.
- All copy lives in `site/content/_index.md`. The YAML front matter holds
  the hero, quick start, feature cards, and "How it works" steps; the
  Markdown body holds the Install and Usage prose. Layout is in
  `site/templates/base.html` and `index.html`.
- Structure inside the Markdown (page sections, card grids, two-column
  blocks) comes from Tera components in `site/templates/components.html`.
  This requires Zola 0.23 or newer, which removed shortcodes in favour of
  components.
- Code blocks that need muted comments or `$` prompts stay raw HTML with
  `<span class="c">` / `<span class="p">`; plain blocks are fenced.
- `demo.gif` stays at the repository root (the README embeds it) and is
  symlinked from `site/static/demo.gif`; Zola copies the target.
- CI downloads a pinned Zola release and verifies it against a SHA-256
  recorded in the workflow, following ADR 0027, then runs `make site`.

## Consequences

- The site config, `site/config.toml`, must stay TOML: Zola reads no other
  format for it. Front matter is YAML to match the daemon's own config file.
- Local preview needs `zola` (`brew install zola`), 0.23 or newer.
  `make site-serve` gives live reload on http://127.0.0.1:1111.
- Content is Tera-templated before Markdown: `{{` or `{%` in the copy must
  be wrapped in `{% raw %}` blocks.
- Zola validates internal anchor links against Markdown headings at build
  time. Links to section ids emitted by components (`#install`, `#usage`)
  must be written as raw HTML anchors; links to headings can use Markdown.
- A raw HTML block that must contain blank lines (the config example) has
  to start on its own line after a blank line, or CommonMark folds it into
  the preceding HTML block.
- Upgrading Zola means updating the version and checksum in the workflow
  and checking the Tera migration notes for the release.

## Rejected alternatives

- Hugo (2026-09-03): multi-page output and a theme layer for one page.
- Keeping plain HTML (2026-09-07): no duplication of layout, but every copy
  edit touched raw HTML tables; Markdown with a small templating step won.
- Zola's class-based syntax highlighting: Zola 0.23 emits numeric
  `z-N` classes, so it cannot replace the semantic `.c` comment styling
  without a generated theme stylesheet.
