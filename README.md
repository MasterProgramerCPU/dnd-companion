# D&D Table Companion

A LAN app for in-person 5e games. It runs on your PC; everyone at the table opens it on
their phone and gets a live character sheet, shared dice, the initiative order, and the
party's stash. No accounts, no internet, no cloud — just your Wi-Fi.

## Running it

One binary, no installer, no runtime to install first. Start it and the table is
live; close it and the session is over. Nothing is left running in the background
and there is nothing to uninstall.

```
./dnd-companion            # Linux / macOS
```

On Windows, double-click **dnd-companion-windows-amd64.exe**. Either way you get a
window with the join QR code in it, the DM console opens in your browser, and
closing the window (or Ctrl-C) stops the server.

That is deliberately the same on every platform. There is no service, no autostart
and no background daemon anywhere — the app is up while you have it open.

| flag | environment | what it does |
| --- | --- | --- |
| `-port` | `DND_PORT` | listen somewhere other than 8787 |
| `-data` | `DND_DATA_DIR` | keep campaigns somewhere other than the default |
| `-url` | `DND_URL` | override the address in the QR code |
| `-no-open` | | don't open the DM console at startup |

**Where campaigns live.** A `data/` folder sitting next to the binary wins, which is
what makes a portable copy — app and campaigns in one folder — work when you move it
to another machine. Otherwise they go to the per-user location: `%LOCALAPPDATA%\DnDCompanion`
on Windows, `~/.local/share/DnDCompanion` on Linux, `~/Library/Application Support/DnDCompanion`
on macOS. The startup banner always prints the exact path it chose.

Campaign files are recorded by name rather than by full path, so a data folder can be
copied between machines — and between Linux and Windows — and still opens.

## How a session goes

1. **You start the app.** It prints a QR code and the join URL:

   ```
     Players join at:  http://192.168.171.132:8787
     DM console:       http://192.168.171.132:8787/dm
   ```

2. **You build the party.** Open `/dm` and add a character for each
   player under **Party → Add character** — name, who's playing them, class, level and a
   colour. That's all you fill in; they do the rest of the sheet themselves.

3. **Players scan the QR and claim their character.** The join screen lists exactly the
   characters you created — players can't invent their own, and a seat already taken is
   marked *joined*. If a player is staring at the screen while you're still adding
   characters, it picks them up on its own within a few seconds.

4. **They're in.** Their phone remembers them, so next session they just open the link.

The DM console isn't locked. Anyone on the Wi-Fi can open `/dm` — there's a confirmation
so nobody lands there by fat-fingering the link, but that's a guard against accidents,
not a lock. It runs on trust, which is the right call at a table where you can all see
each other; just don't expose the port to anything wider.

Each campaign is one SQLite file under `data/` — back that file up and you've backed up
the campaign, delete it and it's gone. `data/registry.db` is the short list of which
campaigns exist and which one is being played.

## What players get

**Sheet** — a full 5e sheet: abilities, saves, all 18 skills with proficiency and
expertise, AC/speed/initiative, HP with temp HP and death saves, hit dice, spell slots,
attacks, inventory, coin. Derived numbers (proficiency bonus, save and skill bonuses,
passive Perception, spell save DC) are computed by the server, so the DM's dashboard and
the player's phone can never disagree.

Tap any modifier, save, skill, or attack to roll it — the result goes to everyone's log
with the character's name on it.

**Dice** — d4 through d100, plus a free-text box for anything the parser handles:

| you type | you get |
| --- | --- |
| `2d6+3` | ordinary roll |
| `4d6kh3` | keep highest 3 (stat rolling) |
| `2d20kl1` | keep lowest (disadvantage, spelled out) |
| `1d8!` | exploding — a max roll rolls again and adds |
| `10d10r1` | reroll any 1 once |
| `8d6min2` | treat any die below 2 as a 2 |
| `d%` | percentile |
| `+7` | shorthand for `1d20+7` |

The Advantage/Disadvantage toggle applies to the next roll and then resets itself.

Rolls are thrown, not just logged. Tap a skill, save, attack or die and real 3D dice
tumble across the screen — [@3d-dice/dice-box](https://fantasticdice.games), vendored into
`web/vendor/dice-box/` so it works with no internet at the table. The dice land, and the
number they land on *is* the roll.

That last part is the important bit and it decides where the randomness lives. dice-box
reads each die's value by ray-casting the settled mesh; there is no way to hand it a
predetermined result. So the phone throws the dice and reports the faces, and the server
does everything else: keep-highest/lowest, advantage, exploding, rerolls, modifiers, the
log. It also sanity-checks what comes back — a d6 reporting a 9 is refused, a roll can
only be cashed in once, and one player cannot cash in another's.

The trade-off: a player who edits their browser's JavaScript could feed the server a
false face. At a table where everyone can see each other that is no worse than palming a
physical die, but it does mean this is not a tamper-proof roller.

Two smaller consequences. Dice added *after* the fact by an exploding `!` or a reroll `r`
come from the server's own RNG, since the phone had no way to know to throw them — they
show in the written breakdown but were never on the table. And if a phone can't run WebGL,
it silently falls back to a built-in canvas dice renderer (`web/js/dice3d.js`) with the
server rolling, so nobody is left unable to roll.

Everyone else at the table gets the same roll as a small banner at the top of their phone
— roller, formula, dice and total — without anything blocking what they were doing. The
Dice tab also keeps the last result on screen, so you can glance back at it.

**Combat** — the live initiative order with the current turn highlighted, everyone's HP,
and a Roll Initiative button that drops the player straight into the DM's order.

**Party** — who's online and how hurt they are, the handouts they've been shown, the
shared treasury, the quest log, NPCs you've met, and shared session notes. A handout pops
up when the DM pushes it and stays in the list afterwards, picture and all.

**Loot** — its own tab, with two views. *On me* is the loot that is currently in this
character's hands; *Shared pile* is everything nobody has picked up yet. Tap **Take** to
pick something up and **Put in pile** to set it back down, and there's a summary of how
many items each of the others is carrying so nobody has to ask who has the rope.

Every piece of loot belongs to exactly one place: a specific character, or the shared
pile. There is no free-typed owner, so loot can't drift onto someone who isn't in the
party. A player can only move loot between the pile and their own pack — handing it
directly to someone else is the DM's call, and the server enforces that rather than
trusting the phone.

There is only one inventory. The **Carried** card on the Sheet tab and the *On me* view
on the Loot tab are the same records — add a torch to your pack and it appears in the
DM's ledger; take something from the shared pile and it appears on your sheet. Players
can add, edit and throw away their own things, and put anything of theirs into the pile.
Clearing items out of the shared pile is the DM's job, so nobody bins the party's
treasure by accident.

**Map** — where the party has been, drawn as a graph. Each place is a node linked to the
one it was reached from, so a campaign that went in a straight line shows as a road and
one that wandered shows its forks. The place you're standing in is a red star, places
you've been are solid gold, and somewhere you've only heard of hangs off a dashed line.
Underneath, the same places in order with the DM's notes on each. Places the DM hasn't
revealed simply aren't there.

## What the DM gets

**Combat** — add the whole party in one tap (optionally auto-rolling their initiative),
add monsters in batches (`Goblin ×3` becomes Goblin 1/2/3, each with its own HP), then
step through turns. Damage and healing on a player's row writes straight to their sheet
and shows up on their phone. Monsters can be marked hidden: players see `???` with no AC
or HP until you reveal them, and even visible monsters show players only "bloodied" or
"near death" rather than exact numbers.

**Party** — build the roster here, and watch it: every PC's HP, AC, passive Perception,
spell save DC, saves and key skills at a glance, so you can call for a check without
asking anyone their bonus.

Tap a character (or **Open sheet**) and their full sheet opens, fully editable — the same
sheet they see on their phone, not a cut-down version. Edits go both ways live: change
their AC here and it updates on their phone; they tick a proficiency and it updates in
front of you while the sheet is open. Useful for fixing a mistake mid-session, applying
a curse, or filling in a sheet for someone who hasn't arrived yet.

**📚 (top right)** — the campaigns menu. Each campaign is a world of its own: its own
characters, loot, journey, handouts and notes, in its own file. Make a new one, rename
one, delete one you're finished with, or hit **Play this** to put a different one on the
table. Switching sends every player back to the join screen to pick a character in the
new campaign — you stay signed in — and nothing is lost: switch back and the old
campaign is exactly as you left it. You can't delete the one you're playing.

**Campaign** — handouts, the loot ledger, quests, NPCs, treasury and notes.

**Handouts** work two ways. At the top of the card is a composer for improvising one
mid-scene: type a title and some text, optionally attach a picture, hit **📣 Push now**
and it's on every phone. It files itself in the library afterwards, so you can take it
back or show it again like any other.

Below that is the library of ones written ahead of time. Give one a title, some text and
a picture (PNG/JPEG/WebP/GIF, up to 10MB), and it sits there until you want it.
**Push** pops it on every phone and adds it to the players' list so they can re-read it
later; **Take back** removes it from their hands but keeps it in yours. Saving never
reveals anything — you can write the whole session's handouts in advance and a player
who goes looking will find nothing until you push. Pictures are stored by content hash,
so pushing the same map twice costs nothing.

The loot list is grouped by who is holding what: the shared pile first, then each
character. Adding or editing an item picks its owner from a dropdown of the actual party
plus the shared pile — you can't type a name, so an item can never end up assigned to
nobody in particular. Old campaigns that recorded a typed name are converted on first
start: names that still match a character move to them, anything else falls back to the
shared pile. Characters that had their own sheet inventory have it folded into the ledger
too, owned by them — the two lists were describing the same things.

**Map** — the journey, and the one bit of the app only you can edit. Add a place, say
which place it was **reached from**, and give it a status:

| status | players see |
| --- | --- |
| *been there* | a solid node on the road |
| *you are here* | the red star — moving it announces the arrival on every phone |
| *heard of it* | a dashed node hanging off wherever the rumour came from |
| *DM only* | nothing at all |

Because every place records where it was reached from, side-trips branch off rather than
flattening into one line. Leave "reached from" as the previous place and you get a plain
chronological road. The graph can't be tangled: a place can't be reached from itself or
from anywhere downstream of it, and deleting a place reattaches whatever came after it to
whatever came before. Hiding a place doesn't break the chain either — anything reached
through it re-links to the nearest place the players can see, so they never notice a gap.

There is no map image to upload; the graph is drawn from the places themselves.

**Secret rolls** — flip the 🔒 toggle on the dice pad and the result appears only on your
screen, marked with a blue edge. No banner fires on anyone's phone. Good for perception checks the party shouldn't know they
failed.


## Building

Go's cross-compiler does the whole matrix from whichever machine you happen to be
sitting at. The SQLite driver is pure Go, so `CGO_ENABLED=0` holds and there is no
C toolchain, no Windows machine and no CI runner in the loop:

```
./build.sh          # windows, linux and macOS — amd64 and arm64 — into dist/
go build ./cmd/dnd-companion    # just this machine
go test ./...
```

A build takes about half a minute for all six targets and each binary is around 15MB
with the web assets, fonts and 3D dice compiled in. `go:embed` puts them *inside* the
executable, so there is no unpack directory, nothing to extract at startup, and no way
for the app and its assets to get separated.

### The tests are a translation of the Python

This started as a Python app, and the two pieces most able to break quietly in a
port — the dice expression parser and the 5e derived-stat maths — are pinned against
it rather than rewritten by eye. `tools/gen_vectors.py` records what the Python
actually did for 51 dice expressions and 15 character sheets into `testdata/`, and the
Go tests replay them demanding identical output, down to the error strings.

That caught two real bugs immediately. Python's `//` rounds toward negative infinity
where Go's `/` truncates toward zero, so a Charisma of 3 has to give -4 and not -3 —
and every modifier on every sheet runs through that division. And a dice term's printed
label is built by walking its modifiers in written order, so they are an ordered slice
here; a Go map would have shuffled `4d6r1kh3` at random.

### Windows notes

Two things get in the way once, and both are Windows being careful rather than
anything being wrong.

**SmartScreen** will say "Windows protected your PC", because the executable isn't
code-signed — a certificate is a yearly expense a LAN dice roller doesn't justify.
**More info → Run anyway.**

**The firewall prompt** matters more, and it's the one thing that will stop phones
connecting. Windows asks whether to allow the app to communicate: tick **Private
networks** and allow it. If that prompt is dismissed or denied, phones fail to connect
with no visible reason. The fix is to set the Wi-Fi network's profile to **Private**
(Settings → Network & Internet → Wi-Fi → your network) and then re-allow the app under
Windows Defender Firewall → *Allow an app through firewall*. A network marked **Public**
blocks this no matter what the app asks for, and that is the single most common reason
a LAN app "just doesn't work" on Windows.

**If the QR points somewhere unreachable**, the app has guessed the wrong adapter. It
asks the OS which interface reaches the internet, and on a machine with Hyper-V, WSL,
VirtualBox or a VPN that can be a virtual one the phones cannot see. Compare the
address in the banner against `ipconfig`, and if they disagree, say which one is right:

```
dnd-companion-windows-amd64.exe -url http://192.168.1.42:8787
```

## Notes

- Everyone must be on the same Wi-Fi. If phones can't reach the PC, it's almost always
  the firewall: allow inbound TCP 8787.
- Handout pictures live in `uploads/` inside the data folder, named by content hash.
- Fonts, dice and every other asset are compiled into the binary and served from it, so
  the app looks and behaves identically with the router unplugged from the internet.
- Nothing here is locked. Anyone on your Wi-Fi with the URL can join as a player or open
  the DM console, and the dice are thrown on the phone rather than the server. That's the
  right trade for a living room and the wrong one for anywhere else — don't expose the
  port to the internet.

## Layout

```
cmd/dnd-companion/   the executable: flags, then hand off to internal/app
internal/
  app/       lifecycle — find the data, serve, print the QR, stop cleanly
  server/    HTTP routes, websocket ops, the permission split
  store/     SQLite: the campaign registry, campaign files, accessors
  state/     the JSON slices pushed to clients (DM and player variants)
  sheet/     the 5e sheet model and derived-stat maths
  dice/      dice expression parser and roller
  hub/       websocket fan-out
web/         compiled into the binary with go:embed
  index.html join screen        player.html  player app       dm.html  DM console
  js/sheet.js   the 5e sheet, shared by the player's view and the DM's
  js/dicebox.js wrapper around the vendored 3D dice tray
  js/dice3d.js  fallback dice renderer for devices without WebGL
  js/common.js  transport, DOM helpers, dice pad, roll log
  js/player.js  character sheet and player tabs
  js/dm.js      DM console
  fonts/        Cinzel + Alegreya Sans (SIL OFL), vendored to work offline
  vendor/dice-box/  @3d-dice/dice-box + its assets, vendored to work offline
testdata/    golden vectors recorded from the original Python implementation
tools/       the generator that produced them
server/      the original Python implementation, kept as the reference
```

State changes go one way: a client sends an op over the websocket, the server validates
it against that device's role, writes SQLite, and broadcasts the affected slice to
everyone. Nothing is trusted from the client, and the DM's view and the players' view of
the same slice are rendered from the same data with different redactions.
