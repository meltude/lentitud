package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateNick(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		nick    string
		wantErr bool
	}{
		{name: "simple", nick: "alice"},
		{name: "mixed", nick: "Alice_01-test"},
		{name: "empty", nick: "", wantErr: true},
		{name: "space", nick: "alice smith", wantErr: true},
		{name: "unicode", nick: "алиса", wantErr: true},
		{name: "too long", nick: strings.Repeat("a", MaxNickBytes+1), wantErr: true},
		{name: "max length", nick: strings.Repeat("a", MaxNickBytes)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateNick(tt.nick)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateNick(%q) error = %v, wantErr %v", tt.nick, err, tt.wantErr)
			}
		})
	}
}

func TestValidateText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		max     int
		wantErr bool
	}{
		{name: "simple", text: "hello", max: 10},
		{name: "unicode", text: "привет", max: 100},
		{name: "tab", text: "hello\tworld", max: 100},
		{name: "empty", text: "", max: 10, wantErr: true},
		{name: "too long", text: "hello", max: 4, wantErr: true},
		{name: "newline", text: "hello\nworld", max: 100, wantErr: true},
		{name: "carriage return", text: "hello\rworld", max: 100, wantErr: true},
		{name: "nul", text: "hello\x00world", max: 100, wantErr: true},
		{name: "control", text: "hello\x01world", max: 100, wantErr: true},
		{name: "del", text: "hello\x7fworld", max: 100, wantErr: true},
		{name: "c1 control", text: "hello\u0085world", max: 100, wantErr: true},
		{name: "bidi override", text: "hello\u202eworld", max: 100, wantErr: true},
		{name: "zero width space", text: "hello\u200bworld", max: 100, wantErr: true},
		{name: "emoji", text: "hello \U0001f600", max: 100},
		{name: "default max", text: "hello", max: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateText(tt.text, tt.max)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateText(%q, %d) error = %v, wantErr %v", tt.text, tt.max, err, tt.wantErr)
			}
		})
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	t.Parallel()

	env := New(TypeMessage, "alice", "hello")
	data, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got Envelope
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got.Type != env.Type || got.Nick != env.Nick || got.Text != env.Text || got.Time != env.Time {
		t.Fatalf("round trip mismatch: got %#v, want %#v", got, env)
	}
}

func TestNickKey(t *testing.T) {
	t.Parallel()

	if got, want := NickKey("Alice"), "alice"; got != want {
		t.Fatalf("NickKey() = %q, want %q", got, want)
	}
}
