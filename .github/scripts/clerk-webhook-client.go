package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	svix "github.com/svix/svix-webhooks/go"
)

func main() {
	var (
		url         = flag.String("url", "", "webhook URL")
		secret      = flag.String("secret", "", "Svix signing secret")
		eventID     = flag.String("event-id", "", "Svix message ID")
		payloadFile = flag.String("payload-file", "", "payload file")
		bodyOutput  = flag.String("body-output", "", "optional response body path")
		requestID   = flag.String("request-id", "clerk-webhook-ci", "request ID")
	)
	flag.Parse()
	if *url == "" || *secret == "" || *eventID == "" || *payloadFile == "" {
		fmt.Fprintln(os.Stderr, "url, secret, event-id, and payload-file are required")
		os.Exit(2)
	}

	payload, err := os.ReadFile(*payloadFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read webhook fixture failed")
		os.Exit(1)
	}
	webhook, err := svix.NewWebhook(*secret)
	if err != nil {
		fmt.Fprintln(os.Stderr, "initialize webhook signer failed")
		os.Exit(1)
	}
	timestamp := time.Now().UTC()
	signature, err := webhook.Sign(*eventID, timestamp, payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sign webhook fixture failed")
		os.Exit(1)
	}

	request, err := http.NewRequest(http.MethodPost, *url, bytes.NewReader(payload))
	if err != nil {
		fmt.Fprintln(os.Stderr, "create webhook request failed")
		os.Exit(1)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("svix-id", *eventID)
	request.Header.Set("svix-timestamp", strconv.FormatInt(timestamp.Unix(), 10))
	request.Header.Set("svix-signature", signature)
	request.Header.Set("X-Request-Id", *requestID)

	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		fmt.Fprintln(os.Stderr, "send webhook request failed")
		os.Exit(1)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		fmt.Fprintln(os.Stderr, "read webhook response failed")
		os.Exit(1)
	}
	if *bodyOutput != "" {
		if err := os.WriteFile(*bodyOutput, body, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "write webhook response failed")
			os.Exit(1)
		}
	}
	fmt.Printf("%d|%s\n", response.StatusCode, response.Header.Get("X-Request-Id"))
}
