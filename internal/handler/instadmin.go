package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/lporcheron/quorum/internal/auth"
	"github.com/lporcheron/quorum/internal/job"
	"github.com/lporcheron/quorum/internal/setting"
	"github.com/lporcheron/quorum/internal/store"
	"github.com/lporcheron/quorum/internal/store/sqlite"
	"github.com/lporcheron/quorum/web/templates"
)

// instanceAdmin gates the instance control page: the signed-in user's
// email must be listed in QUORUM_ADMIN_EMAILS.
func (h *Handler) instanceAdmin(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	user := h.currentUser(r)
	if user == nil {
		redirect(w, r, "/login?next=/admin")
		return nil, false
	}
	if !h.adminEmails[user.Email] {
		h.renderError(w, r, http.StatusForbidden, "error.forbidden")
		return nil, false
	}
	return user, true
}

// ShowInstanceAdmin renders users, hot settings and queue state.
func (h *Handler) ShowInstanceAdmin(w http.ResponseWriter, r *http.Request) {
	user, ok := h.instanceAdmin(w, r)
	if !ok {
		return
	}
	users, err := h.st.ListUsers(r.Context(), 200)
	if err != nil {
		h.domainError(w, r, err)
		return
	}
	pending, err := h.st.CountPendingJobs(r.Context(), job.MaxAttempts)
	if err != nil {
		h.domainError(w, r, err)
		return
	}
	deadRows, err := h.st.ListDeadJobs(r.Context(), sqlite.ListDeadJobsParams{MaxAttempts: job.MaxAttempts, MaxRows: 20})
	if err != nil {
		h.domainError(w, r, err)
		return
	}

	loc := h.locale(r)
	props := templates.InstanceAdminProps{
		Loc:               loc,
		User:              user,
		InstanceName:      h.settings.InstanceName(r.Context()),
		RegistrationsOpen: h.settings.RegistrationsOpen(r.Context()),
		PendingJobs:       pending,
		Saved:             r.URL.Query().Get("saved") == "1",
	}
	for _, u := range users {
		created := u.CreatedAt
		if t, err := store.ParseTime(u.CreatedAt); err == nil {
			created = templates.Stamp(loc.Lang, t, time.UTC)
		}
		props.Users = append(props.Users, templates.InstanceUser{
			Name: u.Name, Email: u.Email, CreatedAt: created,
		})
	}
	for _, j := range deadRows {
		props.DeadJobs = append(props.DeadJobs, templates.DeadJob{
			ID: j.ID, Type: j.Type, Attempts: j.Attempts, LastError: j.LastError.String,
		})
	}
	h.render(w, r, http.StatusOK, templates.InstanceAdminPage(props))
}

// UpdateInstanceSettings saves the hot settings.
func (h *Handler) UpdateInstanceSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.instanceAdmin(w, r); !ok {
		return
	}
	if !h.parseForm(w, r) {
		return
	}
	name := r.PostForm.Get("instance_name")
	if name == "" {
		name = "Quorum"
	}
	if err := h.settings.Set(r.Context(), setting.KeyInstanceName, name); err != nil {
		h.domainError(w, r, err)
		return
	}
	open := strconv.FormatBool(r.PostForm.Get("registrations_open") == "1")
	if err := h.settings.Set(r.Context(), setting.KeyRegistrationsOpen, open); err != nil {
		h.domainError(w, r, err)
		return
	}
	redirect(w, r, "/admin?saved=1")
}

// RetryDeadJob puts a dead job back in the queue.
func (h *Handler) RetryDeadJob(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.instanceAdmin(w, r); !ok {
		return
	}
	id, ok := h.pathID(w, r, "jobID")
	if !ok {
		return
	}
	if err := h.st.RetryJob(r.Context(), sqlite.RetryJobParams{ID: id, RunAt: store.FormatTime(time.Now())}); err != nil {
		h.domainError(w, r, err)
		return
	}
	redirect(w, r, "/admin")
}

// DeleteDeadJob drops a dead job.
func (h *Handler) DeleteDeadJob(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.instanceAdmin(w, r); !ok {
		return
	}
	id, ok := h.pathID(w, r, "jobID")
	if !ok {
		return
	}
	if err := h.st.DeleteJob(r.Context(), id); err != nil {
		h.domainError(w, r, err)
		return
	}
	redirect(w, r, "/admin")
}
