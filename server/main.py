"""D&D table companion — LAN server.

Run: uv run python -m server.main
"""

from __future__ import annotations

import hashlib
import json
import os
import secrets
import socket
import sys
import time
from pathlib import Path

from fastapi import FastAPI, File, HTTPException, Request, UploadFile, WebSocket, WebSocketDisconnect
from fastapi.responses import FileResponse, HTMLResponse, Response
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel

from . import db, paths, state
from . import dice
from .dice import DiceError, evaluate as dice_evaluate, parse as dice_parse
from .dice import MAX_DICE, plan as dice_plan, roll as roll_dice
from .hub import Client, hub

WEB = paths.bundled("web")
CONDITIONS = [
    "blinded", "charmed", "deafened", "frightened", "grappled", "incapacitated",
    "invisible", "paralyzed", "petrified", "poisoned", "prone", "restrained",
    "stunned", "unconscious", "concentrating", "exhaustion",
]

app = FastAPI(title="D&D Table Companion", docs_url=None, redoc_url=None)


@app.middleware("http")
async def cache_headers(request: Request, call_next):
    """Phones cache aggressively, and a phone running yesterday's JavaScript
    against today's server fails in confusing, silent ways — a pushed handout
    that simply never appears. Without an explicit Cache-Control, browsers guess
    a freshness lifetime and skip revalidating entirely, so say it outright:
    the app itself is always revalidated (a cheap 304 on a LAN), while
    content-addressed files are safe to keep forever.
    """
    response = await call_next(request)
    path = request.url.path
    if path.startswith("/uploads/") or path.startswith("/static/vendor/"):
        response.headers["Cache-Control"] = "public, max-age=31536000, immutable"
    elif path.startswith("/static/") or path in ("/", "/play", "/dm"):
        response.headers["Cache-Control"] = "no-cache"
    return response
app.mount("/static", StaticFiles(directory=WEB), name="static")
db.UPLOADS_DIR.mkdir(parents=True, exist_ok=True)
app.mount("/uploads", StaticFiles(directory=db.UPLOADS_DIR), name="uploads")


# ------------------------------------------------------------------ helpers

def lan_ip() -> str:
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        s.connect(("8.8.8.8", 80))
        return s.getsockname()[0]
    except Exception:
        return "127.0.0.1"
    finally:
        s.close()


def device(token: str | None):
    if not token:
        return None
    return db.q1("SELECT * FROM devices WHERE token=?", (token,))


def require(token: str | None, dm: bool = False):
    dev = device(token)
    if dev is None:
        raise HTTPException(401, "unknown device — rejoin from the QR code")
    if dm and dev["role"] != "dm":
        raise HTTPException(403, "DM only")
    db.run("UPDATE devices SET last_seen=? WHERE token=?", (db.now(), token))
    return dev


def deep_merge(base: dict, patch: dict) -> dict:
    for key, value in patch.items():
        if isinstance(value, dict) and isinstance(base.get(key), dict):
            deep_merge(base[key], value)
        else:
            base[key] = value
    return base


def load_sheet(char_id: int) -> tuple[dict, str]:
    row = db.q1("SELECT * FROM characters WHERE id=?", (char_id,))
    if row is None:
        raise HTTPException(404, "no such character")
    return json.loads(row["sheet"]), row["name"]


def save_sheet(char_id: int, sheet: dict) -> None:
    db.run(
        "UPDATE characters SET name=?, sheet=?, updated_at=? WHERE id=?",
        (sheet.get("name") or "Unnamed", json.dumps(sheet), db.now(), char_id),
    )


def clamp_hp(sheet: dict) -> None:
    hp = sheet["hp"]
    hp["max"] = max(0, int(hp.get("max") or 0))
    hp["temp"] = max(0, int(hp.get("temp") or 0))
    hp["current"] = max(-hp["max"], min(int(hp.get("current") or 0), hp["max"]))


# ------------------------------------------------------------------ pages

@app.get("/", response_class=HTMLResponse)
def page_join():
    return FileResponse(WEB / "index.html")


@app.get("/play", response_class=HTMLResponse)
def page_player():
    return FileResponse(WEB / "player.html")


@app.get("/dm", response_class=HTMLResponse)
def page_dm():
    return FileResponse(WEB / "dm.html")


@app.get("/qr.svg")
def qr_svg(request: Request):
    """QR for the join URL. Uses the address the client actually reached us on,
    so it works whether we were started by main() or by a bare uvicorn command."""
    from io import BytesIO

    import qrcode
    import qrcode.image.svg

    target = APP_URL or str(request.base_url).rstrip("/")
    buf = BytesIO()
    qrcode.make(target, image_factory=qrcode.image.svg.SvgPathImage).save(buf)
    return Response(buf.getvalue(), media_type="image/svg+xml")


# ------------------------------------------------------------------ REST

class JoinRequest(BaseModel):
    role: str = "player"
    display_name: str = ""
    character_id: int | None = None


@app.get("/api/lobby")
def api_lobby(request: Request):
    return {
        "campaign": state.campaign(),
        "roster": state.roster(),
        "url": APP_URL or str(request.base_url).rstrip("/"),
        "conditions": CONDITIONS,
    }


@app.post("/api/join")
def api_join(req: JoinRequest):
    role = "dm" if req.role == "dm" else "player"

    char_id = req.character_id
    if role == "player":
        # The DM builds the party; players only claim a seat at it.
        if char_id is None:
            raise HTTPException(400, "pick your character")
        if db.q1("SELECT 1 FROM characters WHERE id=?", (char_id,)) is None:
            raise HTTPException(404, "that character is no longer in the party")
    else:
        char_id = None

    display = (req.display_name or "").strip()[:60]
    if not display and char_id:
        display = load_sheet(char_id)[1]
    token = secrets.token_urlsafe(18)
    db.run(
        "INSERT INTO devices(token, display_name, role, character_id, created_at, last_seen)"
        " VALUES(?,?,?,?,?,?)",
        (token, display or ("DM" if role == "dm" else "Player"), role, char_id, db.now(), db.now()),
    )
    return {"token": token, "role": role, "character_id": char_id, "display_name": display}


IMAGE_TYPES = {"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp",
               "image/gif": ".gif"}
MAX_IMAGE_BYTES = 10 * 1024 * 1024


@app.post("/api/upload")
async def api_upload(token: str, file: UploadFile = File(...)):
    """A picture for a handout. Stored by content hash, so re-uploading the same
    image costs nothing and two handouts can share one file."""
    require(token, dm=True)
    ext = IMAGE_TYPES.get(file.content_type or "")
    if ext is None:
        raise HTTPException(400, "pictures must be PNG, JPEG, WebP or GIF")
    blob = await file.read()
    if not blob:
        raise HTTPException(400, "empty file")
    if len(blob) > MAX_IMAGE_BYTES:
        raise HTTPException(413, f"picture is larger than {MAX_IMAGE_BYTES // 1024 // 1024}MB")
    name = hashlib.sha256(blob).hexdigest()[:16] + ext
    db.UPLOADS_DIR.mkdir(parents=True, exist_ok=True)
    (db.UPLOADS_DIR / name).write_bytes(blob)
    return {"image": name}


@app.get("/api/me")
def api_me(token: str):
    dev = require(token)
    return {
        "token": dev["token"],
        "device_id": db.device_id(dev["token"]),
        "role": dev["role"],
        "character_id": dev["character_id"],
        "display_name": dev["display_name"],
        "campaign": state.campaign(),
        "conditions": CONDITIONS,
        "skills": db.SKILLS,
        "abilities": db.ABILITIES,
    }


# ------------------------------------------------------------------ pushes

async def push_characters() -> None:
    await hub.broadcast("characters", state.characters())


async def push_initiative() -> None:
    await hub.broadcast_split("initiative", state.initiative(), state.initiative_for_players())


async def push_party() -> None:
    await hub.broadcast_split("party", state.party(True), state.party(False))


async def push_roll(row: dict) -> None:
    if row["secret"]:
        await hub.to_dms("roll", row)
    else:
        await hub.broadcast("roll", row)


# ------------------------------------------------------------------ ops

# Rolls the client is part-way through: it has asked what dice to throw and we
# are waiting for it to report what they landed on.
PENDING_ROLLS: dict[str, dict] = {}
PENDING_TTL = 120


def _prune_pending() -> None:
    cutoff = time.time() - PENDING_TTL
    for key in [k for k, v in PENDING_ROLLS.items() if v["ts"] < cutoff]:
        PENDING_ROLLS.pop(key, None)


def _roll_meta(c: Client, p: dict) -> dict:
    return {
        "label": str(p.get("label", ""))[:60],
        "secret": bool(p.get("secret")) and c.is_dm,
        "actor": str(p.get("actor") or c.name)[:60],
        "set_initiative": bool(p.get("set_initiative")),
        "advantage": int(p.get("advantage", 0) or 0),
        "formula": str(p.get("formula", "")).strip(),
    }


async def _record_roll(c: Client, meta: dict, result) -> None:
    cur = db.run(
        "INSERT INTO rolls(ts,actor,character_id,label,formula,total,detail,crit,secret,terms,by_device)"
        " VALUES(?,?,?,?,?,?,?,?,?,?,?)",
        (db.now(), meta["actor"], c.character_id, meta["label"], result.formula,
         result.total, result.detail, result.crit, int(meta["secret"]),
         json.dumps(result.breakdown()), db.device_id(c.token)),
    )
    row = state.roll_row(db.q1("SELECT * FROM rolls WHERE id=?", (cur.lastrowid,)))
    await push_roll(row)
    if meta["set_initiative"] and c.character_id:
        await op_init_self(c, {"init": result.total})


async def op_roll_plan(c: Client, p: dict) -> None:
    """Tell the client which dice to physically throw for this formula."""
    _prune_pending()
    meta = _roll_meta(c, p)
    try:
        specs = dice_parse(meta["formula"], advantage=meta["advantage"])
    except DiceError as exc:
        await hub.send(c.ws, "toast", {"kind": "error", "text": str(exc)})
        return
    roll_id = secrets.token_urlsafe(9)
    PENDING_ROLLS[roll_id] = {"token": c.token, "specs": specs, "meta": meta, "ts": time.time()}
    await hub.send(c.ws, "roll.plan", {"id": roll_id, "dice": dice_plan(specs)})


async def op_roll_commit(c: Client, p: dict) -> None:
    """The client reports what its dice landed on; we do the arithmetic."""
    _prune_pending()
    pending = PENDING_ROLLS.pop(str(p.get("id", "")), None)
    if pending is None or pending["token"] != c.token:
        await hub.send(c.ws, "toast", {"kind": "error", "text": "that roll expired — try again"})
        return
    raw = p.get("values") or []
    values = [[int(v) for v in term][:MAX_DICE] for term in raw][:32]
    meta = pending["meta"]
    try:
        result = dice_evaluate(pending["specs"], meta["formula"], dice.supplied_draw(values))
    except DiceError as exc:
        await hub.send(c.ws, "toast", {"kind": "error", "text": str(exc)})
        return
    await _record_roll(c, meta, result)


async def op_roll(c: Client, p: dict) -> None:
    formula = str(p.get("formula", "")).strip()
    advantage = int(p.get("advantage", 0) or 0)
    secret = bool(p.get("secret")) and c.is_dm
    try:
        result = roll_dice(formula, advantage=advantage)
    except DiceError as exc:
        await hub.send(c.ws, "toast", {"kind": "error", "text": str(exc)})
        return
    meta = _roll_meta(c, p)
    meta["secret"] = secret
    await _record_roll(c, meta, result)


async def op_char_patch(c: Client, p: dict) -> None:
    char_id = int(p.get("id") or 0)
    if not c.is_dm and char_id != c.character_id:
        await hub.send(c.ws, "toast", {"kind": "error", "text": "that isn't your character"})
        return
    sheet, _ = load_sheet(char_id)
    deep_merge(sheet, p.get("patch") or {})
    clamp_hp(sheet)
    save_sheet(char_id, sheet)
    await push_characters()
    await push_initiative()
    await hub.broadcast("roster", state.roster())


async def op_char_hp(c: Client, p: dict) -> None:
    char_id = int(p.get("id") or 0)
    if not c.is_dm and char_id != c.character_id:
        return
    sheet, _ = load_sheet(char_id)
    hp = sheet["hp"]
    delta = int(p.get("delta") or 0)
    if delta < 0:  # damage eats temp HP first
        absorbed = min(hp["temp"], -delta)
        hp["temp"] -= absorbed
        delta += absorbed
    hp["current"] += delta
    if "set" in p:
        hp["current"] = int(p["set"])
    if "temp" in p:
        hp["temp"] = int(p["temp"])
    if hp["current"] > 0:
        sheet["death_saves"] = {"successes": 0, "failures": 0}
    clamp_hp(sheet)
    save_sheet(char_id, sheet)
    await push_characters()
    await push_initiative()


async def op_char_create(c: Client, p: dict) -> None:
    name = str(p.get("name") or "New Adventurer")[:60]
    sheet = db.default_sheet(name, str(p.get("player") or "")[:60])
    for key in ("klass", "race"):
        if p.get(key):
            sheet[key] = str(p[key])[:60]
    sheet["level"] = max(1, min(int(p.get("level") or 1), 20))
    if p.get("color"):
        sheet["color"] = str(p["color"])[:16]
    order = db.q1("SELECT COALESCE(MAX(sort_idx), 0) + 1 AS n FROM characters")["n"]
    db.run("INSERT INTO characters(name, sheet, sort_idx, updated_at) VALUES(?,?,?,?)",
           (name, json.dumps(sheet), order, db.now()))
    await push_characters()
    await hub.broadcast("roster", state.roster())


async def op_char_delete(c: Client, p: dict) -> None:
    char_id = int(p.get("id") or 0)
    db.run("DELETE FROM initiative WHERE character_id=?", (char_id,))
    db.run("DELETE FROM characters WHERE id=?", (char_id,))
    await push_characters()
    await push_initiative()
    await hub.broadcast("roster", state.roster())


# --- initiative -------------------------------------------------------------

async def op_init_add(c: Client, p: dict) -> None:
    count = max(1, min(int(p.get("count") or 1), 20))
    base = str(p.get("name") or "Monster")[:60]
    for i in range(count):
        name = base if count == 1 else f"{base} {i + 1}"
        init = p.get("init")
        if init in (None, ""):
            init = roll_dice(f"1d20+{int(p.get('init_mod') or 0)}").total
        hp = p.get("hp")
        db.run(
            "INSERT INTO initiative(name,character_id,init,hp,hp_max,ac,conditions,note,hidden)"
            " VALUES(?,?,?,?,?,?,'[]',?,?)",
            (name, None, float(init) + (0.001 * (count - i)), hp, hp, p.get("ac"),
             str(p.get("note") or "")[:200], int(bool(p.get("hidden")))),
        )
    await push_initiative()


async def op_init_add_party(c: Client, p: dict) -> None:
    existing = {r["character_id"] for r in db.q("SELECT character_id FROM initiative")}
    for ch in state.characters():
        if ch["id"] in existing:
            continue
        init = 0.0
        if p.get("roll"):
            init = roll_dice(f"1d20+{ch['derived']['initiative']}").total
        db.run(
            "INSERT INTO initiative(name,character_id,init,ac,conditions) VALUES(?,?,?,?,'[]')",
            (ch["name"], ch["id"], float(init), ch["sheet"]["ac"]),
        )
    await push_initiative()


async def op_init_update(c: Client, p: dict) -> None:
    row = db.q1("SELECT * FROM initiative WHERE id=?", (int(p.get("id") or 0),))
    if row is None:
        return
    patch = p.get("patch") or {}

    if row["character_id"] is not None and ("hp" in patch or "hp_delta" in patch or "conditions" in patch):
        sheet, _ = load_sheet(row["character_id"])
        if "hp_delta" in patch:
            delta = int(patch["hp_delta"])
            if delta < 0:
                absorbed = min(sheet["hp"]["temp"], -delta)
                sheet["hp"]["temp"] -= absorbed
                delta += absorbed
            sheet["hp"]["current"] += delta
        if "hp" in patch:
            sheet["hp"]["current"] = int(patch["hp"])
        if "conditions" in patch:
            sheet["conditions"] = patch["conditions"]
        clamp_hp(sheet)
        save_sheet(row["character_id"], sheet)
        await push_characters()

    fields, args = [], []
    for key in ("name", "init", "hp", "hp_max", "ac", "note", "hidden", "defeated"):
        if key in patch and not (row["character_id"] is not None and key == "hp"):
            fields.append(f"{key}=?")
            value = patch[key]
            if key in ("hidden", "defeated"):
                value = int(bool(value))
            args.append(value)
    if "hp_delta" in patch and row["character_id"] is None and row["hp"] is not None:
        fields.append("hp=?")
        args.append(max(0, row["hp"] + int(patch["hp_delta"])))
    if "conditions" in patch and row["character_id"] is None:
        fields.append("conditions=?")
        args.append(json.dumps(patch["conditions"]))
    if fields:
        args.append(row["id"])
        db.run(f"UPDATE initiative SET {', '.join(fields)} WHERE id=?", tuple(args))
    await push_initiative()


async def op_init_remove(c: Client, p: dict) -> None:
    db.run("DELETE FROM initiative WHERE id=?", (int(p.get("id") or 0),))
    await push_initiative()


async def op_init_clear(c: Client, p: dict) -> None:
    db.run("DELETE FROM initiative")
    db.set_meta("encounter", {"round": 0, "turn_id": None, "running": False})
    await push_initiative()


async def op_init_turn(c: Client, p: dict) -> None:
    action = p.get("action", "next")
    data = state.initiative()
    order = [e for e in data["entries"] if not e["defeated"]]
    enc = {"round": data["round"], "turn_id": data["turn_id"], "running": data["running"]}

    if action == "start":
        enc = {"round": 1, "turn_id": order[0]["id"] if order else None, "running": True}
    elif action == "stop":
        enc = {"round": 0, "turn_id": None, "running": False}
    elif order:
        ids = [e["id"] for e in order]
        try:
            idx = ids.index(enc["turn_id"])
        except ValueError:
            idx = -1 if action == "next" else 0
        step = 1 if action == "next" else -1
        nxt = idx + step
        if nxt >= len(ids):
            nxt, enc["round"] = 0, enc["round"] + 1
        elif nxt < 0:
            nxt, enc["round"] = len(ids) - 1, max(1, enc["round"] - 1)
        enc["turn_id"] = ids[nxt]
        enc["running"] = True
    db.set_meta("encounter", enc)
    await push_initiative()


# --- party ------------------------------------------------------------------

async def op_party_set(c: Client, p: dict) -> None:
    key = p.get("key")
    if key not in db.PARTY_DEFAULTS:
        return
    db.set_party(key, p.get("value"))
    await push_party()


def _journey() -> dict:
    return db.get_party("journey")


async def push_journey_change() -> None:
    await push_party()


def _valid_parent(locs: list[dict], parent, self_id: str | None = None) -> str | None:
    """A place is reached from another place — never from itself, and never in a
    loop, or the graph could not be drawn."""
    if not parent:
        return None
    parent = str(parent)
    by_id = {loc["id"]: loc for loc in locs}
    if parent not in by_id or parent == self_id:
        return None
    seen = set()
    walk = parent
    while walk and walk not in seen:
        if walk == self_id:
            return None                       # would close a cycle
        seen.add(walk)
        walk = by_id.get(walk, {}).get("from")
    return parent


async def op_journey_add(c: Client, p: dict) -> None:
    j = _journey()
    locs = j["locations"]
    # by default a new place follows on from the last one, making a straight road
    parent = p.get("from") if "from" in p else (locs[-1]["id"] if locs else None)
    loc = {
        "id": secrets.token_urlsafe(8),
        "name": str(p.get("name") or "New place")[:80],
        "body": str(p.get("body") or "")[:2000],
        "status": p.get("status") if p.get("status") in db.JOURNEY_STATUSES else "visited",
        "from": _valid_parent(locs, parent),
    }
    locs.append(loc)
    db.set_party("journey", j)
    await push_journey_change()


async def op_journey_update(c: Client, p: dict) -> None:
    j = _journey()
    patch = p.get("patch") or {}
    for loc in j["locations"]:
        if loc["id"] != p.get("id"):
            continue
        for key in ("name", "body"):
            if key in patch:
                loc[key] = str(patch[key])[:2000]
        if patch.get("status") in db.JOURNEY_STATUSES:
            loc["status"] = patch["status"]
        if "from" in patch:
            loc["from"] = _valid_parent(j["locations"], patch["from"], loc["id"])
        break
    db.set_party("journey", j)
    await push_journey_change()


async def op_journey_move(c: Client, p: dict) -> None:
    """Reorder the trail — the array order is the order they travelled."""
    j = _journey()
    locs = j["locations"]
    idx = next((i for i, loc in enumerate(locs) if loc["id"] == p.get("id")), None)
    if idx is None:
        return
    dest = max(0, min(idx + int(p.get("by") or 0), len(locs) - 1))
    locs.insert(dest, locs.pop(idx))
    db.set_party("journey", j)
    await push_journey_change()


async def op_journey_remove(c: Client, p: dict) -> None:
    j = _journey()
    gone = str(p.get("id", ""))
    parent = next((loc.get("from") for loc in j["locations"] if loc["id"] == gone), None)
    kept = []
    for loc in j["locations"]:
        if loc["id"] == gone:
            continue
        if loc.get("from") == gone:           # reattach its children further up
            loc["from"] = parent
        kept.append(loc)
    j["locations"] = kept
    db.set_party("journey", j)
    await push_journey_change()


async def op_journey_here(c: Client, p: dict) -> None:
    """Mark one place as where the party is now; anything already passed becomes visited."""
    j = _journey()
    for loc in j["locations"]:
        if loc["id"] == p.get("id"):
            loc["status"] = "current"
        elif loc["status"] == "current":
            loc["status"] = "visited"
    db.set_party("journey", j)
    await push_journey_change()
    here = next((l for l in j["locations"] if l["id"] == p.get("id")), None)
    if here:
        await hub.broadcast("toast", {"kind": "announce", "text": f"The party arrives at {here['name']}"})


# ---------------------------------------------------------------- loot

def _loot() -> list[dict]:
    return [db.normalize_loot(it) for it in db.get_party("loot")]


def _valid_owner(owner) -> int | None:
    """Loot belongs to a real character or to the shared pile. Nothing else."""
    if owner in (None, "", "shared"):
        return None
    try:
        owner = int(owner)
    except (TypeError, ValueError):
        raise HTTPException(400, "loot must go to a character or the shared pile")
    if db.q1("SELECT 1 FROM characters WHERE id=?", (owner,)) is None:
        raise HTTPException(400, "no such character to give that to")
    return owner


def _may_hold(c: Client, owner) -> bool:
    """A player can put things in their own pack or the shared pile; sending
    something to another player is the DM's call."""
    return c.is_dm or owner in (None, c.character_id)


def _may_touch(c: Client, it: dict) -> bool:
    """A player can edit what they are carrying, and the communal pile."""
    return c.is_dm or it["owner"] in (None, c.character_id)


async def _deny(c: Client, text: str) -> None:
    await hub.send(c.ws, "toast", {"kind": "error", "text": text})


async def op_loot_add(c: Client, p: dict) -> None:
    name = str(p.get("name") or "").strip()[:80]
    if not name:
        return
    owner = _valid_owner(p.get("owner"))
    if not _may_hold(c, owner):
        return await _deny(c, "ask the DM to give that to someone else")
    items = _loot()
    items.append(db.normalize_loot({
        "name": name,
        "qty": p.get("qty", 1),
        "notes": p.get("notes", ""),
        "owner": owner,
    }))
    db.set_party("loot", items)
    await push_party()


async def op_loot_update(c: Client, p: dict) -> None:
    items = _loot()
    patch = p.get("patch") or {}
    for it in items:
        if it["id"] != str(p.get("id", "")):
            continue
        if not _may_touch(c, it):
            return await _deny(c, "that isn't yours to change")
        if "owner" in patch and not _may_hold(c, _valid_owner(patch["owner"])):
            return await _deny(c, "ask the DM to give that to someone else")
        if "name" in patch:
            it["name"] = str(patch["name"]).strip()[:80] or it["name"]
        if "qty" in patch:
            it["qty"] = max(1, int(patch["qty"] or 1))
        if "notes" in patch:
            it["notes"] = str(patch["notes"])[:200]
        if "owner" in patch:
            it["owner"] = _valid_owner(patch["owner"])
        break
    db.set_party("loot", items)
    await push_party()


async def op_loot_remove(c: Client, p: dict) -> None:
    items = _loot()
    doomed = next((it for it in items if it["id"] == str(p.get("id", ""))), None)
    if doomed is None:
        return
    # Players can throw away their own things; clearing the shared pile is the
    # DM's job, so nobody can bin the party's treasure by accident.
    if not c.is_dm and doomed["owner"] != c.character_id:
        return await _deny(c, "only the DM can remove that")
    db.set_party("loot", [it for it in items if it["id"] != doomed["id"]])
    await push_party()


async def op_loot_move(c: Client, p: dict) -> None:
    """Pick something up or put it down. A player can only move loot between the
    shared pile and their own pack — handing it to someone else is the DM's call."""
    owner = _valid_owner(p.get("owner"))
    if not _may_hold(c, owner):
        return await _deny(c, "ask the DM to hand that to someone else")
    items = _loot()
    for it in items:
        if it["id"] == str(p.get("id", "")):
            it["owner"] = owner
            break
    db.set_party("loot", items)
    await push_party()


# ---------------------------------------------------------------- handouts

def _handouts() -> list[dict]:
    return [db.normalize_handout(h) for h in db.get_party("handouts")]


async def op_handout_save(c: Client, p: dict) -> None:
    """Write one, or rewrite it. Saving alone never shows it to anyone; passing
    `push` saves and reveals in the same step, so writing one on the fly doesn't
    need the client to guess the id it is about to be given."""
    items = _handouts()
    hid = str(p.get("id") or "")
    target = next((h for h in items if h["id"] == hid), None)
    if target is None:
        target = db.normalize_handout({
            "title": p.get("title"), "body": p.get("body"), "image": p.get("image"),
        })
        items.append(target)
    else:
        if p.get("title") is not None:
            target["title"] = str(p["title"])[:120] or target["title"]
        if p.get("body") is not None:
            target["body"] = str(p["body"])[:8000]
        if "image" in p:
            target["image"] = str(p["image"])[:80] if p["image"] else None

    if p.get("push"):
        target["revealed"] = True
        target["ts"] = db.now()

    items = [db.normalize_handout(h) for h in items]
    db.set_party("handouts", items)
    await push_party()
    if p.get("push"):
        await hub.broadcast("handout", next(h for h in items if h["id"] == target["id"]))


async def op_handout_remove(c: Client, p: dict) -> None:
    db.set_party("handouts", [h for h in _handouts() if h["id"] != str(p.get("id", ""))])
    await push_party()


async def op_handout_push(c: Client, p: dict) -> None:
    """Reveal one and pop it on every phone. It stays in the players' list
    afterwards so they can read it again later."""
    items = _handouts()
    shown = None
    for h in items:
        if h["id"] == str(p.get("id", "")):
            h["revealed"] = True
            h["ts"] = db.now()
            shown = h
            break
    if shown is None:
        return
    db.set_party("handouts", items)
    await push_party()
    await hub.broadcast("handout", shown)


async def op_handout_hide(c: Client, p: dict) -> None:
    """Take one back out of the players' hands."""
    items = _handouts()
    for h in items:
        if h["id"] == str(p.get("id", "")):
            h["revealed"] = False
            break
    db.set_party("handouts", items)
    await push_party()


async def op_announce(c: Client, p: dict) -> None:
    await hub.broadcast("toast", {"kind": "announce", "text": str(p.get("text", ""))[:400]})


async def push_campaigns() -> None:
    await hub.to_dms("campaigns", db.campaigns())


async def op_campaign(c: Client, p: dict) -> None:
    """Rename a campaign — the active one unless another is named."""
    db.rename_campaign(str(p.get("id") or db.active_id()), str(p.get("name", "")))
    await hub.broadcast("campaign", state.campaign())
    await push_campaigns()


async def op_campaign_create(c: Client, p: dict) -> None:
    cid = db.create_campaign(str(p.get("name") or "New Campaign"))
    if p.get("switch"):
        await op_campaign_switch(c, {"id": cid})
    else:
        await push_campaigns()


async def op_campaign_switch(c: Client, p: dict) -> None:
    """Put a different campaign on the table.

    Everything — characters, loot, the journey, and the device tokens players
    joined with — lives inside a campaign's own file, so switching invalidates
    every session. Players are sent back to the join screen to pick a character
    in the new campaign; the DM who threw the switch is carried across so they
    don't have to sign back in mid-sentence.
    """
    cid = str(p.get("id") or "")
    if cid == db.active_id() or not db.open_campaign(cid):
        return
    db.run("INSERT OR REPLACE INTO devices(token, display_name, role, character_id,"
           " created_at, last_seen) VALUES(?,?,?,?,?,?)",
           (c.token, c.name or "DM", "dm", None, db.now(), db.now()))
    await hub.broadcast("campaign.switched", {"id": cid, "name": db.campaign_name()})


async def op_campaign_delete(c: Client, p: dict) -> None:
    if not db.delete_campaign(str(p.get("id") or "")):
        await hub.send(c.ws, "toast", {"kind": "error",
                                       "text": "can't delete the campaign you're playing"})
    await push_campaigns()


# Loot is not in here: it has its own ops so the rules about who can own what
# are enforced server-side rather than trusted to the client's bulk write.
PLAYER_SAFE_PARTY_KEYS = {"gold", "notes"}


async def op_party_set_player(c: Client, p: dict) -> None:
    """Players share the treasury, loot pile and session notes; quests/NPCs stay DM-side."""
    if c.is_dm:
        return await op_party_set(c, p)
    if p.get("key") not in PLAYER_SAFE_PARTY_KEYS:
        await hub.send(c.ws, "toast", {"kind": "error", "text": "the DM keeps that one"})
        return
    await op_party_set(c, p)


async def op_init_self(c: Client, p: dict) -> None:
    """A player sets initiative on their own row only."""
    if c.character_id is None:
        return
    row = db.q1("SELECT * FROM initiative WHERE character_id=?", (c.character_id,))
    value = float(p.get("init") or 0)
    if row is None:
        sheet, name = load_sheet(c.character_id)
        db.run(
            "INSERT INTO initiative(name,character_id,init,ac,conditions) VALUES(?,?,?,?,'[]')",
            (name, c.character_id, value, sheet["ac"]),
        )
    else:
        db.run("UPDATE initiative SET init=? WHERE id=?", (value, row["id"]))
    await push_initiative()


PLAYER_OPS = {
    "roll": op_roll,
    "roll.plan": op_roll_plan,
    "roll.commit": op_roll_commit,
    "char.patch": op_char_patch,
    "char.hp": op_char_hp,
    "party.set": op_party_set_player,
    "init.self": op_init_self,
    "loot.add": op_loot_add,
    "loot.update": op_loot_update,
    "loot.remove": op_loot_remove,
    "loot.move": op_loot_move,
}
DM_OPS = {
    "char.create": op_char_create,
    "char.delete": op_char_delete,
    "init.add": op_init_add,
    "init.add_party": op_init_add_party,
    "init.update": op_init_update,
    "init.remove": op_init_remove,
    "init.clear": op_init_clear,
    "init.turn": op_init_turn,
    "party.set": op_party_set,
    "loot.add": op_loot_add,
    "loot.update": op_loot_update,
    "loot.remove": op_loot_remove,
    "loot.move": op_loot_move,
    "handout.save": op_handout_save,
    "handout.remove": op_handout_remove,
    "handout.push": op_handout_push,
    "handout.hide": op_handout_hide,
    "journey.add": op_journey_add,
    "journey.update": op_journey_update,
    "journey.move": op_journey_move,
    "journey.remove": op_journey_remove,
    "journey.here": op_journey_here,
    "announce": op_announce,
    "campaign.rename": op_campaign,
    "campaign.create": op_campaign_create,
    "campaign.switch": op_campaign_switch,
    "campaign.delete": op_campaign_delete,
}


# ------------------------------------------------------------------ websocket

@app.websocket("/ws")
async def ws_endpoint(ws: WebSocket, token: str = ""):
    dev = device(token)
    if dev is None:
        await ws.close(code=4401)
        return
    await ws.accept()
    client = Client(ws, token, dev["display_name"], dev["role"], dev["character_id"])
    await hub.add(client)
    db.run("UPDATE devices SET last_seen=? WHERE token=?", (db.now(), token))

    try:
        await hub.send(ws, "snapshot", state.snapshot(client.is_dm))
        await hub.broadcast("presence", hub.presence())
        while True:
            msg = await ws.receive_json()
            op = msg.get("op")
            payload = msg.get("data") or {}
            handler = (DM_OPS.get(op) if client.is_dm else None) or PLAYER_OPS.get(op)
            if handler is None:
                await hub.send(ws, "toast", {"kind": "error", "text": f"not allowed: {op}"})
                continue
            try:
                await handler(client, payload)
            except HTTPException as exc:
                await hub.send(ws, "toast", {"kind": "error", "text": exc.detail})
            except Exception as exc:  # keep one bad message from killing the socket
                await hub.send(ws, "toast", {"kind": "error", "text": f"{op} failed: {exc}"})
    except WebSocketDisconnect:
        pass
    finally:
        await hub.drop(ws)
        await hub.broadcast("presence", hub.presence())


# ------------------------------------------------------------------ startup

PORT = int(os.environ.get("DND_PORT") or 8787)
APP_URL = ""


def banner() -> None:
    """Print the join QR. Best effort, and deliberately unable to fail: the QR
    is drawn from half-block glyphs, and a Windows console with output
    redirected encodes as cp1252, which cannot represent them. Losing the
    picture is a nuisance; taking the server down before it binds a port
    because nobody could see the picture is not acceptable."""
    import io

    import qrcode

    if sys.stdout is None:  # a windowed build has no stdout at all
        return

    lines = []
    try:
        qr = qrcode.QRCode(border=1)
        qr.add_data(APP_URL)
        buf = io.StringIO()
        qr.print_ascii(out=buf, invert=True)
        lines.append(buf.getvalue())
    except Exception:
        pass
    lines.append(f"  Players join at:  {APP_URL}")
    lines.append(f"  DM console:       {APP_URL}/dm")
    lines.append(f"  Playing:          {db.campaign_name()}  ({db.DB_PATH})")
    lines.append("\n  Ctrl-C to stop.\n")
    text = "\n".join(lines)

    encoding = getattr(sys.stdout, "encoding", None) or "ascii"
    try:
        text.encode(encoding)
    except UnicodeEncodeError:
        text = "\n".join(lines[1:])  # drop the QR, keep the URL that matters
    try:
        print(text, flush=True)  # flush: stdout is block-buffered when piped
    except Exception:
        pass


def main() -> None:
    global APP_URL
    import uvicorn

    db.init_registry()
    # lan_ip() picks the interface with the default route, which on a Windows
    # box with Hyper-V, WSL or a VPN adapter may not be the one the phones are
    # on. DND_URL is the override when the QR points somewhere unreachable.
    APP_URL = os.environ.get("DND_URL") or f"http://{lan_ip()}:{PORT}"

    # A frozen build has no console to print to and no Ctrl-C to press, so it
    # gets a window instead. --window forces the same from a checkout, and
    # --no-window forces it off, which is how a build gets smoke-tested on a
    # machine with no desktop to open a window on.
    windowed = (paths.FROZEN or "--window" in sys.argv) and "--no-window" not in sys.argv
    if windowed:
        from .desktop import run_windowed

        run_windowed(app, APP_URL, PORT, db.campaign_name(), db.DATA_DIR)
        return

    banner()
    uvicorn.run(app, host="0.0.0.0", port=PORT, log_level="warning")


if __name__ == "__main__":
    main()
