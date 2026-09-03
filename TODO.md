# TODO

Open work, grouped **P1** (known bugs or real exposure) / **P2**
(high-value improvements) / **P3** (polish), each with a rough effort tag:
**[S]** under an hour, **[M]** an afternoon, **[L]** multi-day or needs a
decision. Items deliberately dropped are recorded in `docs/adr/` and listed
under "Not planned" so they are not re-proposed.

The 2026-09 assessment backlog (P1 correctness and security fixes, the
P2 features, and the P3 polish) was worked off on 2026-09-03/04; see the
git log from 79ae532 onwards. Nothing is open right now. Follow-ups that
came out of that work but were not worth a line here (the Apple Developer
ID secrets for ADR-0029, publishing to the AUR) live in the ADRs and the
README.

## P1 — correctness and security

## P2 — features and hardening

## P3 — polish

## Not planned

Scope cuts are recorded as Architecture Decision Records in `docs/adr/`
(see the "Rejected alternatives" sections); this list only points there so
they are not re-proposed.

- Linux AAC decoding — ADR-0016
- Theming via config — ADR-0020
- Channel detail pane, sort options, per-frame style hoisting — ADR-0026
- Protocol min/max version range in hello — ADR-0002
- Shared per-address auth limiter — ADR-0008
- Semver comparison for version-skew restarts — ADR-0006
- CONTRIBUTING.md and a committed CHANGELOG — ADR-0022
- Raising the CI coverage gate — ADR-0025
- Re-arming the stall watchdog on buffer consumption — ADR-0013
