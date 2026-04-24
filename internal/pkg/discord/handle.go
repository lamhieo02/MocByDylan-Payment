package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func getColor(hexColor string) int {
	hexColor = strings.Replace(hexColor, "#", "", -1)
	decimalColor, err := strconv.ParseInt(hexColor, 16, 64)
	if err != nil {
		return 0
	}
	return int(decimalColor)
}

func validateMessage(content *Webhook) error {
	/*
		docs: https://discord.com/developers/docs/resources/channel#embed-object-embed-limits
	*/
	if content.Content == "" && len(content.Embeds) == 0 {
		return errors.New("you must attach at least one of these: content; embeds")
	}

	if len(strings.TrimSpace(content.Content)) > 2000 {
		content.Content = content.Content[:2000]
	}

	if len(content.Embeds) > 10 {
		content.Embeds = content.Embeds[:10]
	}

	for i := 0; i < len(content.Embeds); i++ {
		embed := &content.Embeds[i]
		if len(strings.TrimSpace(embed.Title)) > 256 {
			embed.Title = embed.Title[:256]
		}

		if len(strings.TrimSpace(embed.Description)) > 4096 {
			embed.Description = embed.Description[:4096]
		}

		if len(embed.Fields) > 25 {
			embed.Fields = embed.Fields[:25]
		}

		for _, field := range embed.Fields {
			if len(strings.TrimSpace(field.Name)) > 256 {
				field.Name = field.Name[:256]
			}

			if len(strings.TrimSpace(field.Value)) > 1024 {
				field.Value = field.Value[:1024]
			}
		}

		if len(strings.TrimSpace(embed.Footer.Text)) > 2048 {
			embed.Footer.Text = embed.Footer.Text[:2048]
		}

		if len(strings.TrimSpace(embed.Author.Name)) > 256 {
			embed.Author.Name = embed.Author.Name[:256]
		}
	}

	return nil
}

func sendMessage(client *http.Client, webookUrl string, content Webhook, retryOnRateLimit bool) error {
	ctx := context.Background()

	if err := validateMessage(&content); err != nil {
		return err
	}

	jsonData, err := json.Marshal(content)
	if err != nil {
		return err
	}

	var (
		retryTimes   = 5
		currentRetry = 1
	)

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, webookUrl, bytes.NewBuffer(jsonData))
		if err != nil {
			return err
		}

		req.Header.Add("Content-type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			return err
		}

		switch res.StatusCode {
		case http.StatusNoContent:
			return nil
		case http.StatusTooManyRequests:
			if retryOnRateLimit && currentRetry <= retryTimes {
				currentRetry++
				time.Sleep(time.Second * time.Duration(1<<currentRetry)) // backoff retry
				continue
			}
			return fmt.Errorf("to many request(status code %d)", res.StatusCode)
		default:
			return fmt.Errorf("bad request (status code %d)", res.StatusCode)
		}
	}
}
