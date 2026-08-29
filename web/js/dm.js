/* DM console: initiative control, party dashboard, campaign records, secret dice. */

/* ------------------------------------------------------------ combat tab */

function renderCombat() {
  const host = $('[data-tab=combat]');
  if (editing(host)) return;
  const { entries, round, turn_id, running } = S.initiative;

  const controls = el('div', { class: 'card' },
    el('h3', {}, running ? `Round ${round}` : 'Encounter',
      el('span', { class: 'spacer' }),
      el('span', { class: 'tiny muted' }, `${entries.length} combatants`)),
    el('div', { class: 'row wrap' },
      running
        ? el('button', { class: 'btn gold grow turn-btn', onclick: () => send('init.turn', { action: 'next' }) }, 'Next turn ›')
        : el('button', { class: 'btn gold grow turn-btn', onclick: () => send('init.turn', { action: 'start' }) }, '▶ Start combat'),
      el('button', { class: 'btn', onclick: () => send('init.turn', { action: 'prev' }) }, '‹ Back'),
      el('button', { class: 'btn', onclick: () => send('init.turn', { action: 'stop' }) }, '■ End'),
    ),
    el('div', { class: 'row wrap', style: 'margin-top:8px' },
      el('button', { class: 'btn sm', onclick: () => send('init.add_party', { roll: false }) }, '+ Party'),
      el('button', { class: 'btn sm', onclick: () => send('init.add_party', { roll: true }) }, '+ Party (auto-roll)'),
      el('button', { class: 'btn sm', onclick: addMonster }, '+ Monster'),
      el('button', { class: 'btn sm red', onclick: () => confirm('Clear the whole encounter?') && send('init.clear') }, 'Clear'),
    ),
  );

  const rows = entries.map(e => {
    const cls = ['initrow', e.character_id ? 'pc' : '', e.id === turn_id ? 'turn' : '',
      e.defeated ? 'defeated' : ''].filter(Boolean).join(' ');
    const initInput = el('input', {
      type: 'number', value: Math.round(e.init), class: 'init',
      style: 'width:44px;padding:4px;text-align:center',
      onchange: ev => send('init.update', { id: e.id, patch: { init: parseFloat(ev.target.value || '0') || 0 } }),
    });
    const hpText = e.hp == null ? '—' : `${e.hp}${e.hp_max ? `/${e.hp_max}` : ''}`;
    return el('div', { class: cls },
      initInput,
      el('span', { class: 'grow', onclick: () => editCombatant(e) },
        el('div', { class: 'nm' }, e.name, e.hidden ? ' 👁️‍🗨️' : ''),
        el('div', { class: 'tiny muted' },
          [e.ac != null ? `AC ${e.ac}` : null, `HP ${hpText}`, e.note,
            e.conditions?.length ? e.conditions.join(', ') : null].filter(Boolean).join(' · ')),
      ),
      el('button', { class: 'btn sm red', onclick: () => hitDialog(e, -1) }, '−'),
      el('button', { class: 'btn sm green', onclick: () => hitDialog(e, 1) }, '+'),
      el('button', { class: 'btn sm', onclick: () => send('init.update', { id: e.id, patch: { defeated: !e.defeated } }) },
        e.defeated ? '↺' : '💀'),
    );
  });

  host.replaceChildren(controls,
    ...(rows.length ? rows : [el('div', { class: 'card center muted tiny' }, 'Add the party or a monster to begin.')]));
}

function hitDialog(entry, mult) {
  modal(`${mult < 0 ? 'Damage' : 'Heal'} ${entry.name}`, (close) => {
    const amt = el('input', { type: 'number', inputmode: 'numeric', value: '', placeholder: 'amount' });
    const go = () => {
      const n = Math.abs(parseInt(amt.value || '0', 10) || 0);
      if (n) send('init.update', { id: entry.id, patch: { hp_delta: n * mult } });
      close();
    };
    amt.addEventListener('keydown', e => { if (e.key === 'Enter') go(); });
    const quick = el('div', { class: 'row wrap', style: 'margin-top:10px' },
      ...[1, 2, 5, 10, 20].map(n => el('button', {
        class: 'btn sm', onclick: () => { send('init.update', { id: entry.id, patch: { hp_delta: n * mult } }); close(); },
      }, n)));
    return el('div', {}, amt, quick,
      el('div', { class: 'row', style: 'margin-top:12px' },
        el('button', { class: 'btn grow', onclick: close }, 'Cancel'),
        el('button', { class: 'btn gold grow', onclick: go }, mult < 0 ? 'Apply damage' : 'Heal')));
  });
}

function addMonster() {
  modal('Add to initiative', (close) => {
    const f = {
      name: el('input', { placeholder: 'Goblin' }),
      count: el('input', { type: 'number', value: 1, min: 1, max: 20 }),
      init_mod: el('input', { type: 'number', value: 0 }),
      init: el('input', { type: 'number', placeholder: 'auto-roll' }),
      hp: el('input', { type: 'number', placeholder: '7' }),
      ac: el('input', { type: 'number', placeholder: '15' }),
      note: el('input', { placeholder: 'nimble escape' }),
    };
    const hidden = el('input', { type: 'checkbox' });
    const num = k => (f[k].value === '' ? null : parseInt(f[k].value, 10));
    const go = () => {
      if (!f.name.value.trim()) return toast('Name the monster', 'error');
      send('init.add', {
        name: f.name.value.trim(),
        count: parseInt(f.count.value || '1', 10) || 1,
        init: f.init.value === '' ? null : parseFloat(f.init.value),
        init_mod: parseInt(f.init_mod.value || '0', 10) || 0,
        hp: num('hp'), ac: num('ac'), note: f.note.value.trim(), hidden: hidden.checked,
      });
      close();
    };
    return el('div', {},
      el('label', { class: 'field' }, el('span', {}, 'Name'), f.name),
      el('div', { class: 'grid g2' },
        el('label', { class: 'field' }, el('span', {}, 'How many'), f.count),
        el('label', { class: 'field' }, el('span', {}, 'Init modifier'), f.init_mod),
        el('label', { class: 'field' }, el('span', {}, 'Fixed initiative'), f.init),
        el('label', { class: 'field' }, el('span', {}, 'AC'), f.ac),
        el('label', { class: 'field' }, el('span', {}, 'HP each'), f.hp),
        el('label', { class: 'field' }, el('span', {}, 'Note'), f.note),
      ),
      el('label', { class: 'row', style: 'gap:8px;margin:4px 0 0' }, hidden,
        el('span', { class: 'tiny' }, 'Hide from players (shows as "???")')),
      el('div', { class: 'row', style: 'margin-top:12px' },
        el('button', { class: 'btn grow', onclick: close }, 'Cancel'),
        el('button', { class: 'btn gold grow', onclick: go }, 'Add')));
  });
}

function editCombatant(e) {
  modal(e.name, (close) => {
    const name = el('input', { value: e.name, disabled: !!e.character_id });
    const hp = el('input', { type: 'number', value: e.hp ?? '' });
    const hpMax = el('input', { type: 'number', value: e.hp_max ?? '', disabled: !!e.character_id });
    const ac = el('input', { type: 'number', value: e.ac ?? '', disabled: !!e.character_id });
    const note = el('input', { value: e.note || '' });
    const hidden = el('input', { type: 'checkbox', checked: e.hidden });
    const chosen = new Set(e.conditions || []);
    const chips = el('div', { class: 'row wrap', style: 'gap:6px' }, ...S.conditions.map(c => {
      const b = el('button', { class: `chip ${chosen.has(c) ? 'on' : ''}` }, c);
      b.onclick = () => { chosen.has(c) ? chosen.delete(c) : chosen.add(c); b.classList.toggle('on'); };
      return b;
    }));
    const save = () => {
      const patch = { note: note.value.trim(), hidden: hidden.checked, conditions: [...chosen] };
      if (hp.value !== '') patch.hp = parseInt(hp.value, 10);
      if (!e.character_id) {
        patch.name = name.value.trim() || e.name;
        if (hpMax.value !== '') patch.hp_max = parseInt(hpMax.value, 10);
        if (ac.value !== '') patch.ac = parseInt(ac.value, 10);
      }
      send('init.update', { id: e.id, patch });
      close();
    };
    return el('div', {},
      el('label', { class: 'field' }, el('span', {}, 'Name'), name),
      el('div', { class: 'grid g3' },
        el('label', { class: 'field' }, el('span', {}, 'HP'), hp),
        el('label', { class: 'field' }, el('span', {}, 'Max HP'), hpMax),
        el('label', { class: 'field' }, el('span', {}, 'AC'), ac)),
      e.character_id ? el('div', { class: 'tiny muted', style: 'margin:-4px 0 8px' },
        'Name, max HP and AC live on the player\'s sheet.') : null,
      el('label', { class: 'field' }, el('span', {}, 'Note'), note),
      el('div', { class: 'tiny muted', style: 'margin-bottom:6px' }, 'Conditions'), chips,
      el('label', { class: 'row', style: 'gap:8px;margin-top:10px' }, hidden, el('span', { class: 'tiny' }, 'Hidden from players')),
      el('div', { class: 'row', style: 'margin-top:12px' },
        el('button', { class: 'btn red', onclick: () => { send('init.remove', { id: e.id }); close(); } }, 'Remove'),
        el('button', { class: 'btn grow', onclick: close }, 'Cancel'),
        el('button', { class: 'btn gold grow', onclick: save }, 'Save')));
  });
}

/* ------------------------------------------------------------ party tab */

function renderParty() {
  const host = $('[data-tab=party]');
  if (editing(host)) return;

  const cards = S.characters.map(c => {
    const s = c.sheet, d = c.derived;
    const pct = s.hp.max ? Math.max(0, Math.min(1, s.hp.current / s.hp.max)) : 0;
    const online = S.presence.some(x => x.character_id === c.id);
    const skillTop = [['perception', 'Perception'], ['insight', 'Insight'],
      ['investigation', 'Investigation'], ['stealth', 'Stealth']]
      .map(([k, label]) => `${label} ${sign(d.skills[k])}`).join(' · ');

    return el('div', { class: 'card' },
      el('h3', { class: 'tap', onclick: () => openSheet(c.id) },
        el('i', { class: 'dot' + (online ? ' on' : ''), style: 'margin-right:2px' }),
        s.name, el('span', { class: 'spacer' }),
        el('span', { class: 'tiny muted' }, `${s.klass || '—'} ${s.level}`),
        el('span', { class: 'muted', style: 'margin-left:6px' }, '›')),
      el('div', { class: 'row', style: 'margin-bottom:6px' },
        el('span', { class: 'grow' },
          el('div', { class: 'hpbar' }, el('i', { style: `width:${pct * 100}%`, class: pct > 0.5 ? '' : pct > 0.25 ? 'hurt' : 'bloodied' })),
          el('div', { class: 'tiny muted' }, `${s.hp.current}/${s.hp.max} HP${s.hp.temp ? ` +${s.hp.temp}` : ''}`)),
        el('button', { class: 'btn sm red', onclick: () => charHp(c, -1) }, '−'),
        el('button', { class: 'btn sm green', onclick: () => charHp(c, 1) }, '+'),
      ),
      el('div', { class: 'grid g3', style: 'margin-bottom:6px' },
        el('div', { class: 'statbox' }, el('div', { class: 'k' }, 'AC'), el('div', { class: 'v' }, s.ac)),
        el('div', { class: 'statbox' }, el('div', { class: 'k' }, 'Pass. perc'), el('div', { class: 'v' }, d.passive_perception)),
        el('div', { class: 'statbox' }, el('div', { class: 'k' }, 'Spell DC'), el('div', { class: 'v' }, d.spell_save_dc ?? '—')),
      ),
      el('div', { class: 'tiny muted' }, skillTop),
      el('div', { class: 'tiny muted' }, 'Saves: ' + S.me.abilities.map(a => `${a} ${sign(d.saves[a])}`).join(' ')),
      s.conditions?.length ? el('div', { class: 'row wrap', style: 'gap:5px;margin-top:7px' },
        ...s.conditions.map(x => el('span', { class: 'chip on' }, x))) : null,
      el('div', { class: 'row', style: 'margin-top:9px' },
        el('button', { class: 'btn sm gold', onclick: () => openSheet(c.id) }, 'Open sheet'),
        el('button', { class: 'btn sm', onclick: () => send('roll', { formula: '1d20', label: `${s.name} — DM roll`, secret: true, actor: s.name }) }, 'Secret d20'),
        el('span', { class: 'spacer grow' }),
        el('button', { class: 'btn sm', onclick: () => confirm(`Delete ${s.name}? This cannot be undone.`) && send('char.delete', { id: c.id }) }, 'Delete'),
      ),
    );
  });

  host.replaceChildren(
    el('div', { class: 'card' }, el('h3', {}, 'At the table'),
      el('div', { class: 'row wrap', style: 'gap:6px' },
        ...(S.presence.length ? S.presence.map(p => el('span', { class: 'chip' }, `${p.name}${p.role === 'dm' ? ' (DM)' : ''}`))
          : [el('span', { class: 'muted tiny' }, 'Nobody connected.')])),
      el('div', { class: 'row', style: 'margin-top:10px' },
        el('button', { class: 'btn sm', onclick: showJoinInfo }, 'Show join QR'),
        el('button', { class: 'btn sm gold', onclick: addCharacter }, '+ Add character'),
      )),
    cards.length
      ? el('div', { class: 'party-grid' }, ...cards)
      : el('div', { class: 'card center muted tiny' }, 'No characters yet.'),
  );
}

const CLASSES = ['Artificer', 'Barbarian', 'Bard', 'Cleric', 'Druid', 'Fighter', 'Monk',
  'Paladin', 'Ranger', 'Rogue', 'Sorcerer', 'Warlock', 'Wizard'];
const SWATCHES = ['#c9a227', '#b4432e', '#4b7fa8', '#4f8a52', '#8a5fb0', '#c07a3e', '#3fa89b', '#c85f8e'];

/** The DM builds the party; players claim a seat from the join screen. */
function addCharacter() {
  modal('Add a character to the party', (close) => {
    const name = el('input', { placeholder: 'Thorn Ironhide' });
    const player = el('input', { placeholder: 'who plays them' });
    const klass = el('input', { placeholder: 'Fighter', list: 'class-list' });
    const level = el('input', { type: 'number', value: 1, min: 1, max: 20 });
    const datalist = el('datalist', { id: 'class-list' }, ...CLASSES.map(c => el('option', { value: c })));
    let color = SWATCHES[S.characters.length % SWATCHES.length];
    const swatches = el('div', { class: 'row wrap', style: 'gap:7px' }, ...SWATCHES.map(hex => {
      const b = el('button', {
        class: 'chip', style: `background:${hex};width:30px;height:30px;padding:0;border-radius:50%`,
        onclick: () => { color = hex; $$('button', swatches).forEach(x => x.style.outline = ''); b.style.outline = '2px solid #fff'; },
      });
      if (hex === color) b.style.outline = '2px solid #fff';
      return b;
    }));
    const go = (again) => {
      if (!name.value.trim()) return toast('Name the character', 'error');
      send('char.create', {
        name: name.value.trim(), player: player.value.trim(), klass: klass.value.trim(),
        level: parseInt(level.value || '1', 10) || 1, color,
      });
      if (!again) return close();
      name.value = ''; player.value = ''; klass.value = '';
      color = SWATCHES[(S.characters.length + 1) % SWATCHES.length];
      name.focus();
    };
    return el('div', {}, datalist,
      el('label', { class: 'field' }, el('span', {}, 'Character name'), name),
      el('label', { class: 'field' }, el('span', {}, 'Player'), player),
      el('div', { class: 'grid g2' },
        el('label', { class: 'field' }, el('span', {}, 'Class'), klass),
        el('label', { class: 'field' }, el('span', {}, 'Level'), level)),
      el('div', { class: 'tiny muted', style: 'margin-bottom:6px' }, 'Colour'), swatches,
      el('div', { class: 'tiny muted', style: 'margin-top:10px' },
        'They fill in the rest of the sheet themselves once they join.'),
      el('div', { class: 'row', style: 'margin-top:12px' },
        el('button', { class: 'btn grow', onclick: close }, 'Cancel'),
        el('button', { class: 'btn grow', onclick: () => go(true) }, 'Save & add another'),
        el('button', { class: 'btn gold grow', onclick: () => go(false) }, 'Add')));
  });
}

/* The DM opens a player's sheet: the very same sheet they see, fully editable,
   kept in sync while it is open so a change on their phone shows up here. */
let openSheetCtx = null;
let openSheetRoot = null;

function openSheet(charId) {
  const ch = S.characters.find(x => x.id === charId);
  if (!ch) return;
  openSheetCtx = SheetUI.contextFor(charId);
  openSheetRoot = SheetUI.build(openSheetCtx);
  SheetUI.sync(openSheetRoot, openSheetCtx);

  const close = modal(ch.sheet.name, () => el('div', {},
    el('div', { class: 'tiny muted', style: 'margin:-6px 0 10px' },
      'Editing as the DM — changes appear on their phone straight away.'),
    openSheetRoot,
  ), 'sheet');

  // stop syncing once it is dismissed
  const bg = openSheetRoot.closest('.modal-bg');
  const observer = new MutationObserver(() => {
    if (!document.body.contains(bg)) {
      openSheetCtx = null;
      openSheetRoot = null;
      observer.disconnect();
    }
  });
  observer.observe(document.body, { childList: true });
  return close;
}

/** Keep an open sheet current as state arrives. */
function refreshOpenSheet() {
  if (openSheetCtx && openSheetRoot && openSheetCtx.ch) {
    SheetUI.sync(openSheetRoot, openSheetCtx);
  }
}

function charHp(c, mult) {
  modal(`${mult < 0 ? 'Damage' : 'Heal'} ${c.sheet.name}`, (close) => {
    const amt = el('input', { type: 'number', inputmode: 'numeric', placeholder: 'amount' });
    const go = () => {
      const n = Math.abs(parseInt(amt.value || '0', 10) || 0);
      if (n) send('char.hp', { id: c.id, delta: n * mult });
      close();
    };
    amt.addEventListener('keydown', e => { if (e.key === 'Enter') go(); });
    return el('div', {}, amt,
      el('div', { class: 'row', style: 'margin-top:12px' },
        el('button', { class: 'btn grow', onclick: close }, 'Cancel'),
        el('button', { class: 'btn gold grow', onclick: go }, 'Apply')));
  });
}

function showJoinInfo() {
  modal('Join the table', () => el('div', { class: 'center' },
    el('div', { style: 'background:#fff;padding:12px;border-radius:10px;display:inline-block' },
      el('img', { src: '/qr.svg', style: 'width:220px;height:220px;display:block' })),
    el('div', { style: 'margin-top:12px;font-size:17px' }, location.origin),
    el('div', { class: 'tiny muted' }, 'Same Wi-Fi, scan or type it in'),
  ));
}

/* ------------------------------------------------------------ campaign tab */

function listEditor(title, key, fields, opts = {}) {
  const items = S.party[key] || [];
  const card = el('div', { class: 'card' },
    el('h3', {}, title, el('span', { class: 'spacer' }),
      el('button', { class: 'btn sm', onclick: () => editListItem(key, fields, null, opts) }, '+ Add')));
  for (const [i, it] of items.entries()) {
    card.append(el('div', { class: 'list-item' },
      opts.toggle ? el('input', {
        type: 'checkbox', checked: it[opts.toggle] === 'done',
        onchange: ev => {
          const next = [...items];
          next[i] = { ...it, [opts.toggle]: ev.target.checked ? 'done' : 'active' };
          send('party.set', { key, value: next });
        },
      }) : null,
      el('span', {
        class: 'grow' + (opts.toggle && it[opts.toggle] === 'done' ? ' done' : ''),
        onclick: () => editListItem(key, fields, i, opts),
      },
        el('div', {}, it[fields[0][0]] || '—'),
        el('div', { class: 'tiny muted' }, fields.slice(1).map(f => it[f[0]]).filter(Boolean).join(' — '))),
    ));
  }
  if (!items.length) card.append(el('div', { class: 'muted tiny center' }, 'Nothing here yet.'));
  return card;
}

function editListItem(key, fields, index, opts) {
  const items = S.party[key] || [];
  const it = index === null ? {} : { ...items[index] };
  modal(index === null ? 'New entry' : 'Edit entry', (close) => {
    const inputs = {};
    const body = fields.map(([name, label, kind]) => {
      inputs[name] = kind === 'area'
        ? el('textarea', { value: it[name] || '' })
        : el('input', { value: it[name] || '' });
      return el('label', { class: 'field' }, el('span', {}, label), inputs[name]);
    });
    const save = () => {
      const next = [...items];
      const row = { ...it };
      for (const [name] of fields) row[name] = inputs[name].value.trim();
      if (opts.toggle && !row[opts.toggle]) row[opts.toggle] = 'active';
      if (!row[fields[0][0]]) return toast('Fill in the first field', 'error');
      if (index === null) next.push(row); else next[index] = row;
      send('party.set', { key, value: next });
      close();
    };
    return el('div', {}, ...body,
      el('div', { class: 'row', style: 'margin-top:12px' },
        index !== null ? el('button', { class: 'btn red', onclick: () => { send('party.set', { key, value: items.filter((_, i) => i !== index) }); close(); } }, 'Delete') : null,
        el('button', { class: 'btn grow', onclick: close }, 'Cancel'),
        el('button', { class: 'btn gold grow', onclick: save }, 'Save')));
  });
}

/** Loot always belongs to one character or to the shared pile — never to a
 *  free-typed name — so ownership is a picker, not a text field. */
function ownerOptions(selected) {
  const sel = el('select', {},
    el('option', { value: '' }, '\u2014 Shared pile \u2014'),
    ...S.characters.map(c => el('option', { value: String(c.id) }, c.sheet.name)));
  sel.value = selected == null ? '' : String(selected);
  return sel;
}

const ownerName = (owner) =>
  owner == null ? 'Shared pile'
    : (S.characters.find(c => c.id === owner)?.sheet.name || 'someone who left');

function lootEditor() {
  const items = S.party.loot || [];
  const card = el('div', { class: 'card' },
    el('h3', {}, 'Loot', el('span', { class: 'spacer' }),
      el('span', { class: 'tiny muted' }, `${items.length} item${items.length === 1 ? '' : 's'}`),
      el('button', { class: 'btn sm gold', onclick: () => editLoot(null) }, '+ Add')));

  // grouped: the shared pile first, then each character who is carrying something
  const groups = [{ owner: null, items: items.filter(i => i.owner == null) }];
  for (const c of S.characters) {
    groups.push({ owner: c.id, items: items.filter(i => i.owner === c.id) });
  }
  const orphans = items.filter(i => i.owner != null && !S.characters.some(c => c.id === i.owner));
  if (orphans.length) groups.push({ owner: 'gone', items: orphans });

  for (const g of groups) {
    if (!g.items.length && g.owner !== null) continue;
    card.append(el('div', { class: 'tiny muted', style: 'margin:10px 0 2px;letter-spacing:.06em' },
      g.owner === 'gone' ? 'NO LONGER IN THE PARTY' : ownerName(g.owner).toUpperCase()));
    if (!g.items.length) {
      card.append(el('div', { class: 'muted tiny', style: 'padding:4px 0' }, 'Empty.'));
      continue;
    }
    for (const it of g.items) {
      card.append(el('div', { class: 'list-item', onclick: () => editLoot(it) },
        el('span', { class: 'muted', style: 'width:34px' }, `\u00d7${it.qty}`),
        el('span', { class: 'grow' }, it.name,
          it.notes ? el('div', { class: 'tiny muted' }, it.notes) : null),
        el('span', { class: 'muted tiny' }, '\u203a'),
      ));
    }
  }
  return card;
}

function editLoot(item) {
  const isNew = !item;
  modal(isNew ? 'Add loot' : item.name, (close) => {
    const name = el('input', { value: item?.name || '', placeholder: 'Flame Tongue' });
    const qty = el('input', { type: 'number', inputmode: 'numeric', min: 1, value: item?.qty ?? 1 });
    const notes = el('input', { value: item?.notes || '', placeholder: 'unidentified' });
    const owner = ownerOptions(item ? item.owner : null);
    const save = () => {
      if (!name.value.trim()) return toast('Name the item', 'error');
      const fields = {
        name: name.value.trim(),
        qty: parseInt(qty.value || '1', 10) || 1,
        notes: notes.value.trim(),
        owner: owner.value === '' ? null : Number(owner.value),
      };
      if (isNew) send('loot.add', fields);
      else send('loot.update', { id: item.id, patch: fields });
      close();
    };
    return el('div', {},
      el('label', { class: 'field' }, el('span', {}, 'Item'), name),
      el('div', { class: 'grid g2' },
        el('label', { class: 'field' }, el('span', {}, 'Quantity'), qty),
        el('label', { class: 'field' }, el('span', {}, 'Notes'), notes)),
      el('label', { class: 'field' }, el('span', {}, 'Who has it'), owner),
      el('div', { class: 'tiny muted', style: 'margin:-4px 0 8px' },
        'Loot sits with one character or in the shared pile.'),
      el('div', { class: 'row', style: 'margin-top:8px' },
        !isNew ? el('button', {
          class: 'btn red',
          onclick: () => { if (confirm(`Delete ${item.name}?`)) { send('loot.remove', { id: item.id }); close(); } },
        }, 'Delete') : null,
        el('button', { class: 'btn grow', onclick: close }, 'Cancel'),
        el('button', { class: 'btn gold grow', onclick: save }, 'Save')));
  });
}

/* Handouts are written ahead of time and kept. Saving one does not show it to
   anyone; pushing does, and it then stays in the players' list to re-read. */
/** Straight to the table: type it and push, no saving step. It still lands in
 *  the library afterwards so it can be taken back or shown again. */
function quickHandout() {
  const title = el('input', { placeholder: 'Title' });
  const body = el('textarea', { placeholder: 'Something you improvised…',
    style: 'min-height:62px' });
  let image = null;

  const file = el('input', { type: 'file', accept: 'image/*', class: 'hidden' });
  const picBtn = el('button', { class: 'btn sm', onclick: () => file.click() }, '+ Picture');
  file.addEventListener('change', async () => {
    const f = file.files[0];
    if (!f) return;
    const fd = new FormData();
    fd.append('file', f);
    try {
      const res = await fetch(`/api/upload?token=${encodeURIComponent(S.token)}`,
        { method: 'POST', body: fd });
      const out = await res.json();
      if (!res.ok) throw new Error(out.detail || 'upload failed');
      image = out.image;
      picBtn.classList.add('gold');
      picBtn.textContent = '✓ Picture attached';
    } catch (err) { toast(err.message, 'error'); }
    file.value = '';
  });

  const push = () => {
    if (!title.value.trim() && !body.value.trim() && !image) {
      return toast('Nothing to push yet', 'error');
    }
    send('handout.save', {
      title: title.value.trim() || 'Handout', body: body.value, image, push: true,
    });
    title.value = '';
    body.value = '';
    image = null;
    picBtn.classList.remove('gold');
    picBtn.textContent = '+ Picture';
  };
  body.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) push();
  });

  return el('div', { class: 'quick-handout' }, file,
    el('div', { class: 'tiny muted', style: 'margin-bottom:6px' }, 'Push one on the fly'),
    title,
    el('div', { style: 'height:6px' }),
    body,
    el('div', { class: 'row', style: 'margin-top:8px' },
      picBtn,
      el('span', { class: 'spacer grow' }),
      el('button', { class: 'btn gold', onclick: push }, '📣 Push now')));
}

function handoutLibrary() {
  const items = [...(S.party.handouts || [])].reverse();
  const card = el('div', { class: 'card' },
    el('h3', {}, 'Handouts', el('span', { class: 'spacer' }),
      el('span', { class: 'tiny muted' }, `${items.length} saved`),
      el('button', { class: 'btn sm gold', onclick: () => editHandout(null) }, '+ New')),
    quickHandout());

  for (const h of items) {
    card.append(el('div', { class: 'list-item' },
      h.image ? el('img', { src: `/uploads/${h.image}`, class: 'handout-thumb', alt: '' })
        : el('span', { class: 'handout-thumb muted center', style: 'font-size:18px' }, '📜'),
      el('span', { class: 'grow', onclick: () => editHandout(h) },
        el('div', {}, h.title),
        el('div', { class: 'tiny muted' },
          (h.body || '').slice(0, 60) + ((h.body || '').length > 60 ? '…' : ''))),
      h.revealed
        ? el('button', { class: 'btn sm', onclick: () => send('handout.hide', { id: h.id }) }, 'Take back')
        : null,
      el('button', {
        class: `btn sm ${h.revealed ? '' : 'gold'}`,
        onclick: () => send('handout.push', { id: h.id }),
      }, h.revealed ? 'Show again' : '📣 Push'),
    ));
  }
  if (!items.length) {
    card.append(el('div', { class: 'muted tiny center', style: 'padding:10px 0' },
      'Write one in advance — it stays here until you push it.'));
  }
  return card;
}

function editHandout(h) {
  const isNew = !h;
  modal(isNew ? 'New handout' : h.title, (close) => {
    const title = el('input', { value: h?.title || '', placeholder: 'The innkeeper\'s letter' });
    const body = el('textarea', { value: h?.body || '', style: 'min-height:150px',
      placeholder: 'Text your players see on their phones…' });
    let image = h?.image || null;

    const preview = el('div', { class: 'handout-preview' });
    const drawPreview = () => preview.replaceChildren(
      image
        ? el('div', {},
            el('img', { src: `/uploads/${image}`, alt: 'handout picture' }),
            el('button', {
              class: 'btn sm red', style: 'margin-top:6px',
              onclick: () => { image = null; drawPreview(); },
            }, 'Remove picture'))
        : el('div', { class: 'muted tiny' }, 'No picture'));
    drawPreview();

    const file = el('input', { type: 'file', accept: 'image/*', class: 'hidden' });
    file.addEventListener('change', async () => {
      const f = file.files[0];
      if (!f) return;
      const fd = new FormData();
      fd.append('file', f);
      try {
        const res = await fetch(`/api/upload?token=${encodeURIComponent(S.token)}`,
          { method: 'POST', body: fd });
        const out = await res.json();
        if (!res.ok) throw new Error(out.detail || 'upload failed');
        image = out.image;
        drawPreview();
      } catch (err) { toast(err.message, 'error'); }
      file.value = '';
    });

    const save = (andPush) => {
      if (!title.value.trim() && !body.value.trim()) return toast('Give it a title or some text', 'error');
      send('handout.save', { id: h?.id, title: title.value.trim() || 'Untitled',
                             body: body.value, image, push: andPush });
      close();
    };

    return el('div', {}, file,
      el('label', { class: 'field' }, el('span', {}, 'Title'), title),
      el('label', { class: 'field' }, el('span', {}, 'Text'), body),
      el('div', { class: 'tiny muted', style: 'margin-bottom:5px' }, 'Picture'),
      preview,
      el('button', { class: 'btn sm', style: 'margin-top:8px', onclick: () => file.click() },
        image ? 'Replace picture' : '+ Add picture'),
      el('div', { class: 'row', style: 'margin-top:14px' },
        !isNew ? el('button', {
          class: 'btn red',
          onclick: () => { if (confirm(`Delete "${h.title}"?`)) { send('handout.remove', { id: h.id }); close(); } },
        }, 'Delete') : null,
        el('button', { class: 'btn grow', onclick: close }, 'Cancel'),
        el('button', { class: 'btn grow', onclick: () => save(false) }, 'Save'),
        el('button', { class: 'btn gold grow', onclick: () => save(true) }, 'Save & push'),
      ));
  });
}

function renderCampaign() {
  const host = $('[data-tab=campaign]');
  if (editing(host)) return;
  const p = S.party;
  if (!p.gold) return;

  const coins = el('div', { class: 'grid g6' }, ...['pp', 'gp', 'ep', 'sp', 'cp'].map(c =>
    el('label', { class: 'field', style: 'margin:0' }, el('span', {}, c),
      el('input', {
        type: 'number', value: p.gold[c] ?? 0,
        onchange: e => send('party.set', { key: 'gold', value: { ...p.gold, [c]: parseInt(e.target.value || '0', 10) || 0 } }),
      }))));

  host.replaceChildren(
    handoutLibrary(),
    lootEditor(),
    listEditor('Quests', 'quests', [['title', 'Title'], ['body', 'Details', 'area']], { toggle: 'status' }),
    listEditor('NPCs', 'npcs', [['name', 'Name'], ['role', 'Role'], ['notes', 'Notes', 'area']]),
    el('div', { class: 'card' }, el('h3', {}, 'Party treasury'), coins),
    el('div', { class: 'card' }, el('h3', {}, 'Shared session notes'),
      el('textarea', {
        value: p.notes?.text || '', style: 'min-height:140px',
        onchange: e => send('party.set', { key: 'notes', value: { text: e.target.value } }),
      })),
    el('div', { class: 'card' }, el('h3', {}, 'Campaign'),
      el('label', { class: 'field' }, el('span', {}, 'Name'),
        el('input', {
          value: S.campaign.name,
          onchange: e => send('campaign.rename', { name: e.target.value }),
        })),
      el('div', { class: 'row' },
        el('button', { class: 'btn', onclick: showJoinInfo }, 'Join QR'),
        el('button', { class: 'btn', onclick: logout }, 'Sign out'))),
  );
}

function renderJourneyTab() {
  const host = $('[data-tab=journey]');
  if (editing(host)) return;
  const journey = S.party.journey || { locations: [] };
  const key = JSON.stringify(journey);
  if (host.dataset.jkey === key) return;
  host.dataset.jkey = key;

  const controls = el('div', { class: 'card' },
    el('h3', {}, 'The journey', el('span', { class: 'spacer' }),
      el('span', { class: 'tiny muted' }, `${journey.locations.length} places`),
      el('button', { class: 'btn sm gold', onclick: () => editLocation(null) }, '+ Add place')),
    el('div', { class: 'tiny muted' },
      'Each place records the one it was reached from, so side-trips branch off the road. '
      + 'Tap a node to edit it; “Party is here” moves the star and tells the table.'));

  const graph = el('div', { class: 'card' });
  const graphBody = el('div');
  graph.append(graphBody);
  renderJourney(graphBody, journey, { onPick: (loc) => editLocation(loc) });

  const list = el('div', { class: 'card' }, el('h3', {}, 'In order'));
  const listBody = el('div');
  list.append(listBody);
  renderTrail(listBody, journey, { onPick: (loc) => editLocation(loc) });

  host.replaceChildren(controls, graph, list);
}

function editLocation(loc) {
  const isNew = !loc;
  const locs = (S.party.journey || { locations: [] }).locations;
  modal(isNew ? 'New place' : loc.name, (close) => {
    const name = el('input', { value: loc?.name || '', placeholder: 'Phandalin' });
    const body = el('textarea', { value: loc?.body || '', placeholder: 'What happened here…' });

    // where this place was reached from — anything except itself or its own
    // descendants, which the server also refuses
    const descendants = new Set();
    if (loc) {
      const mark = (id) => {
        descendants.add(id);
        locs.filter(l => l.from === id).forEach(k => mark(k.id));
      };
      mark(loc.id);
    }
    const from = el('select', {},
      el('option', { value: '' }, '— Start of the journey —'),
      ...locs.filter(l => !descendants.has(l.id))
        .map(l => el('option', { value: l.id }, l.name)));
    from.value = isNew ? (locs.length ? locs[locs.length - 1].id : '') : (loc.from || '');

    const statuses = ['visited', 'current', 'rumored', 'hidden'];
    let status = loc?.status || 'visited';
    const seg = el('div', { class: 'seg' }, ...statuses.map(st => {
      const b = el('button', { class: st === status ? 'on' : '' }, STATUS_LABEL[st]);
      b.onclick = () => { status = st; $$('button', seg).forEach(o => o.classList.remove('on')); b.classList.add('on'); };
      return b;
    }));

    const save = () => {
      if (!name.value.trim()) return toast('Name the place', 'error');
      const fields = { name: name.value.trim(), body: body.value.trim(), status,
                       from: from.value || null };
      if (isNew) send('journey.add', fields);
      else send('journey.update', { id: loc.id, patch: fields });
      close();
    };
    return el('div', {},
      el('label', { class: 'field' }, el('span', {}, 'Place'), name),
      el('label', { class: 'field' }, el('span', {}, 'Reached from'), from),
      el('label', { class: 'field' }, el('span', {}, 'Notes'), body),
      el('div', { class: 'tiny muted', style: 'margin-bottom:5px' }, 'Status'), seg,
      !isNew ? el('div', { class: 'row', style: 'margin-top:12px' },
        el('button', { class: 'btn sm grow', onclick: () => { send('journey.move', { id: loc.id, by: -1 }); close(); } }, '↑ Earlier'),
        el('button', { class: 'btn sm grow', onclick: () => { send('journey.move', { id: loc.id, by: 1 }); close(); } }, '↓ Later'),
        el('button', { class: 'btn sm gold grow', onclick: () => { send('journey.here', { id: loc.id }); close(); } }, '★ Party is here'),
      ) : null,
      el('div', { class: 'row', style: 'margin-top:12px' },
        !isNew ? el('button', {
          class: 'btn red',
          onclick: () => { if (confirm(`Delete ${loc.name}?`)) { send('journey.remove', { id: loc.id }); close(); } },
        }, 'Delete') : null,
        el('button', { class: 'btn grow', onclick: close }, 'Cancel'),
        el('button', { class: 'btn gold grow', onclick: save }, 'Save')));
  });
}

let dmDicePad = null;

/** Pick what is being played. Each campaign is a world of its own — its own
 *  characters, loot, journey and handouts. */
function campaignsMenu() {
  modal('Campaigns', (close) => {
    const list = el('div');
    const draw = () => {
      list.replaceChildren(...(S.campaigns || []).map(c => el('div', {
        class: 'list-item' + (c.active ? ' campaign-active' : ''),
      },
        el('span', { class: 'grow' },
          el('div', {}, c.name, c.active ? el('span', { class: 'badge current', style: 'margin-left:8px' }, 'playing') : null),
          el('div', { class: 'tiny muted' }, `last played ${c.last_played.slice(0, 10)}`)),
        el('button', {
          class: 'btn sm', onclick: () => {
            const name = prompt('Rename campaign:', c.name);
            if (name?.trim()) send('campaign.rename', { id: c.id, name: name.trim() });
          },
        }, 'Rename'),
        c.active ? null : el('button', {
          class: 'btn sm red',
          onclick: () => { if (confirm(`Delete "${c.name}" and everything in it?`)) send('campaign.delete', { id: c.id }); },
        }, 'Delete'),
        c.active ? null : el('button', {
          class: 'btn sm gold',
          onclick: () => {
            if (confirm(`Switch to "${c.name}"? Everyone will be sent back to the join screen to pick a character.`)) {
              send('campaign.switch', { id: c.id });
              close();
            }
          },
        }, 'Play this'),
      )));
      if (!(S.campaigns || []).length) {
        list.append(el('div', { class: 'muted tiny center' }, 'No campaigns yet.'));
      }
    };
    draw();
    on('campaigns', draw);

    return el('div', {}, list,
      el('div', { class: 'tiny muted', style: 'margin:12px 0' },
        'Switching sends every player back to the join screen to pick a character '
        + 'in the new campaign. Nothing is lost — each campaign keeps its own everything.'),
      el('div', { class: 'row' },
        el('button', { class: 'btn grow', onclick: close }, 'Close'),
        el('button', {
          class: 'btn gold grow',
          onclick: () => {
            const name = prompt('Name the new campaign:');
            if (name?.trim()) send('campaign.create', { name: name.trim() });
          },
        }, '+ New campaign')));
  });
}

function renderDice() {
  const host = $('[data-tab=dice]');
  if (!dmDicePad) {
    dmDicePad = dicePad(true);
    host.replaceChildren(el('div', { id: 'last-roll' }), dmDicePad,
      el('div', { class: 'card' }, el('h3', {}, 'Hidden rolls'),
        el('div', { class: 'tiny muted' },
          'Secret rolls appear only in your log, marked with a blue edge, and no one else\'s phone shows them landing. Use them for perception checks the party shouldn\'t know they failed.')));
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
  if (S.me.role !== 'dm') { location.href = '/play'; return; }

  setupTabs();
  connBadge();
  $('.title').textContent = S.campaign.name;
  $('#campaigns').onclick = campaignsMenu;

  const rerender = () => {
    refreshOpenSheet();
    renderCombat();
    renderParty();
    renderJourneyTab();
    renderCampaign();
    renderDice();
    renderLog($('#log'));
    $('.title').textContent = S.campaign.name;
  };
  on('any', rerender);
  on('conn', ok => { if (ok) rerender(); });
  // A panel skips re-rendering while a field is focused; catch up when it is left.
  document.addEventListener('focusout', () => setTimeout(rerender, 0));
})();
