package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/prateek/serial-sync/internal/app"
)

type SetupCandidatesCmd struct {
	List    SetupCandidatesListCmd    `cmd:"" default:"withargs" hidden:""`
	Dismiss SetupCandidatesDismissCmd `cmd:"" help:"Dismiss current evidence; new evidence reopens the candidate."`
}

type SetupCandidatesListCmd struct {
	Source string `name:"source" help:"Limit candidates to one source."`
	Format string `name:"format" enum:"text,json" default:"text"`
}

func (cmd *SetupCandidatesListCmd) Run(cli *CLI) error {
	service, close, err := bootstrapMode(cli.ConfigPath, true)
	if err != nil {
		return err
	}
	defer close()
	candidates, err := service.Repo.ListDiscoveryCandidates(context.Background(), cmd.Source)
	if err != nil {
		return err
	}
	if cmd.Format == "json" {
		return printJSON(candidates)
	}
	fmt.Println(app.FormatCandidates(candidates))
	return nil
}

type SetupCandidatesDismissCmd struct {
	ID     string `arg:"" name:"id"`
	Reason string `name:"reason" required:"" help:"Why this evidence needs no mapping change."`
}

func (cmd *SetupCandidatesDismissCmd) Run(cli *CLI) error {
	if strings.TrimSpace(cmd.Reason) == "" {
		return fmt.Errorf("--reason must explain the dismissal")
	}
	service, close, err := bootstrap(cli.ConfigPath)
	if err != nil {
		return err
	}
	defer close()
	if err := service.Repo.DismissDiscoveryCandidate(context.Background(), cmd.ID, cmd.Reason); err != nil {
		return err
	}
	fmt.Printf("Dismissed %s for its current evidence.\n", cmd.ID)
	return nil
}
