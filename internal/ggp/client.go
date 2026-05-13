package ggp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Event struct {
	Ready *Ready
	Frame *Frame
	Score *Score
	Error error
}

type Client struct {
	conn   *websocket.Conn
	Events chan Event
	done   chan struct{}
	mu     sync.Mutex
}

func Connect(ctx context.Context, endpoint string, hello Hello) (*Client, error) {
	if _, err := url.Parse(endpoint); err != nil {
		return nil, fmt.Errorf("invalid game endpoint: %w", err)
	}

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := dialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return nil, err
	}

	client := &Client{
		conn:   conn,
		Events: make(chan Event, 64),
		done:   make(chan struct{}),
	}

	if err := client.write(hello); err != nil {
		_ = conn.Close()
		return nil, err
	}

	go client.readLoop()
	return client, nil
}

func (c *Client) SendInput(input Input) error {
	input.Type = TypeInput
	if input.Kind == "" {
		input.Kind = "key"
	}
	return c.write(input)
}

func (c *Client) SendResize(cols, rows int) error {
	return c.write(Resize{Type: TypeResize, Cols: cols, Rows: rows})
}

func (c *Client) SendFocus(focused bool) error {
	return c.write(Focus{Type: TypeFocus, Focused: focused})
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.done:
		return nil
	default:
		close(c.done)
		return c.conn.Close()
	}
}

func (c *Client) write(value any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.done:
		return errors.New("game connection is closed")
	default:
		return c.conn.WriteJSON(value)
	}
}

func (c *Client) readLoop() {
	defer close(c.Events)
	defer func() { _ = c.Close() }()

	for {
		_, payload, err := c.conn.ReadMessage()
		if err != nil {
			c.emit(Event{Error: err})
			return
		}

		var envelope Envelope
		if err := json.Unmarshal(payload, &envelope); err != nil {
			c.emit(Event{Error: err})
			continue
		}

		switch envelope.Type {
		case TypeReady:
			var ready Ready
			if err := json.Unmarshal(payload, &ready); err != nil {
				c.emit(Event{Error: err})
				continue
			}
			c.emit(Event{Ready: &ready})
		case TypeFrame:
			var frame Frame
			if err := json.Unmarshal(payload, &frame); err != nil {
				c.emit(Event{Error: err})
				continue
			}
			c.emit(Event{Frame: &frame})
		case TypeScore:
			var score Score
			if err := json.Unmarshal(payload, &score); err != nil {
				c.emit(Event{Error: err})
				continue
			}
			c.emit(Event{Score: &score})
		case TypeError:
			var gameErr Error
			if err := json.Unmarshal(payload, &gameErr); err != nil {
				c.emit(Event{Error: err})
				continue
			}
			c.emit(Event{Error: errors.New(gameErr.Message)})
		}
	}
}

func (c *Client) emit(event Event) {
	select {
	case <-c.done:
	case c.Events <- event:
	default:
	}
}
