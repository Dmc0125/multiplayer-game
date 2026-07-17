package core

import (
	"encoding/binary"
	"math"
	"math/rand"
	"time"
)

type GameEventType uint8

const (
	GameEventTypeNone GameEventType = iota
	GameEventTypeCountdown
	GameEventTypeState
	GameEventTypeWinner
)

type GameEvent struct {
	Type GameEventType

	// GameEventTypeCountdown
	Countdown int

	// GameEventTypeState
	LeftPaddleY  float32
	RightPaddleY float32
	BallX        float32
	BallY        float32
	BallVX       float32
	BallVY       float32

	// GameEventTypeWinner
	WinnerLeft bool
}

func (ge *GameEvent) encode() (out []byte) {
	switch ge.Type {
	case GameEventTypeCountdown:
		out = make([]byte, 5)
		out[0] = byte(ge.Type)
		binary.LittleEndian.PutUint32(out[1:], uint32(ge.Countdown))
	case GameEventTypeState:
		out = make([]byte, 0)
		out = append(out, byte(ge.Type))

		encodeFloat32 := func(f float32) {
			out = binary.LittleEndian.AppendUint32(out, math.Float32bits(f))
		}

		encodeFloat32(ge.LeftPaddleY)
		encodeFloat32(ge.RightPaddleY)
		encodeFloat32(ge.BallX)
		encodeFloat32(ge.BallY)
		encodeFloat32(ge.BallVX)
		encodeFloat32(ge.BallVY)
	case GameEventTypeWinner:
		out = make([]byte, 2)
		out[0] = byte(ge.Type)
		if ge.WinnerLeft {
			out[1] = 1
		}
	}
	return
}

const FRAME_TIME_SECONDS = time.Second / 60.0

const GAME_WIDTH = 800.0
const GAME_HEIGHT = 400.0

const PADDLE_WIDTH float32 = 10.0
const PADDLE_HEIGHT float32 = 100.0
const PADDLE_LEFT_X = 50.0
const PADDLE_RIGHT_X = GAME_WIDTH - 50.0 - PADDLE_WIDTH
const PADDLE_SPEED_PER_SECOND = 200.0

const BALL_RADIUS = 5.0
const BALL_SPEED_PER_SECOND = 500.0

type GameBall struct {
	X, Y   float32
	Vx, Vy float32
}

type KeyCode uint8

const (
	KeyCodeArrowUp KeyCode = iota
	KeyCodeArrowDown
	KeyCodeSpace
)

type GamePlayerState struct {
	bot     bool
	keys    map[KeyCode]bool
	PaddleY float32

	// bot
	Move           bool
	PredictedBallY float32
}

type GameStatus uint8

const (
	GameStatusNone GameStatus = iota
	GameStatusCountdown
	GameStatusPlaying
	GameStatusEnded
	GameStatusPaused
)

type GameState struct {
	prevFrameEndTime    time.Time
	deltaTimeSeconds    float64
	timeElapsedSeconds  float64
	status              GameStatus
	ball                GameBall
	playerLeft          GamePlayerState
	playerRight         GamePlayerState
	countdown           int
	lastCountdownUpdate float64
}

func (gs *GameState) start(players map[ConnId]*LobbyPlayer) GameEvent {
	gs.prevFrameEndTime = time.Now()
	gs.timeElapsedSeconds = 0
	gs.deltaTimeSeconds = 0.0
	gs.status = GameStatusCountdown
	gs.countdown = 3
	gs.lastCountdownUpdate = 0

	// ball
	gs.ball.X = GAME_WIDTH / 2
	gs.ball.Y = GAME_HEIGHT / 2

	// ball velocity
	deg := rand.Float32()*5 - 10 // [-5, 5]
	rad := float64(deg * math.Pi / 180)

	gs.ball.Vx = float32(math.Cos(rad)) * BALL_SPEED_PER_SECOND
	gs.ball.Vy = float32(math.Sin(rad)) * BALL_SPEED_PER_SECOND

	if rand.Float32() < 0.5 {
		gs.ball.Vx = -gs.ball.Vx
	}

	for _, p := range players {
		s := GamePlayerState{
			bot:     p.conn == nil,
			keys:    make(map[KeyCode]bool),
			PaddleY: GAME_HEIGHT/2 - PADDLE_HEIGHT/2,
		}
		if p.left {
			gs.playerLeft = s
			if s.bot {
				PredictBallY(gs.ball, &gs.playerLeft, true)
			}
		} else {
			gs.playerRight = s
			if s.bot {
				PredictBallY(gs.ball, &gs.playerRight, false)
			}
		}
	}

	return GameEvent{
		Type:      GameEventTypeCountdown,
		Countdown: gs.countdown,
	}
}

func (gs *GameState) setKey(left bool, key KeyCode, pressed bool) {
	if left {
		gs.playerLeft.keys[key] = pressed
	} else {
		gs.playerRight.keys[key] = pressed
	}
}

func (gs *GameState) running() bool {
	return gs.status == GameStatusPlaying || gs.status == GameStatusCountdown
}

func (gs *GameState) advanceTime() {
	dt := time.Since(gs.prevFrameEndTime).Seconds()
	if gs.running() {
		gs.deltaTimeSeconds = dt
		gs.timeElapsedSeconds += dt
	}
	gs.prevFrameEndTime = time.Now()
}

func PredictBallY(ball GameBall, player *GamePlayerState, paddleLeft bool) {
	if ball.X < PADDLE_LEFT_X || ball.X > PADDLE_RIGHT_X+PADDLE_WIDTH {
		return
	}

	updateBall := func(b *GameBall) {
		fts := float32(FRAME_TIME_SECONDS) / float32(time.Second)
		b.Y += b.Vy * fts
		b.X += b.Vx * fts

		if b.Y-BALL_RADIUS < 0 {
			b.Y = BALL_RADIUS
			b.Vy = -b.Vy
		} else if b.Y+BALL_RADIUS > GAME_HEIGHT {
			b.Y = GAME_HEIGHT - BALL_RADIUS
			b.Vy = -b.Vy
		}
	}

	predictedY := ball.Y
	ok := false

	if paddleLeft && ball.Vx < 0 {
		// ball going left
		ok = true
		for {
			updateBall(&ball)
			if ball.X-BALL_RADIUS < PADDLE_LEFT_X+PADDLE_WIDTH {
				predictedY = ball.Y
				break
			}
		}
	} else if !paddleLeft && ball.Vx > 0 {
		// ball going left
		ok = true
		for {
			updateBall(&ball)
			if ball.X+BALL_RADIUS > PADDLE_RIGHT_X {
				predictedY = ball.Y
				break
			}
		}
	}

	if ok {
		player.PredictedBallY = predictedY
		player.Move = true
	}

	return
}

func (gs *GameState) update() (event GameEvent) {
	if gs.status == GameStatusCountdown {
		const countdownUpdateInterval float64 = 1

		if gs.lastCountdownUpdate+countdownUpdateInterval < gs.timeElapsedSeconds {
			gs.countdown -= 1
			gs.lastCountdownUpdate = gs.timeElapsedSeconds

			if gs.countdown == 0 {
				gs.status = GameStatusPlaying
			}

			event = GameEvent{
				Type:      GameEventTypeCountdown,
				Countdown: gs.countdown,
			}
		}

		return
	}

	dt := float32(gs.deltaTimeSeconds)

	defer func() {
		if event.Type == GameEventTypeNone {
			event.Type = GameEventTypeState
			event.LeftPaddleY = gs.playerLeft.PaddleY
			event.RightPaddleY = gs.playerRight.PaddleY
			event.BallX = gs.ball.X
			event.BallY = gs.ball.Y
			event.BallVX = gs.ball.Vx
			event.BallVY = gs.ball.Vy
		}
	}()

	updatePaddlePosition := func(p *GamePlayerState, moveUp, moveDown bool) {
		if moveUp {
			p.PaddleY += PADDLE_SPEED_PER_SECOND * dt
			if p.PaddleY+PADDLE_HEIGHT > GAME_HEIGHT {
				p.PaddleY = GAME_HEIGHT - PADDLE_HEIGHT
			}
		}
		if moveDown {
			p.PaddleY -= PADDLE_SPEED_PER_SECOND * dt
			if p.PaddleY < 0 {
				p.PaddleY = 0
			}
		}
	}

	updatePaddle := func(p *GamePlayerState, left bool) {
		const deadzone = 10.0
		if p.bot && p.Move {
			var moveUp, moveDown bool
			if p.PredictedBallY > p.PaddleY+PADDLE_HEIGHT-deadzone {
				moveUp = true
			} else if p.PredictedBallY < p.PaddleY+deadzone {
				moveDown = true
			}

			updatePaddlePosition(p, moveUp, moveDown)

			paddleTop := p.PaddleY + PADDLE_HEIGHT
			paddleBottom := p.PaddleY

			if paddleBottom+deadzone < p.PredictedBallY && paddleTop-deadzone > p.PredictedBallY {
				p.Move = false
			}
		} else {
			updatePaddlePosition(p, p.keys[KeyCodeArrowUp], p.keys[KeyCodeArrowDown])
		}
	}

	updatePaddle(&gs.playerLeft, true)
	updatePaddle(&gs.playerRight, false)

	{ // ball
		ball := &gs.ball
		ball.X += ball.Vx * dt
		ball.Y += ball.Vy * dt

		switch {
		case ball.X-BALL_RADIUS < 0:
			// player right wins
			gs.status = GameStatusEnded
			event.Type = GameEventTypeWinner
			event.WinnerLeft = false
			return
		case ball.X+BALL_RADIUS > GAME_WIDTH:
			// player left wins
			gs.status = GameStatusEnded
			event.Type = GameEventTypeWinner
			event.WinnerLeft = true
			return
		case ball.Y-BALL_RADIUS < 0:
			ball.Y = BALL_RADIUS
			ball.Vy = -ball.Vy
			return
		case ball.Y+BALL_RADIUS > GAME_HEIGHT:
			ball.Y = GAME_HEIGHT - BALL_RADIUS
			ball.Vy = -ball.Vy
			return
		}
	}

	bounceAngle := func(ball GameBall, paddleY, vxDir float32) (vx, vy float32) {
		paddleHalfHeight := PADDLE_HEIGHT / 2
		paddleCenter := paddleY + paddleHalfHeight
		hit := ball.Y - paddleCenter

		rel := float32(math.Max(-1, math.Min(1, float64(hit/paddleHalfHeight))))
		angle := 55 * rel

		rad := float64(angle * math.Pi / 180)
		vx = float32(math.Cos(rad)) * BALL_SPEED_PER_SECOND * vxDir
		vy = float32(math.Sin(rad)) * BALL_SPEED_PER_SECOND

		return
	}

	ballCollisionWithPaddles := func(ball *GameBall, p *GamePlayerState, paddleX float32) (collided bool) {
		ballLeft := ball.X - BALL_RADIUS
		ballRight := ball.X + BALL_RADIUS
		ballTop := ball.Y + BALL_RADIUS
		ballBottom := ball.Y - BALL_RADIUS

		paddleLeft := paddleX
		paddleRight := paddleX + PADDLE_WIDTH
		paddleTop := p.PaddleY + PADDLE_HEIGHT
		paddleBottom := p.PaddleY

		xInside := ballRight > paddleLeft && ballLeft < paddleRight
		yInside := ballTop > paddleBottom && ballBottom < paddleTop

		if !(xInside && yInside) {
			return
		}

		collided = true

		overlapLeft := ballRight - paddleLeft
		overlapRight := paddleRight - ballLeft
		overlapTop := paddleTop - ballBottom
		overlapBottom := ballTop - paddleBottom

		xMin := float32(math.Min(float64(overlapLeft), float64(overlapRight)))
		yMin := float32(math.Min(float64(overlapTop), float64(overlapBottom)))

		if xMin < yMin {
			if overlapLeft < overlapRight {
				ball.X = paddleLeft - BALL_RADIUS
				ball.Vx, ball.Vy = bounceAngle(*ball, p.PaddleY, -1)
			} else {
				ball.X = paddleRight + BALL_RADIUS
				ball.Vx, ball.Vy = bounceAngle(*ball, p.PaddleY, 1)
			}
		} else {
			if overlapTop < overlapBottom {
				ball.Y = paddleTop + BALL_RADIUS
			} else {
				ball.Y = paddleBottom - BALL_RADIUS
			}
			ball.Vy = -ball.Vy
		}

		return
	}

	collided := ballCollisionWithPaddles(&gs.ball, &gs.playerLeft, PADDLE_LEFT_X)
	if collided && gs.playerRight.bot {
		PredictBallY(gs.ball, &gs.playerRight, false)
	}
	if collided {
		return
	}

	collided = ballCollisionWithPaddles(&gs.ball, &gs.playerRight, PADDLE_RIGHT_X)
	if collided && gs.playerLeft.bot {
		PredictBallY(gs.ball, &gs.playerLeft, true)
	}

	return
}
