// Package llm is an optional OpenAI-compatible chat client. The game MUST be
// fully playable with the LLM disabled (LLM_BASE_URL unset): every call has a
// 10 s timeout and degrades gracefully to canned text. LLM output is flavor
// only, never on the critical path.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rondlite/neabbs/internal/config"
)

// callTimeout bounds every LLM request.
const callTimeout = 10 * time.Second

// Message is one chat-completions message.
type Message struct {
	Role    string `json:"role"` // system | user | assistant
	Content string `json:"content"`
}

// Client talks to an OpenAI-compatible /chat/completions endpoint.
// A nil Client (LLM disabled) is valid: Enabled reports false and callers
// use their fallback text.
type Client struct {
	baseURL string
	model   string
	apiKey  string
	http    *http.Client
}

// New builds a client from config, or nil if LLM_BASE_URL is unset.
func New(cfg config.Config) *Client {
	if cfg.LLMBaseURL == "" {
		return nil
	}
	return &Client{
		baseURL: cfg.LLMBaseURL,
		model:   cfg.LLMModel,
		apiKey:  cfg.LLMAPIKey,
		http:    &http.Client{Timeout: callTimeout},
	}
}

// Enabled reports whether the LLM is configured. Nil-safe.
func (c *Client) Enabled() bool { return c != nil && c.baseURL != "" }

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens"`
}

type chatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

// Chat sends messages and returns the assistant reply. Any error (disabled,
// timeout, bad status) is returned so the caller can fall back — it is never
// fatal. Nil-safe: a nil client returns ErrDisabled.
func (c *Client) Chat(ctx context.Context, messages []Message) (string, error) {
	if !c.Enabled() {
		return "", ErrDisabled
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	body, err := json.Marshal(chatRequest{
		Model:       c.model,
		Messages:    messages,
		Temperature: 0.8,
		MaxTokens:   400,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return "", fmt.Errorf("llm: status %d: %s", resp.StatusCode, snippet)
	}
	var parsed chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("llm: empty response")
	}
	return parsed.Choices[0].Message.Content, nil
}

// ErrDisabled is returned when the LLM is not configured.
var ErrDisabled = errors.New("llm: disabled")
