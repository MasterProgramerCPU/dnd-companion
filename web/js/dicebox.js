/* Wrapper around the vendored @3d-dice/dice-box.
 *
 * dice-box decides its own numbers — its physics ray-casts the settled mesh to
 * read a face, and there is no way to hand it a predetermined value. So the
 * throw here IS the roll: the server tells us which dice to throw, dice-box
 * throws them, and we report what they landed on. The server still owns all the
 * arithmetic (keep-highest, advantage, modifiers) and the log.
 *
 * If it can't start — no WebGL, an old phone, assets missing — `available`
 * stays false and the caller falls back to the built-in renderer.
 */

const DiceTray = (() => {
  let box = null;
  let ready = null;
  let available = false;
  let mount = null;

  function makeMount() {
    if (mount) return mount;
    mount = el('div', { id: 'dice-tray' });
    document.body.append(mount);
    return mount;
  }

  /** Loads and starts dice-box once; resolves false if it can't run here. */
  function init() {
    if (ready) return ready;
    ready = (async () => {
      try {
        const probe = document.createElement('canvas');
        if (!probe.getContext('webgl2') && !probe.getContext('webgl')) return false;
        const { default: DiceBox } = await import('/static/vendor/dice-box/dice-box.es.min.js');
        makeMount();                       // must exist before dice-box looks it up
        box = new DiceBox({
          container: '#dice-tray',
          assetPath: '/static/vendor/dice-box/assets/',
          theme: 'default',
          themeColor: '#c9a227',
          scale: 5.4,
          gravity: 2,
          friction: 0.8,
          restitution: 0.1,
          spinForce: 5,
          throwForce: 6,
          startingHeight: 12,
          settleTimeout: 5000,
          lightIntensity: 1.1,
          shadowTransparency: 0.75,
          enableShadows: true,
        });
        await box.init();
        available = true;
        return true;
      } catch (err) {
        console.warn('dice-box unavailable, using built-in dice:', err);
        available = false;
        return false;
      }
    })();
    return ready;
  }

  /**
   * Throws `plan` (e.g. [{sides:20, qty:2}]) and resolves with the values
   * grouped per plan entry: [[17, 4]]. dice-box returns one flat array of dice
   * tagged with the notation entry they came from, so regroup by groupId.
   */
  async function throwDice(plan) {
    if (!available) throw new Error('dice tray not available');
    const notation = plan.map(d => ({ qty: d.qty, sides: d.sides }));
    const rolled = await box.roll(notation);
    const flat = Array.isArray(rolled) ? rolled : [rolled];

    const groups = plan.map(() => []);
    for (const die of flat) {
      const g = Number(die.groupId ?? 0);
      (groups[g] || groups[0]).push(die);
    }
    return groups.map((g, i) => {
      g.sort((a, b) => (a.rollId ?? 0) - (b.rollId ?? 0));
      const vals = g.map(die => Number(die.value));
      if (vals.length !== plan[i].qty || vals.some(v => !Number.isInteger(v) || v < 1)) {
        throw new Error(`tray returned ${vals.length} dice for ${plan[i].qty}d${plan[i].sides}`);
      }
      return vals;
    });
  }

  const show = () => { makeMount().classList.add('on'); };
  const hide = () => {
    if (!mount) return;
    mount.classList.remove('on');
    try { box?.clear(); } catch { /* nothing thrown yet */ }
  };

  // Lets the fallback path be exercised deliberately (tests, and a way out if a
  // particular phone renders the tray badly).
  let forced = null;
  return {
    init, throwDice, show, hide,
    get available() { return forced === null ? available : forced; },
    force(v) { forced = v; },
  };
})();
