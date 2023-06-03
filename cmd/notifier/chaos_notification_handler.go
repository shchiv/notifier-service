package main

import (
	"github.com/shchiv/notifier-service/pkg/chaos"
	"github.com/shchiv/notifier-service/pkg/notifier"
	"go.uber.org/zap"
)

var cfg chaos.Config

func init() {
	cfg = chaos.ParseConfig()
}

// doChaosEventDelivery(event *notifier.Event, webhook string, logger *zap.SugaredLogger) error {
func doChaosEventDelivery(_ *notifier.Event, _ string, _ *zap.SugaredLogger) error {
	if err := chaos.Start(cfg); err != nil {
		return err
	}
	return nil
}
