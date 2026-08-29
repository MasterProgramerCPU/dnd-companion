/* Shared client runtime: identity, websocket, DOM helpers, dice UI. */

const S = {
  token: null,
  me: null,
  campaign: { name: '' },
  characters: [],
  initiative: { entries: [], round: 0, turn_id: null, running: false },
  rolls: [],
  party: {},
  presence: [],
  campaigns: [],
  conditions: [],
  skills: {},
  connected: false,
};

const listeners = {};
function on(ev, fn) { (listeners[ev] ||= []).push(fn); }
function emit(ev, data) { (listeners[ev] || []).forEach(fn => fn(data)); }

/* ------------------------------------------------------------ DOM helpers */

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

function el(tag, attrs = {}, ...kids) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === null || v === undefined || v === false) continue;
    if (k === 'class') node.className = v;
    else if (k === 'html') node.innerHTML = v;
    else if (k.startsWith('on')) node.addEventListener(k.slice(2), v);
    else if (k === 'dataset') Object.assign(node.dataset, v);
    else if (k in node && k !== 'list' && typeof v !== 'object') node[k] = v;
    else node.setAttribute(k, v);
  }
  for (const kid of kids.flat()) {
    if (kid === null || kid === undefined || kid === false) continue;
    node.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  }
  return node;
}

const sign = n => (n >= 0 ? `+${n}` : `${n}`);
const titleCase = s => String(s || '').replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase());
const escapeHtml = s => String(s ?? '').replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));

/** Renders dice detail: ~~5~~ -> struck through. */
function detailHtml(text) {
  return escapeHtml(text).replace(/~~(.+?)~~/g, '<del>$1</del>');
}

/** True only while the user is typing inside `root` — buttons don't count.
 *  Re-rendering under a focused text field would eat their keystrokes; re-rendering
 *  under a just-clicked button is exactly what we want. */
function editing(root) {
  const a = document.activeElement;
  return !!a && root.contains(a) && /^(INPUT|TEXTAREA|SELECT)$/.test(a.tagName)
    && a.type !== 'checkbox' && a.type !== 'button';
}

/* ------------------------------------------------------------ transport */

async function api(path, opts = {}) {
  const res = await fetch(path, {
    headers: { 'Content-Type': 'application/json' },
    ...opts,
    body: opts.body ? JSON.stringify(opts.body) : undefined,
  });
  const payload = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(payload.detail || res.statusText);
  return payload;
}

let ws = null;
let retry = 0;

function send(op, data = {}) {
  if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ op, data }));
  else toast('Not connected yet', 'error');
}

function connect() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  ws = new WebSocket(`${proto}://${location.host}/ws?token=${encodeURIComponent(S.token)}`);

  ws.onopen = () => { retry = 0; S.connected = true; emit('conn', true); };
  ws.onclose = (e) => {
    S.connected = false;
    emit('conn', false);
    if (e.code === 4401) { logout(); return; }
    retry = Math.min(retry + 1, 6);
    setTimeout(connect, 400 * retry);
  };
  ws.onmessage = (e) => {
    const { ev, data } = JSON.parse(e.data);
    if (ev === 'snapshot') Object.assign(S, data);
    else if (ev === 'characters') S.characters = data;
    else if (ev === 'initiative') S.initiative = data;
    else if (ev === 'party') S.party = data;
    else if (ev === 'campaign') S.campaign = data;
    else if (ev === 'campaigns') S.campaigns = data;
    else if (ev === 'presence') S.presence = data;
    else if (ev === 'roll') { S.rolls.push(data); S.rolls = S.rolls.slice(-60); }
    emit(ev, data);
    emit('any', ev);
  };
}

function logout() {
  localStorage.removeItem('dnd_token');
  location.href = '/';
}

async function boot() {
  S.token = localStorage.getItem('dnd_token');
  if (!S.token) { location.href = '/'; return false; }
  try {
    S.me = await api(`/api/me?token=${encodeURIComponent(S.token)}`);
  } catch {
    logout();
    return false;
  }
  S.campaign = S.me.campaign;
  S.conditions = S.me.conditions;
  S.skills = S.me.skills;
  connect();
  // Warm the dice tray in the background; rolls made before it is ready simply
  // use the server-rolled path and the built-in dice.
  DiceTray.init();
  return true;
}

/* ------------------------------------------------------------ toasts */

function toast(text, kind = '') {
  let wrap = $('.toast-wrap');
  if (!wrap) document.body.append((wrap = el('div', { class: 'toast-wrap' })));
  const node = el('div', { class: `toast ${kind}` }, text);
  wrap.append(node);
  setTimeout(() => {
    node.style.transition = 'opacity .4s';
    node.style.opacity = '0';
    setTimeout(() => node.remove(), 400);
  }, kind === 'announce' ? 7000 : 3200);
}

/* ------------------------------------------------------------ modal */

function modal(title, buildBody, cls = '') {
  const bg = el('div', { class: 'modal-bg', onclick: e => { if (e.target === bg) bg.remove(); } });
  const box = el('div', { class: `modal ${cls}`.trim() }, el('h2', {}, title));
  const close = () => bg.remove();
  box.append(buildBody(close));
  bg.append(box);
  document.body.append(bg);
  // A whole sheet is for reading and poking at, not filling in top to bottom —
  // focusing its first field would pop the keyboard and scroll it away.
  if (cls !== 'sheet') {
    const first = $('input, select, textarea', box);
    if (first) setTimeout(() => first.focus(), 60);
  }
  return close;
}

/* ------------------------------------------------------------ dice UI */

let advState = 0;

let rollMeta = null;

function doRoll(formula, label, opts = {}) {
  const payload = {
    formula,
    label: label || '',
    advantage: opts.advantage ?? advState,
    secret: !!opts.secret,
    actor: opts.actor,
    set_initiative: !!opts.set_initiative,
  };
  rollMeta = payload;
  // With the tray we throw the dice ourselves and report what they landed on;
  // without it the server rolls and we just draw the result.
  send(DiceTray.available ? 'roll.plan' : 'roll', payload);
  if (!opts.keepAdv) setAdv(0);
}

function setAdv(v) {
  advState = v;
  $$('[data-adv]').forEach(b => b.classList.toggle('on', Number(b.dataset.adv) === v));
}

/** Builds the shared dice pad. `isDm` adds the secret-roll toggle. */
function dicePad(isDm) {
  const custom = el('input', { placeholder: '2d6+3, 4d6kh3, 1d8!', value: '' });
  const label = el('input', { placeholder: 'label (optional)' });
  let secret = false;

  const advSeg = el('div', { class: 'seg' },
    el('button', { dataset: { adv: -1 }, onclick: () => setAdv(advState === -1 ? 0 : -1) }, 'Disadvantage'),
    el('button', { dataset: { adv: 0 }, class: 'on', onclick: () => setAdv(0) }, 'Normal'),
    el('button', { dataset: { adv: 1 }, onclick: () => setAdv(advState === 1 ? 0 : 1) }, 'Advantage'),
  );

  const fire = (f) => {
    if (!f) return;
    doRoll(f, label.value.trim(), { secret });
    label.value = '';
  };

  const pad = el('div', { class: 'dicegrid' },
    [4, 6, 8, 10, 12, 20, 100].map(n =>
      el('button', { class: 'btn', onclick: () => fire(`1d${n}`) }, `d${n}`)),
    el('button', { class: 'btn gold', onclick: () => fire(custom.value.trim()) }, 'Roll'),
  );

  const secretBtn = isDm ? el('button', {
    class: 'btn', onclick: (e) => {
      secret = !secret;
      e.target.classList.toggle('gold', secret);
      e.target.textContent = secret ? '🔒 Secret: on' : '🔒 Secret: off';
    },
  }, '🔒 Secret: off') : null;

  custom.addEventListener('keydown', e => { if (e.key === 'Enter') fire(custom.value.trim()); });

  return el('div', { class: 'card' },
    el('h3', {}, 'Dice'),
    advSeg,
    el('div', { style: 'height:8px' }),
    pad,
    el('div', { style: 'height:8px' }),
    custom, el('div', { style: 'height:6px' }), label,
    secretBtn ? el('div', { class: 'row', style: 'margin-top:8px' }, secretBtn) : null,
  );
}

/* ------------------------------------------------------------ rolling dice */

/** Die silhouettes, so a d20 doesn't look like a d6. */
const DIE_SHAPES = {
  4:   '50,10 93,86 7,86',
  6:   null,                                        // square, drawn as a rect
  8:   '50,6 92,50 50,94 8,50',
  10:  '50,5 91,36 50,95 9,36',
  12:  '50,5 93,36 77,88 23,88 7,36',
  20:  '50,4 92,27 92,73 50,96 8,73 8,27',
  100: '50,5 91,36 50,95 9,36',
};

function dieFace(sides) {
  const pts = DIE_SHAPES[sides];
  const inner = pts
    ? `<polygon class="face" points="${pts}" stroke-linejoin="round"/>`
    : sides === 6
      ? '<rect class="face" x="8" y="8" width="84" height="84" rx="14"/>'
      : '<circle class="face" cx="50" cy="50" r="44"/>';
  return `<svg viewBox="0 0 100 100" aria-hidden="true">${inner}</svg>`;
}

function dieNode(die, sides, { small = false } = {}) {
  const node = el('div', { class: 'die' + (small ? ' small' : '') });
  node.innerHTML = dieFace(sides);
  const label = el('span', { class: 'v' }, die.v);
  node.append(label);
  if (!die.kept) node.classList.add('dropped');
  else if (sides === 20 && die.v === 20) node.classList.add('crit');
  else if (sides === 20 && die.v === 1) node.classList.add('fumble');
  node._label = label;
  node._sides = sides;
  return node;
}

const ANIM_DICE_CAP = 24;   // past this we show the numbers but skip the tumble
const TUMBLE_MS = 620;

/** Builds the tray for one roll. Returns {tray, dice:[nodes]}. */
function buildTray(terms, { small = false } = {}) {
  const tray = el('div', { class: 'dice-tray' + (small ? ' small' : '') });
  const dice = [];
  terms.forEach((term, i) => {
    if (i > 0) tray.append(el('span', { class: 'roll-op' }, term.sign < 0 ? '−' : '+'));
    else if (term.sign < 0) tray.append(el('span', { class: 'roll-op' }, '−'));

    if (term.kind === 'flat') {
      tray.append(el('span', { class: 'roll-flat' }, term.value));
      return;
    }
    for (const d of term.dice) {
      const node = dieNode(d, term.sides, { small });
      tray.append(node);
      dice.push(node);
    }
  });
  return { tray, dice };
}

/** Tumbles the dice, then settles them on their real values. */
function animateDice(dice, onDone) {
  const animate = dice.length <= ANIM_DICE_CAP && !matchMedia('(prefers-reduced-motion: reduce)').matches;
  if (!animate) { onDone?.(); return () => {}; }

  const finals = dice.map(n => n._label.textContent);
  dice.forEach(n => n.classList.add('rolling'));
  const scramble = setInterval(() => {
    for (const n of dice) n._label.textContent = 1 + Math.floor(Math.random() * n._sides);
  }, 55);

  const timers = [setTimeout(() => {
    clearInterval(scramble);
    dice.forEach((n, i) => {
      timers.push(setTimeout(() => {
        n._label.textContent = finals[i];
        n.classList.remove('rolling');
        n.classList.add('landed');
        if (i === dice.length - 1) onDone?.();
      }, i * 55));
    });
    if (!dice.length) onDone?.();
  }, TUMBLE_MS)];

  return () => { clearInterval(scramble); timers.forEach(clearTimeout); };
}

let liveStage = null;

/** Flattens a roll's terms into the individual dice the tray has to throw. */
function diceOf(terms) {
  const out = [];
  for (const t of terms || []) {
    if (t.kind !== 'dice') continue;
    for (const d of t.dice) {
      out.push({
        sides: t.sides,
        v: d.v,
        kept: d.kept,
        crit: t.sides === 20 && d.kept ? (d.v === 20 ? 'crit' : d.v === 1 ? 'fumble' : null) : null,
      });
    }
  }
  return out;
}

/**
 * The full-screen overlay for the person rolling. `tray` means the real dice
 * tray is doing the throwing behind it, so the overlay contributes only the
 * caption and, once the server has done the arithmetic, the result.
 */
function openStage({ actor, label, formula, secret }, { tray = false } = {}) {
  liveStage?.close();

  const caption = el('div', { class: 'roll-caption' },
    el('div', { class: 'who' }, actor || ''),
    el('div', { class: 'what' }, [label, formula, secret ? 'secret' : ''].filter(Boolean).join(' · ')));
  const body = el('div');
  const result = el('div', { class: 'roll-caption roll-result' });
  const stage = el('div', { class: 'roll-stage' + (tray ? ' tray' : '') }, caption, body, result);
  // In tray mode the dice canvas sits between the backdrop and the text, so the
  // backdrop has to be its own element rather than the stage's own background.
  const backdrop = tray ? el('div', { class: 'roll-backdrop' }) : null;
  if (backdrop) document.body.append(backdrop);
  document.body.append(stage);

  let closed = false;
  let autoClose = null;
  const api = {
    stage, body,
    awaiting: true,
    onSkip: null,
    close() {
      if (closed) return;
      closed = true;
      clearTimeout(autoClose);
      api.onSkip = null;
      if (tray) DiceTray.hide();
      stage.classList.add('out');
      backdrop?.classList.add('out');
      setTimeout(() => { stage.remove(); backdrop?.remove(); }, 280);
      if (liveStage === api) liveStage = null;
    },
    showResult(r) {
      if (closed || result.childElementCount) return;
      api.awaiting = false;
      result.append(...[
        el('div', { class: 'roll-breakdown', html: detailHtml(r.detail) }),
        el('div', { class: `roll-total ${r.crit || ''}` }, r.total),
        r.crit ? el('div', { class: `roll-verdict ${r.crit}` },
          r.crit === 'crit' ? 'Critical' : 'Fumble') : null,
        el('div', { class: 'roll-dismiss' }, 'tap to dismiss'),
      ].filter(Boolean));
      autoClose = setTimeout(api.close, r.crit ? 3400 : 2600);
    },
  };

  stage.addEventListener('click', () => {
    if (api.awaiting) { api.onSkip?.(); return; }   // first tap skips the throw
    api.close();
  });
  liveStage = api;
  return api;
}

/** Server rolled it for us (no tray): draw the throw with the built-in dice. */
function showRollStage(r) {
  const dice = diceOf(r.terms);
  const api = openStage(r);
  const canvas = el('canvas', { class: 'dice-canvas' });
  api.body.append(canvas);

  const w = Math.min(window.innerWidth - 32, 480);
  const rows = Math.ceil(Math.max(dice.length, 1) / 5);
  canvas.style.width = `${w}px`;
  canvas.style.height = `${Math.max(260, Math.min(window.innerHeight * 0.46, 300 + (rows - 1) * 46))}px`;

  if (!dice.length) { api.showResult(r); return api.stage; }
  const anim = Dice3D.play(canvas, dice, () => api.showResult(r));
  api.onSkip = () => anim.finish();
  if (matchMedia('(prefers-reduced-motion: reduce)').matches) anim.finish();
  return api.stage;
}

/** The server has told us which dice to throw. Throw them, report the result. */
async function runTrayRoll(planMsg) {
  const meta = rollMeta || {};
  const api = openStage({
    actor: meta.actor || S.me?.display_name,
    label: meta.label, formula: meta.formula, secret: meta.secret,
  }, { tray: true });

  DiceTray.show();
  let values;
  try {
    values = await DiceTray.throwDice(planMsg.dice);
  } catch (err) {
    console.warn('tray throw failed', err);
    api.close();
    toast('The dice got stuck — rolling it the old way', 'error');
    send('roll', meta);                       // fall back to a server roll
    return;
  }
  if (liveStage !== api) return;              // dismissed mid-throw
  send('roll.commit', { id: planMsg.id, values });
}

/** Compact, non-blocking notice for everyone else's rolls. */
function showRollTicker(r) {
  document.querySelectorAll('.roll-ticker').forEach(n => n.remove());
  const { tray, dice } = buildTray(r.terms || [], { small: true });
  const total = el('span', { class: 'tot' }, '…');
  const node = el('div', { class: `roll-ticker ${r.crit || ''} ${r.secret ? 'secret' : ''}` },
    el('span', {},
      el('div', { class: 'who' }, r.actor),
      el('div', { class: 'what' }, [r.label, r.formula].filter(Boolean).join(' · '))),
    tray, el('span', { class: 'roll-eq' }, '='), total);
  document.body.append(node);

  animateDice(dice, () => { total.textContent = r.total; });
  setTimeout(() => {
    node.classList.add('out');
    setTimeout(() => node.remove(), 300);
  }, 3600);
}

/** One roll landed. Mine gets the stage, everyone else's gets the ticker. */
function presentRoll(r) {
  if (!r.terms || !r.terms.length) return;      // rolls logged before this feature
  const mine = r.by_device && r.by_device === S.me?.device_id;
  if (!mine) return showRollTicker(r);
  // If our own dice are already on the table, just fill in the total.
  if (liveStage?.awaiting) liveStage.showResult(r);
  else showRollStage(r);
}

/** Compact summary of the most recent roll, for the Dice tab. */
function lastRollCard() {
  const r = [...S.rolls].reverse().find(x => x.terms && x.terms.length);
  if (!r) return el('div', { class: 'card' }, el('h3', {}, 'Last roll'),
    el('div', { class: 'muted tiny center' }, 'Nothing rolled yet.'));
  const { tray } = buildTray(r.terms, { small: true });
  return el('div', { class: 'card' },
    el('h3', {}, 'Last roll', el('span', { class: 'spacer' }),
      el('span', { class: 'tiny muted' }, [r.actor, r.label].filter(Boolean).join(' · '))),
    el('div', { class: `last-roll ${r.crit || ''}` },
      tray, el('span', { class: 'spacer grow' }),
      el('span', { class: 'roll-eq' }, '='), el('span', { class: 'tot' }, r.total)),
    el('div', { class: 'tiny muted', style: 'margin-top:4px', html: detailHtml(r.detail) }),
  );
}

/* ------------------------------------------------------------ roll log */

function renderLog(container) {
  container.replaceChildren(...[...S.rolls].reverse().map(r => {
    const cls = ['log-entry', r.crit || '', r.secret ? 'secret' : ''].filter(Boolean).join(' ');
    return el('div', { class: cls },
      el('div', { class: 'head' },
        el('span', { class: 'actor' }, r.actor),
        el('span', { class: 'muted tiny' }, [r.label, r.formula].filter(Boolean).join(' · ')),
        el('span', { class: 'total' }, r.total),
      ),
      el('div', { class: 'detail', html: detailHtml(r.detail) }),
    );
  }));
  if (!S.rolls.length) container.append(el('div', { class: 'muted center tiny' }, 'No rolls yet.'));
}

/* ------------------------------------------------------------ journey */

const STATUS_LABEL = { visited: 'been there', current: 'you are here', rumored: 'heard of it', hidden: 'DM only' };

/**
 * Lays the journey out as a tree from each place's "reached from" link.
 * Children are packed left to right and every parent is centred over its own
 * children, so a campaign that never branched draws as a straight road and one
 * that did shows the forks.
 */
function layoutJourney(locs) {
  const byId = new Map(locs.map(l => [l.id, l]));
  const kids = new Map(locs.map(l => [l.id, []]));
  const roots = [];
  for (const loc of locs) {
    const parent = loc.from && byId.has(loc.from) && loc.from !== loc.id ? loc.from : null;
    if (parent) kids.get(parent).push(loc);
    else roots.push(loc);
  }

  const node = new Map();
  let slot = 0;
  const walk = (loc, depth, seen) => {
    if (seen.has(loc.id)) return null;         // defensive: never loop
    seen.add(loc.id);
    const children = kids.get(loc.id).map(k => walk(k, depth + 1, seen)).filter(Boolean);
    const col = children.length
      ? (children[0].col + children[children.length - 1].col) / 2
      : slot++;
    const n = { loc, depth, col };
    node.set(loc.id, n);
    return n;
  };
  const seen = new Set();
  for (const r of roots) walk(r, 0, seen);
  for (const loc of locs) if (!node.has(loc.id)) walk(loc, 0, seen);   // orphans

  const nodes = [...node.values()];
  const edges = nodes
    .filter(n => n.loc.from && node.has(n.loc.from))
    .map(n => ({ from: node.get(n.loc.from), to: n }));
  return {
    nodes,
    edges,
    cols: nodes.length ? Math.max(...nodes.map(n => n.col)) + 1 : 0,
    rows: nodes.length ? Math.max(...nodes.map(n => n.depth)) + 1 : 0,
  };
}

const NODE_FILL = {
  visited: '#c9a227', current: '#b4432e', rumored: '#2b2219', hidden: '#4a2f6b',
};
const NODE_STROKE = {
  visited: '#e0c35e', current: '#e0c35e', rumored: '#c9a227', hidden: '#b18ce0',
};

/** Draws the graph. `opts.onPick(loc)` makes nodes tappable. */
function renderJourney(host, journey, opts = {}) {
  const locs = journey?.locations || [];
  const key = JSON.stringify([locs, !!opts.onPick, !!opts.compact]);
  if (host.dataset.jkey === key) return;
  host.dataset.jkey = key;

  if (!locs.length) {
    host.replaceChildren(el('div', { class: 'muted tiny center', style: 'padding:18px 0' },
      opts.onPick ? 'No places yet — add the first one.' : 'The DM hasn\'t charted anything yet.'));
    return;
  }

  const { nodes, edges, cols, rows } = layoutJourney(locs);
  const single = cols <= 1;
  const ROW = single ? 66 : 96;
  const R = 15;

  // Squeeze the columns to fit the screen before resorting to sideways
  // scrolling — a branch hanging off the right edge is easy to miss.
  const avail = Math.min(760, Math.max(260, window.innerWidth - 56));
  const padX = single ? 22 : Math.min(66, avail * 0.17);
  let COL = single ? 44 : 132;
  if (!single && cols > 1) {
    COL = Math.max(88, Math.min(COL, (avail - padX * 2) / (cols - 1)));
  }
  const maxChars = single ? 26 : Math.max(9, Math.floor(COL / 7.2));
  const padTop = 26;
  // when labels sit under the nodes, the last row needs room for its label too
  const padBottom = single ? 26 : 54;
  const W = padX * 2 + Math.max(0, cols - 1) * COL + (single ? 190 : 0);
  const H = padTop + padBottom + Math.max(0, rows - 1) * ROW;
  const px = n => padX + n.col * COL;
  const py = n => padTop + n.depth * ROW;

  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', `0 0 ${W} ${H}`);
  svg.setAttribute('width', W);
  svg.setAttribute('height', H);
  svg.classList.add('journey-graph');

  const add = (tag, attrs, text) => {
    const n = document.createElementNS('http://www.w3.org/2000/svg', tag);
    for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, v);
    if (text !== undefined) n.textContent = text;
    svg.append(n);
    return n;
  };

  for (const e of edges) {
    const x1 = px(e.from), y1 = py(e.from), x2 = px(e.to), y2 = py(e.to);
    const mid = (y1 + y2) / 2;
    const reached = e.to.loc.status === 'visited' || e.to.loc.status === 'current';
    add('path', {
      d: `M${x1},${y1 + R} C${x1},${mid} ${x2},${mid} ${x2},${y2 - R}`,
      fill: 'none',
      stroke: reached ? '#c9a227' : '#5a4a20',
      'stroke-width': reached ? 2 : 1.5,
      'stroke-dasharray': reached ? '' : '4 4',
      'stroke-linecap': 'round',
    });
  }

  for (const n of nodes) {
    const { loc } = n;
    const x = px(n), y = py(n);
    const cur = loc.status === 'current';
    if (cur) add('circle', { cx: x, cy: y, r: R + 6, fill: '#b4432e33' });
    const c = add('circle', {
      cx: x, cy: y, r: cur ? R + 2 : R,
      fill: NODE_FILL[loc.status] || NODE_FILL.visited,
      stroke: NODE_STROKE[loc.status] || NODE_STROKE.visited,
      'stroke-width': 2,
      'stroke-dasharray': loc.status === 'rumored' ? '3 3' : loc.status === 'hidden' ? '2 3' : '',
      class: opts.onPick ? 'tap' : '',
    });
    if (opts.onPick) c.addEventListener('click', () => opts.onPick(loc));

    add('text', {
      x, y: y + 1, 'text-anchor': 'middle', 'dominant-baseline': 'middle',
      class: 'node-num', fill: loc.status === 'rumored' ? '#c9a227' : '#1a1206',
    }, cur ? '\u2605' : String(locs.indexOf(loc) + 1));

    const label = add('text', {
      x: single ? x + R + 12 : x,
      y: single ? y + 1 : y + R + 17,
      'text-anchor': single ? 'start' : 'middle',
      'dominant-baseline': single ? 'middle' : 'auto',
      class: `node-label ${loc.status}`,
    }, loc.name.length > maxChars ? loc.name.slice(0, maxChars - 1) + '\u2026' : loc.name);
    if (opts.onPick) {
      label.classList.add('tap');
      label.addEventListener('click', () => opts.onPick(loc));
    }
  }

  const wrap = el('div', { class: 'graph-wrap' });
  wrap.append(svg);
  host.replaceChildren(wrap);
}

/** The written trail underneath the graph, where the notes live. */
function renderTrail(host, journey, opts = {}) {
  const locs = journey?.locations || [];
  const trail = el('div', { class: 'trail' });
  for (const [i, loc] of locs.entries()) {
    trail.append(el('div', {
      class: `trail-item ${loc.status}`,
      onclick: opts.onPick ? () => opts.onPick(loc) : null,
      style: opts.onPick ? 'cursor:pointer' : null,
    },
      el('div', { class: 'row' },
        el('span', { class: 'nm grow' }, `${i + 1}. ${loc.name}`),
        el('span', { class: `badge ${loc.status}` }, STATUS_LABEL[loc.status] || loc.status)),
      loc.body ? el('div', { class: 'body' }, loc.body) : null,
    ));
  }
  if (!locs.length) trail.append(el('div', { class: 'muted tiny', style: 'padding:6px 0' }, 'Nothing yet.'));
  host.replaceChildren(trail);
}

/* ------------------------------------------------------------ tabs */

function setupTabs() {
  const go = (name) => {
    $$('.tab-panel').forEach(p => p.classList.toggle('active', p.dataset.tab === name));
    $$('nav.tabbar button').forEach(b => b.classList.toggle('active', b.dataset.tab === name));
    localStorage.setItem('dnd_tab', name);
    window.scrollTo(0, 0);
    emit('tab', name);
  };
  $$('nav.tabbar button').forEach(b => b.addEventListener('click', () => go(b.dataset.tab)));
  const saved = localStorage.getItem('dnd_tab');
  go($$('.tab-panel').some(p => p.dataset.tab === saved) ? saved : $('.tab-panel').dataset.tab);
  return go;
}

function connBadge() {
  const dot = $('.dot');
  if (dot) on('conn', ok => dot.classList.toggle('on', ok));
}

/* ------------------------------------------------------------ shared events */

on('toast', d => toast(d.text, d.kind));
/* A campaign switch swaps the whole database underneath us. Everything the
   page is showing belongs to the old one, and player tokens don't exist in the
   new file, so start over rather than half-update. */
on('campaign.switched', (info) => {
  if (S.me?.role === 'dm') {
    toast(`Now playing: ${info.name}`);
    setTimeout(() => location.reload(), 500);
  } else {
    toast(`The DM switched to ${info.name}`, 'announce');
    setTimeout(logout, 1200);
  }
});

on('roll', presentRoll);
on('roll.plan', runTrayRoll);
/** The full view of one handout: its picture, then its text. */
function showHandout(h) {
  return modal(h.title || 'Handout', () => el('div', { class: 'handout' },
    h.image ? el('img', { src: `/uploads/${h.image}`, alt: h.title || 'handout picture' }) : null,
    h.body ? el('div', { class: 'handout-body' }, h.body) : null,
  ), 'handout');
}

on('handout', h => {
  if (!h || (!h.title && !h.body && !h.image)) return;
  if (S.me?.role === 'dm') { toast(`Pushed "${h.title}" to the table`); return; }
  showHandout(h);
});
