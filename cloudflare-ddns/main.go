package main

import (
	"context"
	"github.com/cloudflare/cloudflare-go"
	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	token := os.Getenv("CF_TOKEN")
	period := os.Getenv("PERIOD")
	domain := os.Getenv("DDNS_DOMAIN")
	pushUrl := os.Getenv("PUSH_URL")

	if period == "" {
		logrus.Error("Missing PERIOD env")
		logrus.Exit(1)
	}
	if pushUrl == "" {
		logrus.Error("Missing PUSH_URL env")
		logrus.Exit(1)
	}
	if domain == "" {
		logrus.Error("Missing DOMAIN env")
		logrus.Exit(1)
	}

	healthCheckCron(period, pushUrl)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cloudflareDdnsCron(ctx, period, token, domain)

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

func healthCheckCron(period string, pushUrl string) {
	c := cron.New()
	c.AddFunc("@every "+period, func() {
		resp, err := http.Get(pushUrl)
		if err != nil {
			logrus.Errorf("%v", err)
		}
		logrus.Infof("Access: %s - %s", pushUrl, resp.Status)
	})
	c.Start()
}

func cloudflareDdnsCron(ctx context.Context, period, token, domain string) {
	api, err := cloudflare.NewWithAPIToken(token)

	if err != nil {
		logrus.WithError(err).Error()
		return
	}

	c := cron.New()
	c.AddFunc("@every "+period, func() {
		logrus.Infoln("Starting new execution")
		err := UpdateDomain(ctx, api, domain, "https://api.ipify.org/")
		if err != nil {
			logrus.WithError(err).Error()
		}
	})
	c.Start()
}
