package auth

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/scs/postgresstore"
	"github.com/alexedwards/scs/sqlite3store"
	"github.com/alexedwards/scs/v2"

	"github.com/lporcheron/quorum/internal/store"
)

// SessionUserKey is where the signed-in user id lives in the session.
const SessionUserKey = "userID"

// NewSessionManager builds the scs manager backed by the sessions
// table (created by migration 00002; its two engine-specific column
// types are substituted when rendering for PostgreSQL).
func NewSessionManager(db *sql.DB, baseURL string, dialect store.Dialect) *scs.SessionManager {
	sm := scs.New()
	if dialect == store.DialectPostgres {
		sm.Store = postgresstore.New(db)
	} else {
		sm.Store = sqlite3store.New(db)
	}
	sm.Lifetime = 30 * 24 * time.Hour
	sm.Cookie.Name = "quorum_session"
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Secure = strings.HasPrefix(baseURL, "https://")
	return sm
}
