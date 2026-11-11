package hub

import (
	"errors"
	"sync"

	"github.com/gorilla/websocket"
)

type Client struct {
	Conn *websocket.Conn

	send          chan []byte
	done          chan struct{}
	stopOnce      sync.Once
	connCloseOnce sync.Once
	Nick          string
}

func NewClient(conn *websocket.Conn, sendBuffer int) *Client {
	return &Client{
		Conn: conn,
		send: make(chan []byte, sendBuffer),
		done: make(chan struct{}),
	}
}

func (c *Client) Send() <-chan []byte {
	return c.send
}

func (c *Client) Done() <-chan struct{} {
	return c.done
}

func (c *Client) Enqueue(msg []byte) bool {
	select {
	case <-c.done:
		return false
	default:
	}

	select {
	case c.send <- msg:
		return true
	case <-c.done:
		return false
	default:
		return false
	}
}

func (c *Client) Stop() {
	c.stopOnce.Do(func() {
		close(c.done)
	})
}

func (c *Client) Close() {
	c.Stop()
	c.connCloseOnce.Do(func() {
		if c.Conn != nil {
			_ = c.Conn.Close()
		}
	})
}

type Hub struct {
	mu      sync.RWMutex
	clients map[string]*Client
}

func New() *Hub {
	return &Hub{
		clients: make(map[string]*Client),
	}
}

func (h *Hub) Add(c *Client, nick string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.clients[nick]; exists {
		return errors.New("nickname already in use")
	}

	c.Nick = nick
	h.clients[nick] = c
	return nil
}

func (h *Hub) Remove(nick string) {
	h.mu.Lock()
	delete(h.clients, nick)
	h.mu.Unlock()
}

// Len returns the number of connected clients.
func (h *Hub) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *Hub) Broadcast(sender *Client, msg []byte) {
	var slow []*Client

	h.mu.RLock()
	for _, c := range h.clients {
		if c == sender {
			continue
		}
		if !c.Enqueue(msg) {
			slow = append(slow, c)
		}
	}
	h.mu.RUnlock()

	for _, c := range slow {
		h.Remove(c.Nick)
		c.Close()
	}
}

func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.clients {
		c.Stop()
	}
	clear(h.clients)
}
