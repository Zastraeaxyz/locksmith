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
