package main

import (
	"context"
	"fmt"

	"github.com/prateek/serial-sync/internal/app"
)

type SetupEnrichCmd struct {
	Source string `name:"source" help:"Restrict enrichment to this configured source."`
}

func (cmd *SetupEnrichCmd) Run(cli *CLI) error {
	return withService(cli.ConfigPath, func(ctx context.Context, service *app.Service) error {
		count, err := service.EnrichStored(ctx, cmd.Source)
		if err != nil {
			return err
		}
		fmt.Printf("enriched %d captured releases; no content hashes or publications changed\n", count)
		return nil
	})
}
