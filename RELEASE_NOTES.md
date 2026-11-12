# Release notes

## 0.2.2 — 2026-11-12

- Clearer download names: `x86_64` for regular Windows and Linux PCs, `intel` for Intel Macs, `arm64` for ARM machines and Apple Silicon Macs.
- The Mac downloads now run on macOS 11 Big Sur and later (previously macOS 13 Ventura or later).

## 0.2.1 — 2026-11-12

- Ready-made downloads for Windows, macOS and Linux (Intel and ARM) on the GitHub Releases page, built automatically for each new version.
- README now shows a screenshot and has a Download section.

## 0.1.1 — 2026-11-11

- Shorter, simpler README.

## 0.0.2 — 2026-11-11

- Tab now jumps between sections (buttons, tower rules, output file) and Shift+Tab goes back. Inside a section use ↑↓ to move between fields and ←→ along the buttons. Each section remembers where you left off.
- The settings panel border lights up while you're working in it.
- New `-step` flag: analyze the file and write the tower STEP without opening the TUI. Use `-out` to choose the file, `-min-time` for the limit, and `-plate` when the 3MF has more than one plate.
- Flags can go before or after the file name.
- The screen layout now fits smaller terminals (down to 80×24).

## 0.0.1 — 2026-11-11

First version.

- Open a sliced `.gcode.3mf` from the command line: `buddy-tower file.gcode.3mf`.
- If the 3MF holds several plates you pick one from a list; with a single plate analysis starts straight away.
- Analysis re-reads the file from disk every time, shows a progress bar with speed and time left, and can be cancelled.
- Works out how long every layer takes, including arcs, acceleration and machine limits from the 3MF.
- Detects layers the slicer already slowed down for cooling and works out how fast they would print at full speed, so the tower is sized for the real print.
- Designs a round tower whose diameter changes layer by layer so every layer reaches your minimum layer time, using the wall count, line widths, speeds and infill from the 3MF.
- Tower rules you can edit live: minimum layer time, safety margin, min/max diameter, steepest overhang and narrowing angles, a wide foot for bed grip, travel overhead and fit tolerance.
- Charts of layer times (with the tower's added time) and the tower's shape.
- Writes the tower as a STEP solid made of exact cylinders and cones, ready to drop onto the plate in Bambu Studio.
