package dice

import (
	"crypto/rand"
	"math/big"
)

// RNG draws from the operating system's entropy source, as the Python version's
// SystemRandom did.
func RNG() Drawer {
	return DrawFunc(func(sides, n int) ([]int, error) {
		out := make([]int, n)
		for i := range out {
			v, err := rand.Int(rand.Reader, big.NewInt(int64(sides)))
			if err != nil {
				return nil, err
			}
			out[i] = int(v.Int64()) + 1
		}
		return out, nil
	})
}

// Supplied replays dice a client physically threw, term by term.
//
// Extra dice a term turns out to need — explosions and rerolls, which depend on
// what was already rolled — come from the server, since the client had no way
// to know to throw them. Those show in the written breakdown but were never on
// the table; that only affects `!` and `r` formulas.
type Supplied struct {
	buckets [][]int
	term    int
	backing Drawer
}

// NewSupplied wraps values as a term-aware draw.
func NewSupplied(values [][]int) *Supplied {
	buckets := make([][]int, len(values))
	for i, v := range values {
		buckets[i] = append([]int(nil), v...)
	}
	return &Supplied{buckets: buckets, backing: RNG()}
}

// Draw takes what the client reported first, then falls back to the server.
func (s *Supplied) Draw(sides, n int) ([]int, error) {
	out := make([]int, 0, n)
	if s.term < len(s.buckets) {
		bucket := s.buckets[s.term]
		for len(bucket) > 0 && len(out) < n {
			out = append(out, bucket[0])
			bucket = bucket[1:]
		}
		s.buckets[s.term] = bucket
	}
	if len(out) < n {
		extra, err := s.backing.Draw(sides, n-len(out))
		if err != nil {
			return nil, err
		}
		out = append(out, extra...)
	}
	// A d6 reporting a 9 is refused: the phone throws the dice, but the server
	// decides whether what came back was possible.
	for _, v := range out {
		if v < 1 || v > sides {
			return nil, errf("impossible value %d on a d%d", v, sides)
		}
	}
	return out, nil
}

// NextTerm advances to the dice thrown for the following term.
func (s *Supplied) NextTerm() { s.term++ }

// Roll rolls a formula with the server's own RNG.
func Roll(formula string, advantage int) (*Result, error) {
	specs, err := Parse(formula, advantage)
	if err != nil {
		return nil, err
	}
	return Evaluate(specs, formula, RNG())
}
