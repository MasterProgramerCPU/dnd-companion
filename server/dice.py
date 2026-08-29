"""Dice expression parser and roller.

Supports expressions like:
    1d20+5          2d6+1d4-1       4d6kh3      2d20kl1
    1d8!            1d10r1          8d6min2     d%
Modifiers on a dice term:
    kh<n> keep highest n      kl<n> keep lowest n
    dh<n> drop highest n      dl<n> drop lowest n
    r<n>  reroll (once) any die <= n
    !     exploding: a die at max value rolls again and adds
    min<n>/max<n>  clamp each die
"""

from __future__ import annotations

import random
import re
from dataclasses import dataclass, field

MAX_DICE = 200
MAX_SIDES = 1000
MAX_EXPLOSIONS = 50

_TERM_RE = re.compile(
    r"(?P<sign>[+-])?\s*"
    r"(?:(?P<count>\d*)[dD](?P<sides>\d+|%)(?P<mods>(?:(?:kh|kl|dh|dl|ro|r|min|max)\d+|!)*)"
    r"|(?P<flat>\d+))",
)
_MOD_RE = re.compile(r"(kh|kl|dh|dl|ro|r|min|max)(\d+)|(!)", re.I)


class DiceError(ValueError):
    pass


@dataclass
class Die:
    value: int
    kept: bool = True
    exploded: bool = False
    rerolled: bool = False

    def render(self) -> str:
        s = str(self.value)
        if self.exploded:
            s += "!"
        if self.rerolled:
            s += "↻"
        return s if self.kept else f"~~{s}~~"


@dataclass
class Term:
    sign: int
    kind: str  # "dice" | "flat"
    text: str
    sides: int = 0
    dice: list[Die] = field(default_factory=list)
    value: int = 0

    def render(self) -> str:
        if self.kind == "flat":
            return str(self.value)
        return f"{self.text}[{', '.join(d.render() for d in self.dice)}]"


@dataclass
class RollResult:
    formula: str
    total: int
    detail: str
    terms: list[Term]

    def breakdown(self) -> list[dict]:
        """Compact structure for the client's rolling animation."""
        out = []
        for t in self.terms:
            if t.kind == "flat":
                out.append({"sign": t.sign, "kind": "flat", "value": t.value})
            else:
                out.append({
                    "sign": t.sign, "kind": "dice", "label": t.text, "sides": t.sides,
                    "value": t.value,
                    "dice": [{"v": d.value, "kept": d.kept, "x": d.exploded, "r": d.rerolled}
                             for d in t.dice],
                })
        return out

    @property
    def d20(self) -> Die | None:
        """The single kept d20, when this reads as one d20 check."""
        for term in self.terms:
            if term.kind == "dice" and term.sides == 20:
                kept = [d for d in term.dice if d.kept]
                if len(kept) == 1:
                    return kept[0]
        return None

    @property
    def crit(self) -> str | None:
        die = self.d20
        if die is None:
            return None
        if die.value == 20:
            return "crit"
        if die.value == 1:
            return "fumble"
        return None


def _parse_mods(raw: str) -> dict:
    mods: dict = {}
    for m in _MOD_RE.finditer(raw or ""):
        if m.group(3):
            mods["explode"] = True
            continue
        name, num = m.group(1).lower(), int(m.group(2))
        mods[name] = num
    return mods


def _roll_term(count: int, sides: int, mods: dict, draw) -> list[Die]:
    """Build one dice term. `draw(sides, n)` supplies the raw values, whether
    they came from the server's RNG or from dice a player physically threw."""
    if count < 1 or count > MAX_DICE:
        raise DiceError(f"dice count must be 1-{MAX_DICE}")
    if sides < 2 or sides > MAX_SIDES:
        raise DiceError(f"die size must be 2-{MAX_SIDES}")

    dice = [Die(v) for v in draw(sides, count)]

    threshold = mods.get("r", mods.get("ro"))
    if threshold is not None:
        for i, die in enumerate(dice):
            if die.value <= threshold:
                dice[i] = Die(draw(sides, 1)[0], rerolled=True)

    if mods.get("explode"):
        extra, budget = [], MAX_EXPLOSIONS
        pending = [d for d in dice if d.value == sides]
        while pending and budget > 0:
            nxt = []
            for _ in pending:
                if budget <= 0:
                    break
                budget -= 1
                die = Die(draw(sides, 1)[0], exploded=True)
                extra.append(die)
                if die.value == sides:
                    nxt.append(die)
            pending = nxt
        dice.extend(extra)

    lo, hi = mods.get("min"), mods.get("max")
    for die in dice:
        if lo is not None:
            die.value = max(die.value, lo)
        if hi is not None:
            die.value = min(die.value, hi)

    order = sorted(range(len(dice)), key=lambda i: dice[i].value)
    drop: set[int] = set()
    if "kh" in mods:
        drop = set(order[: max(0, len(dice) - mods["kh"])])
    elif "kl" in mods:
        drop = set(order[mods["kl"]:])
    elif "dh" in mods:
        drop = set(order[len(dice) - mods["dh"]:]) if mods["dh"] else set()
    elif "dl" in mods:
        drop = set(order[: mods["dl"]])
    for i in drop:
        dice[i].kept = False
    return dice


@dataclass
class TermSpec:
    """One parsed term, before any dice are actually thrown."""
    sign: int
    kind: str                    # "dice" | "flat"
    count: int = 0
    sides: int = 0
    mods: dict = field(default_factory=dict)
    value: int = 0               # flat terms only


def parse(formula: str, *, advantage: int = 0) -> list[TermSpec]:
    """Parse `formula` into terms without rolling anything."""
    expr = (formula or "").strip().replace(" ", "")
    if not expr:
        raise DiceError("empty roll")
    if len(expr) > 200:
        raise DiceError("expression too long")

    # Bare "+5" / "5" style modifiers are treated as a d20 check.
    if re.fullmatch(r"[+-]?\d+", expr):
        expr = f"1d20{expr if expr.startswith(('+', '-')) else '+' + expr}"

    specs: list[TermSpec] = []
    pos, applied_adv = 0, advantage == 0
    while pos < len(expr):
        m = _TERM_RE.match(expr, pos)
        if not m or m.end() == pos:
            raise DiceError(f"could not parse {formula!r} at position {pos}")
        pos = m.end()
        sign = -1 if m.group("sign") == "-" else 1

        if m.group("flat") is not None:
            specs.append(TermSpec(sign, "flat", value=int(m.group("flat"))))
            continue

        sides = 100 if m.group("sides") == "%" else int(m.group("sides"))
        count = int(m.group("count") or 1)
        mods = _parse_mods(m.group("mods"))

        # First d20 term absorbs advantage/disadvantage.
        if not applied_adv and sides == 20 and count == 1 and not (mods.keys() & {"kh", "kl", "dh", "dl"}):
            count = 2
            mods["kh" if advantage > 0 else "kl"] = 1
            applied_adv = True

        if count < 1 or count > MAX_DICE:
            raise DiceError(f"dice count must be 1-{MAX_DICE}")
        if sides < 2 or sides > MAX_SIDES:
            raise DiceError(f"die size must be 2-{MAX_SIDES}")
        specs.append(TermSpec(sign, "dice", count=count, sides=sides, mods=mods))
    return specs


def plan(specs: list[TermSpec]) -> list[dict]:
    """The dice a client has to physically throw, in term order."""
    return [{"sides": t.sides, "qty": t.count} for t in specs if t.kind == "dice"]


def evaluate(specs: list[TermSpec], formula: str, draw) -> RollResult:
    """Build the result from `specs`, taking die values from `draw(sides, n)`."""
    terms: list[Term] = []
    for spec in specs:
        if spec.kind == "flat":
            terms.append(Term(spec.sign, "flat", str(spec.value), value=spec.value))
            continue
        dice = _roll_term(spec.count, spec.sides, spec.mods, draw)
        if hasattr(draw, "next_term"):
            draw.next_term()
        label = f"{spec.count}d{spec.sides}" + "".join(
            f"{k}{v}" if k != "explode" else "!" for k, v in spec.mods.items()
        )
        term = Term(spec.sign, "dice", label, sides=spec.sides, dice=dice)
        term.value = sum(d.value for d in dice if d.kept)
        terms.append(term)

    total = sum(t.sign * t.value for t in terms)
    parts = []
    for i, t in enumerate(terms):
        op = "" if i == 0 and t.sign > 0 else (" - " if t.sign < 0 else " + ")
        parts.append(op + t.render())
    return RollResult(formula=(formula or "").strip(), total=total,
                      detail="".join(parts), terms=terms)


def rng_draw(rng: random.Random | None = None):
    """Dice straight from the server's RNG."""
    r = rng or random.SystemRandom()
    return lambda sides, n: [r.randint(1, sides) for _ in range(n)]


def supplied_draw(values: list[list[int]], rng: random.Random | None = None):
    """Dice thrown by the client, term by term.

    Extra dice a term turns out to need — explosions and rerolls, which depend
    on what was already rolled — come from the server, since the client had no
    way to know to throw them. Those show in the written breakdown but were not
    on screen; that only affects `!` and `r` formulas.
    """
    fallback = rng_draw(rng)
    pending = [list(v) for v in values]
    term = {"i": 0}

    def draw(sides: int, n: int) -> list[int]:
        out: list[int] = []
        if term["i"] < len(pending):
            bucket = pending[term["i"]]
            while bucket and len(out) < n:
                out.append(bucket.pop(0))
        out += fallback(sides, n - len(out))
        for v in out:
            if not (1 <= v <= sides):
                raise DiceError(f"impossible value {v} on a d{sides}")
        return out

    def next_term():
        term["i"] += 1

    draw.next_term = next_term
    return draw


def roll(formula: str, *, advantage: int = 0, rng: random.Random | None = None) -> RollResult:
    """Roll `formula` with the server's own RNG."""
    return evaluate(parse(formula, advantage=advantage), formula, rng_draw(rng))


def roll_supplied(formula: str, values: list[list[int]], *, advantage: int = 0,
                  rng: random.Random | None = None) -> RollResult:
    """Roll `formula` using die values the client physically threw."""
    specs = parse(formula, advantage=advantage)
    draw = supplied_draw(values, rng)
    return evaluate(specs, formula, draw)
