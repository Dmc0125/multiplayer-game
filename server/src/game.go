package main

import (
	"encoding/binary"
	"math"
	"math/rand"
	"time"
)

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
	x, y   float32
	vx, vy float32
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
	paddleY float32

	// bot
	move           bool
	predictedBallY float32
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
	status           GameStatus
	ball             GameBall
	playerLeft       GamePlayerState
	playerRight      GamePlayerState
}

func (gs *GameState) start(players map[ConnId]*LobbyPlayer) {
	gs.prevFrameEndTime = time.Now()
	gs.dt = 0.0
	gs.status = GameStatusPlaying

	// ball
	gs.ball.x = GAME_WIDTH / 2
	gs.ball.y = GAME_HEIGHT / 2

	// ball velocity
	deg := rand.Float32()*5 - 10 // [-5, 5]
	rad := float64(deg * math.Pi / 180)

	gs.ball.vx = float32(math.Cos(rad)) * BALL_SPEED_PER_SECOND
	gs.ball.vy = float32(math.Sin(rad)) * BALL_SPEED_PER_SECOND

	if rand.Float32() < 0.5 {
		gs.ball.vx = -gs.ball.vx
	}

	for _, p := range players {
		s := GamePlayerState{
			bot:     p.conn == nil,
			keys:    make(map[KeyCode]bool),
			paddleY: GAME_HEIGHT/2 - PADDLE_HEIGHT/2,
		}
		if p.left {
			gs.playerLeft = s
			if s.bot {
				predictBallY(gs.ball, &gs.playerLeft, true)
			}
		} else {
			gs.playerRight = s
			if s.bot {
				predictBallY(gs.ball, &gs.playerRight, false)
			}
		}
	}

}

func (gs *GameState) setKey(left bool, key KeyCode, pressed bool) {
	if left {
		gs.playerLeft.keys[key] = pressed
	} else {
		gs.playerRight.keys[key] = pressed
	}
}

func (gs *GameState) advanceTime() {
	dt := time.Since(gs.prevFrameEndTime).Seconds()
	if gs.status == GameStatusPlaying {
		gs.dt = dt
	}
	gs.prevFrameEndTime = time.Now()
}

func (gs *GameState) encode() (out []byte) {
	// Data format:
	//
	// 0. float32 - left paddle y
	// 1. float32 - right paddle y
	// 2. float32 - ball x
	// 3. float32 - ball y

	out = make([]byte, 16+8)
	offset := 0

	encodeFloat32 := func(f float32) {
		binary.LittleEndian.PutUint32(out[offset:], math.Float32bits(f))
		offset += 4
	}

	encodeFloat32(gs.playerLeft.paddleY)
	encodeFloat32(gs.playerRight.paddleY)
	encodeFloat32(gs.ball.x)
	encodeFloat32(gs.ball.y)

	return
}

func predictBallY(ball GameBall, player *GamePlayerState, paddleLeft bool) {
	if ball.x < PADDLE_LEFT_X || ball.x > PADDLE_RIGHT_X+PADDLE_WIDTH {
		return
	}

	updateBall := func(b *GameBall) {
		fts := float32(FRAME_TIME_SECONDS) / float32(time.Second)
		b.y += b.vy * fts
		b.x += b.vx * fts

		if b.y-BALL_RADIUS < 0 {
			b.y = BALL_RADIUS
			b.vy = -b.vy
		} else if b.y+BALL_RADIUS > GAME_HEIGHT {
			b.y = GAME_HEIGHT - BALL_RADIUS
			b.vy = -b.vy
		}
	}

	predictedY := ball.y
	ok := false

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

	if ok {
		player.predictedBallY = predictedY
		player.move = true
	}

	return
}

func (gs *GameState) update() (winner bool, winnerLeft bool) {
	dt := float32(gs.dt)

	updatePaddlePosition := func(p *GamePlayerState, moveUp, moveDown bool) {
		if moveUp {
			p.paddleY += PADDLE_SPEED_PER_SECOND * dt
			if p.paddleY+PADDLE_HEIGHT > GAME_HEIGHT {
				p.paddleY = GAME_HEIGHT - PADDLE_HEIGHT
			}
		}
		if moveDown {
			p.paddleY -= PADDLE_SPEED_PER_SECOND * dt
			if p.paddleY < 0 {
				p.paddleY = 0
			}
		}
	}

	updatePaddle := func(p *GamePlayerState, left bool) {
		const deadzone = 10.0
		if p.bot && p.move {
			var moveUp, moveDown bool
			if p.predictedBallY > p.paddleY+PADDLE_HEIGHT-deadzone {
				moveUp = true
			} else if p.predictedBallY < p.paddleY+deadzone {
				moveDown = true
			}

			updatePaddlePosition(p, moveUp, moveDown)

			paddleTop := p.paddleY + PADDLE_HEIGHT
			paddleBottom := p.paddleY

			if paddleBottom+deadzone < p.predictedBallY && paddleTop-deadzone > p.predictedBallY {
				p.move = false
			}
		} else {
			updatePaddlePosition(p, p.keys[KeyCodeArrowUp], p.keys[KeyCodeArrowDown])
		}
	}

	updatePaddle(&gs.playerLeft, true)
	updatePaddle(&gs.playerRight, false)

	{ // ball
		ball := &gs.ball
		ball.x += ball.vx * dt
		ball.y += ball.vy * dt

		switch {
		case ball.x-BALL_RADIUS < 0:
			// player right wins
			gs.status = GameStatusEnded
			winnerLeft = false
			winner = true
			return
		case ball.x+BALL_RADIUS > GAME_WIDTH:
			// player left wins
			gs.status = GameStatusEnded
			winnerLeft = true
			winner = true
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
	}

	bounceAngle := func(ball GameBall, paddleY, vxDir float32) (vx, vy float32) {
		paddleHalfHeight := PADDLE_HEIGHT / 2
		paddleCenter := paddleY + paddleHalfHeight
		hit := ball.y - paddleCenter

		rel := float32(math.Max(-1, math.Min(1, float64(hit/paddleHalfHeight))))
		angle := 55 * rel

		rad := float64(angle * math.Pi / 180)
		vx = float32(math.Cos(rad)) * BALL_SPEED_PER_SECOND * vxDir
		vy = float32(math.Sin(rad)) * BALL_SPEED_PER_SECOND

		return
	}

	ballCollisionWithPaddles := func(ball *GameBall, p *GamePlayerState, paddleX float32) (collided bool) {
		ballLeft := ball.x - BALL_RADIUS
		ballRight := ball.x + BALL_RADIUS
		ballTop := ball.y + BALL_RADIUS
		ballBottom := ball.y - BALL_RADIUS

		paddleLeft := paddleX
		paddleRight := paddleX + PADDLE_WIDTH
		paddleTop := p.paddleY + PADDLE_HEIGHT
		paddleBottom := p.paddleY

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
				ball.x = paddleLeft - BALL_RADIUS
				ball.vx, ball.vy = bounceAngle(*ball, p.paddleY, -1)
			} else {
				ball.x = paddleRight + BALL_RADIUS
				ball.vx, ball.vy = bounceAngle(*ball, p.paddleY, 1)
			}
		} else {
			if overlapTop < overlapBottom {
				ball.y = paddleTop + BALL_RADIUS
			} else {
				ball.y = paddleBottom - BALL_RADIUS
			}
			ball.vy = -ball.vy
		}

		return
	}

	collided := ballCollisionWithPaddles(&gs.ball, &gs.playerLeft, PADDLE_LEFT_X)
	if collided && gs.playerRight.bot {
		predictBallY(gs.ball, &gs.playerRight, false)
	}
	if collided {
		return
	}

	collided = ballCollisionWithPaddles(&gs.ball, &gs.playerRight, PADDLE_RIGHT_X)
	if collided && gs.playerLeft.bot {
		predictBallY(gs.ball, &gs.playerLeft, true)
	}

	return
}
