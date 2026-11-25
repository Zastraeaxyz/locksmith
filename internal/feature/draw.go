package feature

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/bot"
)

const (
	drawNamespace        = "draw"
	drawBoardNamespace   = "drawboard"
	drawSessionTTL       = 2 * time.Hour
	drawLogLimit         = 50
	drawDescriptionLimit = 3900

	resetNamespace    = "reset"
	resetConfirmInput = "confirm"
	resetConfirmation = "YES RESET"
)

func (f *Feature) handleDrawComponent(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	parts := strings.Split(i.MessageComponentData().CustomID, ":")
	if len(parts) != 3 {
		return bot.ReplyEphemeral(s, i, "This button is out of date.")
	}
	action, sessionID := parts[1], parts[2]

	session, ok := f.session(sessionID)
	if !ok {
		_ = bot.ReplyEphemeral(s, i, "This draw session expired. Run `/lock draw` again.")
		if i.Message != nil {
			_ = bot.ClearComponents(s, i.ChannelID, i.Message.ID)
		}
		return nil
	}

	userID := bot.UserID(i)
	if !session.canUse(userID) {
		if action == "stop" {
			return bot.ReplyEphemeral(s, i, "You can't stop someone else's draw session.")
		}
		return bot.ReplyEphemeral(s, i, "You can't draw someone else's lock.")
	}

	switch action {
	case "next":
		return f.drawNext(ctx, s, i, session)
	case "stop":
		return f.drawStop(ctx, s, i, session)
	case "open":
		return f.drawOpen(ctx, s, i, session)
	case "green":
		return f.drawGreenBack(ctx, s, i, session)
	case "reset":
		return f.drawResetModal(s, i, session)
	case "decide":
		return f.drawAskKeyholder(ctx, s, i, session)
	default:
		return bot.ReplyEphemeral(s, i, "Unknown draw action.")
	}
}

func (f *Feature) drawNext(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, session *drawSession) error {
	if !session.begin() {
		return bot.ReplyEphemeral(s, i, "A draw is already in progress. Try again in a moment.")
	}
	defer session.end()

	if err := bot.DeferUpdate(s, i); err != nil {
		return err
	}

	client, note, ok := f.boardClient(ctx, session)
	if !ok {
		return f.renderBoard(s, i, session, nil, false, note)
	}

	locks, err := client.Loaded(ctx)
	if err != nil {
		note := f.apiFailure(ctx, session.ownerID, err)
		if errors.Is(err, cs.ErrUnauthorized) {
			f.deleteSession(session.id)
			return f.renderBoard(s, i, session, nil, false, note)
		}
		return f.renderBoard(s, i, session, nil, true, note)
	}

	lock, note, ok := f.boardLock(locks, session)
	if !ok {
		return f.renderBoard(s, i, session, nil, false, note)
	}

	switch {
	case lock.IsReady():
		// Keep the session so the ready actions stay available.
		return f.renderBoard(s, i, session, lock, true, "The lock is ready to unlock.")
	case lock.IsFrozen():
		f.deleteSession(session.id)
		return f.renderBoard(s, i, session, lock, false, "The lock is frozen. Drawing stopped.")
	case lock.DrawsAvailable() == 0:
		f.deleteSession(session.id)
		return f.renderBoard(s, i, session, lock, false, "")
	}

	result, err := client.Pick(ctx, lock.ID)
	if err != nil {
		// Leave the board open when draws may still be available so the member
		// can retry.
		return f.renderBoard(s, i, session, lock, true, f.apiFailure(ctx, session.ownerID, err))
	}

	updated := lock
	if result.Lock != nil {
		updated = result.Lock
	}
	session.setLock(updated)
	session.add(drawEntry{
		card:        result.Card,
		dice:        result.Dice,
		byKeyholder: bot.UserID(i) != session.ownerID,
	})

	ready := updated.IsReady()
	keep := boardKeepsOpen(updated)
	drawNote := ""
	if ready {
		drawNote = "That was the last draw. The lock is ready to unlock."
	} else if result.Done {
		drawNote = "That was the last draw."
	}

	if !keep {
		f.deleteSession(session.id)
	}
	return f.renderBoard(s, i, session, updated, keep, drawNote)
}

func (f *Feature) drawStop(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, session *drawSession) error {
	if !session.begin() {
		return bot.ReplyEphemeral(s, i, "A draw is in progress. Try again in a moment.")
	}
	defer session.end()

	if err := bot.DeferUpdate(s, i); err != nil {
		return err
	}
	f.deleteSession(session.id)
	return f.renderBoard(s, i, session, nil, false, "Drawing stopped.")
}

// drawOpen opens a ready lock from the board and reveals the code. The board is
// public, so in a guild the code goes to the presser privately; in a DM or
// group DM it is a normal message, matching /lock open.
func (f *Feature) drawOpen(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, session *drawSession) error {
	if !session.begin() {
		return bot.ReplyEphemeral(s, i, "A draw is in progress. Try again in a moment.")
	}
	defer session.end()

	if err := bot.DeferUpdate(s, i); err != nil {
		return err
	}

	client, note, ok := f.boardClient(ctx, session)
	if !ok {
		return f.renderBoard(s, i, session, nil, false, note)
	}
	locks, err := client.Loaded(ctx)
	if err != nil {
		return f.renderBoard(s, i, session, session.currentLock(), true, f.apiFailure(ctx, session.ownerID, err))
	}

	lock, note, ok := f.boardLock(locks, session)
	if !ok {
		return f.renderBoard(s, i, session, nil, false, note)
	}
	if !lock.IsReady() {
		return f.renderBoard(s, i, session, lock, true, "That lock is not ready to open yet.")
	}

	result, err := client.Open(ctx, lock.ID)
	if err != nil {
		return f.renderBoard(s, i, session, lock, true, f.apiFailure(ctx, session.ownerID, err))
	}

	f.deleteSession(session.id)
	f.forgetLockAutocomplete(session.ownerID)
	f.log.Info("lock opened from the draw board", "user_id", session.ownerID, "lock_id", lock.ID.String())

	if err := f.renderBoard(s, i, session, lock, false, openBoardNote(i)); err != nil {
		return err
	}
	return replyBoardCode(s, i, lock, result.Code)
}

// drawGreenBack returns the last green card to the deck and keeps the board
// open, so the lockee can carry on if the lock is no longer ready.
func (f *Feature) drawGreenBack(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, session *drawSession) error {
	if !session.begin() {
		return bot.ReplyEphemeral(s, i, "A draw is in progress. Try again in a moment.")
	}
	defer session.end()

	if err := bot.DeferUpdate(s, i); err != nil {
		return err
	}

	client, _, err := f.clientFor(ctx, session.ownerID)
	if err != nil {
		return bot.ReplyEphemeralFollowup(s, i, f.apiFailure(ctx, session.ownerID, err))
	}
	if err := client.PutGreenBack(ctx, session.lockID); err != nil {
		f.log.Warn("putting a green card back failed", "user_id", session.ownerID, "lock_id", session.lockID.String(), "err", err)
		return bot.ReplyEphemeralFollowup(s, i, f.apiFailure(ctx, session.ownerID, err))
	}
	f.forgetLockAutocomplete(session.ownerID)

	locks, err := client.Loaded(ctx)
	if err != nil {
		return f.renderBoard(s, i, session, session.currentLock(), true,
			"Put the last green back in the deck, but I could not refresh the lock.")
	}
	lock, note, ok := f.boardLock(locks, session)
	if !ok {
		return f.renderBoard(s, i, session, nil, false, note)
	}
	return f.renderBoard(s, i, session, lock, true, "Put the last green back in the deck.")
}

// drawAskKeyholder asks ChastiSafe to send the keyholder its decision prompts
// for this lock.
func (f *Feature) drawAskKeyholder(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, session *drawSession) error {
	if session.keyholderID == "" {
		return bot.ReplyEphemeral(s, i, "This is a self lock, so there is no keyholder to ask.")
	}
	if !session.begin() {
		return bot.ReplyEphemeral(s, i, "A draw is in progress. Try again in a moment.")
	}
	defer session.end()

	if err := bot.DeferUpdate(s, i); err != nil {
		return err
	}

	client, _, err := f.clientFor(ctx, session.ownerID)
	if err != nil {
		return bot.ReplyEphemeralFollowup(s, i, f.apiFailure(ctx, session.ownerID, err))
	}
	if err := client.RequestKeyholderDecision(ctx, session.lockID); err != nil {
		f.log.Warn("asking the keyholder to decide failed", "user_id", session.ownerID, "lock_id", session.lockID.String(), "err", err)
		return bot.ReplyEphemeralFollowup(s, i, f.apiFailure(ctx, session.ownerID, err))
	}

	lock := session.currentLock()
	if locks, err := client.Loaded(ctx); err == nil {
		if updated := findLockByID(locks, session.lockID); updated != nil {
			lock = updated
			session.setLock(updated)
		}
	}
	return f.renderBoard(s, i, session, lock, true, "Asked your keyholder to decide.")
}

// drawResetModal opens the confirmation form. Reset restarts the lock from the
// beginning, so it is the only board action that asks for typed confirmation.
func (f *Feature) drawResetModal(s *discordgo.Session, i *discordgo.InteractionCreate, session *drawSession) error {
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			Title:    "Reset lock",
			CustomID: resetNamespace + ":confirm:" + session.id,
			Components: []discordgo.MessageComponent{
				&discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					&discordgo.TextInput{
						CustomID:    resetConfirmInput,
						Label:       "Type YES RESET to confirm",
						Style:       discordgo.TextInputShort,
						Placeholder: resetConfirmation,
						MinLength:   len(resetConfirmation),
						MaxLength:   20,
						Required:    true,
					},
				}},
			},
		},
	})
}

// resetConfirmed reports whether the typed confirmation is the exact phrase.
func resetConfirmed(value string) bool {
	return strings.TrimSpace(value) == resetConfirmation
}

// handleResetModal confirms and runs a reset from the draw board.
func (f *Feature) handleResetModal(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	parts := strings.Split(i.ModalSubmitData().CustomID, ":")
	if len(parts) != 3 || parts[1] != "confirm" || strings.TrimSpace(parts[2]) == "" {
		return bot.ReplyEphemeral(s, i, "This reset form is out of date. Run `/lock draw` again.")
	}

	session, ok := f.session(parts[2])
	if !ok {
		return bot.ReplyEphemeral(s, i, "This draw session expired. Run `/lock draw` again.")
	}
	if !session.canUse(bot.UserID(i)) {
		return bot.ReplyEphemeral(s, i, "You can't reset someone else's lock.")
	}

	if !resetConfirmed(modalValue(i.ModalSubmitData().Components, resetConfirmInput)) {
		return bot.ReplyEphemeral(s, i, "Reset cancelled. Type `YES RESET` exactly to confirm.")
	}

	if !session.begin() {
		return bot.ReplyEphemeral(s, i, "A draw is in progress. Try again in a moment.")
	}
	defer session.end()

	if err := bot.DeferEphemeral(s, i); err != nil {
		return err
	}

	client, _, err := f.clientFor(ctx, session.ownerID)
	if err != nil {
		return sendEphemeral(s, i, f.apiFailure(ctx, session.ownerID, err))
	}
	if _, err := client.Reset(ctx, session.lockID); err != nil {
		f.log.Warn("resetting a lock failed", "user_id", session.ownerID, "lock_id", session.lockID.String(), "err", err)
		return sendEphemeral(s, i, f.apiFailure(ctx, session.ownerID, err))
	}
	f.forgetLockAutocomplete(session.ownerID)
	f.log.Info("lock reset", "user_id", session.ownerID, "lock_id", session.lockID.String())

	locks, err := client.Loaded(ctx)
	if err != nil {
		return sendEphemeral(s, i, "The lock was reset, but I could not refresh the board.")
	}
	lock := findLockByID(locks, session.lockID)
	if lock == nil {
		f.deleteSession(session.id)
		if err := f.editBoardMessage(s, session, nil, false, "The lock was reset and is no longer active."); err != nil {
			f.log.Warn("updating the board after reset", "err", err)
		}
		return sendEphemeral(s, i, "The lock was reset and is no longer active.")
	}

	session.setLock(lock)
	if err := f.editBoardMessage(s, session, lock, true, "Lock reset. Starting over from the beginning."); err != nil {
		f.log.Warn("updating the board after reset", "err", err)
	}
	return sendEphemeral(s, i, fmt.Sprintf("**%s** was reset. Drawing starts over from the beginning.", lockName(lock)))
}

// externalKeyholderID returns the keyholder's Discord user ID. ChastiSafe
// identifies an account by the Discord snowflake linked in ChastiSafe, so this
// is directly comparable to the member who pressed a button.
func externalKeyholderID(lock *cs.LoadedLock) string {
	if lock == nil || lock.IsSelfLock() {
		return ""
	}
	id := strings.TrimSpace(lock.KeyholderID.String())
	if id == "" || id == "0" {
		return ""
	}
	return id
}

func interactionName(i *discordgo.InteractionCreate) string {
	if i != nil && i.Member != nil {
		if nick := strings.TrimSpace(i.Member.Nick); nick != "" {
			return nick
		}
		if i.Member.User != nil {
			if name := strings.TrimSpace(i.Member.User.Username); name != "" {
				return name
			}
		}
	}
	if i != nil && i.User != nil {
		if name := strings.TrimSpace(i.User.Username); name != "" {
			return name
		}
	}
	return "this member"
}
