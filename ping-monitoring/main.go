package main

import (
	"github.com/robfig/cron/v3"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func main() {

	healthCheckCron()

	sigs := make(chan os.Signal, 1)
	done := make(chan bool, 1)

	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigs
		log.Println("Got signal", sig)
		done <- true
	}()

	log.Println("Initialized")
	<-done
}

func healthCheckCron() {
	url := os.Getenv("URL")
	c := cron.New()
	c.AddFunc("@every "+os.Getenv("PERIOD"), func() {
		resp, err := http.Get(url)
		if err != nil {
			log.Fatalf("%v", err)
		}
		log.Printf("Access: %s - %s", url, resp.Status)
	})
	c.Start()
}
