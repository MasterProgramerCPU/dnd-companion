"""WebSocket fan-out. Every connection carries its device row so we can gate DM-only data."""

from __future__ import annotations

import asyncio
from dataclasses import dataclass

from fastapi import WebSocket


@dataclass
class Client:
    ws: WebSocket
    token: str
    name: str
    role: str
    character_id: int | None

    @property
    def is_dm(self) -> bool:
        return self.role == "dm"


class Hub:
    def __init__(self) -> None:
        self.clients: dict[WebSocket, Client] = {}
        self._lock = asyncio.Lock()

    async def add(self, client: Client) -> None:
        async with self._lock:
            self.clients[client.ws] = client

    async def drop(self, ws: WebSocket) -> None:
        async with self._lock:
            self.clients.pop(ws, None)

    def presence(self) -> list[dict]:
        seen, out = set(), []
        for c in self.clients.values():
            key = (c.role, c.character_id, c.name)
            if key in seen:
                continue
            seen.add(key)
            out.append({"name": c.name, "role": c.role, "character_id": c.character_id})
        return out

    async def send(self, ws: WebSocket, ev: str, data) -> None:
        try:
            await ws.send_json({"ev": ev, "data": data})
        except Exception:
            await self.drop(ws)

    async def broadcast(self, ev: str, data, *, dm_only: bool = False) -> None:
        for ws, client in list(self.clients.items()):
            if dm_only and not client.is_dm:
                continue
            await self.send(ws, ev, data)

    async def broadcast_split(self, ev: str, dm_data, player_data) -> None:
        """Same event, different payload depending on who is listening."""
        for ws, client in list(self.clients.items()):
            await self.send(ws, ev, dm_data if client.is_dm else player_data)

    async def to_dms(self, ev: str, data) -> None:
        await self.broadcast(ev, data, dm_only=True)


hub = Hub()
