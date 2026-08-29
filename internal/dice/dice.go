// Package dice parses and rolls 5e dice expressions.
//
// Supports expressions like:
//
//	1d20+5          2d6+1d4-1       4d6kh3      2d20kl1
//	1d8!            1d10r1          8d6min2     d%
//
// Modifiers on a dice term:
//
//	kh<n> keep highest n      kl<n> keep lowest n
//	dh<n> drop highest n      dl<n> drop lowest n
//	r<n>  reroll (once) any die <= n
//	!     exploding: a die at max value rolls again and adds
//	min<n>/max<n>  clamp each die
package dice

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	MaxDice       = 200
	MaxSides      = 1000
	MaxExplosions = 50
)

// Error is returned for anything the parser refuses.
type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

func errf(format string, args ...any) error { return &Error{fmt.Sprintf(format, args...)} }

// IsError reports whether err came from this package's validation.
func IsError(err error) bool {
	var e *Error
	return errors.As(err, &e)
}

var (
	termRe = regexp.MustCompile(
		`^([+-])?\s*(?:(\d*)[dD](\d+|%)((?:(?:kh|kl|dh|dl|ro|r|min|max)\d+|!)*)|(\d+))`)
	modRe    = regexp.MustCompile(`(?i)(kh|kl|dh|dl|ro|r|min|max)(\d+)|(!)`)
	bareRe   = regexp.MustCompile(`^[+-]?\d+$`)
	keepDrop = map[string]bool{"kh": true, "kl": true, "dh": true, "dl": true}
)

// Die is one thrown die and what happened to it.
type Die struct {
	Value    int
	Kept     bool
	Exploded bool
	Rerolled bool
}

func (d Die) render() string {
	s := strconv.Itoa(d.Value)
	if d.Exploded {
		s += "!"
	}
	if d.Rerolled {
		s += "↻"
	}
	if !d.Kept {
		return "~~" + s + "~~"
	}
	return s
}

// Mod is one modifier. These are kept in an ordered slice rather than a map
// because the term's printed label is built by walking them in the order they
// were written, and Go map iteration order is deliberately random.
type Mod struct {
	Name string // kh kl dh dl ro r min max explode
	Num  int
}

type Mods []Mod

func (m Mods) get(name string) (int, bool) {
	for _, mod := range m {
		if mod.Name == name {
			return mod.Num, true
		}
	}
	return 0, false
}

func (m Mods) has(name string) bool { _, ok := m.get(name); return ok }

func (m Mods) hasKeepDrop() bool {
	for _, mod := range m {
		if keepDrop[mod.Name] {
			return true
		}
	}
	return false
}

// Term is one evaluated term of the expression.
type Term struct {
	Sign  int
	Kind  string // "dice" | "flat"
	Text  string
	Sides int
	Dice  []Die
	Value int
}

func (t Term) render() string {
	if t.Kind == "flat" {
		return strconv.Itoa(t.Value)
	}
	parts := make([]string, len(t.Dice))
	for i, d := range t.Dice {
		parts[i] = d.render()
	}
	return fmt.Sprintf("%s[%s]", t.Text, strings.Join(parts, ", "))
}

// Result is a completed roll.
type Result struct {
	Formula string
	Total   int
	Detail  string
	Terms   []Term
}

// Spec is one parsed term, before any dice are thrown.
type Spec struct {
	Sign  int
	Kind  string
	Count int
	Sides int
	Mods  Mods
	Value int // flat terms only
}

// Drawer supplies raw die values: n dice of the given size. It is an interface
// rather than a func so a draw can also carry per-term state.
type Drawer interface {
	Draw(sides, n int) ([]int, error)
}

// DrawFunc adapts a plain function to Drawer.
type DrawFunc func(sides, n int) ([]int, error)

// Draw implements Drawer.
func (f DrawFunc) Draw(sides, n int) ([]int, error) { return f(sides, n) }

// TermAware is implemented by draws that track which term they are feeding, so
// client-supplied values stay aligned with the terms they were thrown for.
type TermAware interface{ NextTerm() }

func parseMods(raw string) Mods {
	var mods Mods
	for _, m := range modRe.FindAllStringSubmatch(raw, -1) {
		if m[3] != "" {
			mods = append(mods, Mod{Name: "explode"})
			continue
		}
		n, _ := strconv.Atoi(m[2])
		mods = append(mods, Mod{Name: strings.ToLower(m[1]), Num: n})
	}
	return mods
}

// Parse turns a formula into terms without rolling anything.
func Parse(formula string, advantage int) ([]Spec, error) {
	expr := strings.ReplaceAll(strings.TrimSpace(formula), " ", "")
	if expr == "" {
		return nil, errf("empty roll")
	}
	if len(expr) > 200 {
		return nil, errf("expression too long")
	}
	// Bare "+5" / "5" style modifiers are treated as a d20 check.
	if bareRe.MatchString(expr) {
		if strings.HasPrefix(expr, "+") || strings.HasPrefix(expr, "-") {
			expr = "1d20" + expr
		} else {
			expr = "1d20+" + expr
		}
	}

	var specs []Spec
	pos := 0
	appliedAdv := advantage == 0
	for pos < len(expr) {
		m := termRe.FindStringSubmatch(expr[pos:])
		if m == nil || len(m[0]) == 0 {
			return nil, errf("could not parse '%s' at position %d", formula, pos)
		}
		pos += len(m[0])

		sign := 1
		if m[1] == "-" {
			sign = -1
		}
		if m[5] != "" { // flat term
			v, _ := strconv.Atoi(m[5])
			specs = append(specs, Spec{Sign: sign, Kind: "flat", Value: v})
			continue
		}

		sides := 100
		if m[3] != "%" {
			var err error
			if sides, err = strconv.Atoi(m[3]); err != nil {
				return nil, errf("could not parse '%s' at position %d", formula, pos)
			}
		}
		count := 1
		if m[2] != "" {
			count, _ = strconv.Atoi(m[2])
		}
		mods := parseMods(m[4])

		// The first plain d20 term absorbs advantage/disadvantage.
		if !appliedAdv && sides == 20 && count == 1 && !mods.hasKeepDrop() {
			count = 2
			name := "kl"
			if advantage > 0 {
				name = "kh"
			}
			mods = append(mods, Mod{Name: name, Num: 1})
			appliedAdv = true
		}

		if count < 1 || count > MaxDice {
			return nil, errf("dice count must be 1-%d", MaxDice)
		}
		if sides < 2 || sides > MaxSides {
			return nil, errf("die size must be 2-%d", MaxSides)
		}
		specs = append(specs, Spec{Sign: sign, Kind: "dice", Count: count, Sides: sides, Mods: mods})
	}
	return specs, nil
}

// PlanEntry is one group of dice a client has to physically throw.
type PlanEntry struct {
	Sides int `json:"sides"`
	Qty   int `json:"qty"`
}

// Plan lists the dice to throw, in term order.
func Plan(specs []Spec) []PlanEntry {
	out := []PlanEntry{}
	for _, s := range specs {
		if s.Kind == "dice" {
			out = append(out, PlanEntry{Sides: s.Sides, Qty: s.Count})
		}
	}
	return out
}

func rollTerm(count, sides int, mods Mods, draw Drawer) ([]Die, error) {
	if count < 1 || count > MaxDice {
		return nil, errf("dice count must be 1-%d", MaxDice)
	}
	if sides < 2 || sides > MaxSides {
		return nil, errf("die size must be 2-%d", MaxSides)
	}

	raw, err := draw.Draw(sides, count)
	if err != nil {
		return nil, err
	}
	dice := make([]Die, len(raw))
	for i, v := range raw {
		dice[i] = Die{Value: v, Kept: true}
	}

	// Reroll comes before exploding, and "r" wins over "ro" when both appear.
	threshold, ok := mods.get("r")
	if !ok {
		threshold, ok = mods.get("ro")
	}
	if ok {
		for i := range dice {
			if dice[i].Value <= threshold {
				v, err := draw.Draw(sides, 1)
				if err != nil {
					return nil, err
				}
				dice[i] = Die{Value: v[0], Kept: true, Rerolled: true}
			}
		}
	}

	if mods.has("explode") {
		var extra []Die
		budget := MaxExplosions
		var pending int
		for _, d := range dice {
			if d.Value == sides {
				pending++
			}
		}
		for pending > 0 && budget > 0 {
			next := 0
			for i := 0; i < pending; i++ {
				if budget <= 0 {
					break
				}
				budget--
				v, err := draw.Draw(sides, 1)
				if err != nil {
					return nil, err
				}
				d := Die{Value: v[0], Kept: true, Exploded: true}
				extra = append(extra, d)
				if d.Value == sides {
					next++
				}
			}
			pending = next
		}
		dice = append(dice, extra...)
	}

	// Clamps apply after exploding, to every die including the extras.
	if lo, ok := mods.get("min"); ok {
		for i := range dice {
			if dice[i].Value < lo {
				dice[i].Value = lo
			}
		}
	}
	if hi, ok := mods.get("max"); ok {
		for i := range dice {
			if dice[i].Value > hi {
				dice[i].Value = hi
			}
		}
	}

	// Ascending by value, ties keeping their original order — the Python side
	// relies on a stable sort here and the kept/dropped set differs without it.
	order := make([]int, len(dice))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return dice[order[a]].Value < dice[order[b]].Value })

	drop := map[int]bool{}
	switch {
	case mods.has("kh"):
		n, _ := mods.get("kh")
		for _, i := range order[:max(0, len(dice)-n)] {
			drop[i] = true
		}
	case mods.has("kl"):
		n, _ := mods.get("kl")
		if n < len(order) {
			for _, i := range order[n:] {
				drop[i] = true
			}
		}
	case mods.has("dh"):
		n, _ := mods.get("dh")
		if n > 0 && n <= len(order) {
			for _, i := range order[len(dice)-n:] {
				drop[i] = true
			}
		}
	case mods.has("dl"):
		n, _ := mods.get("dl")
		if n > len(order) {
			n = len(order)
		}
		for _, i := range order[:n] {
			drop[i] = true
		}
	}
	for i := range drop {
		dice[i].Kept = false
	}
	return dice, nil
}

// Evaluate builds the result from specs, taking die values from draw.
func Evaluate(specs []Spec, formula string, draw Drawer) (*Result, error) {
	terms := make([]Term, 0, len(specs))
	aware, _ := draw.(TermAware)
	for _, spec := range specs {
		if spec.Kind == "flat" {
			terms = append(terms, Term{Sign: spec.Sign, Kind: "flat",
				Text: strconv.Itoa(spec.Value), Value: spec.Value})
			continue
		}
		dice, err := rollTerm(spec.Count, spec.Sides, spec.Mods, draw)
		if err != nil {
			return nil, err
		}
		if aware != nil {
			aware.NextTerm()
		}
		label := fmt.Sprintf("%dd%d", spec.Count, spec.Sides)
		for _, m := range spec.Mods {
			if m.Name == "explode" {
				label += "!"
			} else {
				label += m.Name + strconv.Itoa(m.Num)
			}
		}
		term := Term{Sign: spec.Sign, Kind: "dice", Text: label, Sides: spec.Sides, Dice: dice}
		for _, d := range dice {
			if d.Kept {
				term.Value += d.Value
			}
		}
		terms = append(terms, term)
	}

	total := 0
	var sb strings.Builder
	for i, t := range terms {
		total += t.Sign * t.Value
		switch {
		case i == 0 && t.Sign > 0:
		case t.Sign < 0:
			sb.WriteString(" - ")
		default:
			sb.WriteString(" + ")
		}
		sb.WriteString(t.render())
	}
	return &Result{Formula: strings.TrimSpace(formula), Total: total,
		Detail: sb.String(), Terms: terms}, nil
}

// D20 returns the single kept d20, when the roll reads as one d20 check.
func (r *Result) D20() *Die {
	for _, term := range r.Terms {
		if term.Kind != "dice" || term.Sides != 20 {
			continue
		}
		var kept []Die
		for _, d := range term.Dice {
			if d.Kept {
				kept = append(kept, d)
			}
		}
		if len(kept) == 1 {
			return &kept[0]
		}
	}
	return nil
}

// Crit reports "crit", "fumble", or "" for the roll's d20, if it has one.
func (r *Result) Crit() string {
	d := r.D20()
	if d == nil {
		return ""
	}
	switch d.Value {
	case 20:
		return "crit"
	case 1:
		return "fumble"
	}
	return ""
}

// Breakdown is the compact structure the client's rolling animation reads.
// Built as maps rather than structs so the key set matches the wire format the
// existing JavaScript already expects, exactly.
func (r *Result) Breakdown() []map[string]any {
	out := make([]map[string]any, 0, len(r.Terms))
	for _, t := range r.Terms {
		if t.Kind == "flat" {
			out = append(out, map[string]any{"sign": t.Sign, "kind": "flat", "value": t.Value})
			continue
		}
		dice := make([]map[string]any, len(t.Dice))
		for i, d := range t.Dice {
			dice[i] = map[string]any{"v": d.Value, "kept": d.Kept, "x": d.Exploded, "r": d.Rerolled}
		}
		out = append(out, map[string]any{
			"sign": t.Sign, "kind": "dice", "label": t.Text, "sides": t.Sides,
			"value": t.Value, "dice": dice,
		})
	}
	return out
}
