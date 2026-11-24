// Package bot owns the Discord session and command registration.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// CommandHandler runs a slash command.
type CommandHandler func(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error

// ModalHandler runs a modal submission.
type ModalHandler func(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error

// Bot owns the Discord session and dispatches interactions.
type Bot struct {
	Session *discordgo.Session
	Log     *slog.Logger

	guildID string

	mu              sync.RWMutex
	commands        map[string]*discordgo.ApplicationCommand
	commandHandlers map[string]CommandHandler
	modalHandlers   map[string]ModalHandler
	synced          bool
}

// New creates a bot session for one guild.
func New(token, guildID string, log *slog.Logger) (*Bot, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("discord token is empty")
	}
	if log == nil {
		log = slog.Default()
	}

	s, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("creating discord session: %w", err)
	}
	s.Identify.Intents = discordgo.IntentsGuilds
	s.LogLevel = discordgo.LogWarning
	s.ShouldReconnectOnError = true

	b := &Bot{
		Session:         s,
		Log:             log,
		guildID:         strings.TrimSpace(guildID),
		commands:        make(map[string]*discordgo.ApplicationCommand),
		commandHandlers: make(map[string]CommandHandler),
		modalHandlers:   make(map[string]ModalHandler),
	}
	s.AddHandler(b.handleReady)
	s.AddHandler(b.handleInteraction)
	return b, nil
}

// AddCommand registers a slash command and its handler. The command is
// registered as soon as the gateway reports ready.
func (b *Bot) AddCommand(cmd *discordgo.ApplicationCommand, handler CommandHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.commands[cmd.Name] = cmd
	b.commandHandlers[cmd.Name] = handler
}

// AddModal registers a modal handler for a custom ID namespace, for example
// "login" handles "login:code".
func (b *Bot) AddModal(namespace string, handler ModalHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.modalHandlers[namespace] = handler
}

// Run validates the token, connects the gateway, and blocks until ctx is done.
func (b *Bot) Run(ctx context.Context) error {
	me, err := b.Session.User("@me")
	if err != nil {
		return fmt.Errorf("validating bot token: %w", err)
	}
	b.Log.Info("authenticated with discord", "user", me.Username, "user_id", me.ID, "guild_id", b.guildID)

	if err := b.Session.Open(); err != nil {
		return fmt.Errorf("opening gateway: %w", err)
	}
	defer b.Session.Close()
	b.Log.Info("gateway connected")

	<-ctx.Done()
	b.Log.Info("gateway shutting down")
	return nil
}

func (b *Bot) handleReady(s *discordgo.Session, r *discordgo.Ready) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	b.Log.Info("discord ready", "user", r.User.Username, "guilds", len(r.Guilds))

	if b.needsSync() {
		if err := b.syncCommands(s); err != nil {
			// Leaving the flag unset retries on the next READY event.
			b.Log.Error("syncing application commands", "err", err)
		} else {
			b.mu.Lock()
			b.synced = true
			b.mu.Unlock()
		}
	}
	_ = ctx
}

func (b *Bot) needsSync() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return !b.synced
}

func (b *Bot) syncCommands(s *discordgo.Session) error {
	b.mu.RLock()
	cmds := make([]*discordgo.ApplicationCommand, 0, len(b.commands))
	for _, cmd := range b.commands {
		cmds = append(cmds, cmd)
	}
	guildID := b.guildID
	b.mu.RUnlock()

	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })

	if guildID == "" {
		return errors.New("guild id is required for command registration")
	}
	if _, err := s.ApplicationCommandBulkOverwrite(s.State.User.ID, guildID, cmds); err != nil {
		return fmt.Errorf("overwriting guild commands: %w", err)
	}
	b.Log.Info("registered guild commands", "count", len(cmds), "guild_id", guildID)
	return nil
}

func (b *Bot) handleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		name := i.ApplicationCommandData().Name
		b.mu.RLock()
		handler := b.commandHandlers[name]
		b.mu.RUnlock()
		if handler == nil {
			return
		}
		b.run(name, func(ctx context.Context) error { return handler(ctx, s, i) })
	case discordgo.InteractionModalSubmit:
		namespace := customIDNamespace(i.ModalSubmitData().CustomID)
		b.mu.RLock()
		handler := b.modalHandlers[namespace]
		b.mu.RUnlock()
		if handler == nil {
			return
		}
		b.run("modal "+namespace, func(ctx context.Context) error { return handler(ctx, s, i) })
	}
}

func (b *Bot) run(label string, fn func(context.Context) error) {
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				b.Log.Error("interaction handler panicked", "handler", label, "panic", recovered)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := fn(ctx); err != nil {
			b.Log.Error("interaction handler failed", "handler", label, "err", err)
		}
	}()
}

func customIDNamespace(customID string) string {
	if index := strings.IndexByte(customID, ':'); index >= 0 {
		return customID[:index]
	}
	return customID
}

// UserID returns the Discord user behind an interaction.
func UserID(i *discordgo.InteractionCreate) string {
	if i == nil {
		return ""
	}
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

// Defer acknowledges an interaction and buys three seconds of headroom for
// API calls. A private reply is ephemeral.
func Defer(s *discordgo.Session, i *discordgo.InteractionCreate, ephemeral bool) error {
	data := &discordgo.InteractionResponseData{}
	if ephemeral {
		data.Flags = discordgo.MessageFlagsEphemeral
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: data,
	})
}

// DeferEphemeral acknowledges an interaction privately.
func DeferEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return Defer(s, i, true)
}

// Reply answers an interaction, privately when ephemeral is true.
func Reply(s *discordgo.Session, i *discordgo.InteractionCreate, content string, ephemeral bool) error {
	data := &discordgo.InteractionResponseData{Content: content}
	if ephemeral {
		data.Flags = discordgo.MessageFlagsEphemeral
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: data,
	})
}

// ReplyEphemeral answers an interaction privately.
func ReplyEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) error {
	return Reply(s, i, content, true)
}

// Edit replaces a deferred reply.
func Edit(s *discordgo.Session, i *discordgo.InteractionCreate, content string) error {
	_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content})
	return err
}

// EditEphemeral replaces a deferred private reply.
func EditEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) error {
	return Edit(s, i, content)
}

// EditChunks replaces a deferred reply, splitting long text across follow-up
// messages. Follow-ups match the visibility of the original reply.
func EditChunks(s *discordgo.Session, i *discordgo.InteractionCreate, text string, ephemeral bool) error {
	chunks := chunk(text, 1900)
	if len(chunks) == 0 {
		return Edit(s, i, "")
	}
	if err := Edit(s, i, chunks[0]); err != nil {
		return err
	}
	for _, part := range chunks[1:] {
		params := &discordgo.WebhookParams{Content: part}
		if ephemeral {
			params.Flags = discordgo.MessageFlagsEphemeral
		}
		if _, err := s.FollowupMessageCreate(i.Interaction, true, params); err != nil {
			return err
		}
	}
	return nil
}

// EditEphemeralChunks replaces a deferred private reply.
func EditEphemeralChunks(s *discordgo.Session, i *discordgo.InteractionCreate, text string) error {
	return EditChunks(s, i, text, true)
}

func chunk(text string, size int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var out []string
	for len(text) > size {
		cut := strings.LastIndexByte(text[:size], '\n')
		if cut <= 0 {
			cut = size
		}
		out = append(out, text[:cut])
		text = strings.TrimLeft(text[cut:], "\n")
	}
	if text != "" {
		out = append(out, text)
	}
	return out
}
