# ADR-0035: The website looks like the TUI, with self-hosted fonts and decoration-only script

- **Status:** Accepted
- **Date:** 2026-10-06
- **Sources:** `site/static/style.css`, `site/static/site.js`,
  `site/static/fonts/`, `site/templates/`

## Context

The first Zola site (ADR 0032) used the common dark product-page layout:
system fonts, a card grid for features, an amber accent. It read as
generic and shared nothing with the program it presents, whose TUI has a
distinct SomaFM palette, rounded lipgloss cards, a channel list, and a
spectrum visualizer.

## Decision

- The page borrows the TUI's visual language: the palette of
  `internal/ui/styles.go` (SomaFM red, gold, teal) on warm black, or warm
  paper in light mode; panels with their title set into the top border;
  features laid out like the channel list, with a gold selection bar and an
  equalizer on hover; section headings numbered as channels by a CSS
  counter.
- A light terminal theme on top: the navigation is a tmux status line (a
  session block, one window per section named by its shortcut key and
  flagged `*` while on screen), each section opens with a
  `~/somad $ cd <section>` prompt, block cursors blink after the headline
  and at the footer's idle prompt, the quick start's commands type
  themselves out, and faint scanlines cover the hero. None of it shows
  commands or flags soma does not have.
- Titles use a terminal font, Iosevka (heavy, with a light weight for
  the headline's gold word); interface text uses JetBrains Mono; body copy
  uses the system sans. No serif faces. Both web fonts are self-hosted in
  `site/static/fonts/` as Latin subsets in WOFF2, with their SIL OFL
  licence files alongside. The page loads no fonts, styles, or scripts
  from third-party hosts; the GitHub release lookup stays the only outside
  request.
- `site/static/site.js` holds only decoration and conveniences: the
  status line's `*`, the quick start's typing, single-key shortcuts
  mirroring the TUI
  (`f`, `i`, `u`, declared on links as `data-key`), and the quick start's
  copy button. The page reads and works fully without it.
- Motion stays small: blinking cursors, the typing, the hover equalizer,
  and the wires between the "How it works" panels. All of it stops under
  `prefers-reduced-motion`.

## Consequences

- A new font weight or script (glyphs outside Latin) means fetching another
  subset into `fonts/`, not linking a font CDN. The Iosevka files come
  from the `@fontsource/iosevka` package (about 1 MB a weight even in its
  Latin build) cut down with fonttools:
  `pyftsubset iosevka-latin-800-normal.woff2 --unicodes="U+0020-007E,U+00A0-00FF,U+2010-2027,U+2190-2199" --layout-features=kern --flavor=woff2 --no-hinting --desubroutinize`.
- Panels and cards keep the page background so the inset title can mask
  the border; a filled card background would show the seam.
- Changes to the TUI palette should be mirrored in `style.css`.

## Rejected alternatives

- Google Fonts' CDN: a third-party request on every visit and, in the EU,
  a personal-data transfer the page does not need.
- A system-font-only design: workable, but the title face carries most
  of the page's character for about 50 KB of fonts in total.
- A display serif (Instrument Serif) and then a sans grotesk (Bricolage
  Grotesque), both 2026-10-06: the titles should look like a terminal.
- Hero decoration (2026-10-06): an animated block-glyph spectrum, a
  tuning dial with station names drifting under a needle, an "On air"
  badge, and a clock in the status line were tried and removed as too
  busy.
- Other terminal fonts for titles (2026-10-06): IBM Plex Mono and Space
  Mono are too wide for the hero's two-line headline, VT323's pixels read
  as a gimmick at that size, and JetBrains Mono would blur titles into the
  interface text.
