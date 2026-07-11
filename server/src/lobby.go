package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ConnId uint32

var nextConnId = func() func() ConnId {
	var c atomic.Uint32
	return func() ConnId {
		return ConnId(c.Add(1))
	}
}()

type MessageType = byte

const (
	// server -> client

	// PlayerJoined is sent to player when other player joins the lobby
	//
	// Data expected from this message:
	//
	// 0. u8 - MessageTypePlayerJoined
	// 1. u32 - new player conn id
	MessageTypePlayerJoined MessageType = iota
	MessageTypeFull
	MessageTypeStarted
	MessageTypeGameState
	MessageTypeGameEnd
	MessageTypeReady
	// LobbyState is sent to player when they join a lobby
	//
	// Data expected from this message:
	//
	// 0. u8 - MessageTypeLobbyState
	// 1. u32 - player conn id
	// 2. u32 - other connected player conn id
	MessageTypeLobbyState
	MessageTypePlayerLeft
	MessageTypeSaved
	// Pong is a response to a Ping message
	//
	// Data expected from this message:
	//
	// 0. u8 - MessageTypePong
	// 1. u32 - client message id
	MessageTypePong

	// client -> server
	__MessageTypeClientToServer
	MessageTypeKey = 100 + iota - __MessageTypeClientToServer - 1
	MessageTypeStart
	// Ping is used to check if the connection is still alive and to measure latency
	// Data expected from this message:
	//
	// 0. u8 - MessageTypePing
	// 1. u32 - client message id
	MessageTypePing
)

type LobbyMessage struct {
	connId  ConnId
	msgType MessageType
	data    []byte
}

type PlayerJoinMessage struct {
	ctx            context.Context
	connId         ConnId
	conn           *websocket.Conn
	userId         int
	sendLobbyState bool
	sendJoin       bool
}

type LobbyPlayer struct {
	ctx    context.Context
	conn   *websocket.Conn
	userId int
	ready  bool
	left   bool
}

type GameLobby struct {
	index          int
	singleplayer   bool
	messageChan    chan LobbyMessage
	playerJoinChan chan PlayerJoinMessage
	stopChan       chan struct{}
	leaveChan      chan ConnId
	players        map[ConnId]*LobbyPlayer
	game           GameState
}

func newGameLobby(index int, singleplayer bool) *GameLobby {
	return &GameLobby{
		index:          index,
		singleplayer:   singleplayer,
		messageChan:    make(chan LobbyMessage),
		stopChan:       make(chan struct{}),
		leaveChan:      make(chan ConnId),
		playerJoinChan: make(chan PlayerJoinMessage),
		players:        make(map[ConnId]*LobbyPlayer),
	}
}

func (gl *GameLobby) broadcast(msgType MessageType, data []byte) {
	d := []byte{byte(msgType)}
	if data != nil {
		d = append(d, data...)
	}

	for _, p := range gl.players {
		if p.conn != nil {
			p.conn.Write(p.ctx, websocket.MessageBinary, d)
		}
	}
}

func (gl *GameLobby) start(dbConn *pgxpool.Pool) {
	for {
		select {
		case m := <-gl.playerJoinChan:
			if m.sendLobbyState {
				d := binary.LittleEndian.AppendUint32([]byte{MessageTypeLobbyState}, uint32(m.connId))
				for pcid := range gl.players {
					d = binary.LittleEndian.AppendUint32(d, uint32(pcid))
				}
				if err := m.conn.Write(m.ctx, websocket.MessageBinary, d); err != nil {
					slog.Error("unable to send message", "error", err)
					return
				}
			}

			if m.sendJoin {
				d := []byte{byte(MessageTypePlayerJoined)}
				d = binary.LittleEndian.AppendUint32(d, uint32(m.connId))
				for _, p := range gl.players {
					if p.conn != nil {
						if err := p.conn.Write(m.ctx, websocket.MessageBinary, d); err != nil {
							// unable to send message means the player disconnected, which should alreade by handled
							slog.Error("unable to send message", "error", err)
						}
					}
				}
			}

			gl.players[m.connId] = &LobbyPlayer{
				ctx:    m.ctx,
				conn:   m.conn,
				userId: m.userId,
				ready:  false,
				left:   len(gl.players) == 0,
			}
		case <-gl.stopChan:
			return
		case cid := <-gl.leaveChan:
			gl.game.status = GameStatusNone
			delete(gl.players, cid)
			gl.broadcast(MessageTypePlayerLeft, nil)
		case lm := <-gl.messageChan:
			switch lm.msgType {
			case MessageTypeKey:
				if len(lm.data) < 2 {
					slog.Error("invalid message length", "lobby_idx", gl.index, "conn_id", lm.connId, "length", len(lm.data))
					continue
				}

				keyCode := KeyCode(lm.data[0])
				pressed := lm.data[1] == 1

				player := gl.players[lm.connId]
				gl.game.setKey(player.left, keyCode, pressed)
			case MessageTypeStart:
				if gl.game.status == GameStatusEnded || gl.game.status == GameStatusNone {
					player := gl.players[lm.connId]
					if !player.ready {
						player.ready = true
						d := make([]byte, 1)
						if player.left {
							d[0] = 1
						}
						gl.broadcast(MessageTypeReady, d)
					}
				}

				ready := true
				for _, p := range gl.players {
					if p.conn != nil {
						ready = ready && p.ready
					}
				}

				if ready {
					gl.game.start(gl.players)
					gl.broadcast(MessageTypeStarted, gl.game.encode())
					slog.Info("game started", "lobby_idx", gl.index)
				}
			}
		default:
			// update

			if gl.game.status != GameStatusPlaying {
				gl.game.advanceTime()
				continue
			}

			winner, winnerLeft := gl.game.update()
			if winner {
				slog.Info("game ended", "lobby_idx", gl.index, "winner_left", winnerLeft)

				d := make([]byte, 1)
				if winnerLeft {
					d[0] = 1
				}
				gl.broadcast(MessageTypeGameEnd, d)

				// update db stats
				slog.Info("updating db stats", "lobby_idx", gl.index)

				batch := pgx.Batch{}
				col := func(c string) string {
					if gl.singleplayer {
						return fmt.Sprintf("sp_%s", c)
					}
					return c
				}

				for _, p := range gl.players {
					if p.userId > -1 {
						var c string
						if p.left == winnerLeft {
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
					} else {
						slog.Info("updated stats", "lobby_idx", gl.index)
					}
					br.Close()
				}

				slog.Info("broadcasting saved", "lobby_idx", gl.index)
				gl.broadcast(MessageTypeSaved, nil)
			} else {
				gl.broadcast(MessageTypeGameState, gl.game.encode())
			}

			// time

			gl.game.advanceTime()
		}
	}
}

type Lobbies struct {
	lobbies [10]*GameLobby
	// lock is used to prevent cocnurrent access to the lobbies array
	// once lobby is found, and created, it can only be accessed by one goroutine
	lock       sync.Mutex
	free, used int
}

func newLobbies() *Lobbies {
	const maxLobbies = 10
	return &Lobbies{
		lobbies: [maxLobbies]*GameLobby{},
		free:    maxLobbies,
		used:    0,
	}
}

func (l *Lobbies) findFree(singleplayer bool) (lobby *GameLobby) {
	l.lock.Lock()
	defer func() {
		if lobby != nil {
			slog.Info("lobby created", "lobby_idx", lobby.index, "free", l.free, "used", l.used)
		} else {
			slog.Info("lobbies full", "free", l.free, "used", l.used)
		}
		l.lock.Unlock()
	}()

	if !singleplayer {
		// find lobby with single player
		for _, lobby = range l.lobbies {
			if lobby != nil && !lobby.singleplayer && len(lobby.players) == 1 {
				return
			}
		}
	}

	var idx int
	for idx, lobby = range l.lobbies {
		if lobby == nil {
			l.lobbies[idx] = newGameLobby(idx, singleplayer)
			lobby = l.lobbies[idx]
			l.used += 1
			l.free -= 1
			break
		}
	}

	return
}

func (l *Lobbies) freeLobby(lobby *GameLobby, playerLeftConnId ConnId) {
	l.lock.Lock()
	defer l.lock.Unlock()

	if !lobby.singleplayer && len(lobby.players) == 2 {
		lobby.leaveChan <- playerLeftConnId

		slog.Info("player left", "lobby_idx", lobby.index, "player_left_conn_id", playerLeftConnId)
	} else {
		lobby.stopChan <- struct{}{}
		l.lobbies[lobby.index] = nil

		l.used -= 1
		l.free += 1

		slog.Info("lobby deleted", "lobby_idx", lobby.index, "free", l.free, "used", l.used, "player_left_conn_id", playerLeftConnId)
	}
}

func gameHandler(dbConn *pgxpool.Pool) func(w http.ResponseWriter, r *http.Request) {
	lobbies := newLobbies()

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
		lobby := lobbies.findFree(singleplayer)
		defer lobbies.freeLobby(lobby, connId)

		logReqInfo(r, "websocket connection accepted", "conn_id", connId, "user_id", userId)

		if lobby == nil {
			logReqInfo(r, "no free lobby found", "user_id", userId)
			if err := conn.Write(r.Context(), websocket.MessageBinary, []byte{byte(MessageTypeFull)}); err != nil {
				logReqError(r, "unable to send message", "error", err)
				return
			}
			return
		}

		if singleplayer {
			logReqInfo(r, "starting singleplayer game", "user_id", userId)
			go lobby.start(dbConn)

			lobby.playerJoinChan <- PlayerJoinMessage{ctx: r.Context(), connId: nextConnId(), userId: -1}
			lobby.playerJoinChan <- PlayerJoinMessage{ctx: r.Context(), conn: conn, connId: connId, userId: userId, sendLobbyState: true}
		} else {
			m := PlayerJoinMessage{
				ctx:            r.Context(),
				conn:           conn,
				connId:         connId,
				userId:         userId,
				sendLobbyState: true,
				sendJoin:       true,
			}

			if len(lobby.players) == 1 {
				// 1 player
				lobby.playerJoinChan <- m
			} else {
				// 0 players
				go lobby.start(dbConn)
				m.sendJoin = false
				lobby.playerJoinChan <- m
			}
		}

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

			switch m {
			case MessageTypePing:
				d := append([]byte{MessageTypePong}, bytes[1:]...)
				if err := conn.Write(r.Context(), websocket.MessageBinary, d); err != nil {
					logReqError(r, "unable to send message", "error", err)
					return
				}
			default:
				if lobby != nil {
					lobby.messageChan <- LobbyMessage{connId, m, bytes[1:]}
				}
			}
		}
	}
}
