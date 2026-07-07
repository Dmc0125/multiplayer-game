package main

import (
	"encoding/binary"
	"math"
	"math/rand"
	"time"

	"github.com/coder/websocket"
)

const FRAME_TIME_SECONDS = 1.0 / 60.0

const GAME_WIDTH = 800.0
const GAME_HEIGHT = 400.0

const PADDLE_WIDTH float32 = 10.0
const PADDLE_HEIGHT float32 = 100.0
const PADDLE_LEFT_X = 50.0
const PADDLE_RIGHT_X = GAME_WIDTH - 50.0 - PADDLE_WIDTH
const PADDLE_SPEED_PER_SECOND = 200.0

const BALL_RADIUS = 5.0
const BALL_SPEED_PER_SECOND = 500.0

type GamePaddle struct {
	x, y float32
}

type GameBall struct {
	x, y   float32
	vx, vy float32
}

type GamePlayerState struct {
	conn   *websocket.Conn
	userId int
	keys   map[KeyCode]bool
	left   bool
	paddle *GamePaddle

	ready bool
	pause bool
}

type GameStatus uint8

const (
	GameStatusNone GameStatus = iota
	GameStatusPlaying
	GameStatusEnded
	GameStatusPaused
)

type GameState struct {
	prevFrameEndTime time.Time
	dt               float64

	players map[ConnId]*GamePlayerState
	status  GameStatus
	ball    *GameBall
}

func NewGameState() *GameState {
	gs := &GameState{
		prevFrameEndTime: time.Now(),
		dt:               0.0,
		players:          make(map[ConnId]*GamePlayerState),
		status:           GameStatusNone,
		ball:             &GameBall{},
	}
	return gs
}

func (gs *GameState) setKey(connId ConnId, key KeyCode, pressed bool) {
	gs.players[connId].keys[key] = pressed
}

func (gs *GameState) advanceTime() {
	dt := time.Since(gs.prevFrameEndTime).Seconds()
	if dt < FRAME_TIME_SECONDS {
		diff := FRAME_TIME_SECONDS - dt
		time.Sleep(time.Duration(diff * 1e9))
		dt += diff
	}

	if gs.status == GameStatusPlaying {
		gs.dt = dt
	}

	gs.prevFrameEndTime = time.Now()
}

func (gs *GameState) addPlayer(connId ConnId, conn *websocket.Conn, userId int) {
	gs.players[connId] = &GamePlayerState{
		conn:   conn,
		userId: userId,
		keys:   make(map[KeyCode]bool),
		left:   len(gs.players) == 0,
		paddle: &GamePaddle{},
	}
}

func (gs *GameState) removePlayer(connId ConnId) {
	delete(gs.players, connId)
	gs.status = GameStatusNone
}

func (gs *GameState) encode() (out []byte) {
	// connId => 4 bytes, paddleY => 4 bytes = 8 bytes * 2
	// ballX, ballY 8 bytes
	out = make([]byte, 16+8)
	offset := 0

	encodeFloat32 := func(f float32) {
		binary.LittleEndian.PutUint32(out[offset:], math.Float32bits(f))
		offset += 4
	}

	for connId, p := range gs.players {
		binary.LittleEndian.PutUint32(out[offset:], uint32(connId))
		offset += 4
		encodeFloat32(p.paddle.y)
	}

	encodeFloat32(gs.ball.x)
	encodeFloat32(gs.ball.y)
	return
}

func (gs *GameState) start() {
	gs.status = GameStatusPlaying
	gs.dt = 0

	// paddles
	for _, p := range gs.players {
		if p.left {
			p.paddle.x = PADDLE_LEFT_X
		} else {
			p.paddle.x = PADDLE_RIGHT_X
		}
		p.paddle.y = GAME_HEIGHT/2 - PADDLE_HEIGHT/2

		p.ready = false
	}

	// ball
	gs.ball.x = GAME_WIDTH / 2
	gs.ball.y = GAME_HEIGHT / 2

	{ // ball velocity
		deg := rand.Float32()*30 - 15 // [-15, 15]
		rad := float64(deg * math.Pi / 180)

		gs.ball.vx = float32(math.Cos(rad)) * BALL_SPEED_PER_SECOND
		gs.ball.vy = float32(math.Sin(rad)) * BALL_SPEED_PER_SECOND

		if rand.Float32() < 0.5 {
			gs.ball.vx = -gs.ball.vx
		}
	}
}

func predictBallY(ball GameBall, paddleLeft bool) (predictedY float32, ok bool) {
	if ball.x < PADDLE_LEFT_X || ball.x > PADDLE_RIGHT_X {
		return
	}

	updateBall := func(b *GameBall) {
		b.y += b.vy * float32(FRAME_TIME_SECONDS)
		b.x += b.vx * float32(FRAME_TIME_SECONDS)

		if b.y-BALL_RADIUS < 0 {
			b.y = BALL_RADIUS
			b.vy = -b.vy
		} else if b.y+BALL_RADIUS > GAME_HEIGHT {
			b.y = GAME_HEIGHT - BALL_RADIUS
			b.vy = -b.vy
		}
	}

	if paddleLeft && ball.vx < 0 {
		// ball going left
		ok = true
		for {
			updateBall(&ball)
			if ball.x-BALL_RADIUS < PADDLE_LEFT_X+PADDLE_WIDTH {
				predictedY = ball.y
				break
			}
		}
	} else if !paddleLeft && ball.vx > 0 {
		// ball going left
		ok = true
		for {
			updateBall(&ball)
			if ball.x+BALL_RADIUS > PADDLE_RIGHT_X {
				predictedY = ball.y
				break
			}
		}
	}

	return
}

func (gs *GameState) update() (winner bool, connId ConnId) {
	dt := float32(gs.dt)

	for _, p := range gs.players {
		if p.conn == nil {
			predictedY, ok := predictBallY(*gs.ball, p.left)
			const deadzone = 10.0
			if ok {
				if predictedY > p.paddle.y+PADDLE_HEIGHT-deadzone {
					p.keys[KeyCodeArrowUp] = true
				} else if predictedY < p.paddle.y+deadzone {
					p.keys[KeyCodeArrowDown] = true
				}
			}
		}

		// update paddle
		paddle := p.paddle
		if p.keys[KeyCodeArrowUp] {
			paddle.y += PADDLE_SPEED_PER_SECOND * dt
			if paddle.y+PADDLE_HEIGHT > GAME_HEIGHT {
				paddle.y = GAME_HEIGHT - PADDLE_HEIGHT
			}
		}
		if p.keys[KeyCodeArrowDown] {
			paddle.y -= PADDLE_SPEED_PER_SECOND * dt
			if paddle.y < 0 {
				paddle.y = 0
			}
		}

		if p.conn == nil {
			p.keys[KeyCodeArrowUp] = false
			p.keys[KeyCodeArrowDown] = false
		}
	}

	{ // ball
		ball := gs.ball
		ball.x += ball.vx * dt
		ball.y += ball.vy * dt

		setWinner := func(left bool) {
			winner = true
			for c, p := range gs.players {
				if left && p.left {
					connId = c
					return
				} else if !left && !p.left {
					connId = c
					return
				}
			}
		}

		switch {
		case ball.x-BALL_RADIUS < 0:
			// player right wins
			gs.status = GameStatusEnded
			setWinner(false)
			return
		case ball.x+BALL_RADIUS > GAME_WIDTH:
			// player left wins
			gs.status = GameStatusEnded
			setWinner(true)
			return
		case ball.y-BALL_RADIUS < 0:
			ball.y = BALL_RADIUS
			ball.vy = -ball.vy
			return
		case ball.y+BALL_RADIUS > GAME_HEIGHT:
			ball.y = GAME_HEIGHT - BALL_RADIUS
			ball.vy = -ball.vy
			return
		}

		// ball collision with paddles
		for _, p := range gs.players {
			ballLeft := ball.x - BALL_RADIUS
			ballRight := ball.x + BALL_RADIUS
			ballTop := ball.y + BALL_RADIUS
			ballBottom := ball.y - BALL_RADIUS

			paddleLeft := p.paddle.x
			paddleRight := p.paddle.x + PADDLE_WIDTH
			paddleTop := p.paddle.y + PADDLE_HEIGHT
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
				paddleHalfHeight := PADDLE_HEIGHT / 2
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
					ball.x = paddleLeft - BALL_RADIUS
					ball.vx, ball.vy = bounceAngle(-1)
				} else {
					ball.x = paddleRight + BALL_RADIUS
					ball.vx, ball.vy = bounceAngle(1)
				}
			} else {
				if overlapTop < overlapBottom {
					ball.y = paddleTop + BALL_RADIUS
				} else {
					ball.y = paddleBottom - BALL_RADIUS
				}
				ball.vy = -ball.vy
			}

			break
		}
	}

	return
}
