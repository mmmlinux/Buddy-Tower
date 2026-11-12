# Buddy Tower

A terminal app that reads a sliced Bambu Studio / Orca `.gcode.3mf`, measures how long every
layer takes, and designs a round "buddy" tower to print beside your part. The tower's diameter
changes with height so that part + tower always takes at least your minimum layer time, giving
small upper layers time to cool without the slicer crawling.

## Build

```sh
go build -o buddy-tower .
```

## Use

```sh
./buddy-tower model.gcode.3mf
```

1. If the 3MF has more than one plate, choose one and press **Analyze** (single-plate files start analyzing immediately).
2. Watch the progress bar; **Cancel** (or `esc`) stops it. **Re-analyze** always re-reads the file from disk.
3. Adjust the tower rules on the left; the charts and shape update as you type.
4. Press **Write STEP** (`w`). Add the STEP to the same plate in Bambu Studio and re-slice.

Keys: `tab` / `shift+tab` jump between sections (buttons, tower rules, output), `↑↓` move between fields,
`←→` move along the buttons or flip the toggle, `enter` press, `a` analyze, `w` write, `q` quit.

### Just make the STEP

```sh
./buddy-tower model.gcode.3mf -step                 # writes model_tower.step next to the 3MF
./buddy-tower model.gcode.3mf -step -min-time 10 -out ~/tower.step
```

`-step` skips the UI, analyzes the file, writes the STEP using the default tower rules and exits.

Flags (before or after the file name): `-min-time 10` (default: the slicer's own *slow down layer time*),
`-out path.step`, `-plate plate_2` (required with `-step` when the 3MF has several plates), `-step`, and
`-dump` to print a per-layer CSV instead (writes the STEP too if `-out` is given).

### How it sizes the tower

- Layer times are estimated by simulating the gcode with the printer's acceleration, jerk and speed limits
  (within about 2% of Bambu Studio's own total on the sample file).
- Layers the slicer already slowed down for cooling are detected and their full-speed time is used,
  because once the tower is added the slicer stops slowing them. Switch to *as sliced* to turn this off.
- Tower time per layer is modelled from the 3MF's walls, line widths, speeds and infill, ignoring travel to the
  tower (set *Travel overhead* if you want to count it). The part and tower times are simply added.
- Widening is limited to the overhang angle (measured from vertical, 45° default), so the tower starts growing below the layers that need it.
  Narrowing is limited too, and the foot gives a wide first layer for bed grip.
- The real tower usually prints a little slower than the model (overhang speeds on widening sections),
  so the estimate errs on the safe side.

## License

Free to use, modify and share under the MIT License with the Commons Clause: you may not sell it, or sell a
product or service whose value comes mainly from it, without permission. See [LICENSE](LICENSE).

Buddy Tower uses open-source libraries (Charm's Bubble Tea, Bubbles and Lip Gloss, plus their
dependencies) under the permissive MIT and BSD-3-Clause licenses. Their notices are in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md); keep that file with any copy you share,
including built binaries.
