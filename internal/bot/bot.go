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

// ComponentHandler runs a message component interaction (buttons, selects).
type ComponentHandler func(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error

// AutocompleteHandler answers an autocomplete interaction.
type AutocompleteHandler func(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error

// Tracker records anonymous usage events.
type Tracker interface {
	Track(name string, data map[string]any)
}

// Bot owns the Discord session and dispatches interactions.
type Bot struct {
	Session *discordgo.Session
	Log     *slog.Logger

	guildID string

	mu                   sync.RWMutex
	commands             map[string]*discordgo.ApplicationCommand
	commandHandlers      map[string]CommandHandler
	modalHandlers        map[string]ModalHandler
	componentHandlers    map[string]ComponentHandler
	autocompleteHandlers map[string]AutocompleteHandler
	tracker              Tracker
	synced               bool
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
		Session:              s,
		Log:                  log,
		guildID:              strings.TrimSpace(guildID),
		commands:             make(map[string]*discordgo.ApplicationCommand),
		commandHandlers:      make(map[string]CommandHandler),
		modalHandlers:        make(map[string]ModalHandler),
		componentHandlers:    make(map[string]ComponentHandler),
		autocompleteHandlers: make(map[string]AutocompleteHandler),
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

// AddComponent registers a message component handler for a custom ID
// namespace, for example "draw" handles "draw:next:<session>".
func (b *Bot) AddComponent(namespace string, handler ComponentHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.componentHandlers[namespace] = handler
}

// AddAutocomplete registers an autocomplete handler for a top-level command
// name, for example "lock" handles autocomplete requests for /lock.
func (b *Bot) AddAutocomplete(commandName string, handler AutocompleteHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.autocompleteHandlers[commandName] = handler
}

// SetTracker records anonymous usage events for every handled interaction.
// A nil tracker disables tracking.
func (b *Bot) SetTracker(t Tracker) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tracker = t
}

// Track records an anonymous usage event. Handlers can call it to record
// funnel steps that are not interactions, such as a successful login.
func (b *Bot) Track(name string, data map[string]any) {
	b.mu.RLock()
	tracker := b.tracker
	b.mu.RUnlock()
	if tracker == nil {
		return
	}
	tracker.Track(name, data)
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
	base := make([]*discordgo.ApplicationCommand, 0, len(b.commands))
	for _, cmd := range b.commands {
		base = append(base, cmd)
	}
	guildID := b.guildID
	b.mu.RUnlock()

	sort.Slice(base, func(i, j int) bool { return base[i].Name < base[j].Name })

	var errs []error
	if guildID != "" {
		if _, err := s.ApplicationCommandBulkOverwrite(s.State.User.ID, guildID, base); err != nil {
			errs = append(errs, fmt.Errorf("overwriting guild commands: %w", err))
		} else {
			b.Log.Info("registered guild commands", "count", len(base), "guild_id", guildID)
		}
	}

	global := make([]*discordgo.ApplicationCommand, 0, len(base))
	for _, cmd := range base {
		global = append(global, globalCommand(cmd))
	}
	if _, err := s.ApplicationCommandBulkOverwrite(s.State.User.ID, "", global); err != nil {
		errs = append(errs, fmt.Errorf("overwriting global commands: %w", err))
	} else {
		b.Log.Info("registered global commands",
			"count", len(global),
			"note", "user install; may take up to an hour to appear",
		)
	}
	return errors.Join(errs...)
}

// globalCommand returns a copy of cmd with user install enabled. Guild
// commands cannot be user-installable, so the base definitions are registered
// separately for the optional home guild.
func globalCommand(cmd *discordgo.ApplicationCommand) *discordgo.ApplicationCommand {
	clone := *cmd
	integrationTypes := []discordgo.ApplicationIntegrationType{
		discordgo.ApplicationIntegrationGuildInstall,
		discordgo.ApplicationIntegrationUserInstall,
	}
	contexts := []discordgo.InteractionContextType{
		discordgo.InteractionContextGuild,
		discordgo.InteractionContextBotDM,
		discordgo.InteractionContextPrivateChannel,
	}
	clone.IntegrationTypes = &integrationTypes
	clone.Contexts = &contexts
	return &clone
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
		b.Track(commandLabel(i), nil)
		b.run(name, func(ctx context.Context) error { return handler(ctx, s, i) })
	case discordgo.InteractionModalSubmit:
		namespace := customIDNamespace(i.ModalSubmitData().CustomID)
		b.mu.RLock()
		handler := b.modalHandlers[namespace]
		b.mu.RUnlock()
		if handler == nil {
			return
		}
		b.Track("modal "+namespace, nil)
		b.run("modal "+namespace, func(ctx context.Context) error { return handler(ctx, s, i) })
	case discordgo.InteractionMessageComponent:
		namespace := customIDNamespace(i.MessageComponentData().CustomID)
		b.mu.RLock()
		handler := b.componentHandlers[namespace]
		b.mu.RUnlock()
		if handler == nil {
			return
		}
		b.Track("component "+namespace, nil)
		b.run("component "+namespace, func(ctx context.Context) error { return handler(ctx, s, i) })
	case discordgo.InteractionApplicationCommandAutocomplete:
		name := i.ApplicationCommandData().Name
		b.mu.RLock()
		handler := b.autocompleteHandlers[name]
		b.mu.RUnlock()
		if handler == nil {
			return
		}
		// Autocomplete fires on every keystroke, so it is not tracked.
		b.run("autocomplete "+name, func(ctx context.Context) error { return handler(ctx, s, i) })
	}
}

// commandLabel names a slash command, including its subcommand when present,
// for example "/lock status".
func commandLabel(i *discordgo.InteractionCreate) string {
	data := i.ApplicationCommandData()
	if len(data.Options) > 0 {
		opt := data.Options[0]
		if opt.Type == discordgo.ApplicationCommandOptionSubCommand ||
			opt.Type == discordgo.ApplicationCommandOptionSubCommandGroup {
			return "/" + data.Name + " " + opt.Name
		}
	}
	return "/" + data.Name
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

// Username returns the Discord username behind an interaction.
func Username(i *discordgo.InteractionCreate) string {
	if i == nil {
		return ""
	}
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.Username
	}
	if i.User != nil {
		return i.User.Username
	}
	return ""
}

// Autocomplete answers an autocomplete interaction. Discord requires a
// response even when there is nothing to suggest, so choices may be empty.
func Autocomplete(s *discordgo.Session, i *discordgo.InteractionCreate, choices []*discordgo.ApplicationCommandOptionChoice) error {
	if choices == nil {
		choices = []*discordgo.ApplicationCommandOptionChoice{}
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	})
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

// ReplyEmbed answers an interaction with an optional embed and component row.
func ReplyEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, embeds []*discordgo.MessageEmbed, components []discordgo.MessageComponent, ephemeral bool) error {
	return ReplyEmbedAllowed(s, i, embeds, components, ephemeral, nil)
}

// ReplyEmbedAllowed answers an interaction with an embed and explicit allowed
// mentions, so user-authored embed text cannot ping the channel.
func ReplyEmbedAllowed(s *discordgo.Session, i *discordgo.InteractionCreate, embeds []*discordgo.MessageEmbed, components []discordgo.MessageComponent, ephemeral bool, allowedMentions *discordgo.MessageAllowedMentions) error {
	data := &discordgo.InteractionResponseData{
		Embeds:          embeds,
		Components:      components,
		AllowedMentions: allowedMentions,
	}
	if ephemeral {
		data.Flags = discordgo.MessageFlagsEphemeral
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: data,
	})
}

// Edit replaces a deferred reply.
func Edit(s *discordgo.Session, i *discordgo.InteractionCreate, content string) error {
	_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content})
	return err
}

// EditMessage replaces a deferred reply with an optional embed and component
// row. A non-nil empty components slice removes any existing components.
func EditMessage(s *discordgo.Session, i *discordgo.InteractionCreate, content string, embeds []*discordgo.MessageEmbed, components []discordgo.MessageComponent) error {
	_, err := EditMessageGet(s, i, content, embeds, components)
	return err
}

// EditMessageGet replaces a deferred reply like EditMessage and returns the
// edited message, so a caller can keep its ID for a later edit.
func EditMessageGet(s *discordgo.Session, i *discordgo.InteractionCreate, content string, embeds []*discordgo.MessageEmbed, components []discordgo.MessageComponent) (*discordgo.Message, error) {
	return EditMessageGetAllowed(s, i, content, embeds, components, nil)
}

// EditMessageAllowed replaces a deferred reply and sets explicit allowed
// mentions, so user-authored embed text cannot ping the channel.
func EditMessageAllowed(s *discordgo.Session, i *discordgo.InteractionCreate, content string, embeds []*discordgo.MessageEmbed, components []discordgo.MessageComponent, allowedMentions *discordgo.MessageAllowedMentions) error {
	_, err := EditMessageGetAllowed(s, i, content, embeds, components, allowedMentions)
	return err
}

// EditMessageGetAllowed replaces a deferred reply with explicit allowed
// mentions and returns the edited message.
func EditMessageGetAllowed(s *discordgo.Session, i *discordgo.InteractionCreate, content string, embeds []*discordgo.MessageEmbed, components []discordgo.MessageComponent, allowedMentions *discordgo.MessageAllowedMentions) (*discordgo.Message, error) {
	edit := &discordgo.WebhookEdit{AllowedMentions: allowedMentions}
	if content != "" {
		edit.Content = &content
	}
	if embeds != nil {
		edit.Embeds = &embeds
	}
	if components == nil {
		components = []discordgo.MessageComponent{}
	}
	edit.Components = &components
	return s.InteractionResponseEdit(i.Interaction, edit)
}

// DeferUpdate acknowledges a component interaction in place.
func DeferUpdate(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	})
}

// DeleteResponse removes the bot's deferred interaction response.
func DeleteResponse(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return s.InteractionResponseDelete(i.Interaction)
}

// ReplyFollowup sends a follow-up to an interaction that has already been
// acknowledged. An ephemeral follow-up is private.
func ReplyFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, content string, ephemeral bool) error {
	params := &discordgo.WebhookParams{Content: content}
	if ephemeral {
		params.Flags = discordgo.MessageFlagsEphemeral
	}
	_, err := s.FollowupMessageCreate(i.Interaction, true, params)
	return err
}

// ReplyEphemeralFollowup sends a private follow-up to an interaction that has
// already been acknowledged.
func ReplyEphemeralFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, content string) error {
	return ReplyFollowup(s, i, content, true)
}

// EditEmbeds replaces a deferred reply with embeds, then sends any extra
// embeds as follow-ups because Discord caps ten embeds per message.
func EditEmbeds(s *discordgo.Session, i *discordgo.InteractionCreate, embeds []*discordgo.MessageEmbed, ephemeral bool) error {
	const perMessage = 10
	if len(embeds) == 0 {
		return Edit(s, i, "")
	}

	first := embeds
	if len(first) > perMessage {
		first = first[:perMessage]
	}
	if err := EditMessage(s, i, "", first, nil); err != nil {
		return err
	}
	for start := perMessage; start < len(embeds); start += perMessage {
		end := min(start+perMessage, len(embeds))
		params := &discordgo.WebhookParams{Embeds: embeds[start:end]}
		if ephemeral {
			params.Flags = discordgo.MessageFlagsEphemeral
		}
		if _, err := s.FollowupMessageCreate(i.Interaction, true, params); err != nil {
			return err
		}
	}
	return nil
}

// ClearComponents removes the components from a message the bot posted.
func ClearComponents(s *discordgo.Session, channelID, messageID string) error {
	if channelID == "" || messageID == "" {
		return nil
	}
	empty := []discordgo.MessageComponent{}
	_, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel:    channelID,
		ID:         messageID,
		Components: &empty,
	})
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

// EditMessageChunks replaces a deferred reply like EditChunks and keeps the
// component row on the first message, so a button survives a long reply.
func EditMessageChunks(s *discordgo.Session, i *discordgo.InteractionCreate, text string, components []discordgo.MessageComponent, ephemeral bool) error {
	chunks := chunk(text, 1900)
	if len(chunks) == 0 {
		return EditMessage(s, i, "", nil, components)
	}

	if components == nil {
		components = []discordgo.MessageComponent{}
	}
	first := &discordgo.WebhookEdit{Content: &chunks[0], Components: &components}
	if _, err := s.InteractionResponseEdit(i.Interaction, first); err != nil {
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
