"""Builds the JSON slices that get pushed to clients."""

from __future__ import annotations

import json

from . import db

ROLL_LOG_LIMIT = 60


def character(row) -> dict:
    sheet = json.loads(row["sheet"])
    return {
        "id": row["id"],
        "name": row["name"],
        "sort_idx": row["sort_idx"],
        "updated_at": row["updated_at"],
        "sheet": sheet,
        "derived": db.derive(sheet),
    }


def characters() -> list[dict]:
    rows = db.q("SELECT * FROM characters ORDER BY sort_idx, id")
    return [character(r) for r in rows]


def roster() -> list[dict]:
    """Lightweight list for the join screen (no sheet contents)."""
    claimed = {r["character_id"] for r in db.q(
        "SELECT DISTINCT character_id FROM devices WHERE character_id IS NOT NULL")}
    out = []
    for row in db.q("SELECT * FROM characters ORDER BY sort_idx, id"):
        sheet = json.loads(row["sheet"])
        out.append({
            "id": row["id"],
            "name": row["name"],
            "player": sheet.get("player", ""),
            "klass": sheet.get("klass", ""),
            "level": sheet.get("level", 1),
            "color": sheet.get("color", "#c9a227"),
            "claimed": row["id"] in claimed,
        })
    return out


def initiative() -> dict:
    # PC rows mirror the live character sheet so HP has exactly one home.
    sheets = {r["id"]: json.loads(r["sheet"]) for r in db.q("SELECT id, sheet FROM characters")}
    entries = []
    for row in db.q("SELECT * FROM initiative ORDER BY init DESC, id"):
        sheet = sheets.get(row["character_id"])
        if sheet is not None:
            hp, hp_max, ac = sheet["hp"]["current"], sheet["hp"]["max"], sheet["ac"]
            conditions = json.dumps(sheet.get("conditions", []))
        else:
            hp, hp_max, ac = row["hp"], row["hp_max"], row["ac"]
            conditions = row["conditions"]
        entries.append({
            "id": row["id"],
            "name": sheet["name"] if sheet else row["name"],
            "character_id": row["character_id"],
            "init": row["init"],
            "hp": hp,
            "hp_max": hp_max,
            "ac": ac,
            "conditions": json.loads(conditions),
            "note": row["note"],
            "hidden": bool(row["hidden"]),
            "defeated": bool(row["defeated"]),
        })
    enc = db.get_meta("encounter", {"round": 0, "turn_id": None, "running": False})
    return {"entries": entries, **enc}


def initiative_for_players() -> dict:
    """Hidden monsters become anonymous '???' rows; AC and exact HP are withheld."""
    data = initiative()
    visible = []
    for e in data["entries"]:
        if e["hidden"]:
            visible.append({
                "id": e["id"], "name": "???", "character_id": None, "init": e["init"],
                "hp": None, "hp_max": None, "ac": None, "conditions": [],
                "note": "", "hidden": True, "defeated": e["defeated"],
            })
            continue
        e = dict(e)
        if e["character_id"] is None:
            e["ac"] = None
            if e["hp"] is not None and e["hp_max"]:
                pct = e["hp"] / e["hp_max"] if e["hp_max"] else 0
                e["wounds"] = (
                    "unharmed" if pct >= 1 else "hurt" if pct > 0.5
                    else "bloodied" if pct > 0.25 else "near death" if pct > 0 else "down"
                )
                e["hp"] = None
                e["hp_max"] = None
        visible.append(e)
    data["entries"] = visible
    return data


def rolls(include_secret: bool) -> list[dict]:
    where = "" if include_secret else "WHERE secret=0"
    rows = db.q(f"SELECT * FROM rolls {where} ORDER BY id DESC LIMIT ?", (ROLL_LOG_LIMIT,))
    return [roll_row(r) for r in reversed(rows)]


def roll_row(row) -> dict:
    return dict(row) | {"secret": bool(row["secret"]), "terms": json.loads(row["terms"] or "[]")}


def journey_for_players() -> dict:
    """Players see the trail, minus anywhere the DM is still keeping back.

    Dropping a hidden place would orphan anything reached through it, so those
    links are re-pointed at the nearest ancestor the players can actually see —
    the graph stays connected without revealing the gap.
    """
    locs = db.get_party("journey").get("locations", [])
    by_id = {loc["id"]: loc for loc in locs}
    hidden = {loc["id"] for loc in locs if loc.get("status") == "hidden"}

    def visible_ancestor(pid):
        seen = set()
        while pid in hidden and pid not in seen:
            seen.add(pid)
            pid = by_id[pid].get("from")
        return pid if pid in by_id and pid not in hidden else None

    out = []
    for loc in locs:
        if loc["id"] in hidden:
            continue
        item = dict(loc)
        item["from"] = visible_ancestor(loc.get("from"))
        out.append(item)
    return {"locations": out}


def party(is_dm: bool = True) -> dict:
    out = {key: db.get_party(key) for key in db.PARTY_DEFAULTS}
    if not is_dm:
        out["journey"] = journey_for_players()
        # players only ever learn about handouts the DM has actually shown them
        out["handouts"] = [dict(h) for h in out["handouts"] if h.get("revealed")]
    return out


def campaign() -> dict:
    return {"name": db.campaign_name(), "id": db.active_id()}


def snapshot(is_dm: bool) -> dict:
    return {
        "campaign": campaign(),
        "characters": characters(),
        "initiative": initiative() if is_dm else initiative_for_players(),
        "rolls": rolls(include_secret=is_dm),
        "party": party(is_dm),
        "campaigns": db.campaigns() if is_dm else [],
    }
