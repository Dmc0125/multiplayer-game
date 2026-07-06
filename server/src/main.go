package main

import (
	"context"
	crypto "crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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

// auth

var authConfig *oauth2.Config

func authHandler(w http.ResponseWriter, r *http.Request) {
	state := os.Getenv("OAUTH_STATE")
	url := authConfig.AuthCodeURL(state)
	http.Redirect(http.ResponseWriter(w), r, url, http.StatusFound)
}

func callbackHandler(dbConn *pgx.Conn) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		if state != os.Getenv("OAUTH_STATE") {
			http.Error(w, "Error: invalid state", http.StatusBadRequest)
			return
		}

		code := r.URL.Query().Get("code")
		token, err := authConfig.Exchange(context.Background(), code)
		if err != nil {
			log.Printf("Error: unable to exchange code for token: %s", err)
			http.Error(w, "Error: unable to exchange code for token", http.StatusInternalServerError)
			return
		}

		client := authConfig.Client(r.Context(), token)
		resp, err := client.Get("https://www.googleapis.com/oauth2/v3/userinfo")
		if err != nil {
			log.Printf("Error: unable to get user info: %s", err)
			http.Error(w, "Error: unable to get user info", http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		var userInfo struct {
			Email   string `json:"email"`
			Name    string `json:"name"`
			Picture string `json:"picture"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
			log.Printf("Error: unable to decode user info: %s", err)
			http.Error(w, "Error: unable to decode user info", http.StatusInternalServerError)
			return
		}

		insertedUser := dbConn.QueryRow(
			r.Context(),
			`
			insert into users (google_email, google_name, google_avatar_url) values ($1, $2, $3) 
			on conflict (google_email) do nothing returning id
			`,
			userInfo.Email,
			userInfo.Name,
			userInfo.Picture,
		)

		var userId int
		var sessionId string
		insertSession := true

		if err := insertedUser.Scan(&userId); err != nil && err.Error() != "pq: duplicate key value violates unique constraint \"users_google_email_key\"" {
			// already exists
			user := dbConn.QueryRow(r.Context(), "select id from users where google_email = $1", userInfo.Email)
			if err := user.Scan(&userId); err != nil {
				log.Printf("Error: unable to get user: %s", err)
				http.Error(w, "Error: unable to get user", http.StatusInternalServerError)
				return
			}

			session := dbConn.QueryRow(
				r.Context(),
				"select id from sessions where user_id = $1 and expires_at > now()",
				userId,
			)
			err := session.Scan(&sessionId)
			switch {
			case err == nil:
				insertSession = false
			case !errors.Is(err, pgx.ErrNoRows) && err != nil:
				log.Printf("Error: unable to get session: %s", err)
				http.Error(w, "Error: unable to get session", http.StatusInternalServerError)
				return
			}
		} else if err != nil {
			log.Printf("Error: unable to insert user: %s", err)
			http.Error(w, "Error: unable to insert user", http.StatusInternalServerError)
			return
		}

		if insertSession {
			sid := make([]byte, 32)
			if _, err := crypto.Read(sid); err != nil {
				log.Printf("Error: unable to generate session id: %s", err)
				http.Error(w, "Error: unable to generate session id", http.StatusInternalServerError)
				return
			}
			sessionId = hex.EncodeToString(sid)

			expiresAt := time.Now().Add(time.Hour * 24 * 7)
			_, err := dbConn.Exec(
				r.Context(),
				"insert into sessions (id, user_id, expires_at) values ($1, $2, $3)",
				sessionId,
				userId,
				expiresAt,
			)
			if err != nil {
				log.Printf("Error: unable to insert session: %s", err)
				http.Error(w, "Error: unable to insert session", http.StatusInternalServerError)
				return
			}
		}

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
		sessionId, err := r.Cookie("session_id")
		if err != nil {
			log.Printf("Error: unable to get session id: %s", err)
			http.Error(w, "Error: unable to get session id", http.StatusInternalServerError)
			return
		}
		if sessionId != nil {
			_, err := dbConn.Exec(
				r.Context(),
				"delete from sessions where id = $1",
				sessionId.Value,
			)
			if err != nil {
				log.Printf("Error: unable to delete session: %s", err)
				http.Error(w, "Error: unable to delete session", http.StatusInternalServerError)
				return
			}
		}

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

func authenticate(dbConn *pgx.Conn, w http.ResponseWriter, r *http.Request) (userId int, ok bool) {
	sessionId, err := r.Cookie("session_id")
	if err != nil {
		log.Printf("Error: unable to get session id: %s", err)
		http.Error(w, "Error: unable to get session id", http.StatusInternalServerError)
		return
	}
	if sessionId == nil {
		http.Error(w, "Error: session id not found", http.StatusUnauthorized)
		return
	}
	q := `
		select u.id from sessions s join users u on u.id = s.user_id where s.id = $1
	`
	userId = -1
	err = dbConn.QueryRow(r.Context(), q, sessionId.Value).Scan(&userId)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Error: session not found", http.StatusUnauthorized)
	} else if err != nil {
		log.Printf("Error: unable to get session: %s", err)
		http.Error(w, "Error: unable to get session", http.StatusInternalServerError)
	}
	return
}

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

type PlayerState struct {
	conn *websocket.Conn
	keys map[KeyCode]bool
}

type LobbyMessage struct {
	connId  ConnId
	msgType MessageType
	data    []byte
}

type GameLobby struct {
	singleplayer bool
	messageChan  chan LobbyMessage
	stopChan     chan struct{}
	leaveChan    chan ConnId

	//
	game *GameState
}

func NewSingleplayerGameLobby(connId ConnId, conn *websocket.Conn) (gl *GameLobby) {
	gl = &GameLobby{
		singleplayer: true,
		messageChan:  make(chan LobbyMessage),
		stopChan:     make(chan struct{}),
		leaveChan:    make(chan ConnId),
		game:         NewGameState(),
	}
	gl.game.addPlayer(connId, conn)
	gl.game.addPlayer(nextConnId(), nil)
	return
}

func NewMultiplayerGameLobby() (gl *GameLobby) {
	gl = &GameLobby{
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

func (gl *GameLobby) start() {
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
					log.Printf("Error: invalid message length: %d\n", len(lm.data))
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
				}
			}
		default:
			// update

			if gl.game.status == GameStatusPlaying {
				winner, connId := gl.game.update()
				if winner {
					d := []byte{}
					d = binary.LittleEndian.AppendUint32(d, uint32(connId))
					gl.broadcast(MessageTypeGameEnd, d)
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
		userId, _ := authenticate(dbConn, w, r)
		_ = userId
		singleplayer := r.URL.Query().Get("singleplayer") == "1"

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			log.Printf("Error: unable to accept websocket connection: %s", err)
			http.Error(w, "Error: unable to accept websocket connection", http.StatusInternalServerError)
			return
		}

		connId := nextConnId()
		var lobbyIdx int
		var lobby *GameLobby

		if singleplayer {
			freeIdx := -1
			for idx, lobby := range lobbies {
				if lobby == nil {
					freeIdx = idx
					break
				}
			}

			if freeIdx == -1 {
				if err := conn.Write(r.Context(), websocket.MessageBinary, []byte{byte(MessageTypeFull)}); err != nil {
					log.Printf("Error: unable to send message: %s", err)
					return
				}
			} else {
				lobby = NewSingleplayerGameLobby(connId, conn)
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
					log.Printf("Error: unable to send message: %s", err)
					return
				}

				go lobby.start()
			}
		} else {
			// find lobby with single player
			for lIdx, l := range lobbies {
				if l != nil && !l.singleplayer && len(l.game.players) == 1 {
					lobby = l
					lobbyIdx = lIdx

					lobby.game.addPlayer(connId, conn)
					go lobby.start()

					break
				}
			}

			// create new lobby
			if lobby == nil {
				for lIdx, l := range lobbies {
					if l == nil {
						lobby = NewMultiplayerGameLobby()
						lobbyIdx = lIdx
						lobby.game.addPlayer(connId, conn)
						lobbies[lIdx] = lobby
						break
					}
				}
			}

			if lobby == nil {
				if err := conn.Write(r.Context(), websocket.MessageBinary, []byte{byte(MessageTypeFull)}); err != nil {
					log.Printf("Error: unable to send message: %s", err)
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
						log.Printf("Error: unable to send message: %s", err)
						return
					}
				}

				{ // send player joind message
					for pcid, p := range lobby.game.players {
						if pcid != connId {
							d := []byte{byte(MessageTypePlayerJoined)}
							d = binary.LittleEndian.AppendUint32(d, uint32(connId))
							if err := p.conn.Write(r.Context(), websocket.MessageBinary, d); err != nil {
								log.Printf("Error: unable to send message: %s", err)
								return
							}
						}
					}
				}
			}
		}

		defer func() {
			if lobby.singleplayer {
				lobby.stopChan <- struct{}{}
				lobbies[lobbyIdx] = nil
			} else {
				if len(lobby.game.players) == 2 {
					lobby.leaveChan <- connId
					for _, p := range lobby.game.players {
						if p.conn != nil {
							p.conn.Write(r.Context(), websocket.MessageBinary, []byte{byte(MessageTypePlayerLeft)})
						}
					}
				} else {
					lobby.stopChan <- struct{}{}
					lobbies[lobbyIdx] = nil
				}
			}
		}()

		for {
			msgType, bytes, err := conn.Read(r.Context())
			if err != nil {
				return
			}

			if msgType != websocket.MessageBinary {
				log.Printf("Error: unexpected message type: %d\n", msgType)
				continue
			}
			if len(bytes) < 1 {
				log.Printf("Error: invalid message length: %d\n", len(bytes))
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
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	if err := godotenv.Load(); err != nil {
		log.Printf("Warn: unable to load .env: %s\n", err)
	}

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
		log.Printf("Error: unable to connect to db: %s\n", err)
		return
	}

	authConfig = &oauth2.Config{
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("REDIRECT_URL"),
		Scopes:       []string{"openid", "profile", "email"},
		Endpoint:     google.Endpoint,
	}

	http.HandleFunc("/auth", authHandler)
	http.HandleFunc("/callback", callbackHandler(dbConn))
	http.HandleFunc("/logout", authLogoutHandler(dbConn))
	http.HandleFunc("/game", gameHandler(dbConn))

	log.Printf("Listening at http://localhost%s\n", PORT)
	if err := http.ListenAndServe(PORT, nil); err != nil {
		fmt.Printf("Error: %s", err)
	}
}
