# Working on this repo

## Install after changing anything

**The web UI is compiled into the binary** (`assets.go`, `//go:embed all:web`). Editing
`web/` or `internal/` changes nothing you can run until the binary is rebuilt and put
back where the launcher looks for it:

```
./install.sh
```

Do this at the end of any change that touches behaviour, and say so in the summary. A
copy that is already running keeps executing the image it started with — replacing the
file underneath it does nothing — so it has to be closed and reopened as well.

`./build.sh` is the other one: it cross-compiles all six release targets into `dist/`.
It does not install anything.

## Checks before calling something done

```
go build ./... && go vet ./... && go test ./... && gofmt -l .
node --check web/js/<file>.js      # there is no JS build step or linter
```

`testdata/*.json` pins dice and derived-stat vectors recorded from the original Python
implementation, which no longer exists. They are the only independent check that the Go
port still behaves the same, so **do not regenerate them from Go output**. A new key on
`sheet.Derived` has to be listed in `derivedSincePython` in `internal/sheet/sheet_test.go`,
which is deliberately a speed bump: it keeps the vectors meaningful while still allowing
the model to grow.

## The DM/player split is a real boundary

`internal/state` renders the same data twice, once for the DM and once for players, and
the redactions are the point: hidden monsters become `???`, visible ones report "bloodied"
rather than numbers, unrevealed places vanish from the journey, and the bestiary is
dropped entirely.

`state.Party` sends **every key of `store.PartyDefaults()`**, so anything added there
reaches players unless it is explicitly deleted in the `!isDM` branch. Adding DM-only
campaign data means editing that branch too, and it is worth a test that joins as a real
player and greps the payload.

Ops are split the same way in `internal/server/ws.go`: `playerOps` and `dmOps`. A player
sending a DM op gets nothing.

## Where the rules live

`internal/sheet` holds the 5e model and every derived number — modifiers, proficiency
(from level, or from CR for a stat block), saves, skills, passives, rests. Server ops call
into it rather than doing arithmetic themselves, so the DM's dashboard and a player's phone
can never disagree. New rules belong there, next to their tests, not in an op.

A monster is the same free-form map a character sheet is, derived by the same code
(`sheet.Monster`). It carries a challenge rating instead of a level.

## Dice

The phone throws the dice and reports the faces; the **server** does all arithmetic,
validation and logging. Never move rules maths to the client. `web/vendor/dice-box/` is
vendored because the table has no internet.

## Data

Campaigns live in `~/.local/share/DnDCompanion/` (per user), or in a `data/` directory
beside the executable if one exists. One SQLite file per campaign plus a registry.
Copying that file backs up the campaign.

When testing against real data, **copy the data directory** and run with
`DND_DATA_DIR=<copy>` rather than pointing at the live one.
