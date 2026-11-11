package protocol

import (
	"errors"
	"strings"
	"time"
	"unicode"
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
	if len(nick) == 0 || len(nick) > MaxNickBytes {
		return errors.New("nickname must be between 1 and 32 bytes")
	}
	for _, r := range nick {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
			return errors.New("nickname may only contain letters, digits, '_' and '-'")
		}
	}
	return nil
}

func ValidateText(text string, maxBytes int) error {
	if len(text) == 0 || len(text) > maxBytes || strings.ContainsAny(text, "\r\n") {
		return errors.New("invalid message")
	}
	return nil
}
