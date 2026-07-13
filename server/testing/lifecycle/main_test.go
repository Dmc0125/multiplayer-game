package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"server/testing/client"
)

const WS_URL = "ws://localhost:8080/api/game"
const defaultTimeout = 2 * time.Second

func TestSingleplayerLifecycle(t *testing.T) {
	log.Println("[SINGLEPLAYER] starting singleplayer lifecycle test")

	c, err := client.ClientConnect(fmt.Sprintf("%s?singleplayer=1", WS_URL), context.Background())
	require.NoError(t, err)
	defer c.Close()

	{
		log.Println("[SINGLEPLAYER] should receive lobby state")
		msgType, _, err := c.Read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, client.MessageTypeLobbyState, msgType)
	}

	{
		log.Println("[SINGLEPLAYER] send start, should receive ready and then started")
		err := c.Send(client.MessageTypeStart, nil, defaultTimeout)
		require.NoError(t, err)

		msgType, data, err := c.Read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, client.MessageTypeReady, msgType)
		require.Equal(t, []byte{0}, data)

		msgType, _, err = c.Read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, client.MessageTypeStarted, msgType)
	}

	{
		log.Println("[SINGLEPLAYER] should receive game state")
		msgType, _, err := c.Read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, client.MessageTypeGameState, msgType)
	}

	ended := false
	{
		log.Println("[SINGLEPLAYER] should receive game end")
		deadline := time.Now().Add(40 * time.Second)
		for time.Now().Before(deadline) {
			msgType, _, err := c.Read(defaultTimeout)
			if !errors.Is(err, context.DeadlineExceeded) {
				require.NoError(t, err)
			}
			if msgType == client.MessageTypeGameEnd {
				ended = true
				break
			}
		}
	}

	if ended {
		log.Println("[SINGLEPLAYER] should receive saved")
		msgType, _, err := c.Read(10 * time.Second)
		require.NoError(t, err)
		require.Equal(t, client.MessageTypeSaved, msgType)
	}

}

func TestMultiPlayerLifecycle(t *testing.T) {
	log.Println("[MULTIPLAYER] starting multiplayer lifecycle test")

	c1, err := client.ClientConnect(fmt.Sprintf("%s?singleplayer=0", WS_URL), context.Background())
	require.NoError(t, err)
	defer c1.Close()

	{
		log.Println("[MULTIPLAYER] should receive lobby state")
		msgType, _, err := c1.Read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, client.MessageTypeLobbyState, msgType)
	}

	c2, err := client.ClientConnect(fmt.Sprintf("%s?singleplayer=0", WS_URL), context.Background())
	require.NoError(t, err)
	defer c2.Close()
	var c2Id uint32

	{
		log.Println("[MULTIPLAYER] should receive lobby state")
		msgType, data, err := c2.Read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, client.MessageTypeLobbyState, msgType)

		c2Id = binary.LittleEndian.Uint32(data)
	}

	{
		log.Println("[MULTIPLAYER] should receive player joined")
		msgType, data, err := c1.Read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, client.MessageTypePlayerJoined, msgType)

		log.Println("[MULTIPLAYER] should contain correct conn id")
		require.Equal(t, c2Id, binary.LittleEndian.Uint32(data))
	}

	{
		log.Println("[MULTIPLAYER] c1 send start, both should receive ready")
		err := c1.Send(client.MessageTypeStart, nil, defaultTimeout)
		require.NoError(t, err)

		msgType, data, err := c1.Read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, client.MessageTypeReady, msgType)
		require.Equal(t, []byte{1}, data)

		msgType, _, err = c2.Read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, client.MessageTypeReady, msgType)
		require.Equal(t, []byte{1}, data)
	}

	{
		log.Println("[MULTIPLAYER] c2 send start, both should receive ready")
		err := c2.Send(client.MessageTypeStart, nil, defaultTimeout)
		require.NoError(t, err)

		msgType, data, err := c1.Read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, client.MessageTypeReady, msgType)
		require.Equal(t, []byte{0}, data)

		msgType, _, err = c2.Read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, client.MessageTypeReady, msgType)
		require.Equal(t, []byte{0}, data)
	}

	{
		log.Println("[MULTIPLAYER] both should receive started")
		msgType, _, err := c1.Read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, client.MessageTypeStarted, msgType)

		msgType, _, err = c2.Read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, client.MessageTypeStarted, msgType)
	}

	{
		log.Println("[MULTIPLAYER] should receive game end")
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			msgType, _, err := c1.Read(defaultTimeout)
			if !errors.Is(err, context.DeadlineExceeded) {
				require.NoError(t, err)
			}
			if msgType != client.MessageTypeGameState && msgType != client.MessageTypeGameEnd {
				require.FailNow(t, "inalid message type", "msg_type", msgType)
			}
			if msgType == client.MessageTypeGameEnd {
				break
			}
		}

		for {
			msgType, _, err := c2.Read(defaultTimeout)
			require.NoError(t, err)
			if msgType != client.MessageTypeGameState && msgType != client.MessageTypeGameEnd {
				require.FailNow(t, "invalid message type", "msg_type", msgType)
			}
			if msgType == client.MessageTypeGameEnd {
				break
			}
		}
	}

}

func TestLobbiesFull(t *testing.T) {
	log.Println("[LOBBIES FULL] starting lobbies full test")

	for i := 0; i < 10; i++ {
		c, err := client.ClientConnect(fmt.Sprintf("%s?singleplayer=1", WS_URL), context.Background())
		require.NoError(t, err)
		defer c.Close()

		// should receive lobby state
		msgType, _, err := c.Read(defaultTimeout)
		require.NoError(t, err)
		require.Equal(t, client.MessageTypeLobbyState, msgType)
	}

	c, err := client.ClientConnect(fmt.Sprintf("%s?singleplayer=1", WS_URL), context.Background())
	require.NoError(t, err)
	defer c.Close()

	log.Println("[LOBBIES FULL] should receive full")
	msgType, _, err := c.Read(defaultTimeout)
	require.NoError(t, err)
	require.Equal(t, client.MessageTypeFull, msgType)
}

func TestMultiplayerPlayerDisconnects(t *testing.T) {
	log.Println("[MULTIPLAYER PLAYER DISCONNECTS] starting multiplayer player disconnects test")

	c1, err := client.ClientConnect(fmt.Sprintf("%s?singleplayer=0", WS_URL), context.Background())
	require.NoError(t, err)

	c2, err := client.ClientConnect(fmt.Sprintf("%s?singleplayer=0", WS_URL), context.Background())
	require.NoError(t, err)
	defer c2.Close()

	err = c1.Send(client.MessageTypeStart, nil, defaultTimeout)
	require.NoError(t, err)
	err = c2.Send(client.MessageTypeStart, nil, defaultTimeout)
	require.NoError(t, err)

	timer := time.NewTimer(100 * time.Millisecond)
	<-timer.C
	c1.Close()

	// c2 should receive player left
	for {
		// drain game states
		msgType, _, err := c2.Read(defaultTimeout)
		require.NoError(t, err)
		switch msgType {
		case client.MessageTypeGameState, client.MessageTypeLobbyState, client.MessageTypePlayerJoined, client.MessageTypeStarted, client.MessageTypeReady:
		default:
			require.Equal(t, client.MessageTypePlayerLeft, msgType)
			return
		}
	}
}
