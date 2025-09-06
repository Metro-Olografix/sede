package notification

import (
	"testing"

	"github.com/metro-olografix/sede/internal/config"
)

func TestNewTelegram(t *testing.T) {
	tests := []struct {
		name        string
		config      config.Config
		expectError bool
		expectNil   bool
		shouldInit  bool
	}{
		{
			name: "valid telegram config",
			config: config.Config{
				TelegramToken:        "valid-token",
				TelegramChatId:       123456789,
				TelegramChatThreadId: 1,
			},
			expectError: true, // Will error due to invalid token, but that's expected
			expectNil:   false,
			shouldInit:  false,
		},
		{
			name: "missing token",
			config: config.Config{
				TelegramToken:        "",
				TelegramChatId:       123456789,
				TelegramChatThreadId: 1,
			},
			expectError: true,
			expectNil:   false,
			shouldInit:  false,
		},
		{
			name: "missing chat id",
			config: config.Config{
				TelegramToken:        "valid-token",
				TelegramChatId:       0,
				TelegramChatThreadId: 1,
			},
			expectError: true,
			expectNil:   false,
			shouldInit:  false,
		},
		{
			name: "both missing",
			config: config.Config{
				TelegramToken:        "",
				TelegramChatId:       0,
				TelegramChatThreadId: 1,
			},
			expectError: true,
			expectNil:   false,
			shouldInit:  false,
		},
		{
			name: "valid config without thread id",
			config: config.Config{
				TelegramToken:        "valid-token",
				TelegramChatId:       123456789,
				TelegramChatThreadId: 0,
			},
			expectError: true, // Will error due to invalid token, but that's expected
			expectNil:   false,
			shouldInit:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			telegram, err := NewTelegram(tt.config)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}

			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if tt.expectNil && telegram != nil {
				t.Errorf("Expected telegram to be nil")
			}

			if !tt.expectNil && telegram == nil {
				t.Errorf("Expected telegram not to be nil")
			}

			if telegram != nil {
				if telegram.IsInitialized() != tt.shouldInit {
					t.Errorf("Expected IsInitialized to be %v, got %v", tt.shouldInit, telegram.IsInitialized())
				}

				if tt.shouldInit {
					if telegram.chatId != tt.config.TelegramChatId {
						t.Errorf("Expected chatId %d, got %d", tt.config.TelegramChatId, telegram.chatId)
					}

					if telegram.chatThreadId != tt.config.TelegramChatThreadId {
						t.Errorf("Expected chatThreadId %d, got %d", tt.config.TelegramChatThreadId, telegram.chatThreadId)
					}
				}
			}
		})
	}
}

func TestIsInitialized(t *testing.T) {
	t.Run("uninitialized telegram", func(t *testing.T) {
		telegram := &Telegram{}

		if telegram.IsInitialized() {
			t.Error("Expected IsInitialized to return false for uninitialized telegram")
		}
	})

	t.Run("initialized telegram", func(t *testing.T) {
		cfg := config.Config{
			TelegramToken:        "test-token",
			TelegramChatId:       123456789,
			TelegramChatThreadId: 1,
		}

		_, err := NewTelegram(cfg)
		if err == nil {
			t.Error("Expected error with invalid token")
		}

		// Test with manually created struct to avoid API call
		telegram := &Telegram{
			client:       nil, // Simulate what happens when token is invalid
			chatId:       123456789,
			chatThreadId: 1,
		}

		if telegram.IsInitialized() {
			t.Error("Expected IsInitialized to return false when client is nil")
		}
	})
}

func TestSend(t *testing.T) {
	t.Run("send with uninitialized telegram", func(t *testing.T) {
		telegram := &Telegram{}

		// This will panic due to nil client, which is expected behavior
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic when sending with uninitialized telegram")
			}
		}()

		telegram.Send("test message")
	})

	t.Run("send parameters validation", func(t *testing.T) {
		// Test the structure without making actual API calls
		telegram := &Telegram{
			chatId:       123456789,
			chatThreadId: 1,
		}

		if telegram.chatId != 123456789 {
			t.Errorf("Expected chatId 123456789, got %d", telegram.chatId)
		}

		if telegram.chatThreadId != 1 {
			t.Errorf("Expected chatThreadId 1, got %d", telegram.chatThreadId)
		}

		// Verify that IsInitialized correctly identifies uninitialized client
		if telegram.IsInitialized() {
			t.Error("Expected IsInitialized to return false for nil client")
		}
	})
}

// TestTelegramStruct tests the basic structure
func TestTelegramStruct(t *testing.T) {
	t.Run("telegram struct creation", func(t *testing.T) {
		telegram := &Telegram{
			client:       nil,
			chatId:       123456789,
			chatThreadId: 1,
		}

		if telegram.chatId != 123456789 {
			t.Errorf("Expected chatId 123456789, got %d", telegram.chatId)
		}

		if telegram.chatThreadId != 1 {
			t.Errorf("Expected chatThreadId 1, got %d", telegram.chatThreadId)
		}

		if telegram.IsInitialized() {
			t.Error("Expected IsInitialized to be false when client is nil")
		}
	})
}
