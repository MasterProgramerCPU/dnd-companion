# Golden vectors

These files are the recorded behaviour of the original Python implementation of
this app, captured before it was rewritten in Go. `dice_cases.json` holds 51
dice expressions; `derive_cases.json` holds 15 character sheets and the stats
derived from each.

They are read by `internal/dice` and `internal/sheet`, whose tests demand
identical output — the same totals, the same rendered breakdowns, and the same
error strings.

**They cannot be regenerated.** The Python they were recorded from has been
removed, along with the generator that produced them. That is deliberate: these
are no longer a comparison against another implementation, they are the
specification. Nothing here should be edited to make a failing test pass —
a diff against these files means the dice or the 5e maths changed, which is
either a bug or a rules change that needs recording on purpose.

Two things they exist to pin down, both of which were real bugs caught during
the port:

- **Ability modifiers floor toward negative infinity.** A Charisma of 3 gives
  -4, not -3. Python's `//` does this natively; Go's `/` truncates toward zero
  and needs `floorDiv`.
- **A dice term's label follows the order its modifiers were written.**
  `4d6r1kh3` renders in that order, so modifiers are held in a slice — a Go map
  would have shuffled them.

The dice values are drawn from a fixed sequence rather than a random source, so
the results are reproducible without either implementation having to imitate the
other's random number generator. The sequence is duplicated in
`internal/dice/dice_test.go`; if you change one, change both.
