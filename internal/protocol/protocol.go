package protocol

import (
	"errors"
	"fmt"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxNickBytes    = 32
	MaxMessageBytes = 4096
)

type Type string

const (
	TypeJoin    Type = "join"
	TypeWelcome Type = "welcome"
	TypeMessage Type = "message"
	TypeSystem  Type = "system"
	TypeError   Type = "error"
)

type Envelope struct {
	Type Type   `json:"type"`
	Nick string `json:"nick,omitempty"`
	Text string `json:"text,omitempty"`
	Time string `json:"time,omitempty"`
}

func New(t Type, nick, text string) Envelope {
	return Envelope{
		Type: t,
		Nick: nick,
		Text: text,
		Time: time.Now().UTC().Format(time.RFC3339),
	}
}

func ValidateNick(nick string) error {
	if nick == "" {
		return errors.New("nickname is required")
	}
	if len(nick) > MaxNickBytes {
		return fmt.Errorf("nickname is too long: %d bytes (max %d)", len(nick), MaxNickBytes)
	}
	for _, r := range nick {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("nickname contains invalid character %q", r)
	}
	return nil
}

func ValidateText(text string, maxBytes int) error {
	if text == "" {
		return errors.New("message is empty")
	}
	if maxBytes <= 0 {
		maxBytes = MaxMessageBytes
	}
	if len(text) > maxBytes {
		return fmt.Errorf("message is too long: %d bytes (max %d)", len(text), maxBytes)
	}
	if !utf8.ValidString(text) {
		return errors.New("message is not valid UTF-8")
	}
	for _, r := range text {
		switch {
		case r == '\r' || r == '\n':
			return errors.New("message must be a single line")
		case r == '\t':
			continue
		case unicode.IsControl(r):
			return errors.New("message contains a control character")
		case unicode.Is(unicode.Cf, r):
			return errors.New("message contains a formatting character")
		}
	}
	return nil
}
