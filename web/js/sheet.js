/* The 5e character sheet, built once and kept in sync from the campaign state.
 *
 * Both the player's own sheet and the DM opening someone else's use this same
 * code — a second implementation would drift from the first. Everything the
 * sheet needs to know about *whose* sheet it is comes through a context object,
 * so the only difference between the two callers is which id they pass and
 * whether edits are allowed.
 */

const SheetUI = (() => {

const ABILITY_NAMES = { str: 'Strength', dex: 'Dexterity', con: 'Constitution',
  int: 'Intelligence', wis: 'Wisdom', cha: 'Charisma' };

const PASSIVE_SKILLS = [['perception', 'Perception'], ['investigation', 'Investigation'],
  ['insight', 'Insight']];

const getPath = (obj, path) => path.split('.').reduce((o, k) => (o == null ? o : o[k]), obj);

/** An input wired to a sheet path; `sync` fills it, `change` pushes it. */
function bound(ctx, path, type = 'str', attrs = {}) {
  const isBool = type === 'bool';
  const node = el('input', {
    type: isBool ? 'checkbox' : (type === 'int' ? 'number' : 'text'),
    inputmode: type === 'int' ? 'numeric' : undefined,
    ...attrs,
  });
  node.dataset.bind = path;
  node.dataset.type = type;
  node.addEventListener('change', () => {
    let v = node.value;
    if (isBool) v = node.checked;
    else if (type === 'int') v = parseInt(v || '0', 10) || 0;
    ctx.patch(path, v);
  });
  return node;
}

/* A textarea sized to its contents rather than to a guess. Features and notes
 * run to hundreds of lines on a real character, and reading that through a
 * 90-pixel window — a scrollbar inside a scrolling page, on a phone — is the
 * worst of both. Growing the field instead means the page scrolls, once. */
function autoGrow(node) {
  // A hidden tab has no layout, so measuring it would collapse the field to
  // nothing. Leave it for whenever it is shown.
  if (!node.isConnected || node.scrollHeight === 0) return;
  node.style.height = 'auto';
  const borders = node.offsetHeight - node.clientHeight;
  node.style.height = `${node.scrollHeight + borders}px`;
}

/* A field built while its tab was hidden could not be measured, so re-measure
 * whenever a tab is shown. Waiting a frame lets the new layout settle first. */
on('tab', () => requestAnimationFrame(
  () => $$('textarea[data-grow]').forEach(autoGrow)));

function boundArea(ctx, path, attrs = {}) {
  const { grow = true, ...rest } = attrs;
  const node = el('textarea', rest);
  node.dataset.bind = path;
  if (grow) {
    node.dataset.grow = '1';
    node.addEventListener('input', () => autoGrow(node));
  }
  node.addEventListener('change', () => ctx.patch(path, node.value));
  return node;
}

function derived(ctx, path, fmt) {
  const node = el('span', {}, '—');
  node.dataset.derive = path;
  if (fmt) node.dataset.fmt = fmt;
  return node;
}

/* ------------------------------------------------------------ sheet build */

function buildIdentity(ctx) {
  return el('div', { class: 'card' },
    el('h3', {}, 'Character'),
    el('label', { class: 'field' }, el('span', {}, 'Name'), bound(ctx, 'name')),
    el('div', { class: 'grid g2' },
      el('label', { class: 'field' }, el('span', {}, 'Class'), bound(ctx, 'klass')),
      el('label', { class: 'field' }, el('span', {}, 'Level'), bound(ctx, 'level', 'int', { min: 1, max: 20 })),
      el('label', { class: 'field' }, el('span', {}, 'Subclass'), bound(ctx, 'subclass')),
      el('label', { class: 'field' }, el('span', {}, 'Race'), bound(ctx, 'race')),
      el('label', { class: 'field' }, el('span', {}, 'Background'), bound(ctx, 'background')),
      el('label', { class: 'field' }, el('span', {}, 'Player'), bound(ctx, 'player')),
      el('label', { class: 'field' }, el('span', {}, 'Alignment'), bound(ctx, 'alignment')),
    ),
  );
}

function buildVitals(ctx) {
  const amount = el('input', { type: 'number', inputmode: 'numeric', value: 1, min: 0,
    style: 'text-align:center', class: 'grow' });
  const nudge = (mult) => {
    const n = Math.abs(parseInt(amount.value || '0', 10) || 0);
    if (n) send('char.hp', { id: ctx.id, delta: n * mult });
  };

  const deathRow = (kind, color) => {
    const wrap = el('div', { class: 'row' }, el('span', { class: 'tiny muted', style: 'width:62px' },
      kind === 'successes' ? 'Successes' : 'Failures'));
    wrap.dataset.death = kind;
    for (let i = 1; i <= 3; i++) {
      wrap.append(el('button', {
        class: 'btn icon', style: `border-radius:50%;padding:0;width:24px;height:24px;border-color:${color}`,
        onclick: () => {
          const cur = ctx.ch.sheet.death_saves[kind];
          ctx.patch(`death_saves.${kind}`, cur === i ? i - 1 : i);
        },
      }));
    }
    return wrap;
  };

  const deathBox = el('div', { class: 'card hidden', style: 'margin:10px 0 0;background:#2a1613' },
    el('h3', { style: 'color:#e0705c' }, 'Death saves'),
    deathRow('successes', '#4f8a52'),
    el('div', { style: 'height:6px' }),
    deathRow('failures', '#b4432e'),
  );
  deathBox.id = 'death-box';

  return el('div', { class: 'card' },
    el('h3', {}, 'Vitals',
      el('span', { class: 'spacer' }),
      el('label', { class: 'row tiny', style: 'gap:5px;width:auto' },
        bound(ctx, 'inspiration', 'bool'), 'Inspiration'),
    ),
    el('div', { class: 'row', style: 'justify-content:space-between;margin-bottom:4px' },
      el('span', { class: 'tiny muted' }, 'Hit points'),
      el('span', { id: 'hp-text', class: 'tiny' }, '—'),
    ),
    el('div', { class: 'hpbar' }, el('i', { id: 'hp-fill' })),
    el('div', { class: 'row', style: 'margin-top:10px' },
      el('button', { class: 'btn red', onclick: () => nudge(-1) }, '− Damage'),
      amount,
      el('button', { class: 'btn green', onclick: () => nudge(1) }, '+ Heal'),
    ),
    el('div', { class: 'grid g3', style: 'margin-top:10px' },
      el('label', { class: 'field' }, el('span', {}, 'Current'), bound(ctx, 'hp.current', 'int')),
      el('label', { class: 'field' }, el('span', {}, 'Max'), bound(ctx, 'hp.max', 'int')),
      el('label', { class: 'field' }, el('span', {}, 'Temp'), bound(ctx, 'hp.temp', 'int')),
    ),
    deathBox,
    el('div', { class: 'grid g3', style: 'margin-top:10px' },
      el('div', { class: 'statbox' }, el('div', { class: 'k' }, 'Armor class'), el('div', { class: 'v' }, bound(ctx, 'ac', 'int', { style: 'text-align:center;border:0;background:none;font-family:Georgia,serif;font-size:22px' }))),
      el('div', { class: 'statbox', onclick: () => ctx.roll(`1d20${sign(ctx.ch.derived.initiative)}`, 'Initiative') },
        el('div', { class: 'k' }, 'Initiative'), el('div', { class: 'v' }, derived(ctx, 'initiative', 'sign'))),
      el('div', { class: 'statbox' }, el('div', { class: 'k' }, 'Speed'), el('div', { class: 'v' }, bound(ctx, 'speed', 'int', { style: 'text-align:center;border:0;background:none;font-family:Georgia,serif;font-size:22px' }))),
      el('div', { class: 'statbox' }, el('div', { class: 'k' }, 'Prof. bonus'), el('div', { class: 'v' }, derived(ctx, 'prof_bonus', 'sign'))),
      el('div', { class: 'statbox' }, el('div', { class: 'k' }, 'Hit dice'),
        el('div', { class: 'v', id: 'hd-text' }, '—')),
      el('div', { class: 'statbox' }, el('div', { class: 'k' }, 'Pass. perc.'), el('div', { class: 'v' }, derived(ctx, 'passive_perception'))),
      el('div', { class: 'statbox' }, el('div', { class: 'k' }, 'Pass. invest.'), el('div', { class: 'v' }, derived(ctx, 'passive_investigation'))),
      el('div', { class: 'statbox' }, el('div', { class: 'k' }, 'Pass. insight'), el('div', { class: 'v' }, derived(ctx, 'passive_insight'))),
    ),
    buildAdjustments(ctx),
    el('div', { class: 'row', style: 'margin-top:10px;gap:6px' },
      el('label', { class: 'field grow', style: 'margin:0' }, el('span', {}, 'Hit die'), bound(ctx, 'hit_dice.die')),
      el('label', { class: 'field grow', style: 'margin:0' }, el('span', {}, 'Total'), bound(ctx, 'hit_dice.total', 'int')),
      el('label', { class: 'field grow', style: 'margin:0' }, el('span', {}, 'Used'), bound(ctx, 'hit_dice.used', 'int')),
    ),
    el('div', { class: 'row', style: 'margin-top:10px' },
      el('button', { class: 'btn sm grow', onclick: () => restDialog(ctx, 'short') }, '🔥 Short rest'),
      el('button', { class: 'btn sm gold grow', onclick: () => restDialog(ctx, 'long') }, '🌙 Long rest'),
    ),
    el('div', { style: 'margin-top:12px' },
      el('div', { class: 'row', style: 'margin-bottom:6px' },
        el('span', { class: 'tiny muted grow' }, 'Conditions'),
        el('button', { class: 'btn sm', onclick: editConditions }, 'Edit')),
      el('div', { class: 'row wrap', id: 'cond-chips', style: 'gap:6px' }),
    ),
  );
}

/* Initiative is Dex for almost everyone and a flat passive bonus is unusual, so
 * both live behind a disclosure rather than cluttering the vitals. They matter
 * when they matter: an Intelligence-based investigator, or the Observant feat,
 * whose +5 to passive Perception no rolled check ever shows. */
function buildAdjustments(ctx) {
  const abilitySel = el('select', {},
    ...Object.entries(ABILITY_NAMES).map(([k, n]) => el('option', { value: k }, n)));
  abilitySel.dataset.bind = 'initiative_ability';
  abilitySel.addEventListener('change', () => ctx.patch('initiative_ability', abilitySel.value));

  return el('details', { class: 'adjust' },
    el('summary', {}, 'Initiative & passive adjustments'),
    el('div', { class: 'grid g2', style: 'margin-top:8px' },
      el('label', { class: 'field' }, el('span', {}, 'Initiative from'), abilitySel),
      el('label', { class: 'field' }, el('span', {}, 'Initiative bonus'),
        bound(ctx, 'initiative_bonus', 'int')),
    ),
    el('div', { class: 'tiny muted', style: 'margin:4px 0 5px' },
      'Added to the passive score only — not to the check you roll.'),
    el('div', { class: 'grid g3' },
      ...PASSIVE_SKILLS.map(([key, label]) =>
        el('label', { class: 'field', style: 'margin:0' }, el('span', {}, label),
          bound(ctx, `passive_bonus.${key}`, 'int'))),
    ),
  );
}

/* A rest is the one button that changes most of the sheet at once, so it says
 * what it is about to do before it does it. The server is what actually applies
 * the rules — this text only has to match them. */
const REST_TEXT = {
  short: {
    title: 'Take a short rest',
    verb: 'Short rest',
    does: [
      'Gives back anything that recharges on a short rest.',
      'Returns the partial uses of anything that trickles back — a case die, say.',
    ],
    doesnt: 'Hit dice are yours to spend, so this does not spend them for you, ' +
      'and it does not heal, clear temp HP or restore spell slots.',
  },
  long: {
    title: 'Take a long rest',
    verb: 'Long rest',
    does: [
      'Hit points back to full, and temporary hit points gone.',
      'Death saves cleared.',
      'Every spell slot back.',
      'Half your hit dice back, rounded down — always at least one.',
      'Everything that recharges on a short or a long rest.',
    ],
    doesnt: 'Anything you marked as coming back some other way is left alone, ' +
      'and exhaustion is free text here, so lower it yourself.',
  },
};

function restDialog(ctx, kind) {
  const t = REST_TEXT[kind];
  modal(t.title, (close) => el('div', {},
    el('ul', { class: 'restlist' }, ...t.does.map(line => el('li', {}, line))),
    el('div', { class: 'tiny muted', style: 'margin-top:8px' }, t.doesnt),
    el('div', { class: 'row', style: 'margin-top:12px' },
      el('button', { class: 'btn grow', onclick: close }, 'Cancel'),
      el('button', { class: `btn grow ${kind === 'long' ? 'gold' : ''}`.trim(),
        onclick: () => { send('char.rest', { id: ctx.id, kind }); close(); } }, t.verb),
    )));
}

function buildAbilities(ctx) {
  const grid = el('div', { class: 'grid g3' });
  for (const [key, name] of Object.entries(ABILITY_NAMES)) {
    grid.append(el('div', { class: 'ability' },
      el('div', { class: 'name' }, key),
      el('div', { class: 'mod', onclick: () => ctx.roll(`1d20${sign(ctx.ch.derived.mods[key])}`, name) },
        derived(ctx, `mods.${key}`, 'sign')),
      bound(ctx, `abilities.${key}`, 'int', { min: 1, max: 30 }),
    ));
  }
  return el('div', { class: 'card' }, el('h3', {}, 'Abilities'), grid,
    el('div', { class: 'tiny muted center', style: 'margin-top:8px' }, 'Tap a modifier to roll an ability check'));
}

/** Shared row shape for saving throws and skills. */
function profRow(ctx, label, abbr, profPath, bonusPath, rollLabel, maxRank, skill) {
  const pip = el('button', { class: 'pip', style: 'padding:0;cursor:pointer' });
  pip.addEventListener('click', (e) => {
    e.stopPropagation();
    const cur = Number(getPath(ctx.ch.sheet, profPath) || 0);
    const next = maxRank === 1 ? !cur : (cur + 1) % 3;
    ctx.patch(profPath, next);
  });
  pip.dataset.prof = profPath;

  const bonus = derived(ctx, bonusPath, 'sign');
  bonus.className = 'bonus';

  // The ability tag doubles as the control for changing it: a skill can be
  // rolled with a different ability, and this is where you would look for that.
  const tag = abbr ? el('span', { class: skill ? 'abbr tap' : 'abbr' }, abbr) : null;
  if (tag && skill) {
    tag.dataset.skillAbbr = skill;
    tag.title = 'Roll this with a different ability';
    tag.addEventListener('click', (e) => { e.stopPropagation(); editSkillAbility(ctx, skill, label); });
  }

  return el('div', { class: 'rollrow', onclick: () => ctx.roll(`1d20${sign(getPath(ctx.ch.derived, bonusPath))}`, rollLabel) },
    pip, bonus,
    el('span', { class: 'lbl' }, label),
    tag,
  );
}

/* The Dungeon Master's Guide lets a skill be rolled with an ability other than
 * its usual one — Nature with Wisdom for someone who learned it by living in it
 * rather than reading about it. The server derives the bonus either way. */
function editSkillAbility(ctx, skill, label) {
  const standard = S.skills[skill];
  const current = (ctx.ch.sheet.skill_ability || {})[skill] || standard;
  modal(label, (close) => {
    const pick = (ability) => {
      const next = { ...(ctx.ch.sheet.skill_ability || {}) };
      if (ability === standard) delete next[skill]; else next[skill] = ability;
      ctx.patch('skill_ability', next);
      close();
    };
    return el('div', {},
      el('div', { class: 'tiny muted', style: 'margin-bottom:10px' },
        `Normally ${ABILITY_NAMES[standard]}. Pick another if this character is `
        + 'allowed to roll it with something else.'),
      el('div', { class: 'grid g3' },
        ...Object.entries(ABILITY_NAMES).map(([k, n]) => el('button', {
          class: `btn ${k === current ? 'gold' : ''}`.trim(), onclick: () => pick(k),
        }, n, k === standard ? el('div', { class: 'tiny muted' }, 'usual') : null))),
      el('div', { class: 'row', style: 'margin-top:12px' },
        el('button', { class: 'btn grow', onclick: close }, 'Cancel')));
  });
}

function buildSaves(ctx) {
  const card = el('div', { class: 'card' }, el('h3', {}, 'Saving throws'));
  for (const [key, name] of Object.entries(ABILITY_NAMES)) {
    card.append(profRow(ctx, name, null, `save_prof.${key}`, `saves.${key}`, `${name} save`, 1));
  }
  return card;
}

function buildSkills(ctx) {
  const card = el('div', { class: 'card' }, el('h3', {}, 'Skills',
    el('span', { class: 'spacer' }), el('span', { class: 'tiny muted' }, 'tap pip: prof → expertise')));
  for (const [skill, abil] of Object.entries(S.skills)) {
    card.append(profRow(ctx, titleCase(skill), abil, `skill_prof.${skill}`,
      `skills.${skill}`, titleCase(skill), 2, skill));
  }
  return card;
}

function buildAttacks(ctx) {
  const list = el('div', { id: 'attack-list' });
  const add = () => editAttack(ctx, null);
  return el('div', { class: 'card' },
    el('h3', {}, 'Attacks', el('span', { class: 'spacer' }),
      el('button', { class: 'btn sm', onclick: add }, '+ Add')),
    list);
}

function editAttack(ctx, index) {
  const sheet = ctx.ch.sheet;
  const a = index === null ? { name: '', bonus: '+0', damage: '1d6', notes: '' } : { ...sheet.attacks[index] };
  modal(index === null ? 'New attack' : 'Edit attack', (close) => {
    const f = {};
    const field = (key, label, ph) => {
      f[key] = el('input', { value: a[key] || '', placeholder: ph });
      return el('label', { class: 'field' }, el('span', {}, label), f[key]);
    };
    const save = () => {
      const attacks = [...(sheet.attacks || [])];
      const next = { name: f.name.value.trim(), bonus: f.bonus.value.trim(),
        damage: f.damage.value.trim(), notes: f.notes.value.trim() };
      if (!next.name) return toast('Name it first', 'error');
      if (index === null) attacks.push(next); else attacks[index] = next;
      ctx.patch('attacks', attacks);
      close();
    };
    const remove = () => {
      ctx.patch('attacks', sheet.attacks.filter((_, i) => i !== index));
      close();
    };
    return el('div', {},
      field('name', 'Name', 'Longsword'),
      el('div', { class: 'grid g2' }, field('bonus', 'To hit', '+7'), field('damage', 'Damage', '1d8+4')),
      field('notes', 'Notes', 'versatile, reach…'),
      el('div', { class: 'row', style: 'margin-top:12px' },
        index !== null ? el('button', { class: 'btn red', onclick: remove }, 'Delete') : null,
        el('button', { class: 'btn grow', onclick: close }, 'Cancel'),
        el('button', { class: 'btn gold grow', onclick: save }, 'Save'),
      ));
  });
}

/* ------------------------------------------------------- class resources */

/* Ki, rage, bardic inspiration, superiority dice, a detective's case dice: a
 * pool with a size, a spend, and a rest that gives it back. Spending goes
 * through char.resource rather than a sheet patch, so two people tapping the
 * same pip at once can't overwrite each other's arithmetic — the same reason
 * damage is a delta. */

const RECHARGE = { long: 'Long rest', short: 'Short rest', other: '' };

function buildResources(ctx) {
  return el('div', { class: 'card' },
    el('h3', {}, 'Class resources', el('span', { class: 'spacer' }),
      el('button', { class: 'btn sm', onclick: () => editResource(ctx, null) }, '+ Add')),
    el('div', { id: 'res-list' }),
  );
}

function syncResources(ctx, root) {
  const list = $('#res-list', root);
  if (!list || editing(list)) return;
  const resources = ctx.ch.sheet.resources || [];

  list.replaceChildren(...resources.map((r, i) => {
    const max = Math.max(0, Number(r.max) || 0);
    const used = Math.min(Math.max(0, Number(r.used) || 0), max);
    const left = max - used;
    const set = (n) => send('char.resource', { id: ctx.id, res: r.id, set: n });

    const die = (r.die || '').trim();
    return el('div', { class: 'resource' },
      el('div', { class: 'row' },
        el('span', { class: 'grow', onclick: () => editResource(ctx, i) },
          el('span', {}, r.name || 'Resource'),
          el('span', { class: 'tiny muted' }, `  ${left}/${max}`,
            RECHARGE[r.recharge] ? ` · ${RECHARGE[r.recharge]}` : '',
            Number(r.short_regain) > 0 ? ` · +${r.short_regain} short` : '')),
        die ? el('button', {
          class: `btn sm gold ${left ? '' : 'off'}`.trim(),
          title: left ? `Roll ${die} and spend one` : 'None left',
          onclick: () => { if (!left) return toast('None left', 'error');
            set(used + 1); ctx.roll(die, r.name || 'Resource'); },
        }, die) : null,
        el('button', { class: 'btn sm', title: 'Restore all', onclick: () => set(0) }, '↺'),
      ),
      max ? el('div', { class: 'slotpips', style: 'margin-top:6px' },
        ...Array.from({ length: max }, (_, j) => el('i', {
          class: `s ${j < used ? 'used' : ''}`,
          onclick: () => set(j < used ? j : j + 1),
        }))) : null,
    );
  }));
  if (!resources.length) {
    list.append(el('div', { class: 'muted tiny center' },
      'Nothing tracked. Ki, rage, bardic inspiration, superiority dice…'));
  }
}

function editResource(ctx, index) {
  const sheet = ctx.ch.sheet;
  const r = index === null
    ? { id: '', name: '', die: '', max: 1, used: 0, recharge: 'long' }
    : { ...sheet.resources[index] };

  modal(index === null ? 'New resource' : (r.name || 'Resource'), (close) => {
    const name = el('input', { value: r.name || '', placeholder: 'Case Dice' });
    const max = el('input', { type: 'number', inputmode: 'numeric', min: 0, max: 40,
      value: Math.max(0, Number(r.max) || 0) });
    const die = el('input', { value: r.die || '', placeholder: 'd8 — optional' });
    const recharge = el('select', {}, ...Object.entries(RECHARGE).map(([k, label]) =>
      el('option', { value: k, selected: (r.recharge || 'long') === k }, label || 'Something else')));
    const shortRegain = el('input', { type: 'number', inputmode: 'numeric', min: 0, max: 40,
      value: Math.max(0, Number(r.short_regain) || 0) });

    const save = () => {
      if (!name.value.trim()) return toast('Name it first', 'error');
      const list = [...(sheet.resources || [])];
      const next = {
        // Stable across edits: it is what char.resource addresses a pool by.
        id: r.id || `r${Date.now().toString(36)}${Math.floor(Math.random() * 1e4)}`,
        name: name.value.trim().slice(0, 40),
        die: die.value.trim().slice(0, 16),
        max: Math.min(Math.max(0, parseInt(max.value || '0', 10) || 0), 40),
        used: Math.min(Math.max(0, Number(r.used) || 0),
          Math.min(Math.max(0, parseInt(max.value || '0', 10) || 0), 40)),
        recharge: recharge.value,
        short_regain: Math.min(Math.max(0, parseInt(shortRegain.value || '0', 10) || 0), 40),
      };
      if (index === null) list.push(next); else list[index] = next;
      ctx.patch('resources', list);
      close();
    };

    return el('div', {},
      el('label', { class: 'field' }, el('span', {}, 'Name'), name),
      el('div', { class: 'grid g2' },
        el('label', { class: 'field' }, el('span', {}, 'How many'), max),
        el('label', { class: 'field' }, el('span', {}, 'Die to roll'), die)),
      el('div', { class: 'grid g2' },
        el('label', { class: 'field' }, el('span', {}, 'Comes back on'), recharge),
        el('label', { class: 'field' }, el('span', {}, 'Short rest gives back'), shortRegain)),
      el('div', { class: 'tiny muted' },
        'Give it a die and the button rolls it and spends one. Set the short-rest ' +
        'number for a pool that only trickles back — one case die an hour, not all six.'),
      index !== null ? el('div', { class: 'row', style: 'margin-top:8px' },
        el('button', { class: 'btn sm red', onclick: () => {
          ctx.patch('resources', sheet.resources.filter((_, i) => i !== index));
          close();
        } }, 'Remove'),
      ) : null,
      el('div', { class: 'row', style: 'margin-top:8px' },
        el('button', { class: 'btn grow', onclick: close }, 'Cancel'),
        el('button', { class: 'btn gold grow', onclick: save }, 'Save')));
  });
}

/** What a character calls their save DC. Most say spellcasting; some don't. */
function dcName(sheet) {
  return (sheet.spell && sheet.spell.label || '').trim() || 'Spellcasting';
}

function buildSpells(ctx) {
  const abilitySel = el('select', {},
    el('option', { value: '' }, '— none —'),
    ...Object.entries(ABILITY_NAMES).map(([k, n]) => el('option', { value: k }, n)));
  abilitySel.dataset.bind = 'spell.ability';
  abilitySel.addEventListener('change', () => ctx.patch('spell.ability', abilitySel.value));

  const attackBox = el('div', { class: 'statbox', id: 'dc-attack',
    onclick: () => { const b = ctx.ch.derived.spell_attack; if (b != null) ctx.roll(`1d20${sign(b)}`, `${dcName(ctx.ch.sheet)} attack`); } },
    el('div', { class: 'k' }, 'Attack'), el('div', { class: 'v' }, derived(ctx, 'spell_attack', 'sign')));

  return el('div', { class: 'card' },
    el('h3', { id: 'dc-title' }, 'Spellcasting'),
    el('div', { class: 'grid g3' },
      el('label', { class: 'field' }, el('span', {}, 'Ability'), abilitySel),
      el('div', { class: 'statbox' }, el('div', { class: 'k' }, 'Save DC'), el('div', { class: 'v' }, derived(ctx, 'spell_save_dc'))),
      attackBox,
    ),
    el('div', { id: 'slot-list', style: 'margin-top:6px' }),
    el('label', { class: 'field', style: 'margin-top:10px' }, el('span', {}, 'Prepared / known'),
      boundArea(ctx, 'spell.prepared', { placeholder: 'Shield, Misty Step, Fireball…' })),
    el('details', { class: 'adjust', style: 'margin-top:8px' },
      el('summary', {}, 'Not a spellcaster?'),
      el('div', { class: 'tiny muted', style: 'margin:6px 0' },
        'Some classes have a save DC without any spells behind it. Name it here ' +
        'and it stops calling itself spellcasting; turn off the attack bonus if ' +
        'there is no roll to make with it.'),
      el('label', { class: 'field' }, el('span', {}, 'Call this'),
        bound(ctx, 'spell.label', 'str', { placeholder: 'Spellcasting' })),
      el('label', { class: 'row tiny', style: 'gap:6px;width:auto;margin-top:6px' },
        bound(ctx, 'spell.show_attack', 'bool'), 'Show the attack bonus'),
    ),
  );
}

/* An attack column is a button when there is something to roll and plain text
 * when there is not. Leaving it blank used to mean "+0" and "1d4", which quietly
 * invented a roll for an unarmed strike that deals nothing and for a bomb that
 * is a saving throw rather than an attack. */
function rollChip(ctx, formula, wrap, label, cls) {
  const f = (formula || '').trim();
  if (!f) return el('span', { class: 'muted tiny', style: 'width:52px;text-align:center' }, '—');
  return el('button', { class: `btn sm ${cls}`.trim(), onclick: () => ctx.roll(wrap(f), label) }, f);
}

function buildInventory(ctx) {
  return el('div', { class: 'card' },
    el('h3', {}, 'Carried', el('span', { class: 'spacer' }),
      el('button', { class: 'btn sm', onclick: () => editCarried(ctx, null) }, '+ Add')),
    el('div', { id: 'inv-list' }),
    el('div', { class: 'tiny muted', style: 'margin-top:6px' },
      'The same list as the Loot tab \u2014 anything the DM hands you shows up here.'),
    el('div', { class: 'tiny muted', style: 'margin:12px 0 5px' }, 'Coin'),
    el('div', { class: 'grid g6' },
      ...['pp', 'gp', 'ep', 'sp', 'cp'].map(c =>
        el('label', { class: 'field', style: 'margin:0' }, el('span', {}, c), bound(ctx, `gold.${c}`, 'int'))),
    ),
  );
}

/** Add or change something in this character's pack. It lives in the shared
 *  loot ledger, so the DM and the Loot tab see the very same item. */
function editCarried(ctx, item, owner = ctx.id) {
  const isNew = !item;
  modal(isNew ? 'Add to your pack' : item.name, (close) => {
    const name = el('input', { value: item?.name || '', placeholder: 'Rope, 50 ft' });
    const qty = el('input', { type: 'number', inputmode: 'numeric', min: 1, value: item?.qty ?? 1 });
    const notes = el('input', { value: item?.notes || '', placeholder: 'hempen' });
    const mine = !isNew && item.owner === ctx.id;
    const save = () => {
      if (!name.value.trim()) return toast('Name it first', 'error');
      const fields = { name: name.value.trim(),
        qty: parseInt(qty.value || '1', 10) || 1, notes: notes.value.trim() };
      if (isNew) send('loot.add', { ...fields, owner });
      else send('loot.update', { id: item.id, patch: fields });
      close();
    };
    return el('div', {},
      el('label', { class: 'field' }, el('span', {}, 'Item'), name),
      el('div', { class: 'grid g2' },
        el('label', { class: 'field' }, el('span', {}, 'Quantity'), qty),
        el('label', { class: 'field' }, el('span', {}, 'Notes'), notes)),
      mine ? el('div', { class: 'row', style: 'margin-bottom:8px' },
        el('button', { class: 'btn sm grow', onclick: () => { send('loot.move', { id: item.id, owner: null }); close(); } }, 'Put in shared pile'),
        el('button', { class: 'btn sm red', onclick: () => { send('loot.remove', { id: item.id }); close(); } }, 'Drop it'),
      ) : null,
      el('div', { class: 'row', style: 'margin-top:8px' },
        el('button', { class: 'btn grow', onclick: close }, 'Cancel'),
        el('button', { class: 'btn gold grow', onclick: save }, 'Save')));
  });
}

function buildNotes(ctx) {
  return el('div', { class: 'card' },
    el('h3', {}, 'Features & notes'),
    el('label', { class: 'field' }, el('span', {}, 'Features & traits'),
      boundArea(ctx, 'features', { class: 'longtext', placeholder:
        'Class features, feats, racial traits — whatever you need to reach for mid-session.' })),
    el('label', { class: 'field' }, el('span', {}, 'Notes'),
      boundArea(ctx, 'notes', { class: 'longtext', placeholder:
        'Anything else: personality, appearance, who owes you money.' })),
  );
}

function buildSheet(ctx) {
  return el('div', { class: 'sheet' },
    buildVitals(ctx), buildAbilities(ctx), buildSaves(ctx), buildSkills(ctx),
    buildAttacks(ctx), buildResources(ctx), buildSpells(ctx), buildInventory(ctx),
    buildIdentity(ctx), buildNotes(ctx),
    // the caller decides what belongs after the sheet itself
    ...(ctx.footer ? [ctx.footer()] : []),
  );
}

/* ------------------------------------------------------------ sheet sync */

/** Refresh an already-built sheet in place. Inputs the user is currently typing
 *  in are left alone, so a push from the server can't eat a keystroke. */
function syncSheet(ctx, root) {
  const ch = ctx.ch;
  if (!ch || !root) return;
  const { sheet, derived: d } = ch;

  for (const node of $$('[data-bind]', root)) {
    if (node === document.activeElement) continue;
    const v = getPath(sheet, node.dataset.bind);
    if (node.type === 'checkbox') node.checked = !!v;
    else node.value = v ?? '';
    if (node.dataset.grow) autoGrow(node);
  }
  for (const node of $$('[data-derive]', root)) {
    const v = getPath(d, node.dataset.derive);
    node.textContent = v == null ? '—' : (node.dataset.fmt === 'sign' ? sign(v) : v);
  }
  for (const tag of $$('[data-skill-abbr]', root)) {
    const skill = tag.dataset.skillAbbr;
    const ability = (sheet.skill_ability || {})[skill] || S.skills[skill];
    tag.textContent = ability;
    // Flag it when it is not the ability the rules would have used.
    tag.classList.toggle('swapped', ability !== S.skills[skill]);
  }
  for (const pip of $$('[data-prof]', root)) {
    const rank = Number(getPath(sheet, pip.dataset.prof) || 0);
    pip.className = `pip p${rank || ''}`.trim();
  }

  // HP bar + death saves
  const hp = sheet.hp;
  const pct = hp.max ? Math.max(0, Math.min(1, hp.current / hp.max)) : 0;
  const fill = $('#hp-fill', root);
  fill.style.width = `${pct * 100}%`;
  fill.className = pct > 0.5 ? '' : pct > 0.25 ? 'hurt' : 'bloodied';
  $('#hp-text', root).textContent =
    `${hp.current} / ${hp.max}${hp.temp ? ` (+${hp.temp} temp)` : ''}`;
  $('#hd-text', root).textContent =
    `${Math.max(0, (sheet.hit_dice.total || 0) - (sheet.hit_dice.used || 0))}${sheet.hit_dice.die || ''}`;

  const dying = hp.current <= 0;
  $('#death-box', root).classList.toggle('hidden', !dying);
  for (const kind of ['successes', 'failures']) {
    const row = $(`[data-death=${kind}]`, root);
    $$('button', row).forEach((b, i) => {
      b.style.background = i < sheet.death_saves[kind]
        ? (kind === 'successes' ? '#4f8a52' : '#b4432e') : 'transparent';
    });
  }

  // A field is not always measurable at sync time — the DM's sheet is built and
  // synced before it is placed in its modal, and a hidden tab has no layout at
  // all — so measure again once the browser has laid this out.
  requestAnimationFrame(() => $$('textarea[data-grow]', root).forEach(autoGrow));

  // Conditions — only the active ones; tap a chip to clear it.
  const chips = $('#cond-chips', root);
  const active = sheet.conditions || [];
  chips.replaceChildren(...active.map(c => el('button', {
    class: 'chip on',
    onclick: () => ctx.patch('conditions', active.filter(x => x !== c)),
  }, c, el('span', { class: 'muted' }, '×'))));
  if (!active.length) chips.append(el('span', { class: 'tiny muted' }, 'None'));

  // Attacks
  const alist = $('#attack-list', root);
  if (!editing(alist)) {
    alist.replaceChildren(...(sheet.attacks || []).map((a, i) => el('div', { class: 'list-item' },
      el('span', { class: 'grow', onclick: () => editAttack(ctx, i) },
        el('div', {}, a.name),
        a.notes ? el('div', { class: 'tiny muted' }, a.notes) : null),
      rollChip(ctx, a.bonus, f => `1d20${f}`, `${a.name} attack`, ''),
      rollChip(ctx, a.damage, f => f, `${a.name} damage`, 'gold'),
    )));
    if (!(sheet.attacks || []).length) alist.append(el('div', { class: 'muted tiny center' }, 'No attacks yet.'));
  }

  syncResources(ctx, root);

  // The save-DC block: named by the sheet, and the attack hidden when there is
  // no roll to make with it.
  $('#dc-title', root).textContent = dcName(sheet);
  $('#dc-attack', root).classList.toggle('hidden', sheet.spell.show_attack === false);

  // Spell slots
  const slots = $('#slot-list', root);
  if (!editing(slots)) {
    slots.replaceChildren(...Object.entries(sheet.spell.slots)
      .filter(([, s]) => s.total > 0)
      .map(([lvl, s]) => el('div', { class: 'row', style: 'margin-top:7px' },
        el('span', { class: 'tiny muted', style: 'width:42px' }, `Lv ${lvl}`),
        el('div', { class: 'slotpips grow' },
          ...Array.from({ length: s.total }, (_, i) => el('i', {
            class: `s ${i < s.used ? 'used' : ''}`,
            onclick: () => ctx.patch(`spell.slots.${lvl}.used`, i < s.used ? i : i + 1),
          }))),
      )));
    slots.append(el('button', { class: 'btn sm', style: 'margin-top:9px', onclick: editSlots }, 'Edit slots'));
  }

  // Carried items — the same records the Loot tab and the DM's ledger use
  const ilist = $('#inv-list', root);
  const carried = (S.party.loot || []).filter(it => it.owner === ctx.id);
  const carriedKey = JSON.stringify(carried);
  if (!editing(ilist) && ilist.dataset.key !== carriedKey) {
    ilist.dataset.key = carriedKey;
    ilist.replaceChildren(...carried.map(it => el('div', {
      class: 'list-item', onclick: () => editCarried(ctx, it),
    },
      el('span', { class: 'muted', style: 'width:34px' }, `×${it.qty}`),
      el('span', { class: 'grow' }, it.name,
        it.notes ? el('div', { class: 'tiny muted' }, it.notes) : null),
      el('span', { class: 'muted tiny' }, '›'),
    )));
    if (!carried.length) ilist.append(el('div', { class: 'muted tiny center' }, 'Empty pack.'));
  }
}

function editConditions(ctx) {
  const active = new Set(ctx.ch.sheet.conditions || []);
  modal('Conditions', (close) => {
    const chips = el('div', { class: 'row wrap', style: 'gap:7px' }, ...S.conditions.map(c => {
      const b = el('button', { class: `chip ${active.has(c) ? 'on' : ''}` }, c);
      b.onclick = () => { active.has(c) ? active.delete(c) : active.add(c); b.classList.toggle('on'); };
      return b;
    }));
    return el('div', {}, chips,
      el('div', { class: 'row', style: 'margin-top:14px' },
        el('button', { class: 'btn grow', onclick: close }, 'Cancel'),
        el('button', { class: 'btn gold grow', onclick: () => { ctx.patch('conditions', [...active]); close(); } }, 'Save')));
  });
}

function editSlots(ctx) {
  const sheet = ctx.ch.sheet;
  modal('Spell slots', (close) => {
    const inputs = {};
    const rows = Object.keys(sheet.spell.slots).map(lvl => {
      inputs[lvl] = el('input', { type: 'number', inputmode: 'numeric', min: 0, max: 9,
        value: sheet.spell.slots[lvl].total });
      return el('label', { class: 'field' }, el('span', {}, `Level ${lvl} slots`), inputs[lvl]);
    });
    const save = () => {
      const next = {};
      for (const [lvl, node] of Object.entries(inputs)) {
        const total = parseInt(node.value || '0', 10) || 0;
        next[lvl] = { total, used: Math.min(sheet.spell.slots[lvl].used, total) };
      }
      ctx.patch('spell.slots', next);
      close();
    };
    return el('div', {}, ...rows,
      el('div', { class: 'row', style: 'margin-top:8px' },
        el('button', { class: 'btn grow', onclick: close }, 'Cancel'),
        el('button', { class: 'btn gold grow', onclick: save }, 'Save')));
  });
}

/* ------------------------------------------------------------ other tabs */

  /** Everything the sheet needs to know about whose sheet it is. */
  function contextFor(id, { readOnly = false } = {}) {
    const ctx = {
      id,
      readOnly,
      get ch() { return S.characters.find(c => c.id === id); },
      patch(path, value) {
        if (readOnly) return;
        const patch = {};
        let cur = patch;
        const keys = path.split('.');
        keys.forEach((k, i) => { if (i === keys.length - 1) cur[k] = value; else cur = (cur[k] = {}); });
        send('char.patch', { id, patch });
      },
      roll(formula, label) {
        doRoll(formula, label, { actor: ctx.ch ? ctx.ch.sheet.name : undefined });
      },
    };
    return ctx;
  }

  return {
    contextFor,
    build: (ctx) => buildSheet(ctx),
    sync: (root, ctx) => syncSheet(ctx, root),
    /** The add/edit dialog for one carried item, reused by the Loot tab. */
    editItem: (ctx, item, owner) => editCarried(ctx, item, owner),
    ABILITY_NAMES,
  };
})();
