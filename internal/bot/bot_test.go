package bot

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestGlobalCommand(t *testing.T) {
	base := &discordgo.ApplicationCommand{
		Name:        "lock",
		Description: "Work with your ChastiSafe lock",
	}

	global := globalCommand(base)
	if global == base {
		t.Fatal("globalCommand should return a copy")
	}
	if base.IntegrationTypes != nil || base.Contexts != nil {
		t.Fatal("base command must not be mutated")
	}
	if global.Name != base.Name || global.Description != base.Description {
		t.Fatalf("copy changed fields: %+v", global)
	}

	if global.IntegrationTypes == nil {
		t.Fatal("integration types missing")
	}
	types := *global.IntegrationTypes
	if len(types) != 2 ||
		types[0] != discordgo.ApplicationIntegrationGuildInstall ||
		types[1] != discordgo.ApplicationIntegrationUserInstall {
		t.Fatalf("integration types = %v", types)
	}

	if global.Contexts == nil {
		t.Fatal("contexts missing")
	}
	contexts := *global.Contexts
	if len(contexts) != 3 ||
		contexts[0] != discordgo.InteractionContextGuild ||
		contexts[1] != discordgo.InteractionContextBotDM ||
		contexts[2] != discordgo.InteractionContextPrivateChannel {
		t.Fatalf("contexts = %v", contexts)
	}
}

func TestCommandLabel(t *testing.T) {
	cases := []struct {
		name string
		data discordgo.ApplicationCommandInteractionData
		want string
	}{
		{
			name: "plain command",
			data: discordgo.ApplicationCommandInteractionData{Name: "login"},
			want: "/login",
		},
		{
			name: "subcommand",
			data: discordgo.ApplicationCommandInteractionData{
				Name: "lock",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{Name: "status", Type: discordgo.ApplicationCommandOptionSubCommand},
				},
			},
			want: "/lock status",
		},
		{
			name: "subcommand group",
			data: discordgo.ApplicationCommandInteractionData{
				Name: "lock",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{Name: "notify", Type: discordgo.ApplicationCommandOptionSubCommandGroup},
				},
			},
			want: "/lock notify",
		},
		{
			name: "plain option is not part of the label",
			data: discordgo.ApplicationCommandInteractionData{
				Name: "profile",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{Name: "view", Type: discordgo.ApplicationCommandOptionString},
				},
			},
			want: "/profile",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i := &discordgo.InteractionCreate{
				Interaction: &discordgo.Interaction{
					Type: discordgo.InteractionApplicationCommand,
					Data: tc.data,
				},
			}
			if got := commandLabel(i); got != tc.want {
				t.Errorf("commandLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

type recordingTracker struct {
	names []string
}

func (r *recordingTracker) Track(name string, data map[string]any) {
	r.names = append(r.names, name)
}

func TestTrackUsesTracker(t *testing.T) {
	b := &Bot{}
	b.track("/login") // nil tracker must not panic

	rec := &recordingTracker{}
	b.SetTracker(rec)
	b.track("/lock draw")

	if len(rec.names) != 1 || rec.names[0] != "/lock draw" {
		t.Fatalf("tracked = %v", rec.names)
	}
}
