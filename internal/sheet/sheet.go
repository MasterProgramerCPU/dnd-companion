// Package sheet holds the 5e character sheet model and the derived numbers.
//
// Sheets are carried as free-form maps rather than structs: clients patch
// arbitrary fields, and the JavaScript on the other end is the schema. Only the
// derived stats are typed, because those are computed here so the DM's
// dashboard and the player's phone can never disagree.
package sheet

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

// Derived is every number computed from the sheet rather than typed into it.
type Derived struct {
	Mods                 map[string]int `json:"mods"`
	ProfBonus            int            `json:"prof_bonus"`
	Saves                map[string]int `json:"saves"`
	SkillBonuses         map[string]int `json:"skills"`
	Initiative           int            `json:"initiative"`
	PassivePerception    int            `json:"passive_perception"`
	PassiveInvestigation int            `json:"passive_investigation"`
	SpellSaveDC          *int           `json:"spell_save_dc"`
	SpellAttack          *int           `json:"spell_attack"`
}

// Derive computes the sheet's derived stats.
func Derive(s map[string]any) Derived {
	abilities := subMap(s, "abilities")
	mods := make(map[string]int, len(Abilities))
	for _, a := range Abilities {
		mods[a] = AbilityMod(intOr(abilities[a], 10))
	}

	pb := ProfBonus(intOr(s["level"], 1))

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
	for skill, ability := range Skills {
		rank := intOr(skillProf[skill], 0)
		if rank > 2 {
			rank = 2 // expertise is the ceiling; a higher rank is not a bigger bonus
		}
		if rank < 0 {
			rank = 0
		}
		skills[skill] = mods[ability] + pb*rank
	}

	d := Derived{
		Mods:                 mods,
		ProfBonus:            pb,
		Saves:                saves,
		SkillBonuses:         skills,
		Initiative:           mods["dex"] + intOr(s["initiative_bonus"], 0),
		PassivePerception:    10 + skills["perception"],
		PassiveInvestigation: 10 + skills["investigation"],
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
	return map[string]any{
		"name": name, "player": player, "klass": "", "subclass": "", "race": "",
		"background": "", "alignment": "", "level": 1, "color": "#c9a227",
		"abilities": abilities, "save_prof": saveProf, "skill_prof": skillProf,
		"ac": 10, "speed": 30, "initiative_bonus": 0,
		"hp":          map[string]any{"current": 10, "max": 10, "temp": 0},
		"hit_dice":    map[string]any{"die": "d8", "total": 1, "used": 0},
		"death_saves": map[string]any{"successes": 0, "failures": 0},
		"inspiration": false, "conditions": []any{},
		"spell":   map[string]any{"ability": "", "slots": slots, "prepared": ""},
		"attacks": []any{}, "gold": coin(), "features": "", "notes": "",
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
