package hub

import (
	"errors"
	"sync"

	"codeberg.org/samposa_1337/Nkit/internal/protocol"

	"github.com/gorilla/websocket"
)

var ErrNickTaken = errors.New("nickname already in use")

const defaultSendBuffer = 256

type Client struct {
	Conn *websocket.Conn

	send          chan []byte
	done          chan struct{}
	stopOnce      sync.Once
	connCloseOnce sync.Once
	nick          string
}

func NewClient(conn *websocket.Conn, sendBuffer int) *Client {
	if sendBuffer <= 0 {
		sendBuffer = defaultSendBuffer
	}
	return &Client{
		Conn: conn,
		send: make(chan []byte, sendBuffer),
		done: make(chan struct{}),
	}
}

func (c *Client) Nick() string {
	return c.nick
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
	clients map[*Client]struct{}
	nicks   map[string]*Client
}

func New() *Hub {
	return &Hub{
		clients: make(map[*Client]struct{}),
		nicks:   make(map[string]*Client),
	}
}

func (h *Hub) Add(c *Client, nick string) error {
	key := protocol.NickKey(nick)

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.nicks[key]; exists {
		return ErrNickTaken
	}

	c.nick = nick
	h.clients[c] = struct{}{}
	h.nicks[key] = c
	return nil
}

func (h *Hub) Remove(c *Client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		key := protocol.NickKey(c.nick)
		if h.nicks[key] == c {
			delete(h.nicks, key)
		}
	}
	h.mu.Unlock()
}

func (h *Hub) Broadcast(sender *Client, msg []byte) {
	var slow []*Client

	h.mu.RLock()
	for c := range h.clients {
		if c == sender {
			continue
		}
		if !c.Enqueue(msg) {
			slow = append(slow, c)
		}
	}
	h.mu.RUnlock()

	for _, c := range slow {
		h.Remove(c)
		c.Close()
	}
}

func (h *Hub) CloseAll() {
	h.mu.Lock()
	clients := make([]*Client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.clients = make(map[*Client]struct{})
	h.nicks = make(map[string]*Client)
	h.mu.Unlock()

	for _, c := range clients {
		c.Close()
	}
}

func (h *Hub) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *Hub) NickRegistered(nick string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.nicks[protocol.NickKey(nick)]
	return ok
}
