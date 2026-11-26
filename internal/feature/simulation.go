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

const simulationNamespace = "simulation"

// simulationButtons returns the Simulation button for a card template. The
// button carries only the template ID, so the follow-up reply shows no name
// from the template or the member.
func simulationButtons(templateID cs.ID) []discordgo.MessageComponent {
	if strings.TrimSpace(templateID.String()) == "" {
		return nil
	}
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				CustomID: simulationNamespace + ":" + templateID.String(),
				Label:    "Simulation",
				Style:    discordgo.SecondaryButton,
			},
		}},
	}
}

// handleSimulationComponent previews a card template for the member who
// pressed the button. The result is a private follow-up, so pressing it on a
// public share post tells the channel nothing.
func (f *Feature) handleSimulationComponent(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	parts := strings.Split(i.MessageComponentData().CustomID, ":")
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		return bot.ReplyEphemeral(s, i, "This button is out of date.")
	}
	templateID := cs.ID(strings.TrimSpace(parts[1]))

	userID := bot.UserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}
	if err := bot.DeferUpdate(s, i); err != nil {
		return err
	}

	client, _, err := f.clientFor(ctx, userID)
	if err != nil {
		f.track("simulation failed: not logged in")
		return bot.ReplyEphemeralFollowup(s, i, f.apiFailure(ctx, userID, err))
	}

	simulation, err := client.Simulate(ctx, templateID)
	if err != nil {
		f.track("simulation failed: " + simulationFailureReason(err))
		f.log.Info("simulating a lock template failed",
			"user_id", userID,
			"template_id", templateID.String(),
			"err", err,
		)
		if message := simulationFailureText(err); message != "" {
			return bot.ReplyEphemeralFollowup(s, i, message)
		}
		return bot.ReplyEphemeralFollowup(s, i, f.apiFailure(ctx, userID, err))
	}

	f.track("simulation success")
	return bot.ReplyEphemeralFollowup(s, i, simulationText(simulation))
}

// simulationText renders ChastiSafe's statistical preview. Durations are
// periods, so they stay as plain text rather than Discord timestamps.
func simulationText(simulation *cs.Simulation) string {
	if simulation == nil {
		return "ChastiSafe did not return a simulation for that lock."
	}

	var b strings.Builder
	b.WriteString(":game_die: **Simulation**\n\n")

	lines := 0
	if simulation.AvgDuration > 0 {
		fmt.Fprintf(&b, "Average duration: **%s**\n", secondsText(simulation.AvgDuration))
		lines++
	}
	if simulation.MinDuration > 0 && simulation.MaxDuration > 0 {
		if simulation.MinDuration == simulation.MaxDuration {
			fmt.Fprintf(&b, "Duration range: %s\n", secondsText(simulation.MinDuration))
		} else {
			fmt.Fprintf(&b, "Duration range: %s to %s\n",
				secondsText(simulation.MinDuration),
				secondsText(simulation.MaxDuration),
			)
		}
		lines++
	}
	if simulation.AvgCards > 0 {
		fmt.Fprintf(&b, "Average cards drawn: **%d**\n", simulation.AvgCards)
		lines++
	}
	if simulation.MinCards > 0 && simulation.MaxCards > 0 {
		if simulation.MinCards == simulation.MaxCards {
			fmt.Fprintf(&b, "Card range: %d\n", simulation.MinCards)
		} else {
			fmt.Fprintf(&b, "Card range: %d to %d\n", simulation.MinCards, simulation.MaxCards)
		}
		lines++
	}
	if simulation.Regularity > 0 {
		fmt.Fprintf(&b, "Draw interval: every %s\n", cs.FormatMinutes(simulation.Regularity))
		lines++
	}
	if simulation.MinimumDuration > 0 {
		fmt.Fprintf(&b, "Minimum duration: %s\n", secondsText(simulation.MinimumDuration))
		lines++
	}

	if lines == 0 {
		return "ChastiSafe did not return a simulation for that lock."
	}
	if simulation.NumberOfSimulations > 0 {
		fmt.Fprintf(&b, "-# Based on %d simulated runs.\n", simulation.NumberOfSimulations)
	}
	return strings.TrimRight(b.String(), "\n")
}

// secondsText renders a ChastiSafe second count as a plain duration.
func secondsText(seconds int) string {
	if seconds <= 0 {
		return "0 seconds"
	}
	return cs.FormatDuration(time.Duration(seconds) * time.Second)
}

// simulationFailureText explains a simulation the keyholder turned off. Other
// failures fall back to the generic API message.
func simulationFailureText(err error) string {
	if errors.Is(err, cs.ErrSimulationDisabled) {
		return "The keyholder does not want you to simulate this lock."
	}
	return ""
}

// simulationFailureReason buckets a failed simulation for anonymous analytics.
// The labels stay coarse: no IDs, names, or codes.
func simulationFailureReason(err error) string {
	switch {
	case errors.Is(err, cs.ErrUnauthorized):
		return "rejected"
	case errors.Is(err, cs.ErrLockNotFound):
		return "not found"
	case errors.Is(err, cs.ErrNotKeyholder):
		return "not keyholder"
	case errors.Is(err, cs.ErrSimulationDisabled):
		return "disabled"
	case errors.Is(err, cs.ErrNotAllowed):
		return "not allowed"
	default:
		return "unreachable"
	}
}
