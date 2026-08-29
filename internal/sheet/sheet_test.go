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
			if len(mine) != len(c.Derived) {
				t.Errorf("key count: go %d, python %d", len(mine), len(c.Derived))
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
