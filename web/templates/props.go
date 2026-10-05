package templates

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/lporcheron/quorum/internal/auth"
	"github.com/lporcheron/quorum/internal/i18n"
	"github.com/lporcheron/quorum/internal/poll"
)

// HomeProps feeds the landing/creation page.
type HomeProps struct {
	Loc       *i18n.Locale
	User      *auth.User
	Timezones []string
	// CanCreate is false when the instance reserves poll creation for
	// signed-in users and the visitor is signed out.
	CanCreate bool
	Error     string // localized message after a failed submission
	// Submitted, when set, echoes a refused form back in full — the
	// calendar picks its options up again, so nothing is lost.
	Submitted *CreateDraft
}

// CreateDraft is a refused creation form, as posted.
type CreateDraft struct {
	Title, Description, Location, VideoURL string
	Kind, Timezone                         string
	HideParticipants, RequireVoterEmail    bool
	AllowComments, NotifyOrganizer         bool
	Options                                []DraftOption
}

// DraftOption is one posted option row; Start and Duration are empty
// for whole days.
type DraftOption struct {
	Date     string `json:"d"`
	Start    string `json:"s,omitempty"`
	Duration string `json:"m,omitempty"`
}

// draft returns the echoed form, or the defaults of a fresh one.
func (p HomeProps) draft() CreateDraft {
	if p.Submitted != nil {
		return *p.Submitted
	}
	return CreateDraft{Kind: "timed", Timezone: "UTC", AllowComments: true, NotifyOrganizer: true}
}

// draftOptionsJSON seeds the calendar with the echoed options.
func (p HomeProps) draftOptionsJSON() string {
	if p.Submitted == nil || len(p.Submitted.Options) == 0 {
		return ""
	}
	b, err := json.Marshal(p.Submitted.Options)
	if err != nil {
		return ""
	}
	return string(b)
}

// noscriptRows pads the echoed options to the fallback's five rows.
func (p HomeProps) noscriptRows() []DraftOption {
	rows := append([]DraftOption(nil), p.draft().Options...)
	for len(rows) < 5 {
		rows = append(rows, DraftOption{})
	}
	return rows
}

// PollPageProps feeds the public poll page and its grid partial.
type PollPageProps struct {
	Loc       *i18n.Locale
	User      *auth.User
	Poll      poll.Poll
	View      poll.View
	TZ        *time.Location
	TZName    string
	Timezones []string
	// Me is set when the viewer arrived through their personal edit
	// link; EditToken is the raw token for form actions.
	Me         *poll.Participant
	EditToken  string
	EditURL    string // absolute personal link, for the banner
	JustJoined bool
	// Updated flashes the vote-updated confirmation.
	Updated bool
	// IsAdmin reveals participants even when the poll hides them.
	IsAdmin bool
	// FinalizedLabel is the chosen option, formatted for the viewer,
	// when the poll is decided.
	FinalizedLabel string
	// OGImage is the absolute URL of the link-preview banner.
	OGImage string

	// After a refused submission the page is rendered again with the
	// message in place and what the visitor typed: VoteError/Draft for
	// the ballot, CommentError/CommentName/CommentBody for a comment.
	VoteError    string
	Draft        *VoteDraft
	CommentError string
	CommentName  string
	CommentBody  string
}

// VoteDraft is a ballot echoed back after a refused submission.
type VoteDraft struct {
	Name, Email string
	Votes       map[int64]poll.VoteValue
}

func (p PollPageProps) lang() string { return p.Loc.Lang }

// pollPath builds sub-paths under the poll.
func (p PollPageProps) pollPath(suffix string) string {
	return "/polls/" + p.Poll.PublicID + suffix
}

// gridURL is the HTMX endpoint re-rendering the grid in another zone.
func (p PollPageProps) gridURL() string {
	u := p.pollPath("/grid")
	if p.EditToken != "" {
		u += "?p=" + p.EditToken
	}
	return u
}

// voteAction is where the vote form posts. A viewer recognized by
// account posts to the same endpoint as a first vote: the service
// updates their row.
func (p PollPageProps) voteAction() string {
	if p.Me != nil && p.EditToken != "" {
		return p.pollPath("/p/" + p.EditToken + "/votes")
	}
	return p.pollPath("/participants")
}

// myVote returns the viewer's vote for an option, "" if none: the
// refused draft first, else what is recorded.
func (p PollPageProps) myVote(optionID int64) poll.VoteValue {
	if p.Draft != nil {
		return p.Draft.Votes[optionID]
	}
	if p.Me == nil {
		return ""
	}
	return p.View.Votes[p.Me.ID][optionID]
}

// showParticipants decides whether individual rows are visible.
func (p PollPageProps) showParticipants() bool {
	return !p.Poll.HideParticipants || p.IsAdmin
}

// voteClass maps a vote value to its cell class.
func voteClass(v poll.VoteValue) string {
	switch v {
	case poll.VoteYes:
		return "vc vc-yes"
	case poll.VoteIfNeedBe:
		return "vc vc-ifneedbe"
	case poll.VoteNo:
		return "vc vc-no"
	}
	return "vc vc-none"
}

func voteSymbol(v poll.VoteValue) string {
	switch v {
	case poll.VoteYes:
		return "✓"
	case poll.VoteIfNeedBe:
		return "(✓)"
	case poll.VoteNo:
		return "✕"
	}
	return "–"
}

// voteLabelKey maps a vote value to its message id.
func voteLabelKey(v poll.VoteValue) string {
	switch v {
	case poll.VoteYes:
		return "vote.yes"
	case poll.VoteIfNeedBe:
		return "vote.ifneedbe"
	case poll.VoteNo:
		return "vote.no"
	}
	return "vote.noanswer"
}

// AdminProps feeds the admin page, reached either through the
// capability URL or, once the poll is claimed, the session-authorized
// /manage path.
type AdminProps struct {
	Loc  *i18n.Locale
	User *auth.User
	Poll poll.Poll
	View poll.View
	// BasePath is where the admin forms post: the token URL or /manage.
	BasePath  string
	AdminURL  string // absolute admin URL; empty in /manage mode
	PublicURL string
	New       bool // just created or link just regenerated: show the save-this banner
	Saved     bool
	// FinalizedLabel is the chosen option's label once decided.
	FinalizedLabel string
}

func (a AdminProps) lang() string { return a.Loc.Lang }

func (a AdminProps) adminPath(suffix string) string {
	return a.BasePath + suffix
}

// answers counts non-missing votes for an option.
func (a AdminProps) answers(optionID int64) int {
	for _, t := range a.View.Tallies {
		if t.OptionID == optionID {
			return t.Yes + t.IfNeedBe + t.No
		}
	}
	return 0
}

// ErrorProps feeds the error page.
type ErrorProps struct {
	Loc     *i18n.Locale
	User    *auth.User
	Message string
	// BackURL, when set, leads back to the poll the error came from.
	BackURL string
}

func itoa(n int64) string { return fmt.Sprintf("%d", n) }

func hasWinner(tallies []poll.Tally) bool {
	for _, t := range tallies {
		if t.Winner {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ringDash returns the stroke-dasharray for the quorum ring: the arc
// covers the share of participants who can make it.
func ringDash(t poll.Tally, participants int) string {
	const circumference = 2 * 3.14159 * 8
	frac := 0.0
	if participants > 0 {
		frac = float64(t.Yes+t.IfNeedBe) / float64(participants)
	}
	return fmt.Sprintf("%.1f %.1f", frac*circumference, circumference)
}

// meName prefills the vote form: the participant being edited, else
// the signed-in account.
func meName(p PollPageProps) string {
	if p.Draft != nil {
		return p.Draft.Name
	}
	if p.Me != nil {
		return p.Me.Name
	}
	if p.User != nil {
		return p.User.Name
	}
	return ""
}

func meEmail(p PollPageProps) string {
	if p.Draft != nil {
		return p.Draft.Email
	}
	if p.Me != nil {
		return p.Me.Email
	}
	if p.User != nil {
		return p.User.Email
	}
	return ""
}

// durationPicked preselects a duration option: the echoed value, or
// one hour when there is none.
func durationPicked(minutes int, selected string) bool {
	if selected == "" {
		return minutes == 60
	}
	return fmt.Sprintf("%d", minutes) == selected
}

// commentName prefills a guest comment's name: the refused draft, else
// the signed-in account.
func commentName(p PollPageProps) string {
	if p.CommentName != "" {
		return p.CommentName
	}
	if p.User != nil {
		return p.User.Name
	}
	return ""
}
