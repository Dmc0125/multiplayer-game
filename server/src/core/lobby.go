package core

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

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

type latencyHistorgam struct {
	name    string
	limits  []time.Duration
	buckets []atomic.Uint64
}

func NewLatencyHistogram(name string) *latencyHistorgam {
	limits := [...]time.Duration{
		100 * time.Microsecond,
		500 * time.Microsecond,
		1 * time.Millisecond,
		5 * time.Millisecond,
		10 * time.Millisecond,
		25 * time.Millisecond,
		50 * time.Millisecond,
		100 * time.Millisecond,
		250 * time.Millisecond,
		500 * time.Millisecond,
		1 * time.Second,
	}
	buckets := make([]atomic.Uint64, len(limits)+1)
	return &latencyHistorgam{
		name:    name,
		limits:  limits[:],
		buckets: buckets,
	}
}

func (h *latencyHistorgam) observer(value time.Duration) {
	for i, limit := range h.limits {
		if value <= limit {
			h.buckets[i].Add(1)
			return
		}
	}
	h.buckets[len(h.buckets)-1].Add(1)
}

func (h *latencyHistorgam) log(every time.Duration) {
	ticker := time.NewTicker(every)

	for {
		<-ticker.C

		attrs := make([]any, 0)
		for i, limit := range h.limits {
			value := h.buckets[i].Swap(0)
			attrs = append(attrs, fmt.Sprintf("<_%s", limit.String()), value)

		}

		attrs = append(attrs, "spill", h.buckets[len(h.buckets)-1].Swap(0))
		slog.Info(h.name, attrs...)
	}
}

var websocketWriteLatency = NewLatencyHistogram("websocket_write_latency")

func connWrite(ctx context.Context, conn *websocket.Conn, mt websocket.MessageType, data []byte) error {
	start := time.Now()
	defer websocketWriteLatency.observer(time.Since(start))
	return conn.Write(ctx, mt, data)
}

type MessageType = byte

// server -> client
const (
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
)

// client -> server
const (
	MessageTypeKey = 100 + iota
	MessageTypeStart
	// Ping is used to check if the connection is still alive and to measure latency
	// Data expected from this message:
	//
	// 0. u8 - MessageTypePing
	// 1. u32 - client message id
	MessageTypePing
)

type GameLobbyMessage struct {
	connId  ConnId
	msgType MessageType
	data    []byte
}

type GameLobbyPlayerJoinMessage struct {
	ctx            context.Context
	connId         ConnId
	conn           *websocket.Conn
	userId         int
	sendLobbyState bool
	sendJoin       bool
	done           chan bool
}

type GameLobbyLeaveResult struct {
	index       int
	playerCount int
}

type GameLobbyLeaveMessage struct {
	connId ConnId
	done   chan GameLobbyLeaveResult
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
	messageChan    chan GameLobbyMessage
	playerJoinChan chan GameLobbyPlayerJoinMessage
	leaveChan      chan GameLobbyLeaveMessage
	players        map[ConnId]*LobbyPlayer
	game           GameState
}

func newGameLobby(index int, singleplayer bool) *GameLobby {
	return &GameLobby{
		index:          index,
		singleplayer:   singleplayer,
		messageChan:    make(chan GameLobbyMessage),
		leaveChan:      make(chan GameLobbyLeaveMessage),
		playerJoinChan: make(chan GameLobbyPlayerJoinMessage),
		players:        make(map[ConnId]*LobbyPlayer),
	}
}

func (gl *GameLobby) broadcast(msgType MessageType, data []byte) {
	lslog := slog.With("lobby_idx", gl.index)

	d := []byte{byte(msgType)}
	if data != nil {
		d = append(d, data...)
	}

	for _, p := range gl.players {
		if p.conn != nil {
			ctx, cancel := context.WithTimeout(p.ctx, 2*time.Second)
			if err := connWrite(ctx, p.conn, websocket.MessageBinary, d); err != nil {
				lslog.Error("unable to send message", "error", err)
			}
			cancel()
		}
	}
}

func (gl *GameLobby) start(ctx context.Context, dbConn *pgxpool.Pool, lobbiesMessagesChan chan LobbiesMessage) {
	defer func() {
		if err := recover(); err != nil {
			slog.Error("panic in lobby loop", "error", err)
		}
	}()

	lslog := slog.With("lobby_idx", gl.index)

	addPlayer := func(m GameLobbyPlayerJoinMessage) {
		if m.sendLobbyState {
			d := binary.LittleEndian.AppendUint32([]byte{MessageTypeLobbyState}, uint32(m.connId))
			for pcid := range gl.players {
				d = binary.LittleEndian.AppendUint32(d, uint32(pcid))
			}

			ctx, cancel := context.WithTimeout(m.ctx, 2*time.Second)
			defer cancel()
			if err := connWrite(ctx, m.conn, websocket.MessageBinary, d); err != nil {
				m.done <- false
				lslog.Error("unable to send message", "error", err)
				return
			}
		}

		if m.sendJoin {
			d := []byte{byte(MessageTypePlayerJoined)}
			d = binary.LittleEndian.AppendUint32(d, uint32(m.connId))

			for _, p := range gl.players {
				if p.conn != nil {
					ctx, cancel := context.WithTimeout(m.ctx, 2*time.Second)
					defer cancel()
					if err := connWrite(ctx, p.conn, websocket.MessageBinary, d); err != nil {
						// unable to send message means the player disconnected, which should alreade be handled
						lslog.Error("unable to send message", "error", err)
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
		m.done <- true
	}

	handleUpdate := func() {
		defer gl.game.advanceTime()

		if !gl.game.running() {
			return
		}

		event := gl.game.update()

		switch event.Type {
		case GameEventTypeCountdown, GameEventTypeState:
			gl.broadcast(MessageTypeGameState, event.encode())
		case GameEventTypeWinner:
			lslog.Info("game ended", "winner_left", event.WinnerLeft)
			gl.broadcast(MessageTypeGameState, event.encode())

			// update db stats
			lslog.Debug("updating db stats")

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
					if p.left == event.WinnerLeft {
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
					lslog.Error("unable to update stats", "error", err)
				} else {
					lslog.Debug("updated stats")
				}
				br.Close()
			}

			gl.broadcast(MessageTypeSaved, nil)
		}
	}

	updateTicker := time.NewTicker(FRAME_TIME_SECONDS)
	defer updateTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case m := <-gl.playerJoinChan:
			addPlayer(m)
		case m := <-gl.leaveChan:
			lslog.Debug("player left", "player_left_conn_id", m.connId)

			if !gl.singleplayer && len(gl.players) == 2 {
				gl.game.status = GameStatusNone
				delete(gl.players, m.connId)
				gl.broadcast(MessageTypePlayerLeft, nil)

				m.done <- GameLobbyLeaveResult{index: gl.index, playerCount: len(gl.players)}
			} else {
				// either last player in mutliplayer game or singleplayer game
				// so lobby should be deleted
				m.done <- GameLobbyLeaveResult{index: gl.index, playerCount: 0}
				return
			}
		case lm := <-gl.messageChan:
			switch lm.msgType {
			case MessageTypePing:
				player := gl.players[lm.connId]
				ctx, cancel := context.WithTimeout(ctx, 2*time.Second)

				d := append([]byte{MessageTypePong}, lm.data...)
				if err := connWrite(ctx, player.conn, websocket.MessageBinary, d); err != nil {
					lslog.Error("unable to send message", "error", err)
				}

				cancel()
			case MessageTypeKey:
				if len(lm.data) < 2 {
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
					for _, p := range gl.players {
						if p.conn != nil {
							p.ready = false
						}
					}

					startEvent := gl.game.start(gl.players)
					gl.broadcast(MessageTypeStarted, startEvent.encode())
					lslog.Info("game started")
				}
			}
		case <-updateTicker.C:
			handleUpdate()
		}
	}
}

type LobbiesMessageType uint8

const (
	LobbiesMessageTypeJoin LobbiesMessageType = iota
	LobbiesMessageTypeLeave
)

type LobbiesJoinResultType uint8

const (
	LobbiesJoinResultSuccess LobbiesJoinResultType = iota
	LobbiesJoinResultClientDisconnected
	LobbiesJoinResultFull
)

type LobbiesJoinResult struct {
	kind                 LobbiesJoinResultType
	gameLobbyMessageChan chan GameLobbyMessage
	gameLobbyLeaveChan   chan GameLobbyLeaveMessage
}

type LobbiesMessage struct {
	msgType LobbiesMessageType

	// join
	ctx          context.Context
	singleplayer bool
	connId       ConnId
	conn         *websocket.Conn
	userId       int
	joinResult   chan LobbiesJoinResult

	// leave
	lobbyLeaveChan chan GameLobbyLeaveMessage
}

type LobbyStatus struct {
	playerCount int
}

type Lobbies struct {
	lobbies         []*GameLobby
	lobbiesStatuses []LobbyStatus
	free, used      int
	messagesChan    chan LobbiesMessage
}

func newLobbies(count int) *Lobbies {
	lobbies := make([]*GameLobby, count)
	lobbiesStatuses := make([]LobbyStatus, count)

	return &Lobbies{
		lobbies:         lobbies,
		lobbiesStatuses: lobbiesStatuses,
		messagesChan:    make(chan LobbiesMessage),
		free:            count,
		used:            0,
	}
}

func (l *Lobbies) run(dbConn *pgxpool.Pool) {
	assignLobby := func(msg LobbiesMessage) (res LobbiesJoinResult) {
		// check if can join existing lobby
		if !msg.singleplayer {
			for idx, s := range l.lobbiesStatuses {
				if s.playerCount == 1 {
					lobby := l.lobbies[idx]
					res.gameLobbyMessageChan = lobby.messageChan
					res.gameLobbyLeaveChan = lobby.leaveChan

					done := make(chan bool, 1)
					defer close(done)
					lobby.playerJoinChan <- GameLobbyPlayerJoinMessage{
						ctx:            msg.ctx,
						connId:         msg.connId,
						conn:           msg.conn,
						userId:         msg.userId,
						sendLobbyState: true,
						sendJoin:       true,
						done:           done,
					}

					if success := <-done; success {
						l.lobbiesStatuses[idx].playerCount += 1
						res.kind = LobbiesJoinResultSuccess
					} else {
						res.kind = LobbiesJoinResultClientDisconnected
					}
					return
				}
			}
		}

		// create new lobby
		for idx, s := range l.lobbiesStatuses {
			if s.playerCount == 0 {
				l.lobbies[idx] = newGameLobby(idx, msg.singleplayer)
				lobby := l.lobbies[idx]
				res.gameLobbyMessageChan = lobby.messageChan
				res.gameLobbyLeaveChan = lobby.leaveChan

				// intentionally using context.Background() because we don't want the game lobby to
				// be cancelled if the request is cancelled
				ctx, cancel := context.WithCancel(context.Background())
				go lobby.start(ctx, dbConn, l.messagesChan)

				success := false
				var newPlayerCount int

				if msg.singleplayer {
					done := make(chan bool, 2)
					defer close(done)
					lobby.playerJoinChan <- GameLobbyPlayerJoinMessage{
						ctx:    msg.ctx,
						connId: nextConnId(),
						userId: -1,
						done:   done,
					}
					if success := <-done; !success {
						// this should be unreachable so just panic
						panic("unreachable: expected bot to join singleplayer lobby")
					}
					lobby.playerJoinChan <- GameLobbyPlayerJoinMessage{
						ctx:            msg.ctx,
						conn:           msg.conn,
						connId:         msg.connId,
						userId:         msg.userId,
						sendLobbyState: true,
						done:           done,
					}
					success = <-done

					newPlayerCount = 2
				} else {
					done := make(chan bool, 1)
					defer close(done)
					lobby.playerJoinChan <- GameLobbyPlayerJoinMessage{
						ctx:            msg.ctx,
						conn:           msg.conn,
						connId:         msg.connId,
						userId:         msg.userId,
						sendLobbyState: true,
						sendJoin:       true,
						done:           done,
					}
					success = <-done

					newPlayerCount = 1
				}

				if success {
					l.lobbiesStatuses[idx].playerCount = newPlayerCount
					res.kind = LobbiesJoinResultSuccess
				} else {
					cancel() // if the initial player can not join, stop the game lobby
					res.kind = LobbiesJoinResultClientDisconnected
				}

				return
			}
		}

		res.kind = LobbiesJoinResultFull
		return
	}

	for {
		msg := <-l.messagesChan

		switch msg.msgType {
		case LobbiesMessageTypeJoin:
			result := assignLobby(msg)
			if result.kind == LobbiesJoinResultSuccess {
				l.used += 1
				l.free -= 1
			}
			msg.joinResult <- result
		case LobbiesMessageTypeLeave:
			done := make(chan GameLobbyLeaveResult, 1)
			msg.lobbyLeaveChan <- GameLobbyLeaveMessage{
				connId: msg.connId,
				done:   done,
			}
			result := <-done
			close(done)

			if result.playerCount == 0 {
				l.free += 1
				l.used -= 1

				l.lobbies[result.index] = nil
				l.lobbiesStatuses[result.index] = LobbyStatus{}

				slog.Debug("lobby deleted", "lobby_idx", result.index, "free", l.free, "used", l.used)
			} else {
				slog.Debug("lobby status changed", "lobby_idx", result.index, "player_count", result.playerCount)
				l.lobbiesStatuses[result.index].playerCount = result.playerCount
			}
		}
	}
}

func gameHandler(prod bool, dbConn *pgxpool.Pool, lobbiesCount int) func(w http.ResponseWriter, r *http.Request) {
	go websocketWriteLatency.log(10 * time.Second)

	lobbies := newLobbies(lobbiesCount)
	go lobbies.run(dbConn)

	return func(w http.ResponseWriter, r *http.Request) {
		userId, status, err := getUserIdFromSession(dbConn, r)
		if status == http.StatusInternalServerError {
			httpErrorInternal(r, w, err, "unable to get user id")
			return
		}

		rslog := getRequestLogger(r)

		singleplayer := r.URL.Query().Get("singleplayer") == "1"
		rslog.Debug("init websocket connection", "user_id", userId, "singleplayer", singleplayer)

		var connOpts *websocket.AcceptOptions
		if !prod {
			connOpts = &websocket.AcceptOptions{
				InsecureSkipVerify: true,
			}
		}

		conn, err := websocket.Accept(w, r, connOpts)
		if err != nil {
			httpErrorInternal(r, w, err, "unable to accept websocket connection")
			return
		}
		rslog.Debug("websocket connection accepted", "user_id", userId)

		connId := nextConnId()
		joinResult := make(chan LobbiesJoinResult, 1)
		lobbies.messagesChan <- LobbiesMessage{
			msgType:      LobbiesMessageTypeJoin,
			ctx:          r.Context(),
			singleplayer: singleplayer,
			connId:       connId,
			conn:         conn,
			userId:       userId,
			joinResult:   joinResult,
		}

		switch res := <-joinResult; res.kind {
		case LobbiesJoinResultClientDisconnected:
			rslog.Debug("client disconnected", "conn_id", connId, "user_id", userId)
			return
		case LobbiesJoinResultFull:
			rslog.Debug("no free lobby found", "conn_id", connId, "user_id", userId)
			if err := connWrite(r.Context(), conn, websocket.MessageBinary, []byte{MessageTypeFull}); err != nil {
				rslog.Error("unable to send message", "error", err)
				return
			}
			return
		default:
			defer func() {
				lobbies.messagesChan <- LobbiesMessage{
					msgType:        LobbiesMessageTypeLeave,
					connId:         connId,
					lobbyLeaveChan: res.gameLobbyLeaveChan,
				}
			}()

			rslog.Debug("lobby joined", "conn_id", connId, "user_id", userId)
			for {
				msgType, bytes, err := conn.Read(r.Context())
				if err != nil {
					return
				}

				if msgType != websocket.MessageBinary {
					rslog.Error("unexpected message type", "type", msgType)
					continue
				}
				if len(bytes) < 1 {
					rslog.Error("invalid message length", "length", len(bytes))
					continue
				}

				m := MessageType(bytes[0])

				switch m {
				default:
					res.gameLobbyMessageChan <- GameLobbyMessage{connId, m, bytes[1:]}
				}
			}
		}
	}
}
