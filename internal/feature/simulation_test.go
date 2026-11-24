package feature

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"
)

func TestSimulationButtons(t *testing.T) {
	components := simulationButtons("tpl-1")
	if len(components) != 1 {
		t.Fatalf("components = %d, want 1 row", len(components))
	}
	row, ok := components[0].(discordgo.ActionsRow)
	if !ok {
		t.Fatalf("component type = %T", components[0])
	}
	if len(row.Components) != 1 {
		t.Fatalf("buttons = %d, want 1", len(row.Components))
	}
	button, ok := row.Components[0].(discordgo.Button)
	if !ok {
		t.Fatalf("button type = %T", row.Components[0])
	}
	if button.CustomID != "simulation:tpl-1" || button.Label != "Simulation" || button.Style != discordgo.SecondaryButton {
		t.Fatalf("simulation button = %+v", button)
	}
	if got := simulationButtons(""); got != nil {
		t.Fatalf("empty template ID = %v, want no buttons", got)
	}
}

func TestSimulationText(t *testing.T) {
	simulation := &cs.Simulation{
		AvgDuration:         93600,
		MinDuration:         3600,
		MaxDuration:         172800,
		AvgCards:            14,
		MinCards:            8,
		MaxCards:            22,
		Regularity:          240,
		MinimumDuration:     7200,
		NumberOfSimulations: 100,
	}
	text := simulationText(simulation)
	for _, want := range []string{
		"Simulation",
		"Average duration: **1 day 2 hours**",
		"Duration range: 1 hour to 2 days",
		"Average cards drawn: **14**",
		"Card range: 8 to 22",
		"Draw interval: every 4 hours",
		"Minimum duration: 2 hours",
		"Based on 100 simulated runs",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q:\n%s", want, text)
		}
	}
}

func TestSimulationTextFixedRange(t *testing.T) {
	simulation := &cs.Simulation{
		AvgDuration: 3600,
		MinDuration: 3600,
		MaxDuration: 3600,
		AvgCards:    2,
		MinCards:    2,
		MaxCards:    2,
	}
	text := simulationText(simulation)
	for _, want := range []string{
		"Duration range: 1 hour\n",
		"Card range: 2",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Based on") {
		t.Errorf("text with no run count still mentions runs:\n%s", text)
	}
}

func TestSimulationTextEmpty(t *testing.T) {
	for _, simulation := range []*cs.Simulation{nil, {}} {
		text := simulationText(simulation)
		if !strings.Contains(text, "did not return a simulation") {
			t.Errorf("text = %q, want the fallback", text)
		}
	}
}

func TestSimulationFailureReason(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{err: cs.ErrUnauthorized, want: "rejected"},
		{err: &cs.APIError{HTTPStatus: 403}, want: "rejected"},
		{err: cs.ErrLockNotFound, want: "not found"},
		{err: cs.ErrNotKeyholder, want: "not keyholder"},
		{err: cs.ErrNotAllowed, want: "not allowed"},
		{err: cs.ErrInvalidResponse, want: "unreachable"},
	}
	for _, tc := range cases {
		if got := simulationFailureReason(tc.err); got != tc.want {
			t.Errorf("simulationFailureReason(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}
