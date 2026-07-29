package poll

// Tally is the vote count for one option. NoAnswer counts participants
// who never saw the option (it was added after they voted); it is
// distinct from an explicit "no" and excluded from every score.
type Tally struct {
	OptionID int64
	Yes      int
	IfNeedBe int
	No       int
	NoAnswer int
	// Winner marks the best option(s): most "yes", then most
	// "if need be" as tiebreaker. Ties share the crown.
	Winner bool
}

// VoteRecord is one vote as read from storage.
type VoteRecord struct {
	ParticipantID int64
	OptionID      int64
	Value         VoteValue
}

// ComputeTallies counts votes per option and marks the winner(s). An
// option needs at least one positive answer (yes or if-need-be) to win.
func ComputeTallies(options []Option, participantCount int, votes []VoteRecord) []Tally {
	byOption := make(map[int64]*Tally, len(options))
	tallies := make([]Tally, len(options))
	for i, o := range options {
		tallies[i] = Tally{OptionID: o.ID}
		byOption[o.ID] = &tallies[i]
	}

	for _, v := range votes {
		t, ok := byOption[v.OptionID]
		if !ok {
			continue
		}
		switch v.Value {
		case VoteYes:
			t.Yes++
		case VoteIfNeedBe:
			t.IfNeedBe++
		case VoteNo:
			t.No++
		}
	}

	bestYes, bestIfNeedBe := 0, 0
	for i := range tallies {
		t := &tallies[i]
		t.NoAnswer = participantCount - t.Yes - t.IfNeedBe - t.No
		if t.Yes > bestYes || (t.Yes == bestYes && t.IfNeedBe > bestIfNeedBe) {
			bestYes, bestIfNeedBe = t.Yes, t.IfNeedBe
		}
	}
	if bestYes+bestIfNeedBe > 0 {
		for i := range tallies {
			t := &tallies[i]
			t.Winner = t.Yes == bestYes && t.IfNeedBe == bestIfNeedBe
		}
	}
	return tallies
}
