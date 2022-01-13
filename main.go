package main

import (
	"context"
	"log"
	"os"

	"github.com/daichitakahashi/lb/cmd"
	"github.com/daichitakahashi/lb/config"
	"go.uber.org/multierr"
)

func main() {
	ctx := context.Background()

	conf, err := loadConfig()
	if err != nil {
		log.Fatalln(err)
	}

	err = cmd.Run(ctx, conf)
	if err != nil {
		log.Fatal(err)
	}
}

func loadConfig() (c *config.Config, err error) {
	f, err := os.Open("config.json")
	if err != nil {
		return nil, err
	}
	defer func() {
		err = multierr.Append(err, f.Close())
	}()

	return config.Load(f)
}
