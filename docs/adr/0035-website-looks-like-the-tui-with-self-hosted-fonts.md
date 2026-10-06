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
  equalizer on hover; a block-glyph spectrum and a drifting tuning dial in
  the hero; section headings numbered as channels by a CSS counter.
- Two web fonts, Instrument Serif for display and JetBrains Mono for
  interface text, are self-hosted in `site/static/fonts/` (Latin subsets,
  WOFF2, SIL OFL with the licence files alongside). The page loads no fonts,
  styles, or scripts from third-party hosts; the GitHub release lookup stays
  the only outside request.
- `site/static/site.js` holds only decoration and conveniences: the
  spectrum, the dial's lit station, single-key shortcuts mirroring the TUI
  (`f`, `i`, `u`, declared on links as `data-key`), and the quick start's
  copy button. The page reads and works fully without it.
- Animation stops under `prefers-reduced-motion`; the spectrum also pauses
  while hidden or scrolled out of view.

## Consequences

- A new font weight or script (glyphs outside Latin) means fetching another
  subset into `fonts/`, not linking a font CDN. The block glyphs of the
  spectrum are outside the subset and use the system monospace face.
- Panels and cards keep the page background so the inset title can mask
  the border; a filled card background would show the seam.
- The dial's station names are decoration and live in the front matter
  (`dial:`) like the rest of the copy.
- Changes to the TUI palette should be mirrored in `style.css`.

## Rejected alternatives

- Google Fonts' CDN: a third-party request on every visit and, in the EU,
  a personal-data transfer the page does not need.
- A system-font-only design: workable, but the display serif carries most
  of the page's character for under 65 KB of fonts in total.
