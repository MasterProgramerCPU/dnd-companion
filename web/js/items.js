/* The DM's item library: treasure written up ahead of a session and kept with
 * the campaign, so the same potion does not have to be typed again for the
 * next party that finds one.
 *
 * None of this is ever sent to a player; the server drops the whole list from
 * a player's copy of the party state, exactly as it does the bestiary. A
 * player meets an item by being handed one, which arrives in their pack as
 * ordinary loot — droppable, passable, and with no thread back to the shelf it
 * came off.
 *
 * Each item carries two notes and the split is the point: the item note goes
 * with the item and is what a player reads, while the DM notes stay here.
 */

function libraryItems() {
  return (S.party && S.party.items) || [];
}

/* ------------------------------------------------------------ the library */

function itemLibrary() {
  const list = libraryItems();
  const card = el('div', { class: 'card' },
    el('h3', {}, 'Item library', el('span', { class: 'spacer' }),
      el('span', { class: 'tiny muted' }, `${list.length}`),
      el('button', { class: 'btn sm gold', onclick: () => editItem(null) }, '+ Add')),
  );

  for (const it of list) {
    card.append(el('div', { class: 'list-item' },
      el('span', { class: 'grow', onclick: () => editItem(it) },
        el('div', {}, it.name),
        el('div', { class: 'tiny muted' },
          [it.kind, it.qty > 1 ? `×${it.qty} by default` : null, it.notes]
            .filter(Boolean).join(' · '))),
      el('button', { class: 'btn sm gold', onclick: () => giveItem(it) }, 'Give'),
    ));
  }
  if (!list.length) {
    card.append(el('div', { class: 'muted tiny center' },
      'Nothing prepared yet. What you keep here stays yours — players only ever '
      + 'see what you hand them.'));
  }
  card.append(el('div', { class: 'tiny muted', style: 'margin-top:8px' },
    'Giving copies an item into the loot below. The library keeps its own, so the '
    + 'same thing can be found twice.'));
  return card;
}

/* --------------------------------------------------------- handing it over */

function giveItem(it) {
  modal(`Give ${it.name}`, (close) => {
    const owner = ownerOptions(null);
    const qty = el('input', {
      type: 'number', inputmode: 'numeric', min: 1, value: it.qty ?? 1,
    });
    const announce = el('input', { type: 'checkbox' });
    announce.checked = true;

    const go = () => {
      send('item.give', {
        id: it.id,
        owner: owner.value === '' ? null : Number(owner.value),
        qty: Math.max(1, parseInt(qty.value || '1', 10) || 1),
        announce: announce.checked,
      });
      close();
    };
    qty.addEventListener('keydown', (e) => { if (e.key === 'Enter') go(); });

    return el('div', {},
      it.notes
        ? el('div', { class: 'tiny muted', style: 'margin-bottom:9px' },
          `They will read: ${it.notes}`)
        : el('div', { class: 'tiny muted', style: 'margin-bottom:9px' },
          'This one has no note, so they get the name alone.'),
      el('label', { class: 'field' }, el('span', {}, 'Who gets it'), owner),
      el('label', { class: 'field' }, el('span', {}, 'How many'), qty),
      el('label', { class: 'row tiny', style: 'gap:7px;width:auto' },
        announce, 'Say so at the table'),
      el('div', { class: 'tiny muted', style: 'margin-top:5px' },
        'Unticked, it appears in their pack with nothing announced.'),
      el('div', { class: 'row', style: 'margin-top:12px' },
        el('button', { class: 'btn grow', onclick: close }, 'Cancel'),
        el('button', { class: 'btn gold grow', onclick: go }, 'Give')));
  });
}

/* ------------------------------------------------------------- the editor */

/* Saved or cancelled whole rather than field by field, for the reason a stat
 * block is: it is written before a session, so there is nothing to keep in
 * sync mid-scene. */
function editItem(existing) {
  const it = existing
    ? JSON.parse(JSON.stringify(existing))
    : { name: '', kind: '', notes: '', desc: '', qty: 1 };

  modal(existing ? it.name : 'New item', (close) => {
    const name = el('input', { value: it.name || '', placeholder: 'Flame Tongue' });
    const kind = el('input', { value: it.kind || '', placeholder: 'Weapon (longsword), rare' });
    const qty = el('input', {
      type: 'number', inputmode: 'numeric', min: 1, value: it.qty ?? 1,
    });
    const notes = el('input', {
      value: it.notes || '', maxlength: 200,
      placeholder: 'a blackened longsword, warm to the touch',
    });
    const desc = el('textarea', {
      value: it.desc || '', class: 'longtext',
      placeholder: 'What it really does, who wants it back, what happens when they '
        + 'work it out. Players never see any of this.',
    });

    const save = () => {
      if (!name.value.trim()) return toast('Give the item a name', 'error');
      send('item.save', {
        item: {
          ...(it.id ? { id: it.id } : {}),
          name: name.value.trim(),
          kind: kind.value.trim(),
          qty: Math.max(1, parseInt(qty.value || '1', 10) || 1),
          notes: notes.value.trim(),
          desc: desc.value,
        },
      });
      close();
    };

    return el('div', {},
      el('label', { class: 'field' }, el('span', {}, 'Name'), name),
      el('label', { class: 'field' }, el('span', {}, 'Kind'), kind),
      el('label', { class: 'field' }, el('span', {}, 'Quantity to give'), qty),
      el('div', { class: 'tiny muted', style: 'margin:-4px 0 8px' },
        'The number offered when you hand it over. You can change it then.'),
      el('label', { class: 'field' }, el('span', {}, 'Item note'), notes),
      el('div', { class: 'tiny muted', style: 'margin:-4px 0 8px' },
        'Goes with the item. This is what a player reads in their pack.'),
      el('label', { class: 'field' }, el('span', {}, 'DM notes'), desc),
      el('div', { class: 'row', style: 'margin-top:14px' },
        existing ? el('button', {
          class: 'btn sm red',
          onclick: () => {
            if (!confirm(`Delete ${it.name}? Anything already given stays given.`)) return;
            send('item.remove', { id: it.id });
            close();
          },
        }, 'Delete') : null,
        el('span', { class: 'grow' }),
        el('button', { class: 'btn', onclick: close }, 'Cancel'),
        el('button', { class: 'btn gold', onclick: save }, 'Save')));
  }, 'sheet');
}
