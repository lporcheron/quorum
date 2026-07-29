package poll

import "testing"

func opts(ids ...int64) []Option {
	out := make([]Option, len(ids))
	for i, id := range ids {
		out[i] = Option{ID: id, Position: i}
	}
	return out
}

func TestComputeTallies(t *testing.T) {
	// 3 participants, 3 options. Option 20 added after participant 3
	// voted: they have no answer there.
	votes := []VoteRecord{
		{1, 10, VoteYes}, {1, 20, VoteYes}, {1, 30, VoteNo},
		{2, 10, VoteYes}, {2, 20, VoteIfNeedBe}, {2, 30, VoteNo},
		{3, 10, VoteNo}, {3, 30, VoteYes},
	}
	tallies := ComputeTallies(opts(10, 20, 30), 3, votes)

	want := []Tally{
		{OptionID: 10, Yes: 2, IfNeedBe: 0, No: 1, NoAnswer: 0, Winner: true},
		{OptionID: 20, Yes: 1, IfNeedBe: 1, No: 0, NoAnswer: 1, Winner: false},
		{OptionID: 30, Yes: 1, IfNeedBe: 0, No: 2, NoAnswer: 0, Winner: false},
	}
	for i, w := range want {
		if tallies[i] != w {
			t.Errorf("tally[%d] = %+v, want %+v", i, tallies[i], w)
		}
	}
}

func TestIfNeedBeBreaksTies(t *testing.T) {
	votes := []VoteRecord{
		{1, 10, VoteYes}, {1, 20, VoteYes},
		{2, 10, VoteIfNeedBe}, {2, 20, VoteNo},
	}
	tallies := ComputeTallies(opts(10, 20), 2, votes)
	if !tallies[0].Winner || tallies[1].Winner {
		t.Errorf("if-need-be should break the yes tie: %+v", tallies)
	}
}

func TestTiedWinnersShareTheCrown(t *testing.T) {
	votes := []VoteRecord{
		{1, 10, VoteYes}, {1, 20, VoteYes}, {1, 30, VoteNo},
	}
	tallies := ComputeTallies(opts(10, 20, 30), 1, votes)
	if !tallies[0].Winner || !tallies[1].Winner || tallies[2].Winner {
		t.Errorf("expected options 10 and 20 tied winners: %+v", tallies)
	}
}

func TestNoVotesNoWinner(t *testing.T) {
	tallies := ComputeTallies(opts(10, 20), 0, nil)
	for _, ta := range tallies {
		if ta.Winner {
			t.Errorf("winner with zero votes: %+v", ta)
		}
	}
	// Only explicit "no" votes: still no winner.
	tallies = ComputeTallies(opts(10, 20), 1, []VoteRecord{{1, 10, VoteNo}, {1, 20, VoteNo}})
	for _, ta := range tallies {
		if ta.Winner {
			t.Errorf("winner with only no votes: %+v", ta)
		}
	}
}
