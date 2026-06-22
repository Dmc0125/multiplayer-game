package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

const PORT = ":8080"

type MessageType uint8

const (
	MessageTypeConnId MessageType = iota
	MessageTypeGameStart
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
	ConnId ConnId
	Conn   *websocket.Conn
	Cancel context.CancelFunc
}

type GameLoop struct {
	joinChan  chan JoinMessage
	leaveChan chan ConnId
	inputChan chan GameInput
}

const FRAME_TIME_SECONDS float64 = 1.0 / 60.0
const GAME_WIDTH = 800.0
const GAME_HEIGHT = 400.0

const PADDLE_SPEED_PER_SECOND = 200.0
const PADDLE_WIDTH = 10.0
const PADDLE_HEIGHT = 100.0

type Paddle struct {
	width, height float32
	x, y          float32
}

func (gl *GameLoop) run() {
	connections := make(map[ConnId]*websocket.Conn)
	keys := make(map[ConnId]map[KeyCode]bool)
	paddles := make(map[ConnId]*Paddle)

	startTime := time.Now()
	gameRunning := false
	elapsedTime := 0.0
	deltaTime := 0.0

	join := func(connId ConnId, conn *websocket.Conn) {
		connections[connId] = conn
		keys[connId] = make(map[KeyCode]bool)

		var paddleX float32
		if len(paddles) == 0 {
			paddleX = 50
		} else {
			paddleX = GAME_WIDTH - 50 - PADDLE_WIDTH
		}

		paddles[connId] = &Paddle{
			width:  PADDLE_WIDTH,
			height: PADDLE_HEIGHT,
			x:      paddleX,
			y:      GAME_HEIGHT/2 - PADDLE_HEIGHT/2,
		}
	}

	leave := func(connId ConnId) {
		delete(connections, connId)
		delete(keys, connId)
		delete(paddles, connId)
	}

	broadcast := func(msgType MessageType, data []byte) {
		d := []byte{byte(msgType)}
		if data != nil {
			d = append(d, data...)
		}

		for _, conn := range connections {
			if conn == nil {
				panic("nil connection")
			}
			conn.Write(context.Background(), websocket.MessageBinary, d)
		}
	}

	stateEncode := func() []byte {
		encoded := make([]byte, 40)
		offset := 0

		for connId := range connections {
			paddle := paddles[connId]
			binary.LittleEndian.PutUint32(encoded[offset:], uint32(connId))
			offset += 4
			binary.LittleEndian.PutUint32(encoded[offset:], math.Float32bits(paddle.x))
			offset += 4
			binary.LittleEndian.PutUint32(encoded[offset:], math.Float32bits(paddle.y))
			offset += 4
			binary.LittleEndian.PutUint32(encoded[offset:], math.Float32bits(paddle.width))
			offset += 4
			binary.LittleEndian.PutUint32(encoded[offset:], math.Float32bits(paddle.height))
			offset += 4
		}

		return encoded
	}

	for {
		select {
		case j := <-gl.joinChan:
			if len(connections) == 2 {
				j.Cancel()
				j.Conn.Close(websocket.StatusNormalClosure, "Error: game already running")
			} else {
				join(j.ConnId, j.Conn)

				if len(connections) == 2 {
					// start game
					gameRunning = true
					elapsedTime = 0.0
					deltaTime = 0.0

					broadcast(MessageTypeGameStart, stateEncode())
				}
			}
		case connId := <-gl.leaveChan:
			gameRunning = false
			leave(connId)

			broadcast(MessageTypeGameEnd, nil)
		case inp := <-gl.inputChan:
			keys[inp.connId][inp.keyCode] = inp.pressed
		default:
			{ // update
				dt := float32(deltaTime)

				if gameRunning {
					dirty := false

					for connId, key := range keys {
						paddle := paddles[connId]

						if key[KeyCodeArrowUp] {
							paddle.y += PADDLE_SPEED_PER_SECOND * dt
							dirty = true

							if paddle.y+paddle.height > GAME_HEIGHT {
								paddle.y = GAME_HEIGHT - paddle.height
								dirty = true
							}
						}
						if key[KeyCodeArrowDown] {
							paddle.y -= PADDLE_SPEED_PER_SECOND * dt
							dirty = true

							if paddle.y < 0 {
								paddle.y = 0
								dirty = true
							}
						}
					}

					if dirty {
						broadcast(MessageTypeGameState, stateEncode())
					}
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
		gl.joinChan <- JoinMessage{connId, conn, cancel}

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
			case MessageTypeKey:
				gl.inputChan <- GameInput{connId, KeyCode(bytes[1]), bytes[2] == 1}
			case MessageTypeLeave:
				gl.leaveChan <- connId
				return
			}
		}
	}
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	game := GameLoop{
		joinChan:  make(chan JoinMessage),
		leaveChan: make(chan ConnId),
		inputChan: make(chan GameInput),
	}
	go game.run()
	http.HandleFunc("/ws", wsHandler(&game))

	log.Printf("Listening at http://localhost%s\n", PORT)
	if err := http.ListenAndServe(PORT, nil); err != nil {
		fmt.Printf("Error: %s", err)
	}
}
