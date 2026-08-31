package sheet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type deriveCase struct {
	Name    string         `json:"name"`
	Sheet   map[string]any `json:"sheet"`
	Derived map[string]any `json:"derived"`
}

// derivedSincePython are keys Go adds that the recorded Python vectors predate.
// The vectors still pin every number Python produced; this list is the only way
// a new key gets past that guard, so adding one has to be deliberate.
var derivedSincePython = map[string]bool{
	"passive_insight": true,
}

func TestDeriveAgainstPythonVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "derive_cases.json"))
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var cases []deriveCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("no vectors")
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			got, err := json.Marshal(Derive(c.Sheet))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var mine map[string]any
			if err := json.Unmarshal(got, &mine); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			for key, want := range c.Derived {
				if !reflect.DeepEqual(mine[key], want) {
					t.Errorf("%s:\n  go:     %v\n  python: %v", key, mine[key], want)
				}
			}
			for key := range mine {
				if _, inPython := c.Derived[key]; !inPython && !derivedSincePython[key] {
					t.Errorf("derived key %q is not in the Python vectors and not "+
						"listed in derivedSincePython", key)
				}
			}
		})
	}
}

// The trap that motivated the golden vectors in the first place.
func TestAbilityModFloorsTowardNegativeInfinity(t *testing.T) {
	for _, tc := range []struct{ score, want int }{
		{1, -5}, {2, -4}, {3, -4}, {7, -2}, {8, -1}, {9, -1},
		{10, 0}, {11, 0}, {12, 1}, {20, 5}, {30, 10},
	} {
		if got := AbilityMod(tc.score); got != tc.want {
			t.Errorf("AbilityMod(%d) = %d, want %d", tc.score, got, tc.want)
		}
	}
}

// Default sheets must derive without panicking on any missing key.
func TestDeriveToleratesEmptySheet(t *testing.T) {
	d := Derive(map[string]any{})
	if d.ProfBonus != 2 || d.Mods["str"] != 0 || d.SpellSaveDC != nil {
		t.Errorf("empty sheet derived oddly: %+v", d)
	}
}

// A flat passive bonus (the Observant feat, or anything like it) raises what a
// character notices without touching the check they would roll.
func TestPassiveBonusRaisesOnlyThePassiveScore(t *testing.T) {
	s := map[string]any{
		"level":      10,
		"abilities":  map[string]any{"wis": 16, "int": 18},
		"skill_prof": map[string]any{"perception": 1, "investigation": 2, "insight": 2},
		"passive_bonus": map[string]any{
			"perception": 5, "investigation": 5,
		},
	}
	d := Derive(s)
	if d.SkillBonuses["perception"] != 7 || d.SkillBonuses["investigation"] != 12 {
		t.Fatalf("the rolled skill bonus must not move: %+v", d.SkillBonuses)
	}
	for _, tc := range []struct {
		name string
		got  int
		want int
	}{
		{"perception", d.PassivePerception, 22},
		{"investigation", d.PassiveInvestigation, 27},
		{"insight", d.PassiveInsight, 21}, // no bonus given: plain 10 + 11
	} {
		if tc.got != tc.want {
			t.Errorf("passive %s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

func TestInitiativeAbility(t *testing.T) {
	abilities := map[string]any{"dex": 12, "int": 18}
	for _, tc := range []struct {
		ability string
		want    int
	}{
		{"int", 4},   // an Intelligence-based investigator
		{"dex", 1},   // the ordinary case, named explicitly
		{"", 1},      // unset falls back to Dex
		{"bogus", 1}, // so does anything unrecognised
	} {
		s := map[string]any{"abilities": abilities, "initiative_ability": tc.ability}
		if got := Derive(s).Initiative; got != tc.want {
			t.Errorf("initiative_ability=%q gave %+d, want %+d", tc.ability, got, tc.want)
		}
	}

	// The flat bonus still stacks on top of whichever ability was chosen.
	s := map[string]any{"abilities": abilities, "initiative_ability": "int", "initiative_bonus": 2}
	if got := Derive(s).Initiative; got != 6 {
		t.Errorf("int-based initiative with a +2 bonus = %+d, want +6", got)
	}
}

// restSheet is a spent character: hurt, out of slots, down to one case die.
func restSheet() map[string]any {
	return map[string]any{
		"hp":          map[string]any{"current": 12, "max": 73, "temp": 9},
		"death_saves": map[string]any{"successes": 1, "failures": 2},
		"hit_dice":    map[string]any{"die": "d8", "total": 10, "used": 7},
		"spell": map[string]any{"slots": map[string]any{
			"1": map[string]any{"total": 4, "used": 4},
			"2": map[string]any{"total": 3, "used": 1},
		}},
		"resources": []any{
			map[string]any{"id": "case", "name": "Case Dice", "max": 6, "used": 5,
				"recharge": "long", "short_regain": 1},
			map[string]any{"id": "expr", "name": "The Expression", "max": 1, "used": 1,
				"recharge": "short"},
			map[string]any{"id": "eye", "name": "Read the Room", "max": 4, "used": 4,
				"recharge": "long"},
			map[string]any{"id": "bomb", "name": "Spirit Bomb", "max": 1, "used": 1,
				"recharge": "other"},
		},
	}
}

func resourceUsed(t *testing.T, s map[string]any, id string) int {
	t.Helper()
	for _, raw := range s["resources"].([]any) {
		r := raw.(map[string]any)
		if r["id"] == id {
			return intOr(r["used"], -1)
		}
	}
	t.Fatalf("no resource %q", id)
	return -1
}

// A short rest is an hour, not a night: it returns short-rest resources and the
// partial trickle, and touches nothing else.
func TestShortRest(t *testing.T) {
	s := restSheet()
	Rest(s, ShortRest)

	if got := resourceUsed(t, s, "expr"); got != 0 {
		t.Errorf("a short-rest resource should be full again, used = %d", got)
	}
	if got := resourceUsed(t, s, "case"); got != 4 {
		t.Errorf("short_regain 1 of 5 spent should leave 4 used, got %d", got)
	}
	if got := resourceUsed(t, s, "eye"); got != 4 {
		t.Errorf("a long-rest resource must not return on a short rest, used = %d", got)
	}
	if got := resourceUsed(t, s, "bomb"); got != 1 {
		t.Errorf("a resource that recharges some other way must be left alone, used = %d", got)
	}

	hp := s["hp"].(map[string]any)
	if intOr(hp["current"], 0) != 12 || intOr(hp["temp"], 0) != 9 {
		t.Errorf("a short rest must not heal or strip temp HP: %v", hp)
	}
	if got := intOr(s["hit_dice"].(map[string]any)["used"], 0); got != 7 {
		t.Errorf("spending hit dice is the player's choice, used = %d", got)
	}
	slots := s["spell"].(map[string]any)["slots"].(map[string]any)
	if got := intOr(slots["1"].(map[string]any)["used"], 0); got != 4 {
		t.Errorf("a short rest must not restore spell slots, used = %d", got)
	}
}

func TestLongRest(t *testing.T) {
	s := restSheet()
	Rest(s, LongRest)

	hp := s["hp"].(map[string]any)
	if intOr(hp["current"], 0) != 73 {
		t.Errorf("HP should be full, got %v", hp["current"])
	}
	if intOr(hp["temp"], 0) != 0 {
		t.Errorf("temp HP does not survive the night, got %v", hp["temp"])
	}
	death := s["death_saves"].(map[string]any)
	if intOr(death["successes"], -1) != 0 || intOr(death["failures"], -1) != 0 {
		t.Errorf("death saves should be cleared, got %v", death)
	}

	// Half of ten hit dice is five; seven were spent, so two remain spent.
	if got := intOr(s["hit_dice"].(map[string]any)["used"], -1); got != 2 {
		t.Errorf("hit dice used = %d, want 2", got)
	}

	slots := s["spell"].(map[string]any)["slots"].(map[string]any)
	for lvl, raw := range slots {
		if got := intOr(raw.(map[string]any)["used"], -1); got != 0 {
			t.Errorf("slot level %s still %d used", lvl, got)
		}
	}

	for _, id := range []string{"case", "expr", "eye"} {
		if got := resourceUsed(t, s, id); got != 0 {
			t.Errorf("%s should be full after a long rest, used = %d", id, got)
		}
	}
	if got := resourceUsed(t, s, "bomb"); got != 1 {
		t.Errorf("a resource that recharges some other way survives a long rest, used = %d", got)
	}
}

// Hit dice come back at half the total rounded down, but a rest always gives at
// least one back, which is what makes a one- or two-die character work at all.
func TestLongRestHitDiceAlwaysReturnsAtLeastOne(t *testing.T) {
	for _, tc := range []struct{ total, used, want int }{
		{1, 1, 0},    // half of 1 is 0, so the floor of one applies
		{3, 3, 2},    // half of 3 is 1
		{10, 7, 2},   // half of 10 is 5
		{10, 2, 0},   // fewer spent than the rest gives back
		{20, 20, 10}, //
		{0, 0, 0},    // nothing to give back
	} {
		s := map[string]any{"hit_dice": map[string]any{"total": tc.total, "used": tc.used}}
		Rest(s, LongRest)
		if got := intOr(s["hit_dice"].(map[string]any)["used"], -1); got != tc.want {
			t.Errorf("total %d used %d: left %d used, want %d", tc.total, tc.used, got, tc.want)
		}
	}
}

// Resting a sheet with none of the optional blocks must not panic.
func TestRestToleratesEmptySheet(t *testing.T) {
	for _, kind := range []string{ShortRest, LongRest} {
		s := map[string]any{}
		Rest(s, kind)
		if _, ok := s["hp"].(map[string]any); !ok && kind == LongRest {
			t.Errorf("%s rest left no hp block", kind)
		}
	}
}

// The DMG's variant: a skill rolled with an ability other than its usual one.
// Knowing nature by living in it is Wisdom, not Intelligence.
func TestSkillAbilityOverride(t *testing.T) {
	base := map[string]any{
		"level":      10,
		"abilities":  map[string]any{"int": 10, "wis": 18, "cha": 14},
		"skill_prof": map[string]any{"nature": 1, "arcana": 1},
	}
	if got := Derive(base).SkillBonuses["nature"]; got != 4 {
		t.Fatalf("without an override Nature is Intelligence: got %+d, want +4", got)
	}

	base["skill_ability"] = map[string]any{"nature": "wis"}
	d := Derive(base)
	if got := d.SkillBonuses["nature"]; got != 8 {
		t.Errorf("Nature with Wisdom = %+d, want +8", got)
	}
	if got := d.SkillBonuses["arcana"]; got != 4 {
		t.Errorf("overriding one skill must not move another: arcana %+d, want +4", got)
	}

	// An unrecognised ability falls back to the skill's own, never to zero.
	base["skill_ability"] = map[string]any{"nature": "banana"}
	if got := Derive(base).SkillBonuses["nature"]; got != 4 {
		t.Errorf("a bogus ability should fall back to Intelligence: got %+d", got)
	}
}

// Overriding a skill's ability moves the passive score built on it too.
func TestSkillAbilityOverrideReachesPassives(t *testing.T) {
	s := map[string]any{
		"level":         10,
		"abilities":     map[string]any{"wis": 10, "int": 18},
		"skill_prof":    map[string]any{"perception": 1},
		"skill_ability": map[string]any{"perception": "int"},
	}
	if got := Derive(s).PassivePerception; got != 18 { // 10 + (4 + 4)
		t.Errorf("passive perception = %d, want 18", got)
	}
}

func TestSkillAbilityHelper(t *testing.T) {
	s := map[string]any{"skill_ability": map[string]any{"nature": "wis", "stealth": ""}}
	for _, tc := range []struct{ skill, want string }{
		{"nature", "wis"},    // overridden
		{"stealth", "dex"},   // empty override ignored
		{"athletics", "str"}, // untouched
	} {
		if got := SkillAbility(s, tc.skill); got != tc.want {
			t.Errorf("SkillAbility(%s) = %q, want %q", tc.skill, got, tc.want)
		}
	}
}

// A monster's proficiency bonus comes from its challenge rating. The boundaries
// are the whole point, so they are all here.
func TestProfBonusForCR(t *testing.T) {
	for _, tc := range []struct {
		cr   float64
		want int
	}{
		{0, 2}, {0.125, 2}, {0.25, 2}, {0.5, 2}, // everything below 1 sits at +2
		{1, 2}, {4, 2},
		{5, 3}, {8, 3},
		{9, 4}, {12, 4},
		{13, 5}, {16, 5},
		{17, 6}, {20, 6},
		{21, 7}, {24, 7},
		{25, 8}, {28, 8},
		{29, 9}, {30, 9},
		{99, 9}, // clamped rather than run away
	} {
		if got := ProfBonusForCR(tc.cr); got != tc.want {
			t.Errorf("ProfBonusForCR(%v) = %d, want %d", tc.cr, got, tc.want)
		}
	}
}

// A stat block derives from its CR; a character sheet still derives from level.
func TestDeriveUsesCRWhenPresent(t *testing.T) {
	block := map[string]any{"cr": 9.0, "level": 1, "abilities": map[string]any{"dex": 16}}
	if got := Derive(block).ProfBonus; got != 4 {
		t.Errorf("CR 9 with a stray level gave +%d, want +4", got)
	}

	// CR 0 is a real rating, so its presence must count even though it is zero.
	if got := Derive(map[string]any{"cr": 0.0, "level": 20}).ProfBonus; got != 2 {
		t.Errorf("CR 0 gave +%d, want +2", got)
	}

	// No CR at all: still a character, still derived from level.
	if got := Derive(map[string]any{"level": 20}).ProfBonus; got != 6 {
		t.Errorf("a level 20 character gave +%d, want +6", got)
	}
}

func TestCRLabel(t *testing.T) {
	for _, tc := range []struct {
		cr   float64
		want string
	}{
		{0, "0"}, {0.125, "1/8"}, {0.25, "1/4"}, {0.5, "1/2"},
		{1, "1"}, {5, "5"}, {30, "30"},
	} {
		if got := CRLabel(tc.cr); got != tc.want {
			t.Errorf("CRLabel(%v) = %q, want %q", tc.cr, got, tc.want)
		}
	}
}

// A blank stat block must derive without panicking, and must not carry the
// parts of a sheet that only a player has.
func TestMonsterModel(t *testing.T) {
	m := Monster("")
	if m["name"] != "New Creature" {
		t.Errorf("unnamed creature = %v", m["name"])
	}
	if d := Derive(m); d.ProfBonus != 2 || d.PassivePerception != 10 {
		t.Errorf("blank stat block derived oddly: %+v", d)
	}
	for _, absent := range []string{"death_saves", "hit_dice", "spell", "gold", "level", "resources"} {
		if _, present := m[absent]; present {
			t.Errorf("a stat block should not carry %q", absent)
		}
	}
	for _, needed := range []string{"cr", "hp_max", "hp_formula", "abilities", "attacks", "hidden"} {
		if _, present := m[needed]; !present {
			t.Errorf("a stat block needs %q", needed)
		}
	}
}
