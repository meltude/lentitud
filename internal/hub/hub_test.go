package hub

import (
	"testing"
)

func TestRemoveAllowsNickReuse(t *testing.T) {
	t.Parallel()

	h := New()
	first := NewClient(nil, 4)
	second := NewClient(nil, 4)

	if err := h.Add(first, "alice"); err != nil {
		t.Fatalf("Add(first) error = %v", err)
	}
	h.Remove("alice")

	if err := h.Add(second, "alice"); err != nil {
		t.Fatalf("Add(second) after Remove error = %v", err)
	}
	if got := len(h.clients); got != 1 {
		t.Fatalf("Len() = %d, want 1", got)
	}
	if _, ok := h.clients["alice"]; !ok {
		t.Fatal("alice should be registered again")
	}
}

func TestBroadcastExcludesSenderAndDropsSlowClients(t *testing.T) {
	t.Parallel()

	h := New()
	sender := NewClient(nil, 4)
	receiver := NewClient(nil, 4)
	slow := NewClient(nil, 1)

	for _, pair := range []struct {
		client *Client
		nick   string
	}{
		{sender, "sender"},
		{receiver, "receiver"},
		{slow, "slow"},
	} {
		if err := h.Add(pair.client, pair.nick); err != nil {
			t.Fatalf("Add(%q) error = %v", pair.nick, err)
		}
	}

	if !slow.Enqueue([]byte("fill")) {
		t.Fatal("failed to fill slow client buffer")
	}

	msg := []byte("hello")
	h.Broadcast(sender, msg)

	select {
	case got := <-receiver.Send():
		if string(got) != string(msg) {
			t.Fatalf("receiver got %q, want %q", got, msg)
		}
	default:
		t.Fatal("receiver did not get broadcast")
	}

	select {
	case got := <-sender.Send():
		t.Fatalf("sender received own message: %q", got)
	default:
	}

	select {
	case <-slow.Done():
	default:
		t.Fatal("slow client was not closed")
	}
	if _, ok := h.clients["slow"]; ok {
		t.Fatal("slow client nickname was not removed")
	}
	if got := len(h.clients); got != 2 {
		t.Fatalf("Len() = %d, want 2", got)
	}
}

func TestEnqueueAfterStopFails(t *testing.T) {
	t.Parallel()

	c := NewClient(nil, 1)
	c.Stop()

	if c.Enqueue([]byte("late")) {
		t.Fatal("Enqueue() after Stop() = true, want false")
	}
}

func TestClose(t *testing.T) {
	t.Parallel()

	h := New()
	clients := []*Client{NewClient(nil, 4), NewClient(nil, 4)}
	for i, c := range clients {
		if err := h.Add(c, string(rune('a'+i))); err != nil {
			t.Fatalf("Add(%d) error = %v", i, err)
		}
	}

	h.Close()

	if got := len(h.clients); got != 0 {
		t.Fatalf("Len() after CloseAll = %d, want 0", got)
	}
	for i, c := range clients {
		select {
		case <-c.Done():
		default:
			t.Fatalf("client %d was not closed", i)
		}
	}
}
