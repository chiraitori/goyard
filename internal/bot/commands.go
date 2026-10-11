package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"goyard/internal/config"
	"goyard/internal/discord"
	"goyard/internal/presence"
	"goyard/internal/store"
	"goyard/internal/supervisor"
)

type Message struct {
	ID        string
	ChannelID string
	GuildID   string
	AuthorID  string
	IsBot     bool
	Content   string
}

// ExtCommandHandler allows extensions to handle custom bot commands
type ExtCommandHandler func(cmd string, args []string, msg Message) bool

type Handler struct {
	cfg        *config.Config
	manager    *presence.Manager
	store      store.Store
	discord    *discord.RESTClient
	extHandler ExtCommandHandler
}

func NewHandler(cfg *config.Config, manager *presence.Manager, store store.Store, discordClient *discord.RESTClient) *Handler {
	return &Handler{
		cfg:     cfg,
		manager: manager,
		store:   store,
		discord: discordClient,
	}
}

func (h *Handler) SetExtHandler(fn ExtCommandHandler) {
	h.extHandler = fn
}

// SanitizeContent replaces every '@' with '@\u200b' to avoid mentions
func SanitizeContent(s string) string {
	return strings.ReplaceAll(s, "@", "@\u200b")
}

// HandleMessage parses and executes a command from a raw gateway MESSAGE_CREATE event
func (h *Handler) HandleMessage(rawMsg map[string]any) {
	author, ok := rawMsg["author"].(map[string]any)
	if !ok {
		return
	}
	if isBot, ok := author["bot"].(bool); ok && isBot {
		return
	}

	content, _ := rawMsg["content"].(string)
	if !strings.HasPrefix(content, h.cfg.CommandPrefix) {
		return
	}

	trimmed := strings.TrimPrefix(content, h.cfg.CommandPrefix)
	tokens := strings.Split(trimmed, " ")
	if len(tokens) == 0 || tokens[0] == "" {
		return
	}

	cmd := strings.ToLower(tokens[0])
	args := tokens[1:]

	channelID, _ := rawMsg["channel_id"].(string)
	guildID, _ := rawMsg["guild_id"].(string)
	authorID, _ := rawMsg["id"].(string)
	if aID, ok := author["id"].(string); ok {
		authorID = aID
	}

	msg := Message{
		ID:        fmt.Sprintf("%v", rawMsg["id"]),
		ChannelID: channelID,
		GuildID:   guildID,
		AuthorID:  authorID,
		Content:   content,
	}

	// Run in guarded goroutine
	supervisor.Go("bot-cmd-"+cmd, func() {
		h.executeCommand(cmd, args, msg)
	})
}

func (h *Handler) executeCommand(cmd string, args []string, msg Message) {
	ctx := context.Background()

	// Check extension handler first
	if h.extHandler != nil && h.extHandler(cmd, args, msg) {
		return
	}

	switch cmd {
	case "get", "set", "del":
		h.handleKVCommand(ctx, cmd, args, msg)
	case "kv":
		h.handleKVList(ctx, msg)
	case "apikey":
		h.handleAPIKey(ctx, msg)
	case "help":
		h.handleHelp(ctx, msg)
	default:
		// Unknown command: silently ignore
	}
}

func (h *Handler) checkLeakAndMonitored(ctx context.Context, args []string, msg Message) (bool, bool) {
	// 1. API key leak protection
	currentKey, ok, err := h.store.KeyForUser(ctx, msg.AuthorID)
	if err == nil && ok && currentKey != "" {
		for _, arg := range args {
			if arg == currentKey {
				h.handleLeak(ctx, msg)
				return true, false
			}
		}
	}

	// 2. Monitored check
	if _, monitored := h.manager.Get(msg.AuthorID); !monitored {
		h.reply(ctx, msg.ChannelID, ":x: You are not being monitored by this bot", nil)
		return false, false
	}

	return false, true
}

func (h *Handler) handleLeak(ctx context.Context, msg Message) {
	warning := ":x: Whoops, you just posted your API key, this is meant to stay private, regenerating this for you, check your DM"
	h.reply(ctx, msg.ChannelID, warning, nil)

	newKey, err := h.store.RotateKey(ctx, msg.AuthorID)
	if err != nil {
		slog.Error("failed to rotate key after leak", "error", err)
		return
	}

	dmChan, err := h.discord.CreateDM(ctx, msg.AuthorID)
	if err != nil {
		slog.Error("failed to create DM after leak", "error", err)
		return
	}

	dmText := fmt.Sprintf(":repeat: **We've regenerated your api key as you used it in a K/V command.**\nYour new Lanyard API key is `%s`\n\n**ABSOLUTELY DO NOT SHARE OR POST THIS KEY ANYWHERE IT WILL ALLOW ANYONE TO MANAGE YOUR LANYARD K/V**\n*Run `%sapikey` in this DM if you need to re-generate your key*", newKey, h.cfg.CommandPrefix)
	_ = h.discord.SendMessage(ctx, dmChan, dmText, nil)
}

func (h *Handler) handleKVCommand(ctx context.Context, cmd string, args []string, msg Message) {
	leaked, ok := h.checkLeakAndMonitored(ctx, args, msg)
	if leaked || !ok {
		return
	}

	prefix := h.cfg.CommandPrefix

	switch cmd {
	case "get":
		if len(args) != 1 {
			h.reply(ctx, msg.ChannelID, fmt.Sprintf("Invalid usage. Example `get` command usage:\n`%sget <key>`", prefix), nil)
			return
		}
		key := args[0]
		kv, err := h.store.GetKV(ctx, msg.AuthorID)
		if err != nil {
			h.reply(ctx, msg.ChannelID, fmt.Sprintf(":x: Key %s not found in KV", key), nil)
			return
		}
		val, exists := kv[key]
		if !exists {
			h.reply(ctx, msg.ChannelID, fmt.Sprintf(":x: Key %s not found in KV", key), nil)
			return
		}

		safeVal := strings.ReplaceAll(val, "`", "`\u200b")
		replyText := fmt.Sprintf(":white_check_mark: Key: `%s` | Value: ```%s```", key, safeVal)
		sanitized := SanitizeContent(replyText)

		if utf8.RuneCountInString(sanitized) > 2000 {
			tooLong := fmt.Sprintf(":x: The value for `%s` is too long to display in Discord.\nView it at %s/v1/users/%s", key, h.cfg.ExternalURL, msg.AuthorID)
			h.reply(ctx, msg.ChannelID, tooLong, nil)
		} else {
			h.reply(ctx, msg.ChannelID, sanitized, nil)
		}

	case "set":
		if len(args) < 2 {
			h.reply(ctx, msg.ChannelID, fmt.Sprintf("Invalid usage. Example `set` command usage:\n`%sset <key> <value>`", prefix), nil)
			return
		}
		key := args[0]
		val := strings.Join(args[1:], " ")

		if err := h.store.SetKV(ctx, msg.AuthorID, key, val); err != nil {
			h.reply(ctx, msg.ChannelID, fmt.Sprintf(":x: %s", err.Error()), nil)
			return
		}

		newKV, _ := h.store.GetKV(ctx, msg.AuthorID)
		h.manager.Sync(msg.AuthorID, presence.Patch{KV: newKV}, false)

		success := fmt.Sprintf(":white_check_mark: `%s` was set. View it with `%sget %s` or go to %s/v1/users/%s", key, prefix, key, h.cfg.ExternalURL, msg.AuthorID)
		h.reply(ctx, msg.ChannelID, success, nil)

	case "del":
		if len(args) != 1 {
			h.reply(ctx, msg.ChannelID, fmt.Sprintf("Invalid usage. Example `del` command usage:\n`%sdel <key>`", prefix), nil)
			return
		}
		key := args[0]
		_ = h.store.DelKV(ctx, msg.AuthorID, key)
		newKV, _ := h.store.GetKV(ctx, msg.AuthorID)
		h.manager.Sync(msg.AuthorID, presence.Patch{KV: newKV}, false)

		success := fmt.Sprintf(":white_check_mark: Deleted key: `%s`", key)
		h.reply(ctx, msg.ChannelID, success, nil)
	}
}

func (h *Handler) handleKVList(ctx context.Context, msg Message) {
	leaked, ok := h.checkLeakAndMonitored(ctx, nil, msg)
	if leaked || !ok {
		return
	}

	prefix := h.cfg.CommandPrefix
	kv, _ := h.store.GetKV(ctx, msg.AuthorID)

	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}

	keysStr := "No keys"
	if len(keys) > 0 {
		keysStr = strings.Join(keys, ", ")
	}

	reply := fmt.Sprintf("*`%sget <key>` to get a value*\n*`%sdel <key>` to delete an existing key*\n*`%sset <key> <value>` to set a key*\n\n**Keys:** ```%s```", prefix, prefix, prefix, keysStr)
	h.reply(ctx, msg.ChannelID, reply, nil)
}

func (h *Handler) handleAPIKey(ctx context.Context, msg Message) {
	if msg.GuildID != "" {
		h.reply(ctx, msg.ChannelID, ":x: You can only perform this command in DMs with me", nil)
		return
	}

	key, err := h.store.RotateKey(ctx, msg.AuthorID)
	if err != nil {
		h.reply(ctx, msg.ChannelID, ":x: Failed to generate API key", nil)
		return
	}

	desc := fmt.Sprintf("**Absolutely do not share or post this key anywhere, it is a secret key that will allow anyone to manage your Lanyard K/V**\n\n**This key is not to be used in a front-end application/website**\n\nIf you are looking for the public endpoint for your data, you would use your discord user ID like so\n%s/v1/users/%s", h.cfg.ExternalURL, msg.AuthorID)

	embed := &discord.Embed{
		Title:       "Lanyard API Key",
		Description: desc,
		Color:       0x5865F2,
		Footer: &discord.EmbedFooter{
			Text: "Run this command again if you need to re-generate your key",
		},
		Fields: []discord.EmbedField{
			{
				Name:   "Key",
				Value:  fmt.Sprintf("||`%s`||\n-# *click above to reveal*", key),
				Inline: false,
			},
		},
	}
	h.reply(ctx, msg.ChannelID, "", embed)
}

func (h *Handler) handleHelp(ctx context.Context, msg Message) {
	prefix := h.cfg.CommandPrefix
	embed := &discord.Embed{
		Title:       "Lanyard Bot Commands",
		Description: "Store and manage custom data on your Lanyard profile.",
		Color:       0x5865F2,
		Fields: []discord.EmbedField{
			{
				Name:   fmt.Sprintf("`%sget <key>`", prefix),
				Value:  "Get the value of a key",
				Inline: false,
			},
			{
				Name:   fmt.Sprintf("`%sset <key> <value>`", prefix),
				Value:  "Set a key to a value",
				Inline: false,
			},
			{
				Name:   fmt.Sprintf("`%sdel <key>`", prefix),
				Value:  "Delete a key",
				Inline: false,
			},
			{
				Name:   fmt.Sprintf("`%skv`", prefix),
				Value:  "List all of your current keys",
				Inline: false,
			},
			{
				Name:   fmt.Sprintf("`%sapikey`", prefix),
				Value:  "Get (or regenerate) your Lanyard API key, sent via DM",
				Inline: false,
			},
			{
				Name:   "Docs & Source",
				Value:  "[API/Socket docs](https://github.com/Phineas/lanyard#readme) · [Source code](https://github.com/Phineas/lanyard)",
				Inline: false,
			},
		},
	}
	h.reply(ctx, msg.ChannelID, "", embed)
}

func (h *Handler) reply(ctx context.Context, channelID string, content string, embed *discord.Embed) {
	sanitized := SanitizeContent(content)
	if utf8.RuneCountInString(sanitized) > 2000 {
		runes := []rune(sanitized)
		sanitized = string(runes[:2000])
	}
	_ = h.discord.SendMessage(ctx, channelID, sanitized, embed)
}
