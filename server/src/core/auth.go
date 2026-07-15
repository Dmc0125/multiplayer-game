package core

import (
	"context"
	crypto "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"
)

var authConfig *oauth2.Config

type StateStore struct {
	states map[string]time.Time
	lock   sync.Mutex
}

func newStateStore() *StateStore {
	return &StateStore{
		states: make(map[string]time.Time),
	}
}

func (s *StateStore) new() string {
	stateBytes := make([]byte, 32)
	crypto.Read(stateBytes)
	state := hex.EncodeToString(stateBytes)

	s.lock.Lock()
	defer s.lock.Unlock()

	s.states[state] = time.Now().Add(time.Minute * 10)
	return state
}

func (s *StateStore) validate(state string) bool {
	s.lock.Lock()
	defer s.lock.Unlock()

	_, ok := s.states[state]
	delete(s.states, state)

	// delete expired states
	n := time.Now()
	for k := range maps.Keys(s.states) {
		v := s.states[k]
		if n.After(v) {
			delete(s.states, k)
		}
	}

	return ok
}

func createSessionCookie(w http.ResponseWriter, value, domain string, prod bool, expiresAt time.Time) {
	c := http.Cookie{
		Name:     "session_id",
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   prod,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
	}
	if prod {
		c.Domain = domain
	}
	http.SetCookie(w, &c)
}

func getUserIdFromSession(dbConn *pgxpool.Pool, r *http.Request) (userId, status int, err error) {
	sessionId, _ := r.Cookie("session_id")
	if sessionId == nil {
		status = http.StatusUnauthorized
		return
	}

	status = http.StatusOK

	q := "select u.id from sessions s join users u on u.id = s.user_id where s.id = $1 and s.expires_at > now()"
	userId = -1
	err = dbConn.QueryRow(r.Context(), q, sessionId.Value).Scan(&userId)
	if errors.Is(err, pgx.ErrNoRows) {
		status = http.StatusUnauthorized
	} else if err != nil {
		status = http.StatusInternalServerError
		err = fmt.Errorf("unable to query session: %w", err)
	}

	return
}

func authHandler(dbConn *pgxpool.Pool, states *StateStore, clientRedirectUrl string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		rslog := getRequestLogger(r)
		_, status, err := getUserIdFromSession(dbConn, r)

		switch status {
		case http.StatusOK:
			rslog.Debug("user already logged in")
			http.Redirect(w, r, clientRedirectUrl, http.StatusSeeOther)
		case http.StatusInternalServerError:
			rslog.Debug("unable to get user id", "error", err)
			http.Error(w, "Error: unable to get user id", status)
		default:
			rslog.Debug("redirecting to auth")
			url := authConfig.AuthCodeURL(states.new())
			http.Redirect(http.ResponseWriter(w), r, url, http.StatusFound)
		}
	}
}

func callbackHandler(dbConn *pgxpool.Pool, states *StateStore, clientRedirectUrl, domain string, prod bool) func(w http.ResponseWriter, r *http.Request) {
	prefixes := []string{"Paddle", "Smash", "Spin", "Turbo", "Bouncy", "Rally", "Woosh", "Bloop", "Slap", "Bonk", "Zippy", "Twirl", "Whack", "Speedy", "Smashy", "Zappy", "Wiggly", "Floppy", "Boomy", "Pong"}
	suffixes := []string{"Paddle", "Smasher", "Spinner", "Bopper", "Whacker", "Bonker", "Rallyer", "Lobber", "Dinker", "Slapper", "Banger", "Pinger", "Swoosher", "Topspin", "Dropshot", "Volley", "Dasher", "Popper", "Zipper", "Ace"}

	generateRandomUsername := func() string {
		p := prefixes[rand.Intn(len(prefixes))]
		s := suffixes[rand.Intn(len(suffixes))]
		n := rand.Intn(100)
		return fmt.Sprintf("%s%s%d", p, s, n)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		rslog := getRequestLogger(r)

		state := r.URL.Query().Get("state")
		if !states.validate(state) {
			http.Error(w, "Error: invalid state", http.StatusBadRequest)
			return
		}

		rslog.Debug("token exchange")
		code := r.URL.Query().Get("code")
		token, err := authConfig.Exchange(context.Background(), code)
		if err != nil {
			httpErrorInternal(r, w, err, "unable to exchange code for token")
			return
		}
		rslog.Debug("token exchange complete")

		rslog.Debug("getting user info")
		client := authConfig.Client(r.Context(), token)
		resp, err := client.Get("https://www.googleapis.com/oauth2/v3/userinfo")
		if err != nil {
			httpErrorInternal(r, w, err, "unable to get user info")
			return
		}
		defer resp.Body.Close()

		var userInfo struct {
			Email string `json:"email"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
			httpErrorInternal(r, w, err, "unable to decode user info")
			return
		}
		rslog.Debug("getting user info complete")

		rslog.Debug("inserting user")
		dbtx, err := dbConn.Begin(r.Context())
		if err != nil {
			httpErrorInternal(r, w, err, "unable to start transaction")
			return
		}
		defer dbtx.Rollback(r.Context())

		insertedUser := dbtx.QueryRow(
			r.Context(),
			`
			insert into users (google_email, username) values ($1, $2)
			on conflict (google_email) do nothing returning id
			`,
			userInfo.Email,
			generateRandomUsername(),
		)

		var userId int
		var sessionId string

		if err := insertedUser.Scan(&userId); errors.Is(err, pgx.ErrNoRows) {
			// already exists
			rslog.Debug("existing user", "user_id", userId)

			user := dbtx.QueryRow(r.Context(), "select id from users where google_email = $1", userInfo.Email)
			if err := user.Scan(&userId); err != nil {
				httpErrorInternal(r, w, err, "unable to get user")
				return
			}

			session := dbtx.QueryRow(
				r.Context(),
				"select id from sessions where user_id = $1 and expires_at > now()",
				userId,
			)
			err := session.Scan(&sessionId)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				httpErrorInternal(r, w, err, "unable to get session")
				return
			}
		} else if err != nil {
			httpErrorInternal(r, w, err, "unable to insert user")
			return
		} else {
			// user doesn't exist => insert stats
			rslog.Debug("new user", "user_id", userId)

			_, err := dbtx.Exec(
				r.Context(),
				"insert into stats (user_id) values ($1)",
				userId,
			)
			if err != nil {
				httpErrorInternal(r, w, err, "unable to insert stats")
				return
			}
		}

		expiresAt := time.Now().Add(time.Hour * 24 * 7)
		if sessionId == "" {
			rslog.Debug("new session", "user_id", userId)

			sid := make([]byte, 32)
			if _, err := crypto.Read(sid); err != nil {
				httpErrorInternal(r, w, err, "unable to generate session id")
				return
			}
			sessionId = hex.EncodeToString(sid)

			_, err := dbtx.Exec(
				r.Context(),
				"insert into sessions (id, user_id, expires_at) values ($1, $2, $3)",
				sessionId,
				userId,
				expiresAt,
			)
			if err != nil {
				httpErrorInternal(r, w, err, "unable to insert session")
				return
			}
		} else {
			rslog.Debug("existing session", "user_id", userId)
		}

		if err := dbtx.Commit(r.Context()); err != nil {
			httpErrorInternal(r, w, err, "unable to commit transaction")
			return
		}

		rslog.Debug("user inserted")
		createSessionCookie(w, sessionId, domain, prod, expiresAt)
		http.Redirect(w, r, clientRedirectUrl, http.StatusSeeOther)
	}
}

func authLogoutHandler(dbConn *pgxpool.Pool, clientRedirectUrl, domain string, prod bool) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		rslog := getRequestLogger(r)

		sessionId, _ := r.Cookie("session_id")
		if sessionId != nil {
			rslog.Debug("deleting session", "session_id", sessionId.Value)
			_, err := dbConn.Exec(
				r.Context(),
				"delete from sessions where id = $1",
				sessionId.Value,
			)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				httpErrorInternal(r, w, err, "unable to delete session")
				return
			}
		}

		rslog.Debug("deleting session cookie")
		createSessionCookie(w, "", domain, prod, time.Now())
		http.Redirect(w, r, clientRedirectUrl, http.StatusSeeOther)
	}
}
