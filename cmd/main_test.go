package main

import (
	"net/http"
	"testing"
)

func TestHTTPServerConfiguresWebhookTimeouts(t *testing.T) {
	handler := http.NewServeMux()
	server := (&httpServer{addr: ":8082", handler: handler}).newServer()

	if server.Addr != ":8082" {
		t.Fatalf("expected addr :8082, got %q", server.Addr)
	}
	if server.Handler != handler {
		t.Fatal("expected handler to be preserved")
	}
	if server.ReadHeaderTimeout != webhookReadHeaderTimeout {
		t.Fatalf("expected read header timeout %s, got %s", webhookReadHeaderTimeout, server.ReadHeaderTimeout)
	}
	if server.ReadTimeout != webhookReadTimeout {
		t.Fatalf("expected read timeout %s, got %s", webhookReadTimeout, server.ReadTimeout)
	}
	if server.WriteTimeout != webhookWriteTimeout {
		t.Fatalf("expected write timeout %s, got %s", webhookWriteTimeout, server.WriteTimeout)
	}
	if server.IdleTimeout != webhookIdleTimeout {
		t.Fatalf("expected idle timeout %s, got %s", webhookIdleTimeout, server.IdleTimeout)
	}
}
