/* Real 3D dice: actual polyhedra, tumbled by a small rigid-body sim and drawn
 * on a canvas. The trajectory is simulated up front so we know which face ends
 * up on top — the rolled value is then painted onto that face, which is how the
 * die can both tumble freely and still land on the number the server rolled. */

const Dice3D = (() => {
  const PHI = (1 + Math.sqrt(5)) / 2;

  // ---------------------------------------------------------------- vec/quat

  const sub = (a, b) => [a[0] - b[0], a[1] - b[1], a[2] - b[2]];
  const add = (a, b) => [a[0] + b[0], a[1] + b[1], a[2] + b[2]];
  const mul = (a, s) => [a[0] * s, a[1] * s, a[2] * s];
  const dot = (a, b) => a[0] * b[0] + a[1] * b[1] + a[2] * b[2];
  const cross = (a, b) => [
    a[1] * b[2] - a[2] * b[1],
    a[2] * b[0] - a[0] * b[2],
    a[0] * b[1] - a[1] * b[0],
  ];
  const len = a => Math.hypot(a[0], a[1], a[2]);
  const norm = a => { const l = len(a) || 1; return [a[0] / l, a[1] / l, a[2] / l]; };
  const mid = (a, b) => mul(add(a, b), 0.5);

  function centroid(pts) {
    const c = pts.reduce((acc, p) => add(acc, p), [0, 0, 0]);
    return mul(c, 1 / pts.length);
  }

  const qMul = (a, b) => [
    a[0] * b[0] - a[1] * b[1] - a[2] * b[2] - a[3] * b[3],
    a[0] * b[1] + a[1] * b[0] + a[2] * b[3] - a[3] * b[2],
    a[0] * b[2] - a[1] * b[3] + a[2] * b[0] + a[3] * b[1],
    a[0] * b[3] + a[1] * b[2] - a[2] * b[1] + a[3] * b[0],
  ];
  function qNorm(q) {
    const l = Math.hypot(q[0], q[1], q[2], q[3]) || 1;
    return [q[0] / l, q[1] / l, q[2] / l, q[3] / l];
  }
  function qAxis(axis, angle) {
    const a = norm(axis), s = Math.sin(angle / 2);
    return [Math.cos(angle / 2), a[0] * s, a[1] * s, a[2] * s];
  }
  function qRot(q, v) {
    const [w, x, y, z] = q;
    const t = cross([x, y, z], v);
    return add(v, add(mul(t, 2 * w), mul(cross([x, y, z], t), 2)));
  }
  /** Shortest-arc rotation taking unit vector `a` onto unit vector `b`. */
  function qBetween(a, b) {
    const d = dot(a, b);
    if (d > 0.99999) return [1, 0, 0, 0];
    if (d < -0.99999) {
      let axis = cross([1, 0, 0], a);
      if (len(axis) < 1e-6) axis = cross([0, 1, 0], a);
      return qAxis(axis, Math.PI);
    }
    const c = cross(a, b);
    return qNorm([1 + d, c[0], c[1], c[2]]);
  }
  function qSlerp(a, b, t) {
    let d = a[0] * b[0] + a[1] * b[1] + a[2] * b[2] + a[3] * b[3];
    let bb = b;
    if (d < 0) { bb = b.map(x => -x); d = -d; }
    if (d > 0.9995) return qNorm(a.map((x, i) => x + (bb[i] - x) * t));
    const th = Math.acos(d), s = Math.sin(th);
    const wa = Math.sin((1 - t) * th) / s, wb = Math.sin(t * th) / s;
    return qNorm(a.map((x, i) => x * wa + bb[i] * wb));
  }

  // ---------------------------------------------------------------- geometry

  /** Faces of any convex solid, found from the geometry itself: every plane
   *  through three vertices that leaves all other vertices behind it is a face.
   *  Convention-independent, so it can't be thrown off by how the vertex list
   *  happens to be ordered or which dual pairing was intended. */
  function facesOf(verts) {
    const planes = [];
    const n = verts.length;
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n; j++) {
        for (let k = j + 1; k < n; k++) {
          const raw = cross(sub(verts[j], verts[i]), sub(verts[k], verts[i]));
          if (len(raw) < 1e-9) continue;                    // collinear triple
          let nn = norm(raw);
          let d = dot(nn, verts[i]);
          if (d < 0) { nn = mul(nn, -1); d = -d; }          // face outward
          if (d < 1e-6) continue;                           // plane through centre
          if (verts.some(v => dot(nn, v) > d + 1e-6)) continue;  // not a hull face
          if (planes.some(p => dot(p.n, nn) > 1 - 1e-6)) continue;  // already found
          planes.push({ n: nn, d });
        }
      }
    }
    return planes.map(({ n, d }) => {
      const idx = verts.map((v, i) => [dot(n, v), i])
        .filter(([x]) => x > d - 1e-6).map(([, i]) => i);
      const c = centroid(idx.map(i => verts[i]));
      const u = norm(sub(verts[idx[0]], c));
      const w = cross(n, u);
      const ang = i => Math.atan2(dot(sub(verts[i], c), w), dot(sub(verts[i], c), u));
      idx.sort((a, b) => ang(a) - ang(b));
      return { idx, n, c };
    });
  }

  function pm(...vals) {                       // all sign combinations
    const out = [];
    const rec = (i, acc) => {
      if (i === vals.length) return void out.push(acc.slice());
      if (vals[i] === 0) { acc.push(0); rec(i + 1, acc); acc.pop(); return; }
      for (const s of [1, -1]) { acc.push(vals[i] * s); rec(i + 1, acc); acc.pop(); }
    };
    rec(0, []);
    return out;
  }
  const cycle = (a, b, c) => [...pm(a, b, c), ...pm(b, c, a), ...pm(c, a, b)];

  const TETRA = [[1, 1, 1], [1, -1, -1], [-1, 1, -1], [-1, -1, 1]];
  const CUBE = pm(1, 1, 1);
  const OCTA = [...pm(1, 0, 0), ...pm(0, 1, 0), ...pm(0, 0, 1)];
  const ICOSA = cycle(0, 1, PHI);
  const DODECA = [...CUBE, ...cycle(0, 1 / PHI, PHI)];

  /** d10 is a pentagonal trapezohedron. The apex height isn't free: it's fixed
   *  by requiring each kite's four corners to be coplanar, so solve for it. */
  function trapezohedron() {
    const c = 0.35;
    const a36 = Math.PI / 5, a72 = 2 * Math.PI / 5;
    const K = (Math.cos(a36) - 1) * Math.sin(a72) - Math.sin(a36) * (Math.cos(a72) - 1);
    const h = c + (2 * c * Math.sin(a72)) / K;
    const verts = [];
    for (let i = 0; i < 10; i++) {
      const t = (i * Math.PI) / 5;
      verts.push([Math.cos(t), Math.sin(t), i % 2 ? -c : c]);
    }
    verts.push([0, 0, h], [0, 0, -h]);
    return verts;
  }

  function build(verts) {
    const scale = 1 / Math.max(...verts.map(len));
    const v = verts.map(p => mul(p, scale));
    return { verts: v, faces: facesOf(v) };
  }

  let lastPlay = null;

  const SOLIDS = {};
  const VERTS_FOR = { 4: TETRA, 6: CUBE, 8: OCTA, 12: DODECA, 20: ICOSA };
  function solid(sides) {
    if (!SOLIDS[sides]) SOLIDS[sides] = build(VERTS_FOR[sides] || trapezohedron());
    return SOLIDS[sides];
  }

  /** Every die shape we can actually render; anything else borrows the d6. */
  const SHAPE_FOR = s => ([4, 6, 8, 12, 20].includes(s) ? s : (s === 10 || s === 100 ? 10 : 6));

  // ---------------------------------------------------------------- camera

  /* World axes: x right, y away from the viewer, z up against gravity.
   * The camera looks down at the tray from in front and above, at ELEV above
   * the table. Everything — dice bodies, positions, shadows — goes through this
   * one transform, so height and depth can't disagree the way they used to. */
  const ELEV = 57 * Math.PI / 180;
  const CE = Math.cos(ELEV), SE = Math.sin(ELEV);
  /** world -> view: [right, up-on-screen, towards-camera] */
  const toView = v => [v[0], v[2] * CE + v[1] * SE, v[2] * SE - v[1] * CE];
  const UP = [0, 0, 1];

  // ---------------------------------------------------------------- physics

  function rng(seed) {
    let s = seed >>> 0 || 1;
    return () => (s = (s * 1664525 + 1013904223) >>> 0) / 4294967296;
  }

  const DT = 1 / 60;
  const GRAV = 2500;
  const BOUNCE = 0.46;         // floor restitution
  const WALL = 0.52;
  const FRICTION = 0.34;       // contact friction -> converts sliding into roll
  const AIR_SPIN_DRAG = 0.5;
  const ROLL_DRAG = 4.6;
  const SETTLE_SPEED = 105;    // below this it starts toppling onto a face
  const SETTLE_SPIN = 6.5;
  const STIFF = 300;           // how hard gravity topples it flat onto a face
  const DAMP = 2 * Math.sqrt(STIFF);  // critically damped: settles without ringing
  // Past roughly 20 degrees per frame the eye stops reading rotation and starts
  // reading flicker, so spin is capped no matter what a collision imparts.
  const MAX_SPIN = 20;
  const FORCE_SETTLE = 46;    // frames of free tumbling before gravity insists
  const MAX_STEPS = 140;      // safety net; throws normally settle well before this

  const qConj = q => [q[0], -q[1], -q[2], -q[3]];

  /** Rotation that carries `q` to `target`, as an axis * angle vector. */
  function turnToward(q, target) {
    let d = qMul(target, qConj(q));
    if (d[0] < 0) d = d.map(x => -x);
    const s = Math.hypot(d[1], d[2], d[3]);
    if (s < 1e-7) return [0, 0, 0];
    const angle = 2 * Math.atan2(s, d[0]);
    return mul([d[1] / s, d[2] / s, d[3] / s], angle);
  }

  /** Which face is most nearly up, in world terms. */
  function topFace(shape, q) {
    let best = 0, bestDot = -Infinity;
    shape.faces.forEach((f, i) => {
      const d = dot(qRot(q, f.n), UP);
      if (d > bestDot) { bestDot = d; best = i; }
    });
    return best;
  }

  /** A face's own "up" direction — used to keep its numeral oriented with it. */
  const faceUp = (shape, f) =>
    norm(sub(mid(shape.verts[f.idx[0]], shape.verts[f.idx[1]]), f.c));

  /** On-screen angle of that numeral for a given orientation; 0 is upright. */
  const textAngle = (shape, f, q) => {
    const u = toView(qRot(q, faceUp(shape, f)));
    return Math.atan2(-u[1], u[0]) + Math.PI / 2;
  };

  /** Orientation with face `fi` flat on the table and its numeral readable.
   *  The twist is solved in world space: once the face is flat, its "up" vector
   *  lies in the table plane, and screen-up corresponds to world +y. Measuring
   *  the angle after projection instead would be wrong, because the camera
   *  foreshortens the table and a spin about world-up is not a spin about the
   *  view axis. */
  function restingQuat(shape, q, fi) {
    const f = shape.faces[fi];
    const flat = qNorm(qMul(qBetween(qRot(q, f.n), UP), q));
    const u = qRot(flat, faceUp(shape, f));
    return qNorm(qMul(qAxis(UP, Math.PI / 2 - Math.atan2(u[1], u[0])), flat));
  }

  /**
   * Throws every die together — they bounce off the tray and off each other —
   * and records the whole trajectory so playback is just a lookup.
   */
  function simulateAll(dice, R, halfW, halfD, seed0, zMax) {
    // Dice are laid out on a grid at least one diameter apart before they are
    // thrown. Spawning them on top of one another makes the very first collision
    // pass blow them apart, which is the single worst thing the throw can look
    // like — a shove, not a roll.
    const cell = R * CELL;
    const { cols, rows: rowsAvail } = gridCapacity({ halfW, halfD }, R);
    const usedCols = Math.min(dice.length, cols);
    const xStart = -((usedCols - 1) * cell) / 2;

    const bodies = dice.map((die, i) => {
      const rand = rng(seed0 + i * 2654435761);
      const col = i % cols;
      const slot = Math.floor(i / cols);
      const row = slot % rowsAvail;
      const layer = Math.floor(slot / rowsAvail);
      return {
        die,
        shape: solid(SHAPE_FOR(die.sides)),
        rand,
        p: [Math.max(-(halfW - R), Math.min(halfW - R, xStart + col * cell)),
            Math.max(-(halfD - R), halfD - R - row * cell),
            Math.min(zMax, R + 20 + layer * cell + rand() * 20)],
        v: [(rand() - 0.5) * 120, -(168 + rand() * 112), 52 + rand() * 70],
        w: mul(norm([rand() - 0.5, rand() - 0.5, rand() - 0.5]), 11 + rand() * 8),
        qCur: qNorm([rand() - 0.5, rand() - 0.5, rand() - 0.5, rand() - 0.5]),
        target: null,
        face: 0,
        frames: [],
      };
    });

    let steps = 0, still = 0;
    for (; steps < MAX_STEPS; steps++) {
      for (const b of bodies) {
        b.v[2] -= GRAV * DT;
        b.p = add(b.p, mul(b.v, DT));

        // tray walls
        for (const [ax, lim] of [[0, halfW - R], [1, halfD - R]]) {
          if (b.p[ax] < -lim || b.p[ax] > lim) {
            b.p[ax] = Math.max(-lim, Math.min(lim, b.p[ax]));
            b.v[ax] = -b.v[ax] * WALL;
            b.w = add(b.w, mul(norm([b.rand() - 0.5, b.rand() - 0.5, b.rand() - 0.5]), 3));
          }
        }

        // The tray has a lid as well as a floor: a collision must never be able
        // to punt a die up out of the frame.
        if (b.p[2] > zMax) {
          b.p[2] = zMax;
          if (b.v[2] > 0) b.v[2] = -b.v[2] * 0.3;
        }

        // table
        let onFloor = false;
        if (b.p[2] <= R) {
          b.p[2] = R;
          onFloor = true;
          b.v[2] = b.v[2] < -45 ? -b.v[2] * BOUNCE : 0;

          // Friction acts at the contact point, not the centre. That torque is
          // what turns a sliding die into a rolling one — the whole reason the
          // motion reads as dice rather than drifting shapes.
          const r = [0, 0, -R];
          const contact = add(b.v, cross(b.w, r));
          const jt = mul([contact[0], contact[1], 0], -FRICTION);
          b.v = add(b.v, jt);
          // Once it is toppling onto a face, contact friction would fight the
          // topple and set up a wobble, so only the linear part still applies.
          if (!b.target) b.w = add(b.w, mul(cross(r, jt), 2.5 / (R * R)));

          const roll = Math.max(0, 1 - 5.5 * DT);   // rolling resistance
          b.v[0] *= roll;
          b.v[1] *= roll;
        }

        const drag = onFloor ? ROLL_DRAG : AIR_SPIN_DRAG;
        b.w = mul(b.w, Math.max(0, 1 - drag * DT));

        // Once it has lost its energy, gravity topples it flat onto a face.
        // In a big handful, collisions keep re-spinning stragglers, so past
        // FORCE_SETTLE any die already on the table commits regardless — one
        // unlucky die shouldn't hold up the whole throw.
        const slow = onFloor && Math.hypot(b.v[0], b.v[1]) < SETTLE_SPEED && len(b.w) < SETTLE_SPIN;
        if ((slow || steps > FORCE_SETTLE) && onFloor && !b.target) {
          b.face = topFace(b.shape, b.qCur);
          b.target = restingQuat(b.shape, b.qCur, b.face);
          b.ramp = 0;
          b.settleAt = steps;
        }
        if (b.target) {
          b.ramp = Math.min(1, b.ramp + 6 * DT);        // ease the topple in
          b.w = add(b.w, mul(turnToward(b.qCur, b.target), STIFF * DT * b.ramp));
          b.w = mul(b.w, Math.max(0, 1 - DAMP * DT));
          b.v = mul(b.v, Math.max(0, 1 - 9 * DT));
        }

        const sp = len(b.w);
        if (sp > MAX_SPIN) b.w = mul(b.w, MAX_SPIN / sp);
        if (sp > 1e-5) b.qCur = qNorm(qMul(qAxis(b.w, Math.min(sp, MAX_SPIN) * DT), b.qCur));
      }

      // dice knock into each other
      for (let i = 0; i < bodies.length; i++) {
        for (let k = i + 1; k < bodies.length; k++) {
          const a = bodies[i], c = bodies[k];
          const d = sub(c.p, a.p);
          const dist = len(d) || 1e-6;
          const minD = R * 1.94;
          if (dist >= minD) continue;
          const n = mul(d, 1 / dist);
          const push = (minD - dist) / 2;
          a.p = sub(a.p, mul(n, push));
          c.p = add(c.p, mul(n, push));
          const rel = dot(sub(c.v, a.v), n);
          if (rel < 0) {
            const j = mul(n, -rel * 0.62);
            a.v = sub(a.v, j);
            c.v = add(c.v, j);
            a.w = add(a.w, mul(cross(n, j), 1.4 / R));
            c.w = sub(c.w, mul(cross(n, j), 1.4 / R));
          }
          a.p[2] = Math.min(zMax, Math.max(a.p[2], R));
          c.p[2] = Math.min(zMax, Math.max(c.p[2], R));
        }
      }

      // Collisions run after the wall check, so re-clamp before recording:
      // a shove must not be able to push a die through the side of the tray.
      for (const b of bodies) {
        b.p[0] = Math.max(-(halfW - R), Math.min(halfW - R, b.p[0]));
        b.p[1] = Math.max(-(halfD - R), Math.min(halfD - R, b.p[1]));
        b.p[2] = Math.max(R, Math.min(zMax, b.p[2]));
        b.frames.push({ p: b.p.slice(), q: b.qCur });
      }

      // Stop when the motion stops being *visible*, measured directly in pixels
      // rather than guessed at from velocity thresholds: how far did the die
      // centre move, plus how far did a point on its rim sweep. Chasing the last
      // fraction of a degree just leaves the tray sitting there looking frozen,
      // and leaves enough residual error that the final alignment twitches.
      const moved = bodies.reduce((worst, b) => {
        const f = b.frames;
        if (f.length < 2) return Infinity;
        const a = f[f.length - 2], c = f[f.length - 1];
        const spin = 2 * Math.acos(Math.min(1, Math.abs(
          a.q[0] * c.q[0] + a.q[1] * c.q[1] + a.q[2] * c.q[2] + a.q[3] * c.q[3])));
        const slide = Math.hypot(c.p[0] - a.p[0], c.p[1] - a.p[1], c.p[2] - a.p[2]);
        return Math.max(worst, slide + R * spin);
      }, 0);
      still = bodies.every(b => b.target) && moved < 0.45 ? still + 1 : 0;
      if (still >= 3) break;
    }

    // Anything still unsettled at the cutoff is eased the rest of the way, so a
    // pathological throw can't leave a die resting on a corner.
    for (const b of bodies) {
      if (!b.target) {
        b.face = topFace(b.shape, b.qCur);
        b.target = restingQuat(b.shape, b.qCur, b.face);
      }
      // Always finish exactly on the resting orientation, so the numeral is
      // never left a few degrees off true. Eased at both ends and scaled to how
      // far there is to go — a linear tail on a large residual reads as a twitch.
      const from = b.qCur;
      const drift = len(turnToward(from, b.target));
      const extra = drift > 3e-4 ? Math.max(4, Math.min(26, Math.round(drift * 34))) : 0;
      for (let k = 1; k <= extra; k++) {
        const t = k / extra;
        b.frames.push({ p: b.p.slice(), q: qSlerp(from, b.target, t * t * (3 - 2 * t)) });
      }
      b.face = topFace(b.shape, b.frames[b.frames.length - 1].q);
    }
    return bodies;
  }

  function prepareAll(dice, R, halfW, halfD, seed0, zMax) {
    const bodies = simulateAll(dice, R, halfW, halfD, seed0, zMax || Infinity);
    return bodies.map((b, i) => {
      // The rolled value goes on whichever face won; the rest get plausible numbers.
      const rand = rng((seed0 + i) ^ 0x9e3779b9);
      const others = [];
      for (let n = 1; n <= b.die.sides; n++) if (n !== b.die.v) others.push(n);
      for (let k = others.length - 1; k > 0; k--) {
        const m = Math.floor(rand() * (k + 1));
        [others[k], others[m]] = [others[m], others[k]];
      }
      const labels = b.shape.faces.map((_, k) => (k === b.face ? b.die.v : others.pop() ?? b.die.v));
      return { die: b.die, shape: b.shape, frames: b.frames, labels, R, settleAt: b.settleAt ?? 0 };
    });
  }

  // ---------------------------------------------------------------- drawing

  const PALETTE = {
    kept:    { face: '#d4ab2b', edge: '#6d5411', text: '#1a1206' },
    dropped: { face: '#5b5145', edge: '#332d25', text: '#151210' },
    crit:    { face: '#5fc46c', edge: '#2b6b33', text: '#08210c' },
    fumble:  { face: '#d9614c', edge: '#75281a', text: '#2a0a05' },
  };
  const paletteFor = d =>
    !d.kept ? PALETTE.dropped
      : d.crit === 'crit' ? PALETTE.crit
      : d.crit === 'fumble' ? PALETTE.fumble
      : PALETTE.kept;

  const LIGHT = norm([-0.35, -0.7, 0.85]);

  function drawDie(ctx, entry, frame, origin) {
    const { shape, labels, R, die } = entry;
    const pal = paletteFor(die);
    const persp = 3.6;

    // centre of the die, and the point on the table directly beneath it
    const c = toView(frame.p);
    const g = toView([frame.p[0], frame.p[1], 0]);
    const cx = origin.x + c[0], cy = origin.y - c[1];
    const height = frame.p[2] - R;                    // clearance above the table

    // contact shadow: tight and dark on the table, soft and faint in the air
    const lift = Math.min(1, height / 150);
    ctx.save();
    ctx.globalAlpha = 0.42 * (1 - lift * 0.75);
    ctx.fillStyle = '#000';
    ctx.beginPath();
    ctx.ellipse(origin.x + g[0], origin.y - g[1], R * (0.78 + lift * 0.5),
                R * (0.78 + lift * 0.5) * CE, 0, 0, 7);
    ctx.fill();
    ctx.restore();

    const pts = shape.verts.map(v => {
      const r = toView(qRot(frame.q, v));
      const p = persp / (persp - r[2]);
      return { x: cx + r[0] * R * p, y: cy - r[1] * R * p };
    });

    const faces = shape.faces
      .map(f => ({ f, n: toView(qRot(frame.q, f.n)), c: toView(qRot(frame.q, f.c)) }))
      .filter(o => o.n[2] > 0.02)
      .sort((a, b) => a.c[2] - b.c[2]);

    for (const { f, n, c: fc } of faces) {
      const shade = 0.62 + 0.40 * Math.max(0, dot(n, LIGHT));
      ctx.beginPath();
      f.idx.forEach((vi, k) => (k ? ctx.lineTo(pts[vi].x, pts[vi].y) : ctx.moveTo(pts[vi].x, pts[vi].y)));
      ctx.closePath();
      ctx.fillStyle = tint(pal.face, shade);
      ctx.fill();
      ctx.lineWidth = Math.max(1, R * 0.045);
      ctx.strokeStyle = pal.edge;
      ctx.lineJoin = 'round';
      ctx.stroke();

      if (n[2] < 0.40) continue;                     // too oblique to read
      const p = persp / (persp - fc[2]);
      ctx.save();
      ctx.translate(cx + fc[0] * R * p, cy - fc[1] * R * p);
      ctx.rotate(textAngle(shape, f, frame.q));
      ctx.fillStyle = pal.text;
      ctx.globalAlpha = Math.min(1, (n[2] - 0.40) / 0.28);
      const fs = R * (die.sides >= 12 ? 0.44 : 0.58) * (0.55 + 0.45 * n[2]);
      ctx.font = `700 ${fs.toFixed(1)}px Cinzel, Georgia, serif`;
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      ctx.fillText(String(labels[shape.faces.indexOf(f)]), 0, 0);
      ctx.restore();
    }
  }

  function tint(hex, k) {
    const n = parseInt(hex.slice(1), 16);
    const c = [(n >> 16) & 255, (n >> 8) & 255, n & 255]
      .map(v => Math.max(0, Math.min(255, Math.round(v * k))));
    return `rgb(${c[0]},${c[1]},${c[2]})`;
  }

  // ---------------------------------------------------------------- runner

  /**
   * dice: [{sides, v, kept, crit}]. Renders into `canvas`, calls onSettled once
   * every die has stopped. Returns { cancel, finish }.
   */
  function play(canvas, dice, onSettled) {
    const dpr = Math.min(window.devicePixelRatio || 1, 2.5);
    const W = canvas.clientWidth, H = canvas.clientHeight;
    canvas.width = Math.round(W * dpr);
    canvas.height = Math.round(H * dpr);
    const ctx = canvas.getContext('2d');
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

    const n = Math.max(1, dice.length);
    // The tray is a real rectangle on the table; its depth is foreshortened by
    // the camera, so convert the pixels we have vertically back into world depth.
    // Depth maps to screen through SE, not CE — getting this wrong puts the far
    // edge of the tray off the top of the canvas and the dice pop into view.
    const { R, halfW, halfD, zMax, origin } = layout(W, H, n);

    const entries = prepareAll(dice, R, halfW, halfD, 0x51ed270b + n * 7919 +
      dice.reduce((a, d, k) => a + d.v * (k + 13) * 131, 0), zMax);
    lastPlay = entries;                     // inspection hook for tests
    const total = Math.max(...entries.map(e => e.frames.length));

    let raf = null, done = false, start = null;
    const draw = (idx) => {
      ctx.clearRect(0, 0, W, H);
      // far dice first, so nearer ones overlap them correctly
      const shown = entries.map(e => e.frames[Math.min(idx, e.frames.length - 1)]);
      entries
        .map((e, i) => ({ e, f: shown[i] }))
        .sort((a, b) => b.f.p[1] - a.f.p[1])
        .forEach(({ e, f }) => drawDie(ctx, e, f, origin));
    };

    const finish = () => {
      if (done) return;
      done = true;
      cancelAnimationFrame(raf);
      draw(total - 1);
      onSettled?.();
    };

    const tick = (ts) => {
      if (start === null) start = ts;
      const idx = Math.floor((ts - start) / (1000 / 60));
      if (idx >= total - 1) return finish();
      draw(idx);
      raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);

    return { cancel: () => { done = true; cancelAnimationFrame(raf); }, finish };
  }

  /** Draw one frame of a prepared throw — used by play() and by tests. */
  function drawFrame(ctx, entries, idx, W, H) {
    const origin = { x: W / 2, y: H * 0.60 };
    ctx.clearRect(0, 0, W, H);
    entries
      .map(e => ({ e, f: e.frames[Math.min(idx, e.frames.length - 1)] }))
      .sort((a, b) => b.f.p[1] - a.f.p[1])
      .forEach(({ e, f }) => drawDie(ctx, e, f, origin));
  }

  /**
   * Tray dimensions for a canvas, solved so nothing can ever be drawn off it.
   * A die's silhouette can reach BULGE*R because of the perspective on its
   * vertices, and the tray's far edge plus the height of the throw both eat
   * into the top of the frame — so the depth is derived from those limits
   * rather than picked and hoped for.
   */
  const BULGE = 1.18;
  const CELL = 2.08;                       // spawn spacing, in die radii

  /** Tray geometry for a given die size. */
  function trayFor(W, H, R) {
    const originY = H * 0.56;
    const head = Math.max(30, Math.min(H * 0.20, 74));   // room for the arc
    const zMax = R + head;
    const topLimit = (originY - R * BULGE - zMax * CE) / SE;
    const botLimit = (H - originY + R * CE - R * BULGE) / SE;
    return {
      halfW: Math.max(R + 4, W / 2 - R * (BULGE - 0.4)),
      halfD: Math.max(R * 1.2, Math.min(topLimit, botLimit)),
      zMax,
      origin: { x: W / 2, y: originY },
    };
  }

  /** How many dice of radius R fit on the tray floor, one layer, none touching. */
  function gridCapacity(tray, R) {
    const cell = R * CELL;
    const cols = Math.max(1, Math.floor((tray.halfW * 2 - 2 * R) / cell) + 1);
    const rows = Math.max(1, Math.floor((tray.halfD * 2 - 2 * R) / cell) + 1);
    return { cols, rows, total: cols * rows };
  }

  /**
   * Tray dimensions for a canvas, solved so nothing can ever be drawn off it
   * and every die has somewhere to start that isn't inside another die.
   * A die's silhouette can reach BULGE*R because of the perspective on its
   * vertices, and the tray's far edge plus the height of the throw both eat
   * into the top of the frame — so the depth is derived from those limits
   * rather than picked and hoped for.
   */
  function layout(W, H, n) {
    const byCount = n <= 2 ? 46 : n <= 4 ? 40 : n <= 9 ? 34 : 28;
    let R = Math.max(12, Math.min(byCount, H * 0.15, 0.34 * Math.sqrt((W * H) / n)));
    let tray = trayFor(W, H, R);
    // Shrink the dice until the whole handful fits side by side.
    for (let k = 0; k < 16 && R > 12.5; k++) {
      if (gridCapacity(tray, R).total >= n) break;
      R = Math.max(12, R * 0.93);
      tray = trayFor(W, H, R);
    }
    return { R, ...tray };
  }

  return { play, SHAPE_FOR, get _last() { return lastPlay; }, _drawFrame: drawFrame,
           _layout: layout,
           _solid: solid, _qRot: qRot, _topFace: topFace,
           _prepareAll: prepareAll, _textAngle: textAngle, _toView: toView };
})();
