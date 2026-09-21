package main

import (
	"context"
	"fmt"

	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/provider"
	"github.com/prateek/serial-sync/internal/provider/patreon"
)

type SetupMembershipsCmd struct {
	AuthProfile string `name:"auth-profile" help:"Saved session profile to inspect."`
	Format      string `name:"format" enum:"text,json" default:"text"`
}

func (cmd *SetupMembershipsCmd) Run(cli *CLI) error {
	cfg, roots, err := loadConfig(cli.ConfigPath)
	if err != nil {
		return err
	}
	service := app.New(cfg, roots, cli.ConfigPath, nil, provider.NewRegistry(patreon.New()))
	rows, err := service.Memberships(context.Background(), cmd.AuthProfile)
	if err != nil {
		return err
	}
	if cmd.Format == "json" {
		return printJSON(rows)
	}
	fmt.Println(app.FormatMemberships(rows))
	return nil
}
