package handler

import (
	"errors"
	"net/http"

	"github.com/lporcheron/quorum/internal/auth"
	"github.com/lporcheron/quorum/internal/poll"
	"github.com/lporcheron/quorum/internal/space"
	"github.com/lporcheron/quorum/web/templates"
)

const (
	sessSpaceKey      = "spaceID"
	sessInviteLinkKey = "flash.inviteLink"
	sessInviteSentKey = "flash.inviteSent"
)

// spaceError maps space domain errors to pages.
func (h *Handler) spaceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, space.ErrNotFound):
		h.renderError(w, r, http.StatusNotFound, "error.not_found")
	case errors.Is(err, space.ErrForbidden):
		h.renderError(w, r, http.StatusForbidden, "error.forbidden")
	case errors.Is(err, space.ErrNameRequired):
		h.renderError(w, r, http.StatusUnprocessableEntity, "error.title_required")
	case errors.Is(err, space.ErrBadTimezone):
		h.renderError(w, r, http.StatusUnprocessableEntity, "error.bad_timezone")
	case errors.Is(err, space.ErrBadRetention):
		h.renderError(w, r, http.StatusUnprocessableEntity, "error.bad_retention")
	case errors.Is(err, space.ErrBadEmail):
		h.renderError(w, r, http.StatusUnprocessableEntity, "error.email_required")
	case errors.Is(err, space.ErrAlreadyMember):
		h.renderError(w, r, http.StatusConflict, "error.already_member")
	case errors.Is(err, space.ErrInvalidInvitation):
		h.renderError(w, r, http.StatusForbidden, "error.invalid_invitation")
	case errors.Is(err, space.ErrOwnerImmovable):
		h.renderError(w, r, http.StatusConflict, "error.owner_immovable")
	case errors.Is(err, space.ErrNotAMember):
		h.renderError(w, r, http.StatusUnprocessableEntity, "error.not_a_member")
	default:
		h.log.ErrorContext(r.Context(), "space error", "error", err, "path", r.URL.Path)
		h.renderError(w, r, http.StatusInternalServerError, "error.internal")
	}
}

// currentSpace resolves the user's active space: the session choice if
// still valid, else the personal space.
func (h *Handler) currentSpace(r *http.Request, user *auth.User) (space.Space, space.Role, error) {
	if id := h.sessions.GetInt64(r.Context(), sessSpaceKey); id != 0 {
		sp, err := h.spaces.ByID(r.Context(), id)
		if err == nil {
			if role, err := h.spaces.Membership(r.Context(), sp.ID, user.ID); err == nil {
				return sp, role, nil
			}
		}
	}
	sp, err := h.spaces.ByID(r.Context(), user.PersonalSpaceID)
	if err != nil {
		return space.Space{}, "", err
	}
	role, err := h.spaces.Membership(r.Context(), sp.ID, user.ID)
	if err != nil {
		return space.Space{}, "", err
	}
	return sp, role, nil
}

// Dashboard shows the current space's polls plus the user's votes.
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		redirect(w, r, "/login?next=/dashboard")
		return
	}
	sp, role, err := h.currentSpace(r, user)
	if err != nil {
		h.spaceError(w, r, err)
		return
	}
	memberships, err := h.spaces.ForUser(r.Context(), user.ID)
	if err != nil {
		h.spaceError(w, r, err)
		return
	}
	spacePolls, err := h.polls.ListBySpace(r.Context(), sp.ID)
	if err != nil {
		h.domainError(w, r, err)
		return
	}
	voted, err := h.polls.ListVotedBy(r.Context(), user.ID)
	if err != nil {
		h.domainError(w, r, err)
		return
	}

	props := templates.DashboardProps{
		Loc:     h.locale(r),
		User:    user,
		Current: sp,
		Role:    role,
		Spaces:  memberships,
	}
	for _, p := range spacePolls {
		props.Polls = append(props.Polls, templates.SpacePollItem{
			SpacePoll:  p,
			Manageable: p.CreatedByUserID == user.ID || role.AtLeast(space.RoleAdmin),
		})
	}
	for _, p := range voted {
		if p.CreatedByUserID != user.ID {
			props.Voted = append(props.Voted, p)
		}
	}
	h.render(w, r, http.StatusOK, templates.DashboardPage(props))
}

// CreateSpace makes a new space and switches to it.
func (h *Handler) CreateSpace(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		redirect(w, r, "/login?next=/dashboard")
		return
	}
	if !h.parseForm(w, r) {
		return
	}
	sp, err := h.spaces.Create(r.Context(), user.ID, r.PostForm.Get("name"))
	if err != nil {
		h.spaceError(w, r, err)
		return
	}
	h.sessions.Put(r.Context(), sessSpaceKey, sp.ID)
	redirect(w, r, "/dashboard")
}

// SwitchSpace changes the active space.
func (h *Handler) SwitchSpace(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		redirect(w, r, "/login?next=/dashboard")
		return
	}
	if !h.parseForm(w, r) {
		return
	}
	sp, err := h.spaces.BySlug(r.Context(), r.PostForm.Get("slug"))
	if err != nil {
		h.spaceError(w, r, err)
		return
	}
	if _, err := h.spaces.Membership(r.Context(), sp.ID, user.ID); err != nil {
		h.spaceError(w, r, err)
		return
	}
	h.sessions.Put(r.Context(), sessSpaceKey, sp.ID)
	redirect(w, r, "/dashboard")
}

// spaceCtx authorizes a space route at the given minimum role.
func (h *Handler) spaceCtx(w http.ResponseWriter, r *http.Request, min space.Role) (space.Space, space.Role, bool) {
	slug := r.PathValue("slug")
	user := h.currentUser(r)
	if user == nil {
		redirect(w, r, "/login?next=/spaces/"+slug+"/settings")
		return space.Space{}, "", false
	}
	sp, err := h.spaces.BySlug(r.Context(), slug)
	if err != nil {
		h.spaceError(w, r, err)
		return space.Space{}, "", false
	}
	role, err := h.spaces.Require(r.Context(), sp.ID, user.ID, min)
	if err != nil {
		h.spaceError(w, r, err)
		return space.Space{}, "", false
	}
	return sp, role, true
}

// ShowSpaceSettings renders the space control page (admin+).
func (h *Handler) ShowSpaceSettings(w http.ResponseWriter, r *http.Request) {
	sp, role, ok := h.spaceCtx(w, r, space.RoleAdmin)
	if !ok {
		return
	}
	user := h.currentUser(r)
	members, err := h.spaces.Members(r.Context(), sp, user.ID)
	if err != nil {
		h.spaceError(w, r, err)
		return
	}
	invitations, err := h.spaces.Invitations(r.Context(), sp, user.ID)
	if err != nil {
		h.spaceError(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, templates.SpaceSettingsPage(templates.SpaceSettingsProps{
		Loc:         h.locale(r),
		User:        user,
		Space:       sp,
		Role:        role,
		Members:     members,
		Invitations: invitations,
		Timezones:   poll.CommonTimezones,
		InviteLink:  h.sessions.PopString(r.Context(), sessInviteLinkKey),
		InviteSent:  h.sessions.PopBool(r.Context(), sessInviteSentKey),
		Saved:       r.URL.Query().Get("saved") == "1",
	}))
}

// UpdateSpaceSettings saves name, default timezone and retention.
func (h *Handler) UpdateSpaceSettings(w http.ResponseWriter, r *http.Request) {
	sp, _, ok := h.spaceCtx(w, r, space.RoleAdmin)
	if !ok {
		return
	}
	if !h.parseForm(w, r) {
		return
	}
	retention := 0
	if v := r.PostForm.Get("retention_days"); v != "" {
		n, err := atoiInRange(v, 0, 3650)
		if err != nil {
			h.spaceError(w, r, space.ErrBadRetention)
			return
		}
		retention = n
	}
	err := h.spaces.UpdateSettings(r.Context(), sp, h.currentUser(r).ID,
		r.PostForm.Get("name"), r.PostForm.Get("default_timezone"), retention)
	if err != nil {
		h.spaceError(w, r, err)
		return
	}
	redirect(w, r, "/spaces/"+sp.Slug+"/settings?saved=1")
}

// InviteMember creates an invitation; emailed when SMTP is up,
// otherwise the link is shown once for manual delivery.
func (h *Handler) InviteMember(w http.ResponseWriter, r *http.Request) {
	sp, _, ok := h.spaceCtx(w, r, space.RoleAdmin)
	if !ok {
		return
	}
	if !h.parseForm(w, r) {
		return
	}
	role := space.RoleMember
	if r.PostForm.Get("role") == string(space.RoleAdmin) {
		role = space.RoleAdmin
	}
	email := r.PostForm.Get("email")
	token, err := h.spaces.Invite(r.Context(), sp, h.currentUser(r).ID, email, role)
	if err != nil {
		h.spaceError(w, r, err)
		return
	}
	link := h.baseURL + "/invitations/" + token
	if h.mailer.Enabled() {
		loc := h.locale(r)
		err := h.sendMail(r.Context(), email,
			loc.TD("space.invite_subject", map[string]any{"Space": sp.Name}),
			loc.TD("space.invite_body", map[string]any{"Space": sp.Name, "Link": link}),
			loc.T("invitation.accept"), link,
		)
		if err != nil {
			h.log.ErrorContext(r.Context(), "send invitation", "error", err)
			h.renderError(w, r, http.StatusBadGateway, "error.internal")
			return
		}
		h.sessions.Put(r.Context(), sessInviteSentKey, true)
	} else {
		h.sessions.Put(r.Context(), sessInviteLinkKey, link)
	}
	redirect(w, r, "/spaces/"+sp.Slug+"/settings")
}

// CancelInvitation withdraws a pending invitation.
func (h *Handler) CancelInvitation(w http.ResponseWriter, r *http.Request) {
	sp, _, ok := h.spaceCtx(w, r, space.RoleAdmin)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "invitationID")
	if !ok {
		return
	}
	if err := h.spaces.CancelInvitation(r.Context(), sp, h.currentUser(r).ID, id); err != nil {
		h.spaceError(w, r, err)
		return
	}
	redirect(w, r, "/spaces/"+sp.Slug+"/settings")
}

// RemoveMember removes a member (rules live in the domain).
func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	sp, _, ok := h.spaceCtx(w, r, space.RoleAdmin)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "userID")
	if !ok {
		return
	}
	if err := h.spaces.RemoveMember(r.Context(), sp, h.currentUser(r).ID, id); err != nil {
		h.spaceError(w, r, err)
		return
	}
	redirect(w, r, "/spaces/"+sp.Slug+"/settings")
}

// LeaveSpace lets a non-owner member leave their space.
func (h *Handler) LeaveSpace(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	user := h.currentUser(r)
	if user == nil {
		redirect(w, r, "/login?next=/dashboard")
		return
	}
	sp, err := h.spaces.BySlug(r.Context(), slug)
	if err != nil {
		h.spaceError(w, r, err)
		return
	}
	if err := h.spaces.RemoveMember(r.Context(), sp, user.ID, user.ID); err != nil {
		h.spaceError(w, r, err)
		return
	}
	h.sessions.Remove(r.Context(), sessSpaceKey)
	redirect(w, r, "/dashboard")
}

// ChangeMemberRole promotes or demotes between admin and member.
func (h *Handler) ChangeMemberRole(w http.ResponseWriter, r *http.Request) {
	sp, _, ok := h.spaceCtx(w, r, space.RoleOwner)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "userID")
	if !ok {
		return
	}
	if !h.parseForm(w, r) {
		return
	}
	err := h.spaces.ChangeRole(r.Context(), sp, h.currentUser(r).ID, id, space.Role(r.PostForm.Get("role")))
	if err != nil {
		h.spaceError(w, r, err)
		return
	}
	redirect(w, r, "/spaces/"+sp.Slug+"/settings")
}

// TransferOwnership hands the space to another member.
func (h *Handler) TransferOwnership(w http.ResponseWriter, r *http.Request) {
	sp, _, ok := h.spaceCtx(w, r, space.RoleOwner)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "userID")
	if !ok {
		return
	}
	if err := h.spaces.Transfer(r.Context(), sp, h.currentUser(r).ID, id); err != nil {
		h.spaceError(w, r, err)
		return
	}
	redirect(w, r, "/spaces/"+sp.Slug+"/settings")
}

// ShowInvitation renders the accept page for an invitation link.
func (h *Handler) ShowInvitation(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	inv, sp, err := h.spaces.InvitationByToken(r.Context(), token)
	if err != nil {
		h.spaceError(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, templates.InvitationPage(templates.InvitationProps{
		Loc:       h.locale(r),
		User:      h.currentUser(r),
		SpaceName: sp.Name,
		Role:      inv.Role,
		Token:     token,
	}))
}

// AcceptInvitation joins the space and switches to it.
func (h *Handler) AcceptInvitation(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	user := h.currentUser(r)
	if user == nil {
		redirect(w, r, "/login?next=/invitations/"+token)
		return
	}
	sp, err := h.spaces.Accept(r.Context(), token, user.ID)
	if err != nil {
		h.spaceError(w, r, err)
		return
	}
	h.sessions.Put(r.Context(), sessSpaceKey, sp.ID)
	redirect(w, r, "/dashboard")
}
