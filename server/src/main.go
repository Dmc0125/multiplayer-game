package main

import (
	"bufio"
	"context"
	crypto "crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const PORT = ":8080"

type logResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *logResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *logResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.ResponseWriter.(http.Hijacker).Hijack()
}

var _ http.Hijacker = (*logResponseWriter)(nil)

func logRequest(next http.Handler) http.Handler {
	requestId := func() string {
		d := make([]byte, 8)
		crypto.Read(d)
		return hex.EncodeToString(d)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// log method, path, status, and duration
		rw := &logResponseWriter{w, http.StatusOK}
		start := time.Now()

		rid := requestId()
		w.Header().Set("X-Request-Id", rid)
		ctx := context.WithValue(r.Context(), "requestId", rid)

		slog.Info(
			"request incoming",
			"request_id", rid,
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"user_agent", r.UserAgent(),
		)
		next.ServeHTTP(rw, r.WithContext(ctx))
		slog.Info(
			"request complete",
			"request_id", rid,
			"status", rw.status,
			"duration", time.Since(start),
		)
	})
}

func getRequestId(r *http.Request) string {
	return r.Context().Value("requestId").(string)
}

func getUserIdFromSession(dbConn *pgx.Conn, r *http.Request) (userId int, status int) {
	sessionId, _ := r.Cookie("session_id")
	if sessionId == nil {
		status = http.StatusUnauthorized
		return
	}

	status = http.StatusOK

	q := "select u.id from sessions s join users u on u.id = s.user_id where s.id = $1 and s.expires_at > now()"
	userId = -1
	err := dbConn.QueryRow(r.Context(), q, sessionId.Value).Scan(&userId)
	if errors.Is(err, pgx.ErrNoRows) {
		status = http.StatusUnauthorized
	} else if err != nil {
		logReqError(r, "unable to get session", "error", err)
		status = http.StatusInternalServerError
	}

	return
}

func logReqInfo(r *http.Request, msg string, args ...any) {
	a := []any{"request_id", getRequestId(r), "path", r.URL.Path}
	a = append(a, args...)
	slog.Info(msg, a...)
}

func logReqError(r *http.Request, msg string, args ...any) {
	a := []any{"request_id", getRequestId(r), "path", r.URL.Path}
	a = append(a, args...)
	slog.Error(msg, a...)
}

// auth

var authConfig *oauth2.Config

func authHandler(dbConn *pgx.Conn) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		_, status := getUserIdFromSession(dbConn, r)
		switch status {
		case http.StatusOK:
			logReqInfo(r, "user already logged in")
			http.Redirect(w, r, os.Getenv("CLIENT_REDIRECT_URL"), http.StatusSeeOther)
		case http.StatusInternalServerError:
			logReqInfo(r, "unable to get user id", "error", status)
			http.Error(w, "Error: unable to get user id", status)
		default:
			logReqInfo(r, "redirecting to auth")
			state := os.Getenv("OAUTH_STATE")
			url := authConfig.AuthCodeURL(state)
			http.Redirect(http.ResponseWriter(w), r, url, http.StatusFound)
		}
	}
}

func callbackHandler(dbConn *pgx.Conn) func(w http.ResponseWriter, r *http.Request) {
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
		if state != os.Getenv("OAUTH_STATE") {
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
			Email   string `json:"email"`
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

		if sessionId == "" {
			logReqInfo(r, "new session", "user_id", userId)

			sid := make([]byte, 32)
			if _, err := crypto.Read(sid); err != nil {
				logReqError(r, "unable to generate session id", "error", err)
				http.Error(w, "Error: unable to generate session id", http.StatusInternalServerError)
				return
			}
			sessionId = hex.EncodeToString(sid)

			expiresAt := time.Now().Add(time.Hour * 24 * 7)
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
		// TODO: modify config when in prod
		isProd := os.Getenv("ENV") == "prod"
		http.SetCookie(w, &http.Cookie{
			Name:     "session_id",
			Value:    sessionId,
			Path:     "/",
			HttpOnly: true,
			Secure:   isProd,
			SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, os.Getenv("CLIENT_REDIRECT_URL"), http.StatusSeeOther)
	}
}

func authLogoutHandler(dbConn *pgx.Conn) func(w http.ResponseWriter, r *http.Request) {
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
		http.SetCookie(w, &http.Cookie{
			Name:     "session_id",
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   false,
			SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, os.Getenv("CLIENT_REDIRECT_URL"), http.StatusSeeOther)
	}
}

// game

type ConnId uint32

var nextConnId = func() func() ConnId {
	c := 0
	return func() ConnId {
		c++
		return ConnId(c)
	}
}()

type MessageType uint8

const (
	// server -> client
	MessageTypePlayerJoined MessageType = iota
	MessageTypeFull
	MessageTypeStarted
	MessageTypeGameState
	MessageTypeGameEnd
	MessageTypeReady
	// {newPlayerConnId, otherPlayerConnId}
	MessageTypeLobbyState
	MessageTypePlayerLeft
	MessageTypeSaved

	// client -> server
	__MessageTypeClientToServer
	MessageTypeKey = 100 + iota - __MessageTypeClientToServer - 1
	MessageTypeStart
	MessageTypePause
)

type LobbyStatus uint8

const (
	LobbyStatusWaitingForPlayer LobbyStatus = iota
)

type KeyCode uint8

const (
	KeyCodeArrowUp KeyCode = iota
	KeyCodeArrowDown
	KeyCodeSpace
)

type LobbyMessage struct {
	connId  ConnId
	msgType MessageType
	data    []byte
}

type GameLobby struct {
	index        int
	singleplayer bool
	messageChan  chan LobbyMessage
	stopChan     chan struct{}
	leaveChan    chan ConnId

	//
	game *GameState
}

func NewSingleplayerGameLobby(lobbyIdx int, connId ConnId, conn *websocket.Conn, userId int) (gl *GameLobby) {
	gl = &GameLobby{
		index:        lobbyIdx,
		singleplayer: true,
		messageChan:  make(chan LobbyMessage),
		stopChan:     make(chan struct{}),
		leaveChan:    make(chan ConnId),
		game:         NewGameState(),
	}
	gl.game.addPlayer(connId, conn, userId)
	gl.game.addPlayer(nextConnId(), nil, -1)
	return
}

func NewMultiplayerGameLobby(lobbyIdx int) (gl *GameLobby) {
	gl = &GameLobby{
		index:        lobbyIdx,
		singleplayer: false,
		messageChan:  make(chan LobbyMessage),
		stopChan:     make(chan struct{}),
		leaveChan:    make(chan ConnId),
		game:         NewGameState(),
	}
	return
}

func (gl *GameLobby) broadcast(msgType MessageType, data []byte) {
	d := []byte{byte(msgType)}
	if data != nil {
		d = append(d, data...)
	}

	for _, p := range gl.game.players {
		if p.conn != nil {
			p.conn.Write(context.Background(), websocket.MessageBinary, d)
		}
	}
}

func (gl *GameLobby) start(dbConn *pgx.Conn) {
	startTime := time.Now()

	for {
		select {
		case <-gl.stopChan:
			return
		case cid := <-gl.leaveChan:
			gl.game.removePlayer(cid)
		case lm := <-gl.messageChan:
			switch lm.msgType {
			case MessageTypeKey:
				if len(lm.data) < 2 {
					slog.Error("invalid message length", "lobby_idx", gl.index, "conn_id", lm.connId, "length", len(lm.data))
					continue
				}

				keyCode := KeyCode(lm.data[0])
				pressed := lm.data[1] == 1

				gl.game.players[lm.connId].keys[keyCode] = pressed
			case MessageTypeStart:
				if gl.game.status == GameStatusEnded || gl.game.status == GameStatusNone {
					gl.game.players[lm.connId].ready = true

					d := []byte{}
					d = binary.LittleEndian.AppendUint32(d, uint32(lm.connId))
					gl.broadcast(MessageTypeReady, d)
				}

				ready := true
				for _, p := range gl.game.players {
					if p.conn != nil {
						ready = ready && p.ready
					}
				}

				if ready {
					gl.game.start()
					gl.broadcast(MessageTypeStarted, gl.game.encode())
					slog.Info("game started", "lobby_idx", gl.index)
				}
			}
		default:
			// update

			if gl.game.status == GameStatusPlaying {
				winner, winnerConnId := gl.game.update()
				if winner {
					d := []byte{}
					d = binary.LittleEndian.AppendUint32(d, uint32(winnerConnId))
					gl.broadcast(MessageTypeGameEnd, d)

					// update db stats

					batch := pgx.Batch{}
					col := func(c string) string {
						if gl.singleplayer {
							return fmt.Sprintf("sp_%s", c)
						}
						return c
					}

					for connId, p := range gl.game.players {
						if p.userId > -1 {
							var c string
							if connId == winnerConnId {
								c = col("wins")
							} else {
								c = col("losses")
							}
							batch.Queue(
								fmt.Sprintf("update stats set %s = %s + 1 where user_id = $1", c, c),
								p.userId,
							)
						}
					}

					if batch.Len() > 0 {
						br := dbConn.SendBatch(context.Background(), &batch)
						if _, err := br.Exec(); err != nil {
							slog.Error("unable to update stats", "lobby_idx", gl.index, "error", err)
						}
						br.Close()
					}

					gl.broadcast(MessageTypeSaved, nil)
				} else {
					gl.broadcast(MessageTypeGameState, gl.game.encode())
				}
			}

			// time

			dt := time.Since(startTime).Seconds()
			if dt < FRAME_TIME_SECONDS {
				diff := FRAME_TIME_SECONDS - dt
				time.Sleep(time.Duration(diff * 1e9))
				dt += diff
			}

			if gl.game.status == GameStatusPlaying {
				gl.game.dt = dt
			}

			startTime = time.Now()
		}
	}
}

func gameHandler(dbConn *pgx.Conn) func(w http.ResponseWriter, r *http.Request) {
	var lobbies [10]*GameLobby

	return func(w http.ResponseWriter, r *http.Request) {
		userId, status := getUserIdFromSession(dbConn, r)
		if status == http.StatusInternalServerError {
			http.Error(w, "Error: unable to get user id", status)
			return
		}

		singleplayer := r.URL.Query().Get("singleplayer") == "1"
		logReqInfo(r, "init websocket connection", "user_id", userId, "singleplayer", singleplayer)

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			logReqError(r, "unable to accept websocket connection", "error", err)
			http.Error(w, "Error: unable to accept websocket connection", http.StatusInternalServerError)
			return
		}

		connId := nextConnId()
		var lobbyIdx int
		var lobby *GameLobby

		logReqInfo(r, "websocket connection accepted", "conn_id", connId, "user_id", userId)

		if singleplayer {
			freeIdx := -1
			for idx, lobby := range lobbies {
				if lobby == nil {
					freeIdx = idx
					break
				}
			}

			if freeIdx == -1 {
				logReqInfo(r, "no free lobby found", "user_id", userId)
				if err := conn.Write(r.Context(), websocket.MessageBinary, []byte{byte(MessageTypeFull)}); err != nil {
					logReqError(r, "unable to send message", "error", err)
					return
				}
			} else {
				lobby = NewSingleplayerGameLobby(freeIdx, connId, conn, userId)
				lobbyIdx = freeIdx
				lobbies[freeIdx] = lobby

				// send lobby state
				d := []byte{byte(MessageTypeLobbyState)}
				d = binary.LittleEndian.AppendUint32(d, uint32(connId))
				for pcid, p := range lobby.game.players {
					if p.conn == nil {
						d = binary.LittleEndian.AppendUint32(d, uint32(pcid))
					}
				}
				if err := conn.Write(r.Context(), websocket.MessageBinary, d); err != nil {
					logReqError(r, "unable to send message", "error", err)
					return
				}

				go lobby.start(dbConn)
			}
		} else {
			// find lobby with single player
			for lIdx, l := range lobbies {
				if l != nil && !l.singleplayer && len(l.game.players) == 1 {
					lobby = l
					lobbyIdx = lIdx

					lobby.game.addPlayer(connId, conn, userId)
					go lobby.start(dbConn)

					break
				}
			}

			// create new lobby
			if lobby == nil {
				for lIdx, l := range lobbies {
					if l == nil {
						lobby = NewMultiplayerGameLobby(lIdx)
						lobbyIdx = lIdx
						lobby.game.addPlayer(connId, conn, userId)
						lobbies[lIdx] = lobby
						break
					}
				}
			}

			if lobby == nil {
				logReqInfo(r, "no free lobby found", "user_id", userId)
				if err := conn.Write(r.Context(), websocket.MessageBinary, []byte{byte(MessageTypeFull)}); err != nil {
					logReqError(r, "unable to send message", "error", err)
					return
				}
			} else {
				// joined the lobby

				{ // send lobby state to new player
					d := []byte{byte(MessageTypeLobbyState)}
					d = binary.LittleEndian.AppendUint32(d, uint32(connId))

					for pcid := range lobby.game.players {
						if pcid != connId {
							d = binary.LittleEndian.AppendUint32(d, uint32(pcid))
						}
					}

					if err := conn.Write(r.Context(), websocket.MessageBinary, d); err != nil {
						logReqError(r, "unable to send message", "error", err)
						return
					}
				}

				{ // send player joind message
					for pcid, p := range lobby.game.players {
						if pcid != connId {
							d := []byte{byte(MessageTypePlayerJoined)}
							d = binary.LittleEndian.AppendUint32(d, uint32(connId))
							if err := p.conn.Write(r.Context(), websocket.MessageBinary, d); err != nil {
								logReqError(r, "unable to send message", "error", err)
								return
							}
						}
					}
				}
			}
		}

		lobbiesCount := func() (free, used int) {
			for _, l := range lobbies {
				if l == nil {
					free += 1
				} else {
					used += 1
				}
			}
			return
		}

		{
			free, used := lobbiesCount()
			logReqInfo(r, "started game", "user_id", userId, "lobby_idx", lobbyIdx, "free_lobbies", free, "used_lobbies", used)
		}

		defer func() {
			args := []any{}

			if lobby.singleplayer {
				lobby.stopChan <- struct{}{}
				lobbies[lobbyIdx] = nil
			} else {
				if len(lobby.game.players) == 2 {
					args = append(args, "lobby_deleted", false)

					lobby.leaveChan <- connId
					for _, p := range lobby.game.players {
						if p.conn != nil {
							p.conn.Write(r.Context(), websocket.MessageBinary, []byte{byte(MessageTypePlayerLeft)})
						}
					}
				} else {
					args = append(args, "lobby_deleted", true)

					lobby.stopChan <- struct{}{}
					lobbies[lobbyIdx] = nil
				}
			}

			free, used := lobbiesCount()
			args = append([]any{
				"user_id", userId,
				"lobby_idx", lobbyIdx,
				"free_lobbies", free,
				"used_lobbies", used,
				"singleplayer", lobby.singleplayer,
			}, args...)

			slog.Info("connection closed", args...)
		}()

		for {
			msgType, bytes, err := conn.Read(r.Context())
			if err != nil {
				return
			}

			if msgType != websocket.MessageBinary {
				logReqError(r, "unexpected message type", "type", msgType)
				continue
			}
			if len(bytes) < 1 {
				logReqError(r, "invalid message length", "length", len(bytes))
				continue
			}

			m := MessageType(bytes[0])
			if lobby != nil {
				lobby.messageChan <- LobbyMessage{connId, m, bytes[1:]}
			}
		}
	}
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: true,
	})))

	if err := godotenv.Load(); err != nil {
		slog.Warn("unable to load .env", "error", err)
	}
	slog.Info("loaded .env")

	dbUrl := fmt.Sprintf(
		"postgresql://%s:%s@%s:%s/%s",
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_HOST"),
		os.Getenv("DB_PORT"),
		os.Getenv("DB"),
	)
	dbConn, err := pgx.Connect(context.Background(), dbUrl)
	if err != nil {
		slog.Error("unable to connect to db", "error", err)
		return
	}
	slog.Info("connected to db")

	authConfig = &oauth2.Config{
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("REDIRECT_URL"),
		Scopes:       []string{"openid", "profile", "email"},
		Endpoint:     google.Endpoint,
	}
	slog.Info("loaded auth config")

	mux := http.NewServeMux()
	mux.HandleFunc("/auth", authHandler(dbConn))
	mux.HandleFunc("/callback", callbackHandler(dbConn))
	mux.HandleFunc("/logout", authLogoutHandler(dbConn))
	mux.HandleFunc("/game", gameHandler(dbConn))

	handler := logRequest(mux)

	slog.Info("listening at http://localhost:8080")
	if err := http.ListenAndServe(PORT, handler); err != nil {
		fmt.Printf("Error: %s", err)
	}
}
