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
	"math"
	"math/rand"
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

type MessageType uint8

const (
	MessageTypeConnId MessageType = iota
	MessageTypeGameStart
	MessageTypeGameRestart
	MessageTypeGameEnd
	MessageTypeGameState
	MessageTypeKey
	MessageTypeLeave
)

type ConnId uint32

var nextConnId = func() func() ConnId {
	c := 0
	return func() ConnId {
		c++
		return ConnId(c)
	}
}()

type KeyCode uint8

const (
	KeyCodeArrowUp KeyCode = iota
	KeyCodeArrowDown
)

type GameInput struct {
	connId  ConnId
	keyCode KeyCode
	pressed bool
}

type JoinMessage struct {
	ConnId       ConnId
	Conn         *websocket.Conn
	Cancel       context.CancelFunc
	Singleplayer bool
}

type GameLoop struct {
	joinChan    chan JoinMessage
	leaveChan   chan ConnId
	inputChan   chan GameInput
	restartChan chan ConnId
}

const (
	FRAME_TIME_SECONDS float64 = 1.0 / 60.0
	GAME_WIDTH                 = 800.0
	GAME_HEIGHT                = 400.0

	PADDLE_SPEED_PER_SECOND = 200.0
	PADDLE_WIDTH            = 10.0
	PADDLE_HEIGHT           = 100.0

	PADDLE_LEFT_X  = 50.0
	PADDLE_RIGHT_X = GAME_WIDTH - 50.0

	BALL_SPEED_PER_SECOND = 500.0
	BALL_RADIUS           = 5.0
)

type Paddle struct {
	width, height float32
	x, y          float32
}

type Ball struct {
	radius float32
	x, y   float32
	vx, vy float32
}

type PlayerState struct {
	left       bool
	connection *websocket.Conn
	keys       map[KeyCode]bool
	paddle     Paddle
	bot        bool
	restart    bool
}

func (gl *GameLoop) run() {
	leftMissing := true
	players := make(map[ConnId]*PlayerState)
	singleplayer := false
	var ball Ball

	startTime := time.Now()
	gameRunning := false
	elapsedTime := 0.0
	deltaTime := 0.0

	join := func(connId ConnId, bot bool, conn *websocket.Conn) {
		ps := &PlayerState{
			connection: conn,
			keys:       make(map[KeyCode]bool),
			bot:        bot,
		}

		var paddleX float32
		if leftMissing {
			ps.left = true
			leftMissing = false
			paddleX = 50
		} else {
			ps.left = false
			paddleX = GAME_WIDTH - 50 - PADDLE_WIDTH
		}

		ps.paddle = Paddle{
			width:  PADDLE_WIDTH,
			height: PADDLE_HEIGHT,
			x:      paddleX,
			y:      GAME_HEIGHT/2 - PADDLE_HEIGHT/2,
		}
		players[connId] = ps
	}

	leave := func(connId ConnId) {
		if players[connId].left {
			leftMissing = true
		}
		delete(players, connId)
	}

	broadcast := func(msgType MessageType, data []byte) {
		d := []byte{byte(msgType)}
		if data != nil {
			d = append(d, data...)
		}

		for _, p := range players {
			if p.connection != nil {
				p.connection.Write(context.Background(), websocket.MessageBinary, d)
			}
		}
	}

	stateEncode := func() []byte {
		encoded := make([]byte, 40+12)
		offset := 0

		encodef32 := func(f float32) {
			binary.LittleEndian.PutUint32(encoded[offset:], math.Float32bits(f))
			offset += 4
		}

		for connId, p := range players {
			binary.LittleEndian.PutUint32(encoded[offset:], uint32(connId))
			offset += 4
			encodef32(p.paddle.x)
			encodef32(p.paddle.y)
			encodef32(p.paddle.width)
			encodef32(p.paddle.height)
		}

		encodef32(ball.x)
		encodef32(ball.y)
		encodef32(ball.radius)

		return encoded
	}

	predictBallY := func(ball Ball, paddle Paddle) (float32, bool) {
		if ball.x < PADDLE_LEFT_X || ball.x > PADDLE_RIGHT_X {
			return 0, false
		}

		updateBall := func(b *Ball) {
			b.y += b.vy * float32(FRAME_TIME_SECONDS)
			b.x += b.vx * float32(FRAME_TIME_SECONDS)

			if b.y-b.radius < 0 {
				b.y = b.radius
				b.vy = -b.vy
			} else if b.y+b.radius > GAME_HEIGHT {
				b.y = GAME_HEIGHT - b.radius
				b.vy = -b.vy
			}
		}

		if paddle.x == PADDLE_LEFT_X && ball.vx < 0 { // left paddle
			for {
				updateBall(&ball)
				if ball.x-ball.radius < paddle.x+paddle.width {
					return ball.y, true
				}
			}
		} else if ball.vx > 0 { // right paddle
			for {
				updateBall(&ball)
				if ball.x+ball.radius > paddle.x {
					return ball.y, true
				}
			}
		}

		return 0, false
	}

	startGame := func() {
		gameRunning = true
		elapsedTime = 0.0
		deltaTime = 0.0

		// paddles
		for _, p := range players {
			p.paddle.y = GAME_HEIGHT/2 - p.paddle.height/2
		}

		// ball
		ball.radius = BALL_RADIUS
		ball.x = GAME_WIDTH/2 - ball.radius/2
		ball.y = GAME_HEIGHT/2 - ball.radius/2

		{ // ball velocity
			deg := rand.Float32()*30 - 15 // [-15, 15]
			rad := float64(deg * math.Pi / 180)

			ball.vx = float32(math.Cos(rad)) * BALL_SPEED_PER_SECOND
			ball.vy = float32(math.Sin(rad)) * BALL_SPEED_PER_SECOND

			if rand.Float32() < 0.5 {
				ball.vx = -ball.vx
			}
		}

		broadcast(MessageTypeGameStart, stateEncode())
	}

	for {
		select {
		case j := <-gl.joinChan:
			if len(players) == 2 {
				j.Cancel()
				j.Conn.Close(websocket.StatusNormalClosure, "Error: game already running")
			} else {
				join(j.ConnId, false, j.Conn)

				if len(players) == 1 && j.Singleplayer {
					join(nextConnId(), true, nil)
					singleplayer = true
					startGame()
				}

				if len(players) == 2 {
					startGame()
				}
			}
		case connId := <-gl.leaveChan:
			gameRunning = false
			if singleplayer {
				for c, p := range players {
					if p.bot {
						leave(c)
						break
					}
				}
			} else {
				broadcast(MessageTypeLeave, nil)
			}

			leave(connId)
		case inp := <-gl.inputChan:
			p := players[inp.connId]
			p.keys[inp.keyCode] = inp.pressed
		case connId := <-gl.restartChan:
			if !gameRunning {
				if singleplayer {
					startGame()
				} else {
					players[connId].restart = true
					playersReady := true
					for _, p := range players {
						if !p.restart {
							playersReady = false
							break
						}
					}

					if playersReady {
						startGame()
						for _, p := range players {
							p.restart = false
						}
					}
				}
			}
		default:
			{ // update
				dt := float32(deltaTime)

				if gameRunning {
					// paddles
					for _, p := range players {
						func() {
							if p.bot {
								if predictedY, ok := predictBallY(ball, p.paddle); ok {
									if predictedY > p.paddle.y+p.paddle.height {
										p.keys[KeyCodeArrowUp] = true
									} else if predictedY < p.paddle.y {
										p.keys[KeyCodeArrowDown] = true
									}
								}
							}
							defer func() {
								if p.bot {
									p.keys[KeyCodeArrowUp] = false
									p.keys[KeyCodeArrowDown] = false
								}
							}()

							//

							paddle := &p.paddle

							if p.keys[KeyCodeArrowUp] {
								paddle.y += PADDLE_SPEED_PER_SECOND * dt

								if paddle.y+paddle.height > GAME_HEIGHT {
									paddle.y = GAME_HEIGHT - paddle.height
								}
							}
							if p.keys[KeyCodeArrowDown] {
								paddle.y -= PADDLE_SPEED_PER_SECOND * dt

								if paddle.y < 0 {
									paddle.y = 0
								}
							}
						}()
					}

					func() {
						sendState := true
						defer func() {
							if sendState {
								broadcast(MessageTypeGameState, stateEncode())
							}
						}()

						// ball
						ball.x += ball.vx * dt
						ball.y += ball.vy * dt

						switch {
						case ball.x-ball.radius < 0:
							// player right wins
							sendState = false
							broadcast(MessageTypeGameEnd, []byte{1})
							gameRunning = false
							return
						case ball.x+ball.radius > GAME_WIDTH:
							// player left wins
							sendState = false
							broadcast(MessageTypeGameEnd, []byte{0})
							gameRunning = false
							return
						case ball.y-ball.radius < 0:
							ball.y = ball.radius
							ball.vy = -ball.vy
							return
						case ball.y+ball.radius > GAME_HEIGHT:
							ball.y = GAME_HEIGHT - ball.radius
							ball.vy = -ball.vy
							return
						}

						// ball collision with paddles

						for _, p := range players {
							ballLeft := ball.x - ball.radius
							ballRight := ball.x + ball.radius
							ballTop := ball.y + ball.radius
							ballBottom := ball.y - ball.radius

							paddleLeft := p.paddle.x
							paddleRight := p.paddle.x + p.paddle.width
							paddleTop := p.paddle.y + p.paddle.height
							paddleBottom := p.paddle.y

							xInside := ballRight > paddleLeft && ballLeft < paddleRight
							yInside := ballTop > paddleBottom && ballBottom < paddleTop

							if !(xInside && yInside) {
								continue
							}

							overlapLeft := ballRight - paddleLeft
							overlapRight := paddleRight - ballLeft
							overlapTop := paddleTop - ballBottom
							overlapBottom := ballTop - paddleBottom

							xMin := float32(math.Min(float64(overlapLeft), float64(overlapRight)))
							yMin := float32(math.Min(float64(overlapTop), float64(overlapBottom)))

							bounceAngle := func(vxDir float32) (vx, vy float32) {
								paddleHalfHeight := p.paddle.height / 2
								paddleCenter := p.paddle.y + paddleHalfHeight
								hit := ball.y - paddleCenter

								rel := float32(math.Max(-1, math.Min(1, float64(hit/paddleHalfHeight))))
								angle := 55 * rel

								rad := float64(angle * math.Pi / 180)
								vx = float32(math.Cos(rad)) * BALL_SPEED_PER_SECOND * vxDir
								vy = float32(math.Sin(rad)) * BALL_SPEED_PER_SECOND

								return
							}

							if xMin < yMin {
								if overlapLeft < overlapRight {
									ball.x = paddleLeft - ball.radius
									ball.vx, ball.vy = bounceAngle(-1)
								} else {
									ball.x = paddleRight + ball.radius
									ball.vx, ball.vy = bounceAngle(1)
								}
							} else {
								if overlapTop < overlapBottom {
									ball.y = paddleTop + ball.radius
								} else {
									ball.y = paddleBottom - ball.radius
								}
								ball.vy = -ball.vy
							}

							break
						}
					}()
				}
			}

			{ // time
				dt := time.Since(startTime).Seconds()
				if dt < FRAME_TIME_SECONDS {
					diff := FRAME_TIME_SECONDS - dt
					time.Sleep(time.Duration(diff * 1e9))
					dt += diff
				}

				// set time
				if gameRunning {
					elapsedTime += dt
					deltaTime = dt
				}

				startTime = time.Now()
			}
		}
	}
}

func wsHandler(gl *GameLoop) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			log.Printf("Error: unable to accept websocket connection: %s", err)
			http.Error(w, "Error: unable to accept websocket connection", http.StatusInternalServerError)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		connId := nextConnId()
		{ // send conn id
			d := []byte{byte(MessageTypeConnId)}
			d = binary.LittleEndian.AppendUint32(d, uint32(connId))

			ctx, _ := context.WithTimeout(context.Background(), time.Second)
			if err := conn.Write(ctx, websocket.MessageBinary, d); err != nil {
				log.Printf("Error: unable to send conn id: %s", err)
				return
			}
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		for {
			msgType, bytes, err := conn.Read(ctx)
			if errors.Is(err, context.Canceled) {
				return
			} else if err != nil {
				gl.leaveChan <- connId
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

			switch m {
			case MessageTypeGameStart:
				singleplayer := bytes[1] == 0
				gl.joinChan <- JoinMessage{connId, conn, cancel, singleplayer}
			case MessageTypeGameRestart:
				gl.restartChan <- connId
			case MessageTypeKey:
				gl.inputChan <- GameInput{connId, KeyCode(bytes[1]), bytes[2] == 1}
			case MessageTypeLeave:
				gl.leaveChan <- connId
				return
			}
		}
	}
}

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
		http.Redirect(w, r, os.Getenv("CLIENT_REDIRECT_URL"), http.StatusPermanentRedirect)
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
		http.Redirect(w, r, os.Getenv("CLIENT_REDIRECT_URL"), http.StatusPermanentRedirect)
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

	game := GameLoop{
		joinChan:    make(chan JoinMessage),
		leaveChan:   make(chan ConnId),
		inputChan:   make(chan GameInput),
		restartChan: make(chan ConnId),
	}
	go game.run()
	http.HandleFunc("/ws", wsHandler(&game))

	log.Printf("Listening at http://localhost%s\n", PORT)
	if err := http.ListenAndServe(PORT, nil); err != nil {
		fmt.Printf("Error: %s", err)
	}
}
