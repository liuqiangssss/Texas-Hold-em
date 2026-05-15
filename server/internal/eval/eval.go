// Package eval provides 7-card poker hand evaluation for Texas Hold'em.
//
// Cards are 2-byte strings ("As", "Td", "2c"). The evaluator returns a Score
// that orders any two hands by strength via simple integer comparison, plus
// the best 5-card combination for display.
package eval

import "sort"

// Category is the broad hand class (HighCard ... StraightFlush). RoyalFlush
// collapses into StraightFlush with TopRank = Ace.
type Category int

const (
	HighCard Category = iota
	OnePair
	TwoPair
	ThreeOfAKind
	Straight
	Flush
	FullHouse
	FourOfAKind
	StraightFlush
)

func (c Category) String() string {
	switch c {
	case HighCard:
		return "HighCard"
	case OnePair:
		return "OnePair"
	case TwoPair:
		return "TwoPair"
	case ThreeOfAKind:
		return "ThreeOfAKind"
	case Straight:
		return "Straight"
	case Flush:
		return "Flush"
	case FullHouse:
		return "FullHouse"
	case FourOfAKind:
		return "FourOfAKind"
	case StraightFlush:
		return "StraightFlush"
	}
	return "?"
}

// Score is a packed integer (Category in the high bits, kickers in the low
// bits) such that a > b iff hand a beats hand b. Equal scores tie.
type Score uint64

// Result is the outcome of evaluating a 7-card holding.
type Result struct {
	Score    Score
	Category Category
	Best5    []string
}

// Compare returns -1 / 0 / +1 based on Score.
func Compare(a, b Score) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// Evaluate7 picks the strongest 5-card hand from `cards` (length 5, 6, or 7).
// It panics for invalid input — callers are expected to feed validated card
// strings (rank in 2-9TJQKA, suit in shdc).
func Evaluate7(cards []string) Result {
	if len(cards) < 5 || len(cards) > 7 {
		panic("eval: need 5..7 cards")
	}
	parsed := make([]card, len(cards))
	for i, c := range cards {
		parsed[i] = parseCard(c)
	}
	return evaluate(parsed)
}

// ---- internals ----

type card struct {
	rank int    // 2..14
	suit byte   // 's','h','d','c'
	str  string // original 2-byte code
}

func parseCard(c string) card {
	if len(c) != 2 {
		panic("eval: invalid card " + c)
	}
	r := rankValue(c[0])
	if r == 0 {
		panic("eval: bad rank " + c)
	}
	switch c[1] {
	case 's', 'h', 'd', 'c':
	default:
		panic("eval: bad suit " + c)
	}
	return card{rank: r, suit: c[1], str: c}
}

func rankValue(b byte) int {
	switch b {
	case '2':
		return 2
	case '3':
		return 3
	case '4':
		return 4
	case '5':
		return 5
	case '6':
		return 6
	case '7':
		return 7
	case '8':
		return 8
	case '9':
		return 9
	case 'T':
		return 10
	case 'J':
		return 11
	case 'Q':
		return 12
	case 'K':
		return 13
	case 'A':
		return 14
	}
	return 0
}

// evaluate looks at all C(n,5) 5-card subsets and keeps the highest Score.
// For 7 cards that's 21 evaluations, plenty fast for MVP.
func evaluate(cards []card) Result {
	n := len(cards)
	bestScore := Score(0)
	var bestHand [5]card
	tmp := make([]card, 5)
	idx := []int{0, 1, 2, 3, 4}
	for {
		for i := 0; i < 5; i++ {
			tmp[i] = cards[idx[i]]
		}
		sc, _ := score5(tmp)
		if sc > bestScore {
			bestScore = sc
			copy(bestHand[:], tmp)
		}
		// next combination (lex order)
		i := 4
		for i >= 0 && idx[i] == n-5+i {
			i--
		}
		if i < 0 {
			break
		}
		idx[i]++
		for j := i + 1; j < 5; j++ {
			idx[j] = idx[j-1] + 1
		}
	}

	cat := categoryFromScore(bestScore)
	best := make([]string, 5)
	// Order best5 by descending rank, suits arbitrary, for display.
	tmp2 := append([]card(nil), bestHand[:]...)
	sort.Slice(tmp2, func(i, j int) bool { return tmp2[i].rank > tmp2[j].rank })
	for i, c := range tmp2 {
		best[i] = c.str
	}
	return Result{Score: bestScore, Category: cat, Best5: best}
}

func categoryFromScore(s Score) Category {
	return Category(s >> 60)
}

// score5 returns the (Score, Category) of an exact 5-card hand. The packing
// is: bits 60..63 = Category, bits 0..59 = up to 5 kickers (4 bits each is
// not enough — ranks go to 14 = 0xE — so we use 8 bits per kicker).
func score5(h [5]card) (Score, Category) {
	ranks := make([]int, 5)
	for i, c := range h {
		ranks[i] = c.rank
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ranks)))

	flush := h[0].suit == h[1].suit && h[1].suit == h[2].suit && h[2].suit == h[3].suit && h[3].suit == h[4].suit
	straightHigh := isStraight(ranks)

	// Count rank multiplicities.
	counts := map[int]int{}
	for _, r := range ranks {
		counts[r]++
	}
	type rc struct{ rank, count int }
	groups := make([]rc, 0, len(counts))
	for r, c := range counts {
		groups = append(groups, rc{r, c})
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].count != groups[j].count {
			return groups[i].count > groups[j].count
		}
		return groups[i].rank > groups[j].rank
	})

	switch {
	case flush && straightHigh > 0:
		return pack(StraightFlush, []int{straightHigh}), StraightFlush
	case groups[0].count == 4:
		// FourOfAKind: quads rank, then kicker
		return pack(FourOfAKind, []int{groups[0].rank, groups[1].rank}), FourOfAKind
	case groups[0].count == 3 && len(groups) >= 2 && groups[1].count >= 2:
		return pack(FullHouse, []int{groups[0].rank, groups[1].rank}), FullHouse
	case flush:
		return pack(Flush, ranks), Flush
	case straightHigh > 0:
		return pack(Straight, []int{straightHigh}), Straight
	case groups[0].count == 3:
		// trips + 2 kickers
		k := []int{groups[0].rank}
		for _, g := range groups[1:] {
			k = append(k, g.rank)
		}
		return pack(ThreeOfAKind, k), ThreeOfAKind
	case groups[0].count == 2 && len(groups) >= 2 && groups[1].count == 2:
		// two pair + 1 kicker
		return pack(TwoPair, []int{groups[0].rank, groups[1].rank, groups[2].rank}), TwoPair
	case groups[0].count == 2:
		k := []int{groups[0].rank}
		for _, g := range groups[1:] {
			k = append(k, g.rank)
		}
		return pack(OnePair, k), OnePair
	default:
		return pack(HighCard, ranks), HighCard
	}
}

// isStraight returns the high card of the straight (5..14), or 0 if none.
// `ranks` must be sorted desc and length 5. Recognizes A-2-3-4-5 wheel.
func isStraight(ranks []int) int {
	// Reject duplicates.
	for i := 1; i < 5; i++ {
		if ranks[i] == ranks[i-1] {
			return 0
		}
	}
	if ranks[0]-ranks[4] == 4 {
		return ranks[0]
	}
	// Wheel: A,5,4,3,2
	if ranks[0] == 14 && ranks[1] == 5 && ranks[2] == 4 && ranks[3] == 3 && ranks[4] == 2 {
		return 5
	}
	return 0
}

// pack writes Category in the top nibble and up to 5 kickers in the lower
// 40 bits (8 bits each). Kickers should already be ordered most-significant-first.
func pack(cat Category, kickers []int) Score {
	var s Score = Score(cat) << 60
	for i := 0; i < 5; i++ {
		var k int
		if i < len(kickers) {
			k = kickers[i]
		}
		s |= Score(k&0xff) << (uint(4-i) * 8)
	}
	return s
}
