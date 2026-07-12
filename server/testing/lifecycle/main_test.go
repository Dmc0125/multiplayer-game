package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

type MessageType = byte

const (
	// server -> client
	MessageTypePlayerJoined MessageType = iota
	MessageTypeFull
	MessageTypeStarted
	MessageTypeGameState
	MessageTypeGameEnd
	MessageTypeReady
	MessageTypeLobbyState
	MessageTypePlayerLeft
	MessageTypeSaved
	MessageTypePong

	// client -> server
	__MessageTypeClientToServer
	MessageTypeKey = 100 + iota - __MessageTypeClientToServer - 1
	MessageTypeStart
	MessageTypePing
)

type Client struct {
	ctx  context.Context
	conn *websocket.Conn
}

func clientConnect(url string, ctx context.Context) (*Client, error) {
	conn, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		return nil, err
	}
	return &Client{ctx, conn}, nil
}

func (c *Client) read(timeout time.Duration) (byte, []byte, error) {
	ctx, cancel := context.WithTimeout(c.ctx, timeout)
	defer cancel()

	mt, data, err := c.conn.Read(ctx)
	if err != nil {
		return 0, nil, err
	}
	if mt != websocket.MessageBinary {
		return 0, nil, errors.New("unexpected message type")
	}
	if len(data) < 1 {
		return 0, nil, errors.New("invalid message length, expected at least 1 byte")
	}
	return data[0], data[1:], nil
}

func (c *Client) send(msgType MessageType, data []byte, timeout time.Duration) error {
	d := []byte{byte(msgType)}
	if data != nil {
		d = append(d, data...)
	}
	ctx, cancel := context.WithTimeout(c.ctx, timeout)
	defer cancel()
	return c.conn.Write(ctx, websocket.MessageBinary, d)
}

func (c *Client) sendStart(timeout time.Duration) error {
	return c.send(MessageTypeStart, nil, timeout)
}

const WS_URL = "ws://localhost:8080/api/game"
const defaultTimeout = 2 * time.Second

func TestSingleplayerLifecycle(t *testing.T) {
	log.Println("[SINGLEPLAYER] starting singleplayer lifecycle test")

	client, err := clientConnect(fmt.Sprintf("%s?singleplayer=1", WS_URL), context.Background())
	require.NoError(t, err)
	defer client.conn.Close(websocket.StatusNormalClosure, "")

	{
		log.Println("[SINGLEPLAYER] should receive lobby state")
		msgType, _, err := client.read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, MessageTypeLobbyState, msgType)
	}

	{
		log.Println("[SINGLEPLAYER] send start, should receive ready and then started")
		err := client.send(MessageTypeStart, nil, defaultTimeout)
		require.NoError(t, err)

		msgType, data, err := client.read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, MessageTypeReady, msgType)
		require.Equal(t, []byte{0}, data)

		msgType, _, err = client.read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, MessageTypeStarted, msgType)
	}

	{
		log.Println("[SINGLEPLAYER] should receive game state")
		msgType, _, err := client.read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, MessageTypeGameState, msgType)
	}

	ended := false
	{
		log.Println("[SINGLEPLAYER] should receive game end")
		deadline := time.Now().Add(40 * time.Second)
		for time.Now().Before(deadline) {
			msgType, _, err := client.read(defaultTimeout)
			if !errors.Is(err, context.DeadlineExceeded) {
				require.NoError(t, err)
			}
			if msgType == MessageTypeGameEnd {
				ended = true
				break
			}
		}
	}

	if ended {
		log.Println("[SINGLEPLAYER] should receive saved")
		msgType, _, err := client.read(10 * time.Second)
		require.NoError(t, err)
		require.Equal(t, MessageTypeSaved, msgType)
	}

}

func TestMultiPlayerLifecycle(t *testing.T) {
	log.Println("[MULTIPLAYER] starting multiplayer lifecycle test")

	c1, err := clientConnect(fmt.Sprintf("%s?singleplayer=0", WS_URL), context.Background())
	require.NoError(t, err)
	defer c1.conn.Close(websocket.StatusNormalClosure, "")

	{
		log.Println("[MULTIPLAYER] should receive lobby state")
		msgType, _, err := c1.read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, MessageTypeLobbyState, msgType)
	}

	c2, err := clientConnect(fmt.Sprintf("%s?singleplayer=0", WS_URL), context.Background())
	require.NoError(t, err)
	defer c2.conn.Close(websocket.StatusNormalClosure, "")
	var c2Id uint32

	{
		log.Println("[MULTIPLAYER] should receive lobby state")
		msgType, data, err := c2.read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, MessageTypeLobbyState, msgType)

		c2Id = binary.LittleEndian.Uint32(data)
	}

	{
		log.Println("[MULTIPLAYER] should receive player joined")
		msgType, data, err := c1.read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, MessageTypePlayerJoined, msgType)

		log.Println("[MULTIPLAYER] should contain correct conn id")
		require.Equal(t, c2Id, binary.LittleEndian.Uint32(data))
	}

	{
		log.Println("[MULTIPLAYER] c1 send start, both should receive ready")
		err := c1.send(MessageTypeStart, nil, defaultTimeout)
		require.NoError(t, err)

		msgType, data, err := c1.read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, MessageTypeReady, msgType)
		require.Equal(t, []byte{1}, data)

		msgType, _, err = c2.read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, MessageTypeReady, msgType)
		require.Equal(t, []byte{1}, data)
	}

	{
		log.Println("[MULTIPLAYER] c2 send start, both should receive ready")
		err := c2.send(MessageTypeStart, nil, defaultTimeout)
		require.NoError(t, err)

		msgType, data, err := c1.read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, MessageTypeReady, msgType)
		require.Equal(t, []byte{0}, data)

		msgType, _, err = c2.read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, MessageTypeReady, msgType)
		require.Equal(t, []byte{0}, data)
	}

	{
		log.Println("[MULTIPLAYER] both should receive started")
		msgType, _, err := c1.read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, MessageTypeStarted, msgType)

		msgType, _, err = c2.read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, MessageTypeStarted, msgType)
	}

	{
		log.Println("[MULTIPLAYER] should receive game end")
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			msgType, _, err := c1.read(defaultTimeout)
			if !errors.Is(err, context.DeadlineExceeded) {
				require.NoError(t, err)
			}
			if msgType != MessageTypeGameState && msgType != MessageTypeGameEnd {
				require.FailNow(t, "inalid message type", "msg_type", msgType)
			}
			if msgType == MessageTypeGameEnd {
				break
			}
		}

		for {
			msgType, _, err := c2.read(defaultTimeout)
			require.NoError(t, err)
			if msgType != MessageTypeGameState && msgType != MessageTypeGameEnd {
				require.FailNow(t, "invalid message type", "msg_type", msgType)
			}
			if msgType == MessageTypeGameEnd {
				break
			}
		}
	}

}

func TestLobbiesFull(t *testing.T) {
	log.Println("[LOBBIES FULL] starting lobbies full test")

	for i := 0; i < 10; i++ {
		c, err := clientConnect(fmt.Sprintf("%s?singleplayer=1", WS_URL), context.Background())
		require.NoError(t, err)
		defer c.conn.Close(websocket.StatusNormalClosure, "")

		// should receive lobby state
		msgType, _, err := c.read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, MessageTypeLobbyState, msgType)
	}

	client, err := clientConnect(fmt.Sprintf("%s?singleplayer=1", WS_URL), context.Background())
	require.NoError(t, err)
	defer client.conn.Close(websocket.StatusNormalClosure, "")

	log.Println("[LOBBIES FULL] should receive full")
	msgType, _, err := client.read(defaultTimeout)
	require.NoError(t, err)
	require.Equal(t, MessageTypeFull, msgType)
}

func TestMultiplayerPlayerDisconnects(t *testing.T) {
	log.Println("[MULTIPLAYER PLAYER DISCONNECTS] starting multiplayer player disconnects test")

	c1, err := clientConnect(fmt.Sprintf("%s?singleplayer=0", WS_URL), context.Background())
	require.NoError(t, err)

	c2, err := clientConnect(fmt.Sprintf("%s?singleplayer=0", WS_URL), context.Background())
	require.NoError(t, err)
	defer c2.conn.Close(websocket.StatusNormalClosure, "")

	err = c1.send(MessageTypeStart, nil, defaultTimeout)
	require.NoError(t, err)
	err = c2.send(MessageTypeStart, nil, defaultTimeout)
	require.NoError(t, err)

	timer := time.NewTimer(100 * time.Millisecond)
	<-timer.C
	c1.conn.Close(websocket.StatusNormalClosure, "")

	// c2 should receive player left
	for {
		// drain game states
		msgType, _, err := c2.read(defaultTimeout)
		require.NoError(t, err)
		switch msgType {
		case MessageTypeGameState, MessageTypeLobbyState, MessageTypePlayerJoined, MessageTypeStarted, MessageTypeReady:
		default:
			require.Equal(t, MessageTypePlayerLeft, msgType)
			return
		}
	}
}
