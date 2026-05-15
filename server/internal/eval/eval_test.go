package eval

import "testing"

func mustEval(t *testing.T, cards ...string) Result {
	t.Helper()
	return Evaluate7(cards)
}

func TestCategoryDetection(t *testing.T) {
	cases := []struct {
		name string
		hand []string
		want Category
	}{
		{"royal flush", []string{"As", "Ks", "Qs", "Js", "Ts", "2c", "3d"}, StraightFlush},
		{"straight flush", []string{"9h", "8h", "7h", "6h", "5h", "Kc", "Ad"}, StraightFlush},
		{"four of a kind", []string{"As", "Ah", "Ad", "Ac", "Ks", "Qd", "2c"}, FourOfAKind},
		{"full house", []string{"As", "Ah", "Ad", "Ks", "Kh", "2c", "3d"}, FullHouse},
		{"flush", []string{"As", "Js", "9s", "5s", "2s", "Kc", "Qd"}, Flush},
		{"straight", []string{"9h", "8d", "7s", "6c", "5h", "Ad", "2c"}, Straight},
		{"wheel straight", []string{"As", "2d", "3s", "4c", "5h", "Kc", "Qd"}, Straight},
		{"three of a kind", []string{"As", "Ah", "Ad", "Ks", "Qd", "9c", "2h"}, ThreeOfAKind},
		{"two pair", []string{"As", "Ah", "Ks", "Kd", "Qc", "9d", "2h"}, TwoPair},
		{"one pair", []string{"As", "Ah", "Kd", "Qc", "9d", "2h", "3s"}, OnePair},
		{"high card", []string{"As", "Kh", "Qd", "9c", "7s", "5h", "3d"}, HighCard},
	}
	for _, tc := range cases {
		got := mustEval(t, tc.hand...)
		if got.Category != tc.want {
			t.Errorf("%s: got %s, want %s (score=%x)", tc.name, got.Category, tc.want, uint64(got.Score))
		}
	}
}

func TestKickerOrdering(t *testing.T) {
	// Pair of aces, K kicker beats Q kicker.
	a := mustEval(t, "As", "Ah", "Kd", "9c", "7s", "3h", "2d")
	b := mustEval(t, "As", "Ah", "Qd", "9c", "7s", "3h", "2d")
	if Compare(a.Score, b.Score) <= 0 {
		t.Errorf("expected K-kicker to beat Q-kicker, a=%x b=%x", uint64(a.Score), uint64(b.Score))
	}
}

func TestStraightOverPair(t *testing.T) {
	straight := mustEval(t, "9h", "8d", "7s", "6c", "5h", "Ad", "Kc")
	pair := mustEval(t, "Ah", "As", "Kd", "Qc", "Js", "8h", "3d")
	if Compare(straight.Score, pair.Score) <= 0 {
		t.Errorf("straight should beat pair")
	}
}

func TestTieScores(t *testing.T) {
	a := mustEval(t, "As", "Ah", "Kd", "Qc", "Jh", "9s", "2c")
	b := mustEval(t, "Ad", "Ac", "Kh", "Qs", "Jd", "9c", "2h")
	if Compare(a.Score, b.Score) != 0 {
		t.Errorf("identical hands should tie, got %x vs %x", uint64(a.Score), uint64(b.Score))
	}
}

func TestFullHouseBeatsFlush(t *testing.T) {
	fh := mustEval(t, "As", "Ah", "Ad", "Ks", "Kh", "2c", "3d")
	fl := mustEval(t, "Ks", "Js", "9s", "5s", "2s", "Ah", "Qd")
	if Compare(fh.Score, fl.Score) <= 0 {
		t.Errorf("full house should beat flush")
	}
}
