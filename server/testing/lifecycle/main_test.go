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

func readMessage(
	t *testing.T,
	c *client.Client,
	timeout time.Duration,
) (client.MessageType, []byte) {
	t.Helper()

	msgType, data, err := c.Read(timeout)
	require.NoError(t, err)

	return msgType, data
}

func readLobbyState(
	t *testing.T,
	c *client.Client,
	timeout time.Duration,
) (uint32, []uint32) {
	t.Helper()

	msgType, data := readMessage(t, c, timeout)
	require.Equal(t, client.MessageTypeLobbyState, msgType)
	require.GreaterOrEqual(t, len(data), 4)

	connID := binary.LittleEndian.Uint32(data[:4])

	otherIDs := make([]uint32, 0, (len(data)-4)/4)
	for offset := 4; offset+4 <= len(data); offset += 4 {
		otherIDs = append(
			otherIDs,
			binary.LittleEndian.Uint32(data[offset:offset+4]),
		)
	}

	return connID, otherIDs
}

func requireWinnerGameState(
	t *testing.T,
	data []byte,
) {
	t.Helper()

	// GameEventTypeWinner:
	//
	// 0. u8 - event type
	// 1. u8 - winner left flag
	require.Len(t, data, 2)
	require.Equal(t, byte(client.GameEventTypeWinner), data[0])
	require.Contains(t, []byte{0, 1}, data[1])
}

func readUntilWinner(
	t *testing.T,
	c *client.Client,
	timeout time.Duration,
) {
	t.Helper()

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		msgType, data, err := c.Read(defaultTimeout)

		if errors.Is(err, context.DeadlineExceeded) {
			continue
		}

		require.NoError(t, err)

		switch msgType {
		case client.MessageTypeGameState:
			if len(data) > 0 && data[0] == byte(client.GameEventTypeWinner) {
				requireWinnerGameState(t, data)
				return
			}
		default:
			t.Fatalf("unexpected message type while waiting for winner: %d", msgType)
		}
	}

	t.Fatalf("timed out waiting for winner game state")
}

func TestSingleplayerLifecycle(t *testing.T) {
	log.Println("[SINGLEPLAYER] starting singleplayer lifecycle test")

	c, err := client.ClientConnect(
		fmt.Sprintf("%s?singleplayer=1", WS_URL),
		context.Background(),
	)
	require.NoError(t, err)
	defer c.Close()

	{
		log.Println("[SINGLEPLAYER] should receive lobby state")

		ownID, otherIDs := readLobbyState(t, c, defaultTimeout)

		require.NotZero(t, ownID)
		require.Len(t, otherIDs, 1)
	}

	{
		log.Println(
			"[SINGLEPLAYER] send start, should receive ready and then started",
		)

		err := c.Send(client.MessageTypeStart, nil, defaultTimeout)
		require.NoError(t, err)

		msgType, data := readMessage(t, c, defaultTimeout)
		require.Equal(t, client.MessageTypeReady, msgType)
		require.Equal(t, []byte{0}, data)

		msgType, _ = readMessage(t, c, defaultTimeout)
		require.Equal(t, client.MessageTypeStarted, msgType)
	}

	{
		log.Println("[SINGLEPLAYER] should receive game state")

		msgType, data := readMessage(t, c, defaultTimeout)
		require.Equal(t, client.MessageTypeGameState, msgType)
		require.NotEmpty(t, data)

		require.Equal(t, byte(client.GameEventTypeCountdown), data[0])
	}

	{
		log.Println("[SINGLEPLAYER] should receive winner game state")
		readUntilWinner(t, c, 40*time.Second)
	}

	{
		log.Println("[SINGLEPLAYER] should receive saved")

		msgType, data := readMessage(t, c, 10*time.Second)
		require.Equal(t, client.MessageTypeSaved, msgType)
		require.Empty(t, data)
	}
}
func TestMultiPlayerLifecycle(t *testing.T) {
	log.Println("[MULTIPLAYER] starting multiplayer lifecycle test")

	c1, err := client.ClientConnect(
		fmt.Sprintf("%s?singleplayer=0", WS_URL),
		context.Background(),
	)
	require.NoError(t, err)
	defer c1.Close()

	c1ID, c1OtherIDs := readLobbyState(t, c1, defaultTimeout)
	require.NotZero(t, c1ID)
	require.Empty(t, c1OtherIDs)

	c2, err := client.ClientConnect(
		fmt.Sprintf("%s?singleplayer=0", WS_URL),
		context.Background(),
	)
	require.NoError(t, err)
	defer c2.Close()

	c2ID, c2OtherIDs := readLobbyState(t, c2, defaultTimeout)
	require.NotZero(t, c2ID)
	require.Equal(t, []uint32{c1ID}, c2OtherIDs)

	{
		log.Println("[MULTIPLAYER] c1 should receive player joined")

		msgType, data := readMessage(t, c1, defaultTimeout)
		require.Equal(t, client.MessageTypePlayerJoined, msgType)
		require.Len(t, data, 4)
		require.Equal(t, c2ID, binary.LittleEndian.Uint32(data))
	}

	{
		log.Println(
			"[MULTIPLAYER] c1 sends start; both players receive ready",
		)

		err := c1.Send(client.MessageTypeStart, nil, defaultTimeout)
		require.NoError(t, err)

		msgType, data := readMessage(t, c1, defaultTimeout)
		require.Equal(t, client.MessageTypeReady, msgType)
		require.Equal(t, []byte{1}, data)

		msgType, data = readMessage(t, c2, defaultTimeout)
		require.Equal(t, client.MessageTypeReady, msgType)
		require.Equal(t, []byte{1}, data)
	}

	{
		log.Println(
			"[MULTIPLAYER] c2 sends start; both players receive ready",
		)

		err := c2.Send(client.MessageTypeStart, nil, defaultTimeout)
		require.NoError(t, err)

		msgType, data := readMessage(t, c1, defaultTimeout)
		require.Equal(t, client.MessageTypeReady, msgType)
		require.Equal(t, []byte{0}, data)

		msgType, data = readMessage(t, c2, defaultTimeout)
		require.Equal(t, client.MessageTypeReady, msgType)
		require.Equal(t, []byte{0}, data)
	}

	{
		log.Println("[MULTIPLAYER] both players should receive started")

		msgType, _ := readMessage(t, c1, defaultTimeout)
		require.Equal(t, client.MessageTypeStarted, msgType)

		msgType, _ = readMessage(t, c2, defaultTimeout)
		require.Equal(t, client.MessageTypeStarted, msgType)
	}

	{
		log.Println("[MULTIPLAYER] both players should receive winner state")

		readUntilWinner(t, c1, 30*time.Second)
		readUntilWinner(t, c2, 30*time.Second)
	}

	{
		log.Println("[MULTIPLAYER] both players should receive saved")

		msgType, data := readMessage(t, c1, defaultTimeout)
		require.Equal(t, client.MessageTypeSaved, msgType)
		require.Empty(t, data)

		msgType, data = readMessage(t, c2, defaultTimeout)
		require.Equal(t, client.MessageTypeSaved, msgType)
		require.Empty(t, data)
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
	log.Println(
		"[MULTIPLAYER PLAYER DISCONNECTS] starting multiplayer disconnect test",
	)

	c1, err := client.ClientConnect(
		fmt.Sprintf("%s?singleplayer=0", WS_URL),
		context.Background(),
	)
	require.NoError(t, err)

	c2, err := client.ClientConnect(
		fmt.Sprintf("%s?singleplayer=0", WS_URL),
		context.Background(),
	)
	require.NoError(t, err)
	defer c2.Close()

	// Consume the initial lobby state from c1.
	readLobbyState(t, c1, defaultTimeout)

	// Consume c2's lobby state and verify that it sees c1.
	_, otherIDs := readLobbyState(t, c2, defaultTimeout)
	require.Len(t, otherIDs, 1)

	// c1 receives the join notification.
	msgType, _ := readMessage(t, c1, defaultTimeout)
	require.Equal(t, client.MessageTypePlayerJoined, msgType)

	err = c1.Send(client.MessageTypeStart, nil, defaultTimeout)
	require.NoError(t, err)

	err = c2.Send(client.MessageTypeStart, nil, defaultTimeout)
	require.NoError(t, err)

	// Drain the expected ready/started messages before disconnecting.
	msgType, _ = readMessage(t, c1, defaultTimeout)
	require.Equal(t, client.MessageTypeReady, msgType)

	msgType, _ = readMessage(t, c2, defaultTimeout)
	require.Equal(t, client.MessageTypeReady, msgType)

	msgType, _ = readMessage(t, c1, defaultTimeout)
	require.Equal(t, client.MessageTypeReady, msgType)

	msgType, _ = readMessage(t, c2, defaultTimeout)
	require.Equal(t, client.MessageTypeReady, msgType)

	msgType, _ = readMessage(t, c1, defaultTimeout)
	require.Equal(t, client.MessageTypeStarted, msgType)

	msgType, _ = readMessage(t, c2, defaultTimeout)
	require.Equal(t, client.MessageTypeStarted, msgType)

	require.NoError(t, c1.Close())

	{
		log.Println("[MULTIPLAYER PLAYER DISCONNECTS] c2 should receive player left")

		deadline := time.Now().Add(defaultTimeout)

		for time.Now().Before(deadline) {
			msgType, data, err := c2.Read(defaultTimeout)
			require.NoError(t, err)

			switch msgType {
			case client.MessageTypeGameState:
				// Game states may already be queued.
				require.NotEmpty(t, data)

			case client.MessageTypePlayerLeft:
				require.Empty(t, data)
				return

			default:
				t.Fatalf(
					"unexpected message type while waiting for player left: %d",
					msgType,
				)
			}
		}

		t.Fatal("timed out waiting for player left")
	}
}
