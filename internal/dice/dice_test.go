package dice

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The same fixed sequence tools/gen_vectors.py draws from, so a Go roll and a
// Python roll of the same formula are comparable without either side having to
// reproduce the other's random number generator.
var seq = []int{7, 1, 20, 3, 6, 6, 2, 19, 5, 4, 8, 20, 1, 12, 6, 9, 3, 17, 2, 10}

func seqDraw() Drawer {
	i := 0
	return DrawFunc(func(sides, n int) ([]int, error) {
		out := make([]int, n)
		for k := 0; k < n; k++ {
			v := seq[i%len(seq)]
			i++
			out[k] = (v-1)%sides + 1
		}
		return out, nil
	})
}

type diceCase struct {
	Formula   string           `json:"formula"`
	Advantage int              `json:"advantage"`
	Plan      []PlanEntry      `json:"plan"`
	Total     int              `json:"total"`
	Detail    string           `json:"detail"`
	Breakdown []map[string]any `json:"breakdown"`
	Crit      *string          `json:"crit"`
	Error     string           `json:"error"`
}

func load(t *testing.T) []diceCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "dice_cases.json"))
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var cases []diceCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("no vectors")
	}
	return cases
}

// roundtrip puts Go values through JSON so numbers compare as float64 on both
// sides, matching how the golden file was decoded.
func roundtrip(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func TestAgainstPythonVectors(t *testing.T) {
	for _, c := range load(t) {
		t.Run(c.Formula+"/adv"+string(rune('0'+c.Advantage+1)), func(t *testing.T) {
			specs, err := Parse(c.Formula, c.Advantage)

			if c.Error != "" {
				if err == nil {
					t.Fatalf("expected rejection %q, got none", c.Error)
				}
				if err.Error() != c.Error {
					t.Errorf("error text\n  go:     %q\n  python: %q", err.Error(), c.Error)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := roundtrip(t, Plan(specs)); !reflect.DeepEqual(got, roundtrip(t, c.Plan)) {
				t.Errorf("plan\n  go:     %v\n  python: %v", got, c.Plan)
			}

			res, err := Evaluate(specs, c.Formula, seqDraw())
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if res.Total != c.Total {
				t.Errorf("total: go %d, python %d", res.Total, c.Total)
			}
			if res.Detail != c.Detail {
				t.Errorf("detail\n  go:     %q\n  python: %q", res.Detail, c.Detail)
			}
			want := ""
			if c.Crit != nil {
				want = *c.Crit
			}
			if res.Crit() != want {
				t.Errorf("crit: go %q, python %q", res.Crit(), want)
			}
			if got := roundtrip(t, res.Breakdown()); !reflect.DeepEqual(got, roundtrip(t, c.Breakdown)) {
				t.Errorf("breakdown\n  go:     %v\n  python: %v", got, c.Breakdown)
			}
		})
	}
}
