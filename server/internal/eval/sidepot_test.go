package eval

import (
	"reflect"
	"testing"
)

func TestSinglePot(t *testing.T) {
	pots := BuildPots(map[int]int{0: 100, 1: 100, 2: 100}, map[int]bool{})
	if len(pots) != 1 {
		t.Fatalf("want 1 pot, got %d (%+v)", len(pots), pots)
	}
	if pots[0].Amount != 300 {
		t.Errorf("amount = %d, want 300", pots[0].Amount)
	}
	if !reflect.DeepEqual(pots[0].Eligible, []int{0, 1, 2}) {
		t.Errorf("eligible = %v", pots[0].Eligible)
	}
}

func TestTwoSidePots(t *testing.T) {
	// Seat 0: 50 (all-in), seat 1: 200, seat 2: 200
	// main pot: 50*3 = 150 (eligible 0,1,2)
	// side pot: 150*2 = 300 (eligible 1,2)
	pots := BuildPots(map[int]int{0: 50, 1: 200, 2: 200}, map[int]bool{})
	if len(pots) != 2 {
		t.Fatalf("want 2 pots, got %d (%+v)", len(pots), pots)
	}
	if pots[0].Amount != 150 || !reflect.DeepEqual(pots[0].Eligible, []int{0, 1, 2}) {
		t.Errorf("main pot wrong: %+v", pots[0])
	}
	if pots[1].Amount != 300 || !reflect.DeepEqual(pots[1].Eligible, []int{1, 2}) {
		t.Errorf("side pot wrong: %+v", pots[1])
	}
}

func TestFoldedDeadMoney(t *testing.T) {
	// Seat 0 folds with 50 in. Seat 1 and 2 see it down with 200 each.
	// Single pot of 50 + 200 + 200 = 450, eligible 1,2
	pots := BuildPots(
		map[int]int{0: 50, 1: 200, 2: 200},
		map[int]bool{0: true},
	)
	if len(pots) != 1 {
		t.Fatalf("want 1 pot, got %d (%+v)", len(pots), pots)
	}
	if pots[0].Amount != 450 {
		t.Errorf("amount = %d, want 450", pots[0].Amount)
	}
	if !reflect.DeepEqual(pots[0].Eligible, []int{1, 2}) {
		t.Errorf("eligible = %v", pots[0].Eligible)
	}
}

func TestThreeWayAllInDifferentStacks(t *testing.T) {
	// Seat 0: 30 all-in, seat 1: 80 all-in, seat 2: 200
	// Pot 1: 30*3 = 90 (0,1,2)
	// Pot 2: 50*2 = 100 (1,2)
	// Pot 3: 120 (2 only — uncalled, but kept as side pot per algorithm)
	pots := BuildPots(map[int]int{0: 30, 1: 80, 2: 200}, map[int]bool{})
	if len(pots) != 3 {
		t.Fatalf("want 3 pots, got %d (%+v)", len(pots), pots)
	}
	if pots[0].Amount != 90 || !reflect.DeepEqual(pots[0].Eligible, []int{0, 1, 2}) {
		t.Errorf("pot 0: %+v", pots[0])
	}
	if pots[1].Amount != 100 || !reflect.DeepEqual(pots[1].Eligible, []int{1, 2}) {
		t.Errorf("pot 1: %+v", pots[1])
	}
	if pots[2].Amount != 120 || !reflect.DeepEqual(pots[2].Eligible, []int{2}) {
		t.Errorf("pot 2: %+v", pots[2])
	}
}
