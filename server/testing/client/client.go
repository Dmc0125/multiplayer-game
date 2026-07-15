package client

import (
	"context"
	"errors"
	"server/src/core"
	"time"

	"github.com/coder/websocket"
)

type Client struct {
	ctx  context.Context
	Conn *websocket.Conn
}

func ClientConnect(url string, ctx context.Context) (*Client, error) {
	conn, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		return nil, err
	}
	return &Client{ctx, conn}, nil
}

func (c *Client) Close() error {
	return c.Conn.Close(websocket.StatusNormalClosure, "")
}

func (c *Client) Read(timeout time.Duration) (byte, []byte, error) {
	ctx, cancel := context.WithTimeout(c.ctx, timeout)
	defer cancel()

	mt, data, err := c.Conn.Read(ctx)
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

func (c *Client) Send(msgType core.MessageType, data []byte, timeout time.Duration) error {
	d := []byte{byte(msgType)}
	if data != nil {
		d = append(d, data...)
	}
	ctx, cancel := context.WithTimeout(c.ctx, timeout)
	defer cancel()
	return c.Conn.Write(ctx, websocket.MessageBinary, d)
}

func (c *Client) SendStart(timeout time.Duration) error {
	return c.Send(core.MessageTypeStart, nil, timeout)
}
