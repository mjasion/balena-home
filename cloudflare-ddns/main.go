package main

import (
	"context"
	"github.com/cloudflare/cloudflare-go"
	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
	"os"
	"os/signal"
	"syscall"
)

func main() {

	updateDomain()

	sigs := make(chan os.Signal, 1)
	done := make(chan bool, 1)

	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigs
		logrus.WithField("signal", sig).Infoln("Got signal")
		done <- true
	}()

	logrus.Infoln("Initialized")
	<-done
}

func updateDomain() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	token := os.Getenv("CF_TOKEN")
	period := os.Getenv("PERIOD")
	domain := os.Getenv("DOMAIN")
	api, err := cloudflare.NewWithAPIToken(token)

	if err != nil {
		logrus.WithError(err).Error()
		return
	}

	if period == "" {
		logrus.Error("Missing PERIOD env")
		logrus.Exit(1)
	}
	if domain == "" {
		logrus.Error("Missing DOMAIN env")
		logrus.Exit(1)
	}

	c := cron.New()
	c.AddFunc("@every "+period, func() {
		err := UpdateDomain(ctx, api, domain, "https://api.ipify.org/")
		if err != nil {
			logrus.WithError(err).Error()
		}
	})
	c.Start()
}
