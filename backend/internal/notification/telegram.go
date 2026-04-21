package notification

import (
	"context"
	"fmt"

	"github.com/go-telegram/bot"
)

// Dispatcher holds a single Telegram bot client. Each Send call specifies
// its own chatID / threadID so the same bot can notify multiple spaces.
type Dispatcher struct {
	client *bot.Bot
}

// NewDispatcher builds a Dispatcher for the given bot token. An empty token
// returns an uninitialised dispatcher whose Send is a no-op, so callers can
// treat "no Telegram configured" as non-fatal without branching.
func NewDispatcher(token string) (*Dispatcher, error) {
	if token == "" {
		return &Dispatcher{}, fmt.Errorf("telegram token not set")
	}

	b, err := bot.New(token)
	if err != nil {
		return &Dispatcher{}, err
	}

	return &Dispatcher{client: b}, nil
}

func (d *Dispatcher) IsInitialized() bool {
	return d != nil && d.client != nil
}

// Send posts msg to chatID / threadID. chatID == 0 is treated as "no
// Telegram target" and returns nil — lets spaces without Telegram config
// go through the toggle flow cleanly.
func (d *Dispatcher) Send(chatID int64, threadID int, msg string) error {
	if !d.IsInitialized() || chatID == 0 {
		return nil
	}

	_, err := d.client.SendMessage(context.TODO(), &bot.SendMessageParams{
		ChatID:          chatID,
		Text:            msg,
		MessageThreadID: threadID,
	})
	return err
}
