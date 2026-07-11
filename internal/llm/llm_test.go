package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rondlite/neabbs/internal/config"
	"github.com/rondlite/neabbs/internal/content"
)

func TestDisabledClient(t *testing.T) {
	c := New(config.Config{}) // LLM_BASE_URL unset
	if c.Enabled() {
		t.Fatal("client with no base URL reports enabled")
	}
	// Nil-safe: fallback path returns the fallback and false.
	npc := &content.NPC{Name: "x", Fallback: "canned"}
	sys := BuildSystemPrompt("", npc, func(string) bool { return false })
	reply, viaLLM := c.Reply(context.Background(), sys, nil, "hoi", npc.Fallback)
	if viaLLM || reply != "canned" {
		t.Fatalf("disabled reply = %q, %v", reply, viaLLM)
	}
}

func TestChatRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(chatResponse{
			Choices: []struct {
				Message Message `json:"message"`
			}{{Message: Message{Role: "assistant", Content: "  hallo daar  "}}},
		})
	}))
	defer srv.Close()

	c := New(config.Config{LLMBaseURL: srv.URL, LLMModel: "test"})
	if !c.Enabled() {
		t.Fatal("client reports disabled")
	}
	reply, viaLLM := c.Reply(context.Background(), "sys", nil, "hoi", "fallback")
	if !viaLLM || reply != "hallo daar" {
		t.Fatalf("reply = %q, viaLLM=%v", reply, viaLLM)
	}
}

func TestChatServerErrorFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(config.Config{LLMBaseURL: srv.URL, LLMModel: "test"})
	reply, viaLLM := c.Reply(context.Background(), "sys", nil, "hoi", "fallback")
	if viaLLM || reply != "fallback" {
		t.Fatalf("error path = %q, viaLLM=%v (want fallback)", reply, viaLLM)
	}
}

func TestSystemPromptGatesFacts(t *testing.T) {
	npc := &content.NPC{
		Name:    "beheerder",
		Persona: "humeurig",
		KnowsFlags: map[string]string{
			"heeft_pas": "het wachtwoord is koffie86",
			"nooit":     "dit mag nooit lekken",
		},
	}
	// Player holds heeft_pas but not nooit.
	sys := BuildSystemPrompt("basis", npc, func(f string) bool { return f == "heeft_pas" })
	if !contains(sys, "koffie86") {
		t.Fatal("held-flag fact missing from prompt")
	}
	if contains(sys, "nooit lekken") {
		t.Fatal("un-held-flag fact leaked into prompt")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
