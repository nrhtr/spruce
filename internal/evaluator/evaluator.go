package evaluator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/nrhtr/spruce/internal/config"
	dbgen "github.com/nrhtr/spruce/internal/db/generated"
	"github.com/nrhtr/spruce/internal/platform"
)

const systemPrompt = `You are an expert auction evaluator helping a buyer identify high-quality deals on vintage electronics and other items.

You will be given search criteria and a listing. Evaluate the listing against the criteria and respond with valid JSON only, matching this exact schema:
{"score": <float 0.0-10.0>, "reasoning": "<1-3 sentence explanation>", "flags": ["<flag1>"]}

Score interpretation:
  9-10: Exceptional deal, act immediately
  7-8:  Good match, worth serious consideration
  5-6:  Partial match, notable caveats
  3-4:  Poor match or significant concerns
  0-2:  Not relevant or likely problematic

Possible flags (use only when applicable): price_high, price_suspiciously_low, condition_uncertain,
description_vague, auction_ending_soon, seller_new, location_inconvenient,
suspicious_listing, not_matching_criteria, rare_find, great_condition, good_price`

type Result struct {
	Score     float64  `json:"score"`
	Reasoning string   `json:"reasoning"`
	Flags     []string `json:"flags"`
}

type Evaluator struct {
	client anthropic.Client
	model  string
	log    *slog.Logger
	ready  bool
}

func New(cfg *config.Config, log *slog.Logger) *Evaluator {
	if cfg.AnthropicAPIKey == "" {
		log.Warn("ANTHROPIC_API_KEY not set; Claude evaluation disabled")
		return &Evaluator{log: log, model: cfg.ClaudeModel}
	}
	client := anthropic.NewClient(option.WithAPIKey(cfg.AnthropicAPIKey))
	return &Evaluator{
		client: client,
		model:  cfg.ClaudeModel,
		log:    log,
		ready:  true,
	}
}

func (e *Evaluator) Model() string { return e.model }

func (e *Evaluator) Evaluate(ctx context.Context, l platform.Listing, s dbgen.Search) (*Result, error) {
	if !e.ready {
		return &Result{Score: 5.0, Reasoning: "AI evaluation disabled (no API key)."}, nil
	}

	userMsg := buildUserMessage(l, s)

	msg, err := e.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     e.model,
		MaxTokens: 500,
		System: []anthropic.TextBlockParam{
			{
				Text: systemPrompt,
				CacheControl: anthropic.CacheControlEphemeralParam{
					TTL: anthropic.CacheControlEphemeralTTLTTL5m,
				},
			},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(userMsg)),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("claude evaluation: %w", err)
	}

	var text string
	for _, block := range msg.Content {
		if block.Type == "text" {
			text = block.Text
			break
		}
	}

	text = extractJSON(text)

	var result Result
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		e.log.Warn("claude returned non-JSON", "response", text, "listing", l.ExternalID)
		return &Result{Score: 5.0, Reasoning: "Evaluation returned unexpected format."}, nil
	}

	return &result, nil
}

func buildUserMessage(l platform.Listing, s dbgen.Search) string {
	var sb strings.Builder

	sb.WriteString("SEARCH CRITERIA:\n")
	fmt.Fprintf(&sb, "Name: %s\n", s.Name)
	fmt.Fprintf(&sb, "Keywords: %s\n", s.Keywords)
	if s.Description != "" {
		fmt.Fprintf(&sb, "What I'm looking for: %s\n", s.Description)
	}
	if s.MinPrice.Valid && s.MaxPrice.Valid {
		fmt.Fprintf(&sb, "Price range: %.2f–%.2f %s\n", s.MinPrice.Float64, s.MaxPrice.Float64, s.Currency)
	} else if s.MaxPrice.Valid {
		fmt.Fprintf(&sb, "Max price: %.2f %s\n", s.MaxPrice.Float64, s.Currency)
	}
	if s.Location.Valid {
		fmt.Fprintf(&sb, "Location preference: %s\n", s.Location.String)
	}

	sb.WriteString("\nLISTING:\n")
	fmt.Fprintf(&sb, "Platform: %s\n", l.Platform)
	fmt.Fprintf(&sb, "Title: %s\n", l.Title)
	if l.Price != nil {
		fmt.Fprintf(&sb, "Price: %.2f %s\n", *l.Price, l.Currency)
	}
	if l.Condition != "" {
		fmt.Fprintf(&sb, "Condition: %s\n", l.Condition)
	}
	if l.Location != "" {
		fmt.Fprintf(&sb, "Location: %s\n", l.Location)
	}
	if l.EndTime != nil {
		remaining := time.Until(*l.EndTime).Round(time.Minute)
		fmt.Fprintf(&sb, "Auction ends: %s (in %s)\n", l.EndTime.Format(time.RFC822), remaining)
	}
	fmt.Fprintf(&sb, "URL: %s\n", l.URL)
	if l.Description != "" {
		desc := l.Description
		if len(desc) > 2000 {
			desc = desc[:2000] + "..."
		}
		fmt.Fprintf(&sb, "\nDescription:\n%s\n", desc)
	}

	return sb.String()
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		lines := strings.SplitN(s, "\n", 2)
		if len(lines) == 2 {
			s = lines[1]
		}
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}
