package eval

import "sort"

// Pot represents a single (main or side) pot at showdown.
//
// `Eligible` lists the seats whose contributions cap >= the level of this pot
// AND who have not folded — i.e. the seats allowed to win this pot. Folded
// seats still contribute money (they were forced to during betting), but they
// can never be eligible.
type Pot struct {
	Amount   int
	Eligible []int
}

// BuildPots groups per-seat total contributions for the hand into a sequence
// of (main, side1, side2 ...) pots according to standard poker rules:
//
//   - Find the smallest non-zero contribution among non-folded seats: m.
//   - Form a pot taking min(commit_i, m) from EVERY seat (folded included).
//   - Eligibility = non-folded seats whose original commit >= m at this layer.
//   - Subtract m from every seat's remaining commit, drop seats at 0,
//     repeat with the next smallest among remaining non-folded.
//
// `contributions` and `folded` are indexed by seat id (typically 0..5). Seats
// with zero contribution are ignored; folded seats with positive contribution
// keep contributing dead money to layers up to their cap.
func BuildPots(contributions map[int]int, folded map[int]bool) []Pot {
	if len(contributions) == 0 {
		return nil
	}
	remaining := make(map[int]int, len(contributions))
	for s, v := range contributions {
		if v > 0 {
			remaining[s] = v
		}
	}

	pots := make([]Pot, 0, 4)
	for {
		// pick smallest non-zero contribution among non-folded seats
		level := 0
		for s, v := range remaining {
			if folded[s] {
				continue
			}
			if v > 0 && (level == 0 || v < level) {
				level = v
			}
		}
		if level == 0 {
			// no eligible seats left to form pots — any leftover is dead
			// money carried by folded seats; merge it into the last pot if
			// any, otherwise drop it (shouldn't happen in normal hands).
			if len(pots) > 0 {
				dead := 0
				for _, v := range remaining {
					dead += v
				}
				if dead > 0 {
					pots[len(pots)-1].Amount += dead
				}
			}
			break
		}

		// build the pot at this level
		amount := 0
		eligible := make([]int, 0, 6)
		for s, v := range remaining {
			take := v
			if take > level {
				take = level
			}
			if take <= 0 {
				continue
			}
			amount += take
			remaining[s] = v - take
			if !folded[s] && v >= level {
				eligible = append(eligible, s)
			}
		}
		sort.Ints(eligible)
		pots = append(pots, Pot{Amount: amount, Eligible: eligible})

		// drop fully-consumed seats
		for s, v := range remaining {
			if v <= 0 {
				delete(remaining, s)
			}
		}
		if len(remaining) == 0 {
			break
		}
	}
	return pots
}
