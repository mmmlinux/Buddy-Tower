# Buddy Tower

Small, fast layers print badly because they don't get time to cool. Buddy Tower reads a sliced
Bambu Studio / Orca `.gcode.3mf`, works out how long every layer takes, and designs a round tower
to print next to your part. The tower gets wider where the part's layers are fast, so every layer
takes at least your minimum time.

You get a STEP file. Drop it on the same plate in Bambu Studio and re-slice.

## Build

```sh
go build -o buddy-tower .
```

## Use

```sh
./buddy-tower model.gcode.3mf
```

Pick a plate if there's more than one, let it analyze, adjust the tower rules if you like, then
press **Write STEP**.

- `tab` / `shift+tab`: switch section
- `↑↓`: move between fields
- `←→`: move along the buttons, flip toggles
- `enter`: press a button
- `q`: quit

To skip the interface and just write the STEP:

```sh
./buddy-tower model.gcode.3mf -step
```

| Flag | What it does |
|---|---|
| `-min-time 10` | Minimum layer time in seconds (default: the slicer's own setting) |
| `-out tower.step` | Where to write the STEP (default: next to the 3MF) |
| `-plate plate_2` | Which plate to use when there are several |
| `-step` | No interface, just write the STEP |
| `-dump` | Print a per-layer CSV of times |

## License

Free to use, modify and share under the MIT License with the Commons Clause: you may not sell it,
or sell a product or service whose value comes mainly from it, without permission. See
[LICENSE](LICENSE). Third-party libraries are listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
