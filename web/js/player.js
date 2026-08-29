/* Player view: their own character sheet, dice, combat order, loot, journey. */

const myChar = () => S.characters.find(c => c.id === S.me.character_id);
const rollAs = (formula, label) => doRoll(formula, label, { actor: myChar()?.sheet.name });

// The sheet itself lives in sheet.js so the DM can open the very same one.
let sheetCtx = null;
let sheetRoot = null;
let boundId = null;

/** Draw or refresh this player's own sheet in the Sheet tab. */
function renderSheet() {
  const host = $('[data-tab=sheet]');
  const ch = S.characters.find(c => c.id === S.me.character_id);
  if (!ch) {
    boundId = null;
    host.replaceChildren(el('div', { class: 'card center muted' },
      'Your character is gone. ', el('a', { href: '#', onclick: logout }, 'Rejoin')));
    return;
  }
  if (boundId !== ch.id) {
    sheetCtx = SheetUI.contextFor(ch.id);
    sheetCtx.footer = () => el('div', { class: 'center', style: 'margin:18px 0' },
      el('button', { class: 'btn', onclick: logout }, 'Leave table'));
    sheetRoot = SheetUI.build(sheetCtx);
    host.replaceChildren(sheetRoot);
    boundId = ch.id;
  }
  SheetUI.sync(sheetRoot, sheetCtx);
}

function renderCombat() {
  const host = $('[data-tab=combat]');
  const { entries, round, turn_id, running } = S.initiative;
  const ch = myChar();

  const head = el('div', { class: 'card' },
    el('h3', {}, running ? `Round ${round}` : 'Not in combat',
      el('span', { class: 'spacer' }),
      el('button', {
        class: 'btn sm gold',
        onclick: () => send('roll', {
          formula: `1d20${sign(ch ? ch.derived.initiative : 0)}`,
          label: 'Initiative',
          actor: ch?.sheet.name,
          set_initiative: true,
        }),
      }, 'Roll initiative')),
    el('div', { class: 'tiny muted' }, 'Your roll drops you straight into the DM\'s order.'),
  );

  const rows = entries.map(e => {
    const mine = e.character_id === S.me.character_id;
    const cls = ['initrow', e.character_id ? 'pc' : '', e.id === turn_id ? 'turn' : '',
      e.defeated ? 'defeated' : ''].filter(Boolean).join(' ');
    let health = null;
    if (e.hp != null && e.hp_max) {
      const pct = Math.max(0, Math.min(1, e.hp / e.hp_max));
      health = el('div', { style: 'width:70px' },
        el('div', { class: 'hpbar' }, el('i', { style: `width:${pct * 100}%`,
          class: pct > 0.5 ? '' : pct > 0.25 ? 'hurt' : 'bloodied' })),
        el('div', { class: 'tiny muted center' }, `${e.hp}/${e.hp_max}`));
    } else if (e.wounds) {
      health = el('span', { class: 'tiny muted' }, e.wounds);
    }
    return el('div', { class: cls },
      el('span', { class: 'init' }, Math.floor(e.init) || '–'),
      el('span', { class: 'grow' },
        el('div', { class: 'nm' }, e.name, mine ? ' (you)' : ''),
        e.conditions?.length ? el('div', { class: 'tiny muted' }, e.conditions.join(', ')) : null),
      health,
      e.ac != null ? el('span', { class: 'tiny muted' }, `AC ${e.ac}`) : null,
    );
  });

  host.replaceChildren(head,
    ...(rows.length ? rows : [el('div', { class: 'card center muted tiny' }, 'The DM hasn\'t started an encounter.')]));
}

function renderParty() {
  const host = $('[data-tab=party]');
  if (editing(host)) return;
  const p = S.party;
  if (!p.gold) return;

  const coins = el('div', { class: 'grid g6' }, ...['pp', 'gp', 'ep', 'sp', 'cp'].map(c =>
    el('label', { class: 'field', style: 'margin:0' }, el('span', {}, c),
      el('input', {
        type: 'number', inputmode: 'numeric', value: p.gold[c] ?? 0,
        onchange: e => send('party.set', { key: 'gold', value: { ...p.gold, [c]: parseInt(e.target.value || '0', 10) || 0 } }),
      }))));

  const quests = el('div', {}, ...(p.quests || []).map(q => el('div', { class: 'list-item' },
    el('span', { class: 'grow' + (q.status === 'done' ? ' done' : '') }, q.title,
      q.body ? el('div', { class: 'tiny muted' }, q.body) : null))));
  if (!(p.quests || []).length) quests.append(el('div', { class: 'muted tiny center' }, 'No quests logged.'));

  const npcs = el('div', {}, ...(p.npcs || []).map(n => el('div', { class: 'list-item' },
    el('span', { class: 'grow' }, n.name,
      el('div', { class: 'tiny muted' }, [n.role, n.notes].filter(Boolean).join(' — '))))));
  if (!(p.npcs || []).length) npcs.append(el('div', { class: 'muted tiny center' }, 'No one met yet.'));

  const notes = el('textarea', {
    value: p.notes?.text || '', placeholder: 'Shared session notes — everyone can write here.',
    style: 'min-height:150px',
    onchange: e => send('party.set', { key: 'notes', value: { text: e.target.value } }),
  });

  const party = S.characters.map(c => {
    const pct = c.sheet.hp.max ? Math.max(0, Math.min(1, c.sheet.hp.current / c.sheet.hp.max)) : 0;
    const online = S.presence.some(x => x.character_id === c.id);
    return el('div', { class: 'list-item' },
      el('i', { class: 'dot' + (online ? ' on' : ''), style: 'align-self:center' }),
      el('span', { class: 'grow' }, c.sheet.name,
        el('div', { class: 'tiny muted' }, `${c.sheet.klass || '—'} ${c.sheet.level} · AC ${c.sheet.ac} · PP ${c.derived.passive_perception}`)),
      el('span', { style: 'width:64px' },
        el('div', { class: 'hpbar' }, el('i', { style: `width:${pct * 100}%`, class: pct > 0.5 ? '' : pct > 0.25 ? 'hurt' : 'bloodied' })),
        el('div', { class: 'tiny muted center' }, `${c.sheet.hp.current}/${c.sheet.hp.max}`)),
    );
  });

  const handouts = [...(p.handouts || [])].reverse();
  const handoutCard = handouts.length
    ? el('div', { class: 'card' }, el('h3', {}, 'Handouts'),
        ...handouts.map(h => el('div', { class: 'list-item', onclick: () => showHandout(h) },
          h.image ? el('img', { src: `/uploads/${h.image}`, class: 'handout-thumb', alt: '' })
            : el('span', { class: 'handout-thumb muted center', style: 'font-size:18px' }, '📜'),
          el('span', { class: 'grow' }, h.title,
            el('div', { class: 'tiny muted' },
              (h.body || '').slice(0, 50) + ((h.body || '').length > 50 ? '…' : ''))),
          el('span', { class: 'muted tiny' }, '›'))))
    : null;

  host.replaceChildren(
    el('div', { class: 'card' }, el('h3', {}, 'The party'), ...party),
    handoutCard,
    el('div', { class: 'card' }, el('h3', {}, 'Treasury'), coins),
    el('div', { class: 'card' }, el('h3', {}, 'Quests'), quests),
    el('div', { class: 'card' }, el('h3', {}, 'People we\'ve met'), npcs),
    el('div', { class: 'card' }, el('h3', {}, 'Session notes'), notes),
  );
}

let lootView = 'mine';

function renderLoot() {
  const host = $('[data-tab=loot]');
  if (editing(host)) return;
  const items = S.party.loot || [];
  const meId = S.me.character_id;
  const mine = items.filter(i => i.owner === meId);
  const shared = items.filter(i => i.owner == null);
  const key = JSON.stringify([items, lootView, meId]);
  if (host.dataset.key === key) return;
  host.dataset.key = key;

  const seg = el('div', { class: 'seg' },
    el('button', {
      class: lootView === 'mine' ? 'on' : '',
      onclick: () => { lootView = 'mine'; renderLoot(); },
    }, `On me (${mine.length})`),
    el('button', {
      class: lootView === 'shared' ? 'on' : '',
      onclick: () => { lootView = 'shared'; renderLoot(); },
    }, `Shared pile (${shared.length})`));

  const list = el('div');
  const shown = lootView === 'mine' ? mine : shared;
  for (const it of shown) {
    list.append(el('div', { class: 'list-item' },
      el('span', { class: 'muted', style: 'width:34px' }, `\u00d7${it.qty}`),
      el('span', { class: 'grow', onclick: () => SheetUI.editItem(sheetCtx, it) }, it.name,
        it.notes ? el('div', { class: 'tiny muted' }, it.notes) : null),
      lootView === 'mine'
        ? el('button', { class: 'btn sm', onclick: () => send('loot.move', { id: it.id, owner: null }) }, 'Put in pile')
        : el('button', { class: 'btn sm gold', onclick: () => send('loot.move', { id: it.id, owner: meId }) }, 'Take'),
    ));
  }
  if (!shown.length) {
    list.append(el('div', { class: 'muted tiny center', style: 'padding:14px 0' },
      lootView === 'mine' ? 'You are not carrying any loot.' : 'The shared pile is empty.'));
  }

  // who has everything else, so nobody has to ask
  const others = S.characters
    .filter(c => c.id !== meId)
    .map(c => ({ c, n: items.filter(i => i.owner === c.id).length }))
    .filter(x => x.n);

  host.replaceChildren(
    el('div', { class: 'card' },
      el('h3', {}, 'Loot', el('span', { class: 'spacer' }),
        el('button', {
          class: 'btn sm',
          onclick: () => SheetUI.editItem(sheetCtx, null, lootView === 'shared' ? null : S.me.character_id),
        }, '+ Add')),
      seg, el('div', { style: 'height:10px' }), list),
    others.length
      ? el('div', { class: 'card' }, el('h3', {}, 'Carried by the others'),
          ...others.map(({ c, n }) => el('div', { class: 'list-item' },
            el('span', { class: 'grow' }, c.sheet.name),
            el('span', { class: 'muted tiny' }, `${n} item${n === 1 ? '' : 's'}`))))
      : null,
  );
}

function renderProgress() {
  const host = $('[data-tab=progress]');
  const journey = S.party.journey || { locations: [] };
  const key = JSON.stringify(journey);
  if (host.dataset.jkey === key) return;
  host.dataset.jkey = key;
  const here = journey.locations.find(l => l.status === 'current');
  const reached = journey.locations.filter(l => l.status === 'visited' || l.status === 'current');

  const head = el('div', { class: 'card' },
    el('h3', {}, 'Where we are'),
    here
      ? el('div', {}, el('div', { style: 'font-size:18px;font-family:Georgia,serif' }, here.name),
          here.body ? el('div', { class: 'tiny muted', style: 'white-space:pre-wrap;margin-top:4px' }, here.body) : null)
      : el('div', { class: 'muted tiny' }, 'The DM hasn\'t marked where the party is yet.'),
  );

  const graphCard = el('div', { class: 'card' },
    el('h3', {}, 'The road so far', el('span', { class: 'spacer' }),
      el('span', { class: 'tiny muted' }, `${reached.length} visited`)));
  const graphBody = el('div');
  graphCard.append(graphBody);
  renderJourney(graphBody, journey, {});

  const listCard = el('div', { class: 'card' }, el('h3', {}, 'In order'));
  const listBody = el('div');
  listCard.append(listBody);
  renderTrail(listBody, journey, {});

  host.replaceChildren(head, graphCard, listCard);
}

let dicePadNode = null;

function renderDice() {
  const host = $('[data-tab=dice]');
  // The pad holds typed input, so build it once and only refresh what changes.
  if (!dicePadNode) {
    dicePadNode = dicePad(false);
    const quick = el('div', { class: 'card' }, el('h3', {}, 'Quick rolls'),
      el('div', { class: 'row wrap' },
        ...[['Initiative', () => `1d20${sign(myChar().derived.initiative)}`],
          ['Death save', () => '1d20'],
          ['Hit die', () => `1${myChar().sheet.hit_dice.die || 'd8'}`],
        ].map(([label, f]) => el('button', { class: 'btn', onclick: () => rollAs(f(), label) }, label))));
    host.replaceChildren(el('div', { id: 'last-roll' }), dicePadNode, quick);
  }
  const slot = $('#last-roll', host);
  const key = String(S.rolls.at(-1)?.id ?? '');
  if (slot.dataset.key !== key) {
    slot.dataset.key = key;
    slot.replaceChildren(lastRollCard());
  }
}

/* ------------------------------------------------------------ boot */

(async () => {
  if (!(await boot())) return;
  if (S.me.role === 'dm') { location.href = '/dm'; return; }

  setupTabs();
  connBadge();
  $('.title').textContent = S.campaign.name;

  const rerender = () => {
    renderSheet();
    renderCombat();
    renderParty();
    renderLoot();
    renderProgress();
    renderDice();
    renderLog($('#log'));
    $('.who').textContent = myChar()?.sheet.name || S.me.display_name;
  };
  on('any', rerender);
  on('conn', ok => { if (ok) rerender(); });
  // A panel skips re-rendering while a field is focused; catch up when it is left.
  document.addEventListener('focusout', () => setTimeout(rerender, 0));
})();
