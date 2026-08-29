"""SQLite storage. Small dataset, single writer, WAL mode -> one shared connection."""

from __future__ import annotations

import json
import os
import pathlib
import secrets
import sqlite3
import threading
from datetime import datetime, timezone
from pathlib import Path

DATA_DIR = Path(os.environ.get("DND_DATA_DIR") or Path(__file__).resolve().parent.parent / "data")
UPLOADS_DIR = DATA_DIR / "uploads"
CAMPAIGNS_DIR = DATA_DIR / "campaigns"
REGISTRY_PATH = DATA_DIR / "registry.db"

# Each campaign is its own SQLite file, and a small registry says which one is
# being played. Keeping them separate means a campaign is one file you can copy,
# archive or delete, and no query anywhere has to filter by campaign.
DB_PATH = DATA_DIR / "campaign.db"

_lock = threading.RLock()
_conn: sqlite3.Connection | None = None

SCHEMA = """
CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS devices (
    token        TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    role         TEXT NOT NULL,
    character_id INTEGER,
    created_at   TEXT NOT NULL,
    last_seen    TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS characters (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    sheet      TEXT NOT NULL,
    sort_idx   INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS rolls (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    ts           TEXT NOT NULL,
    actor        TEXT NOT NULL,
    character_id INTEGER,
    label        TEXT NOT NULL DEFAULT '',
    formula      TEXT NOT NULL,
    total        INTEGER NOT NULL,
    detail       TEXT NOT NULL,
    crit         TEXT,
    secret       INTEGER NOT NULL DEFAULT 0,
    terms        TEXT NOT NULL DEFAULT '[]',
    by_device    TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS initiative (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL,
    character_id INTEGER,
    init         REAL NOT NULL DEFAULT 0,
    hp           INTEGER,
    hp_max       INTEGER,
    ac           INTEGER,
    conditions   TEXT NOT NULL DEFAULT '[]',
    note         TEXT NOT NULL DEFAULT '',
    hidden       INTEGER NOT NULL DEFAULT 0,
    defeated     INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS party (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
"""

# ---------------------------------------------------------------- 5e model

SKILLS: dict[str, str] = {
    "acrobatics": "dex", "animal_handling": "wis", "arcana": "int", "athletics": "str",
    "deception": "cha", "history": "int", "insight": "wis", "intimidation": "cha",
    "investigation": "int", "medicine": "wis", "nature": "int", "perception": "wis",
    "performance": "cha", "persuasion": "cha", "religion": "int",
    "sleight_of_hand": "dex", "stealth": "dex", "survival": "wis",
}
ABILITIES = ["str", "dex", "con", "int", "wis", "cha"]

PARTY_DEFAULTS: dict[str, object] = {
    "gold": {"pp": 0, "gp": 0, "ep": 0, "sp": 0, "cp": 0},
    "loot": [],
    "quests": [],
    "npcs": [],
    "notes": {"text": ""},
    # Handouts are written in advance and kept; pushing one reveals it.
    "handouts": [],
    # The journey: places the party has reached, linked by where they travelled
    # from, so the whole campaign draws as a graph.
    "journey": {"locations": []},
}

# A location is visible to players unless the DM marks it "hidden".
JOURNEY_STATUSES = ["visited", "current", "rumored", "hidden"]


def now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def default_sheet(name: str = "New Adventurer", player: str = "") -> dict:
    return {
        "name": name,
        "player": player,
        "klass": "",
        "subclass": "",
        "race": "",
        "background": "",
        "alignment": "",
        "level": 1,
        "color": "#c9a227",
        "abilities": {a: 10 for a in ABILITIES},
        "save_prof": {a: False for a in ABILITIES},
        "skill_prof": {s: 0 for s in SKILLS},  # 0 none, 1 proficient, 2 expertise
        "ac": 10,
        "speed": 30,
        "initiative_bonus": 0,
        "hp": {"current": 10, "max": 10, "temp": 0},
        "hit_dice": {"die": "d8", "total": 1, "used": 0},
        "death_saves": {"successes": 0, "failures": 0},
        "inspiration": False,
        "conditions": [],
        "spell": {
            "ability": "",
            "slots": {str(lvl): {"total": 0, "used": 0} for lvl in range(1, 10)},
            "prepared": "",
        },
        "attacks": [],
        "gold": {"pp": 0, "gp": 0, "ep": 0, "sp": 0, "cp": 0},
        "features": "",
        "notes": "",
    }


def ability_mod(score: int) -> int:
    return (int(score) - 10) // 2


def prof_bonus(level: int) -> int:
    return 2 + max(0, (int(level) - 1)) // 4


def derive(sheet: dict) -> dict:
    """Derived stats computed server-side so DM and player views never disagree."""
    abilities = {a: int(sheet.get("abilities", {}).get(a, 10)) for a in ABILITIES}
    mods = {a: ability_mod(v) for a, v in abilities.items()}
    pb = prof_bonus(sheet.get("level", 1))

    saves = {}
    for a in ABILITIES:
        saves[a] = mods[a] + (pb if sheet.get("save_prof", {}).get(a) else 0)

    skills = {}
    for skill, abil in SKILLS.items():
        rank = int(sheet.get("skill_prof", {}).get(skill, 0) or 0)
        skills[skill] = mods[abil] + pb * min(rank, 2)

    spell_ability = sheet.get("spell", {}).get("ability") or ""
    spell_mod = mods.get(spell_ability, 0)
    return {
        "mods": mods,
        "prof_bonus": pb,
        "saves": saves,
        "skills": skills,
        "initiative": mods["dex"] + int(sheet.get("initiative_bonus", 0) or 0),
        "passive_perception": 10 + skills["perception"],
        "passive_investigation": 10 + skills["investigation"],
        "spell_save_dc": (8 + pb + spell_mod) if spell_ability else None,
        "spell_attack": (pb + spell_mod) if spell_ability else None,
    }


# ---------------------------------------------------------------- connection

# ---------------------------------------------------------------- registry

REGISTRY_SCHEMA = """
CREATE TABLE IF NOT EXISTS campaigns (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    file       TEXT NOT NULL,
    created    TEXT NOT NULL,
    last_played TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS registry_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
"""

_registry: sqlite3.Connection | None = None


def registry() -> sqlite3.Connection:
    global _registry
    if _registry is None:
        DATA_DIR.mkdir(parents=True, exist_ok=True)
        CAMPAIGNS_DIR.mkdir(parents=True, exist_ok=True)
        _registry = sqlite3.connect(REGISTRY_PATH, check_same_thread=False)
        _registry.row_factory = sqlite3.Row
        _registry.executescript(REGISTRY_SCHEMA)
        _registry.commit()
    return _registry


def _reg_run(sql: str, args: tuple = ()) -> None:
    with _lock:
        conn = registry()
        conn.execute(sql, args)
        conn.commit()


def _reg_all(sql: str, args: tuple = ()) -> list[sqlite3.Row]:
    with _lock:
        return registry().execute(sql, args).fetchall()


def campaigns() -> list[dict]:
    rows = _reg_all("SELECT * FROM campaigns ORDER BY last_played DESC, created DESC")
    active = active_id()
    return [{"id": r["id"], "name": r["name"], "created": r["created"],
             "last_played": r["last_played"], "active": r["id"] == active} for r in rows]


def active_id() -> str | None:
    rows = _reg_all("SELECT value FROM registry_meta WHERE key='active'")
    return rows[0]["value"] if rows else None


def create_campaign(name: str) -> str:
    cid = secrets.token_urlsafe(8)
    CAMPAIGNS_DIR.mkdir(parents=True, exist_ok=True)
    _reg_run("INSERT INTO campaigns(id,name,file,created,last_played) VALUES(?,?,?,?,?)",
             (cid, (name or "New Campaign").strip()[:80] or "New Campaign",
              str(CAMPAIGNS_DIR / f"{cid}.db"), now(), now()))
    return cid


def rename_campaign(cid: str, name: str) -> None:
    _reg_run("UPDATE campaigns SET name=? WHERE id=?", ((name or "").strip()[:80] or "Untitled", cid))


def delete_campaign(cid: str) -> bool:
    """Forget a campaign and remove its file. Refuses to delete the last one."""
    rows = _reg_all("SELECT * FROM campaigns WHERE id=?", (cid,))
    if not rows or len(_reg_all("SELECT id FROM campaigns")) <= 1:
        return False
    if cid == active_id():
        return False
    path = pathlib.Path(rows[0]["file"])
    _reg_run("DELETE FROM campaigns WHERE id=?", (cid,))
    for suffix in ("", "-wal", "-shm"):
        f = pathlib.Path(str(path) + suffix)
        if f.exists():
            f.unlink()
    return True


def campaign_name(cid: str | None = None) -> str:
    cid = cid or active_id()
    rows = _reg_all("SELECT name FROM campaigns WHERE id=?", (cid,))
    return rows[0]["name"] if rows else "The Campaign"


def open_campaign(cid: str) -> bool:
    """Point the app at another campaign's file."""
    global _conn, DB_PATH
    rows = _reg_all("SELECT * FROM campaigns WHERE id=?", (cid,))
    if not rows:
        return False
    with _lock:
        if _conn is not None:
            _conn.close()
            _conn = None
        DB_PATH = pathlib.Path(rows[0]["file"])
    _reg_run("UPDATE registry_meta SET value=? WHERE key='active'", (cid,))
    _reg_run("INSERT OR IGNORE INTO registry_meta(key,value) VALUES('active',?)", (cid,))
    _reg_run("UPDATE campaigns SET last_played=? WHERE id=?", (now(), cid))
    init_db()
    return True


def init_registry() -> None:
    """Make sure there is a registry with at least one campaign, and open it."""
    registry()
    if not _reg_all("SELECT id FROM campaigns"):
        legacy = DATA_DIR / "campaign.db"
        if legacy.exists():
            # the single-campaign file this app used to have becomes campaign one
            cid = secrets.token_urlsafe(8)
            name = "The Campaign"
            try:
                con = sqlite3.connect(f"file:{legacy}?mode=ro", uri=True)
                row = con.execute("SELECT value FROM meta WHERE key='campaign_name'").fetchone()
                con.close()
                if row:
                    name = json.loads(row[0])
            except sqlite3.Error:
                pass
            _reg_run("INSERT INTO campaigns(id,name,file,created,last_played) VALUES(?,?,?,?,?)",
                     (cid, name, str(legacy), now(), now()))
        else:
            cid = create_campaign("The Campaign")
        _reg_run("INSERT OR REPLACE INTO registry_meta(key,value) VALUES('active',?)", (cid,))
    open_campaign(active_id() or campaigns()[0]["id"])


def connect() -> sqlite3.Connection:
    global _conn
    if _conn is None:
        DATA_DIR.mkdir(parents=True, exist_ok=True)
        _conn = sqlite3.connect(DB_PATH, check_same_thread=False)
        _conn.row_factory = sqlite3.Row
        _conn.execute("PRAGMA journal_mode=WAL")
        _conn.execute("PRAGMA foreign_keys=ON")
        _conn.executescript(SCHEMA)
        _conn.commit()
    return _conn


def q(sql: str, args: tuple = ()) -> list[sqlite3.Row]:
    with _lock:
        return connect().execute(sql, args).fetchall()


def q1(sql: str, args: tuple = ()) -> sqlite3.Row | None:
    rows = q(sql, args)
    return rows[0] if rows else None


def run(sql: str, args: tuple = ()) -> sqlite3.Cursor:
    with _lock:
        conn = connect()
        cur = conn.execute(sql, args)
        conn.commit()
        return cur


def get_meta(key: str, default=None):
    row = q1("SELECT value FROM meta WHERE key=?", (key,))
    return json.loads(row["value"]) if row else default


def set_meta(key: str, value) -> None:
    run(
        "INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
        (key, json.dumps(value)),
    )


def get_party(key: str):
    row = q1("SELECT value FROM party WHERE key=?", (key,))
    if row:
        return json.loads(row["value"])
    return json.loads(json.dumps(PARTY_DEFAULTS[key]))


def set_party(key: str, value) -> None:
    run(
        "INSERT INTO party(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
        (key, json.dumps(value)),
    )


def migrate() -> None:
    """Additive migrations so an existing campaign.db keeps working."""
    have = {r["name"] for r in q("PRAGMA table_info(rolls)")}
    for col, decl in (("terms", "TEXT NOT NULL DEFAULT '[]'"),
                      ("by_device", "TEXT NOT NULL DEFAULT ''")):
        if col not in have:
            run(f"ALTER TABLE rolls ADD COLUMN {col} {decl}")

    # Loot used to record a free-text "claimed_by" name, which meant it could be
    # assigned to anyone at all. It now belongs either to a real character or to
    # the shared pile, so convert the old names to ids where they still match.
    # There used to be exactly one handout, overwritten each time. Keep whatever
    # was in it as the first entry of the library.
    row = q1("SELECT value FROM party WHERE key='handout'")
    if row:
        old = json.loads(row["value"])
        if old.get("title") or old.get("body"):
            existing = get_party("handouts")
            set_party("handouts", existing + [{
                "id": secrets.token_urlsafe(8),
                "title": old.get("title") or "Handout",
                "body": old.get("body") or "",
                "image": None,
                "revealed": bool(old.get("shown")),
                "ts": old.get("ts") or now(),
            }])
        run("DELETE FROM party WHERE key='handout'")

    # The journey used to be pinned to an uploaded map image. It is now a graph,
    # so each place records which place it was reached from; the old flat order
    # becomes a straight chain.
    # The DM console used to be behind a pin. It runs on trust now, so drop the
    # stored secret rather than leaving it lying in the campaign file.
    run("DELETE FROM meta WHERE key='dm_pin'")

    row = q1("SELECT value FROM party WHERE key='journey'")
    if row:
        data = json.loads(row["value"])
        locs = data.get("locations", [])
        if "map" in data or any("from" not in loc for loc in locs):
            prev = None
            chain = []
            for loc in locs:
                item = {k: v for k, v in loc.items() if k not in ("x", "y")}
                item.setdefault("from", prev)
                chain.append(item)
                prev = item["id"]
            set_party("journey", {"locations": chain})

    row = q1("SELECT value FROM party WHERE key='loot'")
    if row:
        items = json.loads(row["value"])
        if any("claimed_by" in it or "owner" not in it for it in items):
            names = {r["name"]: r["id"] for r in q("SELECT id, name FROM characters")}
            set_party("loot", [normalize_loot(it, names) for it in items])

    # A character's pack and the loot ledger were two lists describing the same
    # things. Fold each sheet's inventory into the loot list, owned by them.
    moved = []
    for r in q("SELECT id, sheet FROM characters"):
        sheet = json.loads(r["sheet"])
        if "inventory" not in sheet:
            continue
        for it in sheet.pop("inventory") or []:
            moved.append(normalize_loot({**it, "owner": r["id"]}))
        run("UPDATE characters SET sheet=?, updated_at=? WHERE id=?",
            (json.dumps(sheet), now(), r["id"]))
    if moved:
        set_party("loot", [normalize_loot(it) for it in get_party("loot")] + moved)


def normalize_loot(it: dict, names: dict[str, int] | None = None) -> dict:
    """One loot item in its current shape, whatever shape it arrived in."""
    owner = it.get("owner")
    if owner is None and it.get("claimed_by") and names is not None:
        owner = names.get(str(it["claimed_by"]).strip())
    return {
        "id": str(it.get("id") or secrets.token_urlsafe(8)),
        "name": str(it.get("name") or "")[:80],
        "qty": max(1, int(it.get("qty") or 1)),
        "notes": str(it.get("notes") or "")[:200],
        "owner": int(owner) if owner is not None else None,
    }


def device_id(token: str) -> str:
    """Stable public id for a device — lets a client recognise its own rolls
    without any token material going out over the broadcast."""
    import hashlib
    return hashlib.sha256(token.encode()).hexdigest()[:12]


def normalize_handout(h: dict) -> dict:
    return {
        "id": str(h.get("id") or secrets.token_urlsafe(8)),
        "title": str(h.get("title") or "Untitled")[:120],
        "body": str(h.get("body") or "")[:8000],
        "image": (str(h["image"])[:80] if h.get("image") else None),
        "revealed": bool(h.get("revealed")),
        "ts": str(h.get("ts") or now()),
    }


def init_db() -> None:
    """Create schema and seed defaults."""
    connect()
    UPLOADS_DIR.mkdir(parents=True, exist_ok=True)
    migrate()
    if get_meta("campaign_name") is None:
        set_meta("campaign_name", "The Campaign")
    if get_meta("encounter") is None:
        set_meta("encounter", {"round": 0, "turn_id": None, "running": False})
    for key in PARTY_DEFAULTS:
        if q1("SELECT 1 FROM party WHERE key=?", (key,)) is None:
            set_party(key, PARTY_DEFAULTS[key])
