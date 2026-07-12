package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/rondlite/neabbs/internal/config"
	"github.com/rondlite/neabbs/internal/content"
	"github.com/rondlite/neabbs/internal/llm"
)

// runGenposts is the offline board-texture generator. It calls the LLM to
// draft atmospheric filler posts as YAML for human review — it never runs
// in-game and never writes to the DB.
func runGenposts(args []string) error {
	fs := flag.NewFlagSet("genposts", flag.ContinueOnError)
	boardID := fs.String("board", "", "board id to theme the posts around (required)")
	level := fs.Int("level", 0, "THIS level to stamp on the drafts")
	count := fs.Int("count", 10, "how many posts to draft")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *boardID == "" {
		fs.Usage()
		return fmt.Errorf("genposts: --board is required")
	}

	cfg := config.FromEnv()
	client := llm.New(cfg)
	if !client.Enabled() {
		return fmt.Errorf("genposts: LLM disabled (set LLM_BASE_URL / LLM_MODEL / LLM_API_KEY)")
	}

	cset, err := content.Load(cfg.ContentDir)
	if err != nil {
		return fmt.Errorf("genposts: load content: %w", err)
	}
	b := cset.BoardByID(*boardID)
	if b == nil {
		return fmt.Errorf("genposts: no board %q", *boardID)
	}

	system := llm.GenpostSystemPrompt(cset.Prompts["genposts"], b.ID, b.Name.NL, *level, *count)

	out, err := client.Chat(context.Background(), []llm.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: fmt.Sprintf("Genereer %d berichten voor %s.", *count, b.Name)},
	})
	if err != nil {
		return fmt.Errorf("genposts: %w", err)
	}
	fmt.Fprintf(os.Stderr, "# genposts draft for board %q (level %d) — REVIEW BEFORE USE\n", b.ID, *level)
	fmt.Println(strings.TrimSpace(out))
	return nil
}
