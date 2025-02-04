package notification

import (
	"fmt"

	"github.com/go-telegram/bot"
	"github.com/metro-olografix/sede/internal/config"
	"golang.org/x/net/context"
)

type Telegram struct {
	client       *bot.Bot
	chatId       int64
	chatThreadId int
}

func NewTelegram(cfg config.Config) (*Telegram, error) {
	if (cfg.TelegramChatId == 0) || (cfg.TelegramToken == "") {
		return &Telegram{}, fmt.Errorf("telegram token or chat id not set")
	}

	b, err := bot.New(cfg.TelegramToken)

	if err != nil {
		return &Telegram{}, err
	}

	return &Telegram{
		client:       b,
		chatId:       cfg.TelegramChatId,
		chatThreadId: cfg.TelegramChatThreadId,
	}, nil
}

func (telegram *Telegram) IsInitialized() bool {
	return telegram.client != nil
}

func (t *Telegram) Send(msg string) error {
	params := &bot.SendMessageParams{
		ChatID:          t.chatId,
		Text:            msg,
		MessageThreadID: t.chatThreadId,
	}

	_, err := t.client.SendMessage(context.TODO(), params)
	if err != nil {
		return err
	}

	return nil
}
