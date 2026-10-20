package hub

import (
	"errors"
	"testing"
)

func TestAddDuplicateNickIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	h := New()
	first := NewClient(nil, 4)
	second := NewClient(nil, 4)

	if err := h.Add(first, "Alice"); err != nil {
		t.Fatalf("Add(first) error = %v", err)
	}
	if err := h.Add(second, "alice"); !errors.Is(err, ErrNickTaken) {
		t.Fatalf("Add(second) error = %v, want ErrNickTaken", err)
	}
	if got := h.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1", got)
	}
	if !h.NickRegistered("ALICE") {
		t.Fatal("ALICE should be registered")
	}
}

func TestRemoveAllowsNickReuse(t *testing.T) {
	t.Parallel()

	h := New()
	first := NewClient(nil, 4)
	second := NewClient(nil, 4)

	if err := h.Add(first, "alice"); err != nil {
		t.Fatalf("Add(first) error = %v", err)
	}
	h.Remove(first)

	if err := h.Add(second, "alice"); err != nil {
		t.Fatalf("Add(second) after Remove error = %v", err)
	}
	if got := h.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1", got)
	}
	if !h.NickRegistered("alice") {
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
	if h.NickRegistered("slow") {
		t.Fatal("slow client nickname was not removed")
	}
	if got := h.Len(); got != 2 {
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

func TestStopAndCloseAreIdempotent(t *testing.T) {
	t.Parallel()

	c := NewClient(nil, 1)
	c.Stop()
	c.Stop()
	c.Close()
	c.Close()

	select {
	case <-c.Done():
	default:
		t.Fatal("client was not stopped")
	}
}

func TestCloseAllNow(t *testing.T) {
	t.Parallel()

	h := New()
	clients := []*Client{NewClient(nil, 4), NewClient(nil, 4)}
	for i, c := range clients {
		if err := h.Add(c, string(rune('a'+i))); err != nil {
			t.Fatalf("Add(%d) error = %v", i, err)
		}
	}

	h.CloseAllNow()
	h.CloseAll()
	h.CloseAllNow()

	if got := h.Len(); got != 0 {
		t.Fatalf("Len() after CloseAllNow = %d, want 0", got)
	}
	for i, c := range clients {
		select {
		case <-c.Done():
		default:
			t.Fatalf("client %d was not closed", i)
		}
	}
	if h.NickRegistered("a") || h.NickRegistered("b") {
		t.Fatal("nicknames were not released")
	}
}

func TestCloseAll(t *testing.T) {
	t.Parallel()

	h := New()
	clients := []*Client{NewClient(nil, 4), NewClient(nil, 4)}
	for i, c := range clients {
		if err := h.Add(c, string(rune('a'+i))); err != nil {
			t.Fatalf("Add(%d) error = %v", i, err)
		}
	}

	h.CloseAll()

	if got := h.Len(); got != 0 {
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
