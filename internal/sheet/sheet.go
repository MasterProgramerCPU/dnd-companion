// Package sheet holds the 5e character sheet model and the derived numbers.
//
// Sheets are carried as free-form maps rather than structs: clients patch
// arbitrary fields, and the JavaScript on the other end is the schema. Only the
// derived stats are typed, because those are computed here so the DM's
// dashboard and the player's phone can never disagree.
package sheet

import "fmt"

// Abilities in the order the sheet displays them.
var Abilities = []string{"str", "dex", "con", "int", "wis", "cha"}

// Skills maps each of the 18 skills to the ability that governs it.
var Skills = map[string]string{
	"acrobatics": "dex", "animal_handling": "wis", "arcana": "int", "athletics": "str",
	"deception": "cha", "history": "int", "insight": "wis", "intimidation": "cha",
	"investigation": "int", "medicine": "wis", "nature": "int", "perception": "wis",
	"performance": "cha", "persuasion": "cha", "religion": "int",
	"sleight_of_hand": "dex", "stealth": "dex", "survival": "wis",
}

// floorDiv divides rounding toward negative infinity.
//
// This is not the same as Go's `/`, which truncates toward zero, and the
// difference is load-bearing: a Charisma of 3 must give -4, not -3. Every
// modifier on every sheet depends on getting this right.
func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// AbilityMod is the 5e modifier for an ability score.
func AbilityMod(score int) int { return floorDiv(score-10, 2) }

// ProfBonus is the proficiency bonus at a given character level.
func ProfBonus(level int) int {
	if level < 1 {
		level = 1
	}
	return 2 + (level-1)/4
}

// ProfBonusForCR is the proficiency bonus of a monster at a challenge rating.
// A monster's bonus comes from its CR, not from any level it does not have, and
// every CR below 1 — the fractional ones — sits at +2 like CR 1 itself.
func ProfBonusForCR(cr float64) int {
	if cr < 1 {
		cr = 1
	}
	if cr > 30 {
		cr = 30
	}
	return 2 + int(cr-1)/4
}

// Derived is every number computed from the sheet rather than typed into it.
type Derived struct {
	Mods                 map[string]int `json:"mods"`
	ProfBonus            int            `json:"prof_bonus"`
	Saves                map[string]int `json:"saves"`
	SkillBonuses         map[string]int `json:"skills"`
	Initiative           int            `json:"initiative"`
	PassivePerception    int            `json:"passive_perception"`
	PassiveInvestigation int            `json:"passive_investigation"`
	PassiveInsight       int            `json:"passive_insight"`
	SpellSaveDC          *int           `json:"spell_save_dc"`
	SpellAttack          *int           `json:"spell_attack"`
}

// validAbility reports whether a sheet named a real ability. Sheets are written
// by clients, so an unrecognised name always falls back rather than deriving
// nonsense from a key that isn't there.
func validAbility(v any) (string, bool) {
	a, _ := v.(string)
	for _, known := range Abilities {
		if a == known {
			return a, true
		}
	}
	return "", false
}

// InitiativeAbility is the ability an initiative roll is based on. It is Dex
// for almost everyone, but some classes and homebrew key it off something else
// (an Intelligence-based investigator, a Wisdom-based monk), so the sheet may
// name a different one. An unset or unrecognised value means Dex.
func InitiativeAbility(s map[string]any) string {
	if a, ok := validAbility(s["initiative_ability"]); ok {
		return a
	}
	return "dex"
}

// SkillAbility is the ability a skill is rolled with. Each skill has a standard
// one, but a character may be allowed to use another — Wisdom for Nature when
// the knowledge came from living in it rather than studying it. This is the
// Dungeon Master's Guide's own variant, so the sheet can say so per skill.
func SkillAbility(s map[string]any, skill string) string {
	if a, ok := validAbility(subMap(s, "skill_ability")[skill]); ok {
		return a
	}
	return Skills[skill]
}

// PassiveSkills are the three passive scores the sheet reports.
var PassiveSkills = []string{"perception", "investigation", "insight"}

// Derive computes the sheet's derived stats.
func Derive(s map[string]any) Derived {
	abilities := subMap(s, "abilities")
	mods := make(map[string]int, len(Abilities))
	for _, a := range Abilities {
		mods[a] = AbilityMod(intOr(abilities[a], 10))
	}

	// A stat block states a challenge rating instead of a level, and its
	// proficiency bonus follows from that.
	pb := ProfBonus(intOr(s["level"], 1))
	if cr, ok := s["cr"]; ok && cr != nil {
		pb = ProfBonusForCR(floatOr(cr, 0))
	}

	saveProf := subMap(s, "save_prof")
	saves := make(map[string]int, len(Abilities))
	for _, a := range Abilities {
		saves[a] = mods[a]
		if boolOr(saveProf[a], false) {
			saves[a] += pb
		}
	}

	skillProf := subMap(s, "skill_prof")
	skills := make(map[string]int, len(Skills))
	for skill := range Skills {
		ability := SkillAbility(s, skill)
		rank := intOr(skillProf[skill], 0)
		if rank > 2 {
			rank = 2 // expertise is the ceiling; a higher rank is not a bigger bonus
		}
		if rank < 0 {
			rank = 0
		}
		skills[skill] = mods[ability] + pb*rank
	}

	// A flat adjustment to a passive score that no skill bonus accounts for:
	// the Observant feat's +5, and anything else that raises what a character
	// notices without touching the check they would roll.
	passiveBonus := subMap(s, "passive_bonus")
	passive := func(skill string) int {
		return 10 + skills[skill] + intOr(passiveBonus[skill], 0)
	}

	d := Derived{
		Mods:                 mods,
		ProfBonus:            pb,
		Saves:                saves,
		SkillBonuses:         skills,
		Initiative:           mods[InitiativeAbility(s)] + intOr(s["initiative_bonus"], 0),
		PassivePerception:    passive("perception"),
		PassiveInvestigation: passive("investigation"),
		PassiveInsight:       passive("insight"),
	}

	if ability, _ := subMap(s, "spell")["ability"].(string); ability != "" {
		spellMod := mods[ability] // an unknown ability contributes 0, as in Python
		dc := 8 + pb + spellMod
		atk := pb + spellMod
		d.SpellSaveDC = &dc
		d.SpellAttack = &atk
	}
	return d
}

func subMap(s map[string]any, key string) map[string]any {
	if m, ok := s[key].(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// intOr coerces a JSON value to an int, mirroring Python's int(x or default):
// null, absent and unparseable all fall back.
func intOr(v any, def int) int {
	switch n := v.(type) {
	case nil:
		return def
	case float64:
		return int(n)
	case int:
		return n
	case bool:
		if n {
			return 1
		}
		return 0
	}
	return def
}

// floatOr coerces a JSON number, allowing the fractional challenge ratings
// (1/8, 1/4, 1/2) that the weakest monsters use.
func floatOr(v any, def float64) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return def
}

func boolOr(v any, def bool) bool {
	switch b := v.(type) {
	case nil:
		return def
	case bool:
		return b
	case float64:
		return b != 0
	}
	return def
}

// Default returns a fresh level-1 sheet.
func Default(name, player string) map[string]any {
	if name == "" {
		name = "New Adventurer"
	}
	abilities := map[string]any{}
	saveProf := map[string]any{}
	for _, a := range Abilities {
		abilities[a] = 10
		saveProf[a] = false
	}
	skillProf := map[string]any{}
	for s := range Skills {
		skillProf[s] = 0
	}
	slots := map[string]any{}
	for lvl := 1; lvl <= 9; lvl++ {
		slots[itoa(lvl)] = map[string]any{"total": 0, "used": 0}
	}
	coin := func() map[string]any {
		return map[string]any{"pp": 0, "gp": 0, "ep": 0, "sp": 0, "cp": 0}
	}
	passiveBonus := map[string]any{}
	for _, skill := range PassiveSkills {
		passiveBonus[skill] = 0
	}
	return map[string]any{
		"name": name, "player": player, "klass": "", "subclass": "", "race": "",
		"background": "", "alignment": "", "level": 1, "color": "#c9a227",
		"abilities": abilities, "save_prof": saveProf, "skill_prof": skillProf,
		"ac": 10, "speed": 30, "initiative_bonus": 0,
		"initiative_ability": "dex", "passive_bonus": passiveBonus,
		"skill_ability": map[string]any{},
		"hp":            map[string]any{"current": 10, "max": 10, "temp": 0},
		"hit_dice":      map[string]any{"die": "d8", "total": 1, "used": 0},
		"death_saves":   map[string]any{"successes": 0, "failures": 0},
		"inspiration":   false, "conditions": []any{},
		// label and show_attack let the block stand in for a non-caster's class
		// DC — a Detective's, a Monk's ki — which has a save DC but no attack.
		"spell": map[string]any{"ability": "", "slots": slots, "prepared": "",
			"label": "", "show_attack": true},
		"attacks": []any{}, "resources": []any{},
		"gold": coin(), "features": "", "notes": "",
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// ------------------------------------------------------------------- rests

// The two rests, as the sheet records them.
const (
	ShortRest = "short"
	LongRest  = "long"
)

// Rest applies a short or a long rest to a sheet in place and reports, in the
// table's own words, what it did. Anything it cannot decide on its own it
// leaves alone: spending hit dice to heal on a short rest is the player's
// choice, not an automatic refill, and exhaustion is free text here rather
// than a level this code could count down.
func Rest(s map[string]any, kind string) []string {
	if kind == ShortRest {
		return shortRest(s)
	}
	return longRest(s)
}

// shortRest gives back what an hour buys: resources that recharge on a short
// rest, and the partial trickle of those that only return one or two uses.
func shortRest(s map[string]any) []string {
	var did []string
	for _, raw := range resourceList(s) {
		r, _ := raw.(map[string]any)
		if r == nil {
			continue
		}
		maxUses := max(0, intOr(r["max"], 0))
		used := clampInt(intOr(r["used"], 0), 0, maxUses)
		if used == 0 {
			continue
		}
		switch {
		case str(r["recharge"]) == ShortRest:
			r["used"] = 0
			did = append(did, fmt.Sprintf("%s back to full", name(r)))
		case intOr(r["short_regain"], 0) > 0:
			back := min(intOr(r["short_regain"], 0), used)
			r["used"] = used - back
			did = append(did, fmt.Sprintf("%s +%d", name(r), back))
		}
	}
	return did
}

// longRest is the full night: hit points, spell slots, half the hit dice, and
// every resource that recharges on either kind of rest.
func longRest(s map[string]any) []string {
	var did []string

	hp, _ := s["hp"].(map[string]any)
	if hp == nil {
		hp = map[string]any{}
		s["hp"] = hp
	}
	maxHP := max(0, intOr(hp["max"], 0))
	if intOr(hp["current"], 0) != maxHP {
		did = append(did, fmt.Sprintf("HP to %d", maxHP))
	}
	hp["current"] = maxHP
	// Temporary hit points do not survive the night.
	hp["temp"] = 0
	s["death_saves"] = map[string]any{"successes": 0, "failures": 0}

	// Half the total hit dice come back, rounded down, but never none at all.
	if hd, ok := s["hit_dice"].(map[string]any); ok {
		total := max(0, intOr(hd["total"], 0))
		used := clampInt(intOr(hd["used"], 0), 0, total)
		if used > 0 {
			back := min(max(1, total/2), used)
			hd["used"] = used - back
			did = append(did, fmt.Sprintf("%d hit %s", back, plural(back, "die", "dice")))
		}
	}

	if spell, ok := s["spell"].(map[string]any); ok {
		if slots, ok := spell["slots"].(map[string]any); ok {
			restored := 0
			for _, raw := range slots {
				slot, _ := raw.(map[string]any)
				if slot == nil {
					continue
				}
				restored += clampInt(intOr(slot["used"], 0), 0, max(0, intOr(slot["total"], 0)))
				slot["used"] = 0
			}
			if restored > 0 {
				did = append(did, fmt.Sprintf("%d spell %s", restored, plural(restored, "slot", "slots")))
			}
		}
	}

	// A long rest also covers everything a short rest would have given back.
	for _, raw := range resourceList(s) {
		r, _ := raw.(map[string]any)
		if r == nil {
			continue
		}
		recharge := str(r["recharge"])
		if recharge != ShortRest && recharge != LongRest {
			continue // comes back some other way; not ours to reset
		}
		if clampInt(intOr(r["used"], 0), 0, max(0, intOr(r["max"], 0))) == 0 {
			continue
		}
		r["used"] = 0
		did = append(did, name(r))
	}
	return did
}

func resourceList(s map[string]any) []any {
	list, _ := s["resources"].([]any)
	return list
}

func name(r map[string]any) string {
	if n := str(r["name"]); n != "" {
		return n
	}
	return "Resource"
}

func str(v any) string { s, _ := v.(string); return s }

func clampInt(v, lo, hi int) int { return min(max(v, lo), hi) }

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// ------------------------------------------------------------- stat blocks

// Monster returns a blank stat block.
//
// A stat block is the same free-form map a character sheet is, and derives
// through the same code, because a goblin's Dexterity save is worked out
// exactly like a player's. What it leaves out is everything that only a player
// has: death saves, hit dice, spell slots, inspiration, coin, a pack.
//
// It carries a challenge rating where a sheet carries a level, and one thing a
// sheet has no use for: an HP formula, so a batch of six goblins can each roll
// their own hit points instead of sharing one number.
func Monster(name string) map[string]any {
	if name == "" {
		name = "New Creature"
	}
	abilities := map[string]any{}
	saveProf := map[string]any{}
	for _, a := range Abilities {
		abilities[a] = 10
		saveProf[a] = false
	}
	skillProf := map[string]any{}
	for s := range Skills {
		skillProf[s] = 0
	}
	return map[string]any{
		"name": name, "kind": "", "cr": 0.0,
		"abilities": abilities, "save_prof": saveProf, "skill_prof": skillProf,
		"skill_ability": map[string]any{},
		"ac":            12, "speed": 30,
		"hp_max": 10, "hp_formula": "",
		"initiative_bonus": 0, "initiative_ability": "dex",
		"passive_bonus": map[string]any{},
		"attacks":       []any{},
		// Whether players see it as ??? when it is dropped into the order.
		"hidden":   false,
		"features": "", "notes": "",
	}
}

// CRLabel renders a challenge rating the way a stat block prints it.
func CRLabel(cr float64) string {
	switch {
	case cr <= 0:
		return "0"
	case cr < 0.2:
		return "1/8"
	case cr < 0.3:
		return "1/4"
	case cr < 0.6:
		return "1/2"
	}
	if cr == float64(int(cr)) {
		return fmt.Sprintf("%d", int(cr))
	}
	return fmt.Sprintf("%g", cr)
}
