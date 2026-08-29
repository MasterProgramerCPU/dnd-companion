"""Generate language-agnostic golden vectors from the working Python implementation.

These are the specification for the Go port: same inputs, byte-identical outputs.
Dice draws come from a fixed cycling sequence rather than an RNG, so the Go side
can reproduce them exactly without reimplementing Mersenne Twister.
"""

from __future__ import annotations

import json
import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))

from server import db, dice  # noqa: E402

OUT = pathlib.Path(__file__).resolve().parent.parent / "testdata"

# A deterministic stand-in for the RNG. Values are cycled and folded into range,
# so a term asking for a d6 and a term asking for a d20 both get something legal
# from the same sequence.
SEQ = [7, 1, 20, 3, 6, 6, 2, 19, 5, 4, 8, 20, 1, 12, 6, 9, 3, 17, 2, 10]


def seq_draw():
    i = {"n": 0}

    def draw(sides: int, n: int) -> list[int]:
        out = []
        for _ in range(n):
            v = SEQ[i["n"] % len(SEQ)]
            i["n"] += 1
            out.append((v - 1) % sides + 1)
        return out

    return draw


FORMULAS = [
    ("1d20+5", 0), ("2d6+3", 0), ("d20", 0), ("3d8-2", 0), ("1d4", 0),
    ("4d6kh3", 0), ("2d20kl1", 0), ("4d6dl1", 0), ("4d6dh1", 0), ("5d10kh2", 0),
    ("1d8!", 0), ("3d6!", 0), ("2d20!", 0),
    ("10d10r1", 0), ("4d6ro2", 0), ("6d6r2", 0),
    ("8d6min2", 0), ("8d6max4", 0), ("4d10min3max7", 0),
    ("d%", 0), ("2d%", 0),
    ("+7", 0), ("-3", 0), ("5", 0), ("+0", 0),
    ("1d20+5", 1), ("1d20+5", -1), ("1d20", 1), ("2d20kl1", 1), ("1d6+2", 1),
    ("2d6+1d4-1", 0), ("4d6kh3+2", 0), ("1d20+3+1d4", 0),
    ("6d6kh3!", 0), ("4d6r1kh3", 0), ("2d20kh1", 0),
    ("100d6", 0), ("1d1000", 0), ("1d2", 0),
]

BAD = ["", "   ", "1d1", "0d6", "201d6", "1d1001", "abc", "d", "1d20kh", "+", "1d20+" ,"x" * 201]


def dice_cases() -> list[dict]:
    cases = []
    for formula, adv in FORMULAS:
        specs = dice.parse(formula, advantage=adv)
        result = dice.evaluate(specs, formula, seq_draw())
        cases.append({
            "formula": formula,
            "advantage": adv,
            "plan": dice.plan(specs),
            "total": result.total,
            "detail": result.detail,
            "breakdown": result.breakdown(),
            "crit": result.crit,
        })
    for formula in BAD:
        try:
            dice.parse(formula)
            raise AssertionError(f"expected {formula!r} to be rejected")
        except dice.DiceError as exc:
            cases.append({"formula": formula, "advantage": 0, "error": str(exc)})
    return cases


def derive_cases() -> list[dict]:
    sheets = []

    base = db.default_sheet("Default")
    sheets.append(("default", base))

    # Odd and low scores: Python floors toward negative infinity, which is the
    # single easiest thing to get wrong in another language.
    odd = db.default_sheet("Odd Scores")
    odd["abilities"] = {"str": 7, "dex": 9, "con": 11, "int": 13, "wis": 15, "cha": 3}
    odd["level"] = 5
    sheets.append(("odd_scores", odd))

    ext = db.default_sheet("Extremes")
    ext["abilities"] = {"str": 1, "dex": 30, "con": 20, "int": 8, "wis": 10, "cha": 2}
    ext["level"] = 20
    sheets.append(("extremes", ext))

    full = db.default_sheet("Full Caster")
    full["level"] = 11
    full["abilities"] = {"str": 8, "dex": 14, "con": 16, "int": 20, "wis": 12, "cha": 10}
    full["save_prof"] = {"str": False, "dex": True, "con": True, "int": True,
                         "wis": False, "cha": False}
    full["skill_prof"] = dict.fromkeys(db.SKILLS, 0)
    full["skill_prof"]["arcana"] = 2
    full["skill_prof"]["perception"] = 1
    full["skill_prof"]["stealth"] = 2
    full["skill_prof"]["investigation"] = 1
    full["spell"]["ability"] = "int"
    full["initiative_bonus"] = 3
    sheets.append(("full_caster", full))

    # Every proficiency-bonus breakpoint.
    for lvl in (1, 4, 5, 8, 9, 12, 13, 16, 17, 20):
        s = db.default_sheet(f"Level {lvl}")
        s["level"] = lvl
        sheets.append((f"level_{lvl}", s))

    # Ranks above expertise are clamped, not multiplied.
    clamp = db.default_sheet("Clamped")
    clamp["skill_prof"] = dict.fromkeys(db.SKILLS, 0)
    clamp["skill_prof"]["athletics"] = 5
    sheets.append(("clamped_rank", clamp))

    return [{"name": n, "sheet": s, "derived": db.derive(s)} for n, s in sheets]


def main() -> None:
    OUT.mkdir(exist_ok=True)
    for name, data in (("dice_cases.json", dice_cases()), ("derive_cases.json", derive_cases())):
        path = OUT / name
        path.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n")
        print(f"{path.name}: {len(data)} cases")


if __name__ == "__main__":
    main()
