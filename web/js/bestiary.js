/* The DM's bestiary: stat blocks kept with the campaign, and the dialog that
 * drops a batch of one into the initiative order.
 *
 * A stat block is stored as the same free-form map a character sheet is, and
 * the server derives it with the same code, so a goblin's Dexterity save is
 * worked out exactly like a player's. What it leaves out is everything only a
 * player has — death saves, hit dice, spell slots, coin, a pack — and what it
 * adds is a challenge rating and an HP formula, so six goblins can each roll
 * their own hit points.
 *
 * None of this is ever sent to a player; the server drops the whole list from
 * a player's copy of the party state.
 */

const ABIL = ['str', 'dex', 'con', 'int', 'wis', 'cha'];

/* The ratings a stat block actually prints, in order, so a picker can offer
 * them rather than asking the DM to type 0.125. */
const CR_STEPS = [0, 0.125, 0.25, 0.5, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13,
  14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30];

function crLabel(cr) {
  const n = Number(cr) || 0;
  if (n <= 0) return '0';
  if (n < 0.2) return '1/8';
  if (n < 0.3) return '1/4';
  if (n < 0.6) return '1/2';
  return String(n);
}

/** The proficiency bonus a rating carries — mirrors sheet.ProfBonusForCR. */
function crProf(cr) {
  const n = Math.min(30, Math.max(1, Number(cr) || 0));
  return 2 + Math.floor((n - 1) / 4);
}

const mod = (score) => Math.floor(((Number(score) || 10) - 10) / 2);

function creatures() {
  return (S.party && S.party.bestiary) || [];
}

/* ------------------------------------------------------------ the library */

function bestiaryCard() {
  const list = creatures();
  const card = el('div', { class: 'card' },
    el('h3', {}, 'Bestiary', el('span', { class: 'spacer' }),
      el('span', { class: 'tiny muted' }, `${list.length}`),
      el('button', { class: 'btn sm gold', onclick: () => editCreature(null) }, '+ Add')),
  );

  for (const c of list) {
    card.append(el('div', { class: 'list-item' },
      el('span', { class: 'grow', onclick: () => editCreature(c) },
        el('div', {}, c.name),
        el('div', { class: 'tiny muted' },
          [c.kind, `CR ${crLabel(c.cr)}`, `AC ${c.ac ?? '—'}`,
            c.hp_formula ? `HP ${c.hp_formula}` : `HP ${c.hp_max ?? '—'}`,
            c.hidden ? 'hidden by default' : null].filter(Boolean).join(' · '))),
      el('button', { class: 'btn sm gold', onclick: () => sendToCombat(c) }, 'To combat'),
    ));
  }
  if (!list.length) {
    card.append(el('div', { class: 'muted tiny center' },
      'No creatures yet. Anything you add here stays yours — players never receive it.'));
  }
  card.append(el('div', { class: 'tiny muted', style: 'margin-top:8px' },
    'Kept with this campaign. Switching campaigns swaps the bestiary with it.'));
  return card;
}

/** The Bestiary tab. */
function renderBestiary() {
  const host = $('[data-tab=bestiary]');
  if (!host || editing(host)) return;
  host.replaceChildren(bestiaryCard());
}

/* ------------------------------------------------------- into the fight */

function sendToCombat(c) {
  modal(`${c.name} — into combat`, (close) => {
    const count = el('input', { type: 'number', inputmode: 'numeric', min: 1, max: 20, value: 1 });
    const hidden = el('input', { type: 'checkbox' });
    hidden.checked = !!c.hidden;
    const sharedInit = el('input', { type: 'checkbox' });

    const go = () => {
      send('init.add_bestiary', {
        id: c.id,
        count: Math.min(20, Math.max(1, parseInt(count.value || '1', 10) || 1)),
        hidden: hidden.checked,
        shared_init: sharedInit.checked,
      });
      close();
    };
    count.addEventListener('keydown', (e) => { if (e.key === 'Enter') go(); });

    return el('div', {},
      el('div', { class: 'tiny muted', style: 'margin-bottom:9px' },
        c.hp_formula
          ? `Each one rolls ${c.hp_formula} for its own hit points.`
          : `Each one starts on ${c.hp_max ?? 0} hit points.`),
      el('label', { class: 'field' }, el('span', {}, 'How many'), count),
      el('label', { class: 'row tiny', style: 'gap:7px;width:auto' },
        hidden, 'Hidden — players see ??? until you reveal it'),
      el('label', { class: 'row tiny', style: 'gap:7px;width:auto;margin-top:7px' },
        sharedInit, 'One initiative roll for the whole batch'),
      el('div', { class: 'row', style: 'margin-top:12px' },
        el('button', { class: 'btn grow', onclick: close }, 'Cancel'),
        el('button', { class: 'btn gold grow', onclick: go }, 'Add to combat')));
  });
}

/** The Combat tab's own way in: pick a creature, then the batch dialog. */
function addFromBestiary() {
  const list = creatures();
  if (!list.length) {
    return toast('No creatures yet — add one under Campaign', 'error');
  }
  modal('From the bestiary', (close) => {
    const rows = list.map(c => el('div', {
      class: 'list-item', onclick: () => { close(); sendToCombat(c); },
    },
      el('span', { class: 'grow' },
        el('div', {}, c.name),
        el('div', { class: 'tiny muted' },
          [c.kind, `CR ${crLabel(c.cr)}`, `AC ${c.ac ?? '—'}`].filter(Boolean).join(' · '))),
      el('span', { class: 'muted tiny' }, '›'),
    ));
    return el('div', {}, ...rows,
      el('div', { class: 'row', style: 'margin-top:10px' },
        el('button', { class: 'btn grow', onclick: close }, 'Cancel')));
  });
}

/* ------------------------------------------------------- the stat block */

/* The editor. A creature is saved or cancelled whole rather than field by
 * field: it is prepared before a session rather than edited live, so there is
 * nothing to keep in sync mid-scene and a half-saved monster is worse than an
 * unsaved one. */
function editCreature(existing) {
  const c = existing ? JSON.parse(JSON.stringify(existing)) : {
    name: '', kind: '', cr: 0, ac: 12, hp_max: 10, hp_formula: '', speed: 30,
    abilities: { str: 10, dex: 10, con: 10, int: 10, wis: 10, cha: 10 },
    save_prof: {}, skill_prof: {}, attacks: [], features: '', notes: '',
    hidden: false, initiative_bonus: 0,
  };
  c.abilities = c.abilities || {};
  c.attacks = c.attacks || [];
  c.save_prof = c.save_prof || {};
  c.skill_prof = c.skill_prof || {};

  modal(existing ? c.name : 'New creature', (close) => {
    const f = {};
    const text = (key, label, ph, attrs = {}) => {
      f[key] = el('input', { value: c[key] ?? '', placeholder: ph, ...attrs });
      return el('label', { class: 'field' }, el('span', {}, label), f[key]);
    };
    const num = (key, label, attrs = {}) => {
      f[key] = el('input', { type: 'number', inputmode: 'numeric', value: c[key] ?? 0, ...attrs });
      return el('label', { class: 'field' }, el('span', {}, label), f[key]);
    };

    // Challenge rating drives the proficiency bonus, so show what it buys.
    const crSel = el('select', {}, ...CR_STEPS.map(v =>
      el('option', { value: String(v), selected: Number(c.cr || 0) === v }, crLabel(v))));
    const profNote = el('span', { class: 'tiny muted' });
    const showProf = () => {
      profNote.textContent = `proficiency +${crProf(parseFloat(crSel.value))}`;
    };
    crSel.addEventListener('change', showProf);
    showProf();

    // Abilities, with the modifier shown as it is typed.
    const abilInputs = {};
    const abilGrid = el('div', { class: 'grid g3' }, ...ABIL.map(a => {
      const input = el('input', {
        type: 'number', inputmode: 'numeric', min: 1, max: 30,
        value: c.abilities[a] ?? 10, style: 'text-align:center',
      });
      const show = el('div', { class: 'tiny muted center' });
      const refresh = () => {
        const m = mod(input.value);
        show.textContent = `${m >= 0 ? '+' : ''}${m}`;
      };
      input.addEventListener('input', refresh);
      refresh();
      abilInputs[a] = input;
      return el('div', { class: 'field', style: 'margin:0' },
        el('span', { style: 'text-transform:uppercase' }, a), input, show);
    }));

    // Saving throw proficiencies, as six toggles.
    const saveChips = el('div', { class: 'row wrap', style: 'gap:6px' });
    const saves = { ...c.save_prof };
    for (const a of ABIL) {
      const chip = el('button', { class: `chip ${saves[a] ? 'on' : ''}`.trim() }, a.toUpperCase());
      chip.addEventListener('click', () => {
        saves[a] = !saves[a];
        chip.className = `chip ${saves[a] ? 'on' : ''}`.trim();
      });
      saveChips.append(chip);
    }

    // Attacks, the same shape a character sheet uses.
    const attacks = [...c.attacks];
    const atkList = el('div', {});
    const drawAttacks = () => {
      atkList.replaceChildren(...attacks.map((a, i) => el('div', { class: 'list-item' },
        el('span', { class: 'grow' },
          el('div', {}, a.name || 'Attack'),
          el('div', { class: 'tiny muted' },
            [a.bonus, a.damage, a.notes].filter(Boolean).join(' · '))),
        el('button', {
          class: 'btn sm red',
          onclick: () => { attacks.splice(i, 1); drawAttacks(); },
        }, '×'),
      )));
      if (!attacks.length) {
        atkList.append(el('div', { class: 'muted tiny center' }, 'No attacks yet.'));
      }
    };
    drawAttacks();

    const an = el('input', { placeholder: 'Scimitar' });
    const ab = el('input', { placeholder: '+4' });
    const ad = el('input', { placeholder: '1d6+2' });
    const anote = el('input', { placeholder: 'reach 5 ft, one target' });
    const addAttack = () => {
      if (!an.value.trim()) return toast('Name the attack', 'error');
      attacks.push({
        name: an.value.trim(), bonus: ab.value.trim(),
        damage: ad.value.trim(), notes: anote.value.trim(),
      });
      an.value = ab.value = ad.value = anote.value = '';
      drawAttacks();
      an.focus();
    };

    const features = el('textarea', {
      value: c.features || '', class: 'longtext',
      placeholder: 'Traits, reactions, legendary actions — whatever you need mid-fight.',
    });
    const notes = el('textarea', {
      value: c.notes || '',
      placeholder: 'Your own notes. Players never see any of this.',
    });
    const hidden = el('input', { type: 'checkbox' });
    hidden.checked = !!c.hidden;

    const save = () => {
      const name = f.name.value.trim();
      if (!name) return toast('Give the creature a name', 'error');
      const abilities = {};
      for (const a of ABIL) abilities[a] = parseInt(abilInputs[a].value || '10', 10) || 10;
      const saveProf = {};
      for (const a of ABIL) saveProf[a] = !!saves[a];

      send('bestiary.save', {
        creature: {
          ...(c.id ? { id: c.id } : {}),
          name,
          kind: f.kind.value.trim(),
          cr: parseFloat(crSel.value) || 0,
          ac: parseInt(f.ac.value || '10', 10) || 10,
          hp_max: parseInt(f.hp_max.value || '0', 10) || 0,
          hp_formula: f.hp_formula.value.trim(),
          speed: parseInt(f.speed.value || '30', 10) || 0,
          initiative_bonus: parseInt(f.initiative_bonus.value || '0', 10) || 0,
          abilities,
          save_prof: saveProf,
          skill_prof: c.skill_prof,
          attacks,
          features: features.value,
          notes: notes.value,
          hidden: hidden.checked,
        },
      });
      close();
    };

    return el('div', {},
      text('name', 'Name', 'Ticket Wraith'),
      text('kind', 'Kind', 'Medium undead, lawful evil'),
      el('label', { class: 'field' },
        el('span', {}, 'Challenge rating ', profNote), crSel),
      el('div', { class: 'grid g3' },
        num('ac', 'AC'), num('hp_max', 'Hit points'), num('speed', 'Speed')),
      text('hp_formula', 'Hit point formula', '6d8+6 — optional'),
      el('div', { class: 'tiny muted', style: 'margin:-4px 0 8px' },
        'Given a formula, every copy you add to combat rolls its own hit points. '
        + 'Left blank, they all start on the flat number above.'),
      num('initiative_bonus', 'Initiative bonus (on top of DEX)'),

      el('div', { class: 'tiny muted', style: 'margin:10px 0 5px' }, 'Abilities'),
      abilGrid,
      el('div', { class: 'tiny muted', style: 'margin:10px 0 5px' }, 'Saving throw proficiency'),
      saveChips,

      el('div', { class: 'tiny muted', style: 'margin:12px 0 5px' }, 'Attacks'),
      atkList,
      el('div', { class: 'grid g2', style: 'margin-top:6px' },
        el('label', { class: 'field', style: 'margin:0' }, el('span', {}, 'Name'), an),
        el('label', { class: 'field', style: 'margin:0' }, el('span', {}, 'To hit'), ab)),
      el('div', { class: 'grid g2' },
        el('label', { class: 'field', style: 'margin:0' }, el('span', {}, 'Damage'), ad),
        el('label', { class: 'field', style: 'margin:0' }, el('span', {}, 'Notes'), anote)),
      el('button', { class: 'btn sm', style: 'margin-top:7px', onclick: addAttack }, '+ Add attack'),

      el('label', { class: 'field', style: 'margin-top:12px' },
        el('span', {}, 'Traits & actions'), features),
      el('label', { class: 'field' }, el('span', {}, 'DM notes'), notes),
      el('label', { class: 'row tiny', style: 'gap:7px;width:auto' },
        hidden, 'Hidden by default when added to combat'),

      el('div', { class: 'row', style: 'margin-top:14px' },
        existing ? el('button', {
          class: 'btn sm red',
          onclick: () => {
            if (!confirm(`Delete ${c.name}? This cannot be undone.`)) return;
            send('bestiary.remove', { id: c.id });
            close();
          },
        }, 'Delete') : null,
        el('span', { class: 'grow' }),
        el('button', { class: 'btn', onclick: close }, 'Cancel'),
        el('button', { class: 'btn gold', onclick: save }, 'Save')));
  }, 'sheet');
}
