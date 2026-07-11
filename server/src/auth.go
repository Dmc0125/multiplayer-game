package main

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
	"golang.org/x/oauth2"
)

var authConfig *oauth2.Config

type StateStore struct {
	states map[string]time.Time
	lock   sync.Mutex
}

func NewStateStore() *StateStore {
	return &StateStore{
		states: make(map[string]time.Time),
	}
}

func (s *StateStore) New() string {
	stateBytes := make([]byte, 32)
	crypto.Read(stateBytes)
	state := hex.EncodeToString(stateBytes)

	s.lock.Lock()
	defer s.lock.Unlock()

	s.states[state] = time.Now().Add(time.Minute * 10)
	return state
}

func (s *StateStore) Validate(state string) bool {
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

func authHandler(dbConn *pgx.Conn, states *StateStore, clientRedirectUrl string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		_, status := getUserIdFromSession(dbConn, r)
		switch status {
		case http.StatusOK:
			logReqInfo(r, "user already logged in")
			http.Redirect(w, r, clientRedirectUrl, http.StatusSeeOther)
		case http.StatusInternalServerError:
			logReqInfo(r, "unable to get user id", "error", status)
			http.Error(w, "Error: unable to get user id", status)
		default:
			logReqInfo(r, "redirecting to auth")
			url := authConfig.AuthCodeURL(states.New())
			http.Redirect(http.ResponseWriter(w), r, url, http.StatusFound)
		}
	}
}

func callbackHandler(dbConn *pgx.Conn, states *StateStore, clientRedirectUrl, domain string, prod bool) func(w http.ResponseWriter, r *http.Request) {
	prefixes := []string{"Paddle", "Smash", "Spin", "Turbo", "Bouncy", "Rally", "Woosh", "Bloop", "Slap", "Bonk", "Zippy", "Twirl", "Whack", "Speedy", "Smashy", "Zappy", "Wiggly", "Floppy", "Boomy", "Pong"}
	suffixes := []string{"Paddle", "Smasher", "Spinner", "Bopper", "Whacker", "Bonker", "Rallyer", "Lobber", "Dinker", "Slapper", "Banger", "Pinger", "Swoosher", "Topspin", "Dropshot", "Volley", "Dasher", "Popper", "Zipper", "Ace"}

	generateRandomUsername := func() string {
		p := prefixes[rand.Intn(len(prefixes))]
		s := suffixes[rand.Intn(len(suffixes))]
		n := rand.Intn(100)
		return fmt.Sprintf("%s%s%d", p, s, n)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		if !states.Validate(state) {
			logReqInfo(r, "invalid state", "state", state)
			http.Error(w, "Error: invalid state", http.StatusBadRequest)
			return
		}

		logReqInfo(r, "token exchange")
		code := r.URL.Query().Get("code")
		token, err := authConfig.Exchange(context.Background(), code)
		if err != nil {
			logReqError(r, "unable to exchange code for token", "error", err)
			http.Error(w, "Error: unable to exchange code for token", http.StatusInternalServerError)
			return
		}
		logReqInfo(r, "token exchange complete")

		logReqInfo(r, "getting user info")
		client := authConfig.Client(r.Context(), token)
		resp, err := client.Get("https://www.googleapis.com/oauth2/v3/userinfo")
		if err != nil {
			logReqError(r, "unable to get user info", "error", err)
			http.Error(w, "Error: unable to get user info", http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		var userInfo struct {
			Email string `json:"email"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
			logReqError(r, "unable to decode user info", "error", err)
			http.Error(w, "Error: unable to decode user info", http.StatusInternalServerError)
			return
		}
		logReqInfo(r, "getting user info complete")

		logReqInfo(r, "inserting user")
		dbtx, err := dbConn.Begin(r.Context())
		if err != nil {
			logReqError(r, "unable to start transaction", "error", err)
			http.Error(w, "Error: unable to start transaction", http.StatusInternalServerError)
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
			logReqInfo(r, "existing user", "user_id", userId)

			user := dbtx.QueryRow(r.Context(), "select id from users where google_email = $1", userInfo.Email)
			if err := user.Scan(&userId); err != nil {
				logReqError(r, "unable to get user", "error", err)
				http.Error(w, "Error: unable to get user", http.StatusInternalServerError)
				return
			}

			session := dbtx.QueryRow(
				r.Context(),
				"select id from sessions where user_id = $1 and expires_at > now()",
				userId,
			)
			err := session.Scan(&sessionId)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				logReqError(r, "unable to get session", "error", err)
				http.Error(w, "Error: unable to get session", http.StatusInternalServerError)
				return
			}
		} else if err != nil {
			logReqError(r, "unable to insert user", "error", err)
			http.Error(w, "Error: unable to insert user", http.StatusInternalServerError)
			return
		} else {
			// user doesn't exist => insert stats
			logReqInfo(r, "new user", "user_id", userId)

			_, err := dbtx.Exec(
				r.Context(),
				"insert into stats (user_id) values ($1)",
				userId,
			)
			if err != nil {
				logReqError(r, "unable to insert stats", "error", err)
				http.Error(w, "Error: unable to insert stats", http.StatusInternalServerError)
				return
			}
		}

		expiresAt := time.Now().Add(time.Hour * 24 * 7)
		if sessionId == "" {
			logReqInfo(r, "new session", "user_id", userId)

			sid := make([]byte, 32)
			if _, err := crypto.Read(sid); err != nil {
				logReqError(r, "unable to generate session id", "error", err)
				http.Error(w, "Error: unable to generate session id", http.StatusInternalServerError)
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
				logReqError(r, "unable to insert session", "error", err)
				http.Error(w, "Error: unable to insert session", http.StatusInternalServerError)
				return
			}
		} else {
			logReqInfo(r, "existing session", "user_id", userId)
		}

		if err := dbtx.Commit(r.Context()); err != nil {
			logReqError(r, "unable to commit transaction", "error", err)
			http.Error(w, "Error: unable to commit transaction", http.StatusInternalServerError)
			return
		}

		logReqInfo(r, "user inserted")

		logReqInfo(r, "setting session cookie")
		createSessionCookie(w, sessionId, domain, prod, expiresAt)
		http.Redirect(w, r, clientRedirectUrl, http.StatusSeeOther)
	}
}

func authLogoutHandler(dbConn *pgx.Conn, clientRedirectUrl, domain string, prod bool) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionId, _ := r.Cookie("session_id")
		if sessionId != nil {
			logReqInfo(r, "deleting session", "session_id", sessionId.Value)
			_, err := dbConn.Exec(
				r.Context(),
				"delete from sessions where id = $1",
				sessionId.Value,
			)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				logReqError(r, "unable to delete session", "error", err)
				http.Error(w, "Error: unable to delete session", http.StatusInternalServerError)
				return
			}
		}

		logReqInfo(r, "deleting session cookie")
		createSessionCookie(w, "", domain, prod, time.Now())
		http.Redirect(w, r, clientRedirectUrl, http.StatusSeeOther)
	}
}
