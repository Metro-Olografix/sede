package notification

import (
	"testing"
)

func TestNewDispatcher_MissingToken(t *testing.T) {
	d, err := NewDispatcher("")
	if err == nil {
		t.Fatal("expected error for empty token")
	}
	if d == nil {
		t.Fatal("expected non-nil dispatcher even on error")
	}
	if d.IsInitialized() {
		t.Error("expected IsInitialized false for empty token")
	}
}

func TestDispatcher_IsInitialized(t *testing.T) {
	var nilD *Dispatcher
	if nilD.IsInitialized() {
		t.Error("nil dispatcher should not be initialized")
	}

	empty := &Dispatcher{}
	if empty.IsInitialized() {
		t.Error("dispatcher with nil client should not be initialized")
	}
}

func TestDispatcher_Send_NoOpWhenUninitialized(t *testing.T) {
	d := &Dispatcher{}
	if err := d.Send(12345, 1, "hello"); err != nil {
		t.Errorf("expected nil error from uninitialized Send, got %v", err)
	}
}

func TestDispatcher_Send_NoOpWhenChatIDZero(t *testing.T) {
	d := &Dispatcher{}
	if err := d.Send(0, 0, "hello"); err != nil {
		t.Errorf("expected nil error when chatID is 0, got %v", err)
	}
}
