# TUI-style Interface — Tasks

Each task includes its tests and must pass `just verify` before it is done.
Every task changes the browser page, so each also needs `just browser-check`.

| ID | Description | Complexity | Suggested Model | Status | Notes |
| --- | --- | --- | --- | --- | --- |
| TUI-INTERFACE-01 | Replace the inline styles in `index.html` with a One Dark, terminal-style stylesheet: CSS custom properties for the palette, a system monospace font stack, thin borders, compact spacing, and clearly different `focus` and `disabled` states; works at phone width. Keep every id, class and ARIA attribute the script and browser check use. | small | sonnet | done | Q3. Markup and CSS only; `app.js` unchanged. |
| TUI-INTERFACE-02 | Show a line count and a byte count in each box's status area, recomputed about 150 ms after the last input and after remote text arrives; extend the browser check to cover both numbers across two browsers. | moderate | sonnet | done | Depends on -01. Q1. Measure a 10 MiB box; no per-keystroke full scans. |
| TUI-INTERFACE-03 | Add the Alt-based keyboard shortcuts (Alt+N add, Alt+1…9 focus, Alt+C copy, Alt+Backspace remove) through one `keydown` listener that reuses the existing add, copy and remove code; add the one-line hint bar and `title` hints on the buttons; extend the browser check to cover the shortcuts. | moderate | sonnet | pending | Depends on -01. Q2, Q4. Match on `event.code`, not `event.key`, because Alt changes the typed character on macOS. |
