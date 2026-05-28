package main

import (
	"fmt"
	"log"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const baseURL = "https://msi.admiralty.co.uk/NoticesToMariners/Weekly"

func initClient() (*http.Client, string, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, "", err
	}
	client := &http.Client{Jar: jar}

	resp, err := client.Get(baseURL)
	if err != nil {
		return nil, "", fmt.Errorf("get base page: %w", err)
	}
	defer resp.Body.Close()

	token, err := extractCSRFToken(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("csrf: %w", err)
	}

	return client, token, nil
}

func handleMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	raw := strings.FieldsFunc(msg.Text, func(r rune) bool {
		return r == '\n' || r == ' ' || r == ','
	})

	var notices []string
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s != "" {
			notices = append(notices, s)
		}
	}

	if len(notices) == 0 {
		reply := tgbotapi.NewMessage(msg.Chat.ID, "Send notice numbers, one per line:\n2269(P)/26\n1848(T)/26")
		bot.Send(reply)
		return
	}

	bot.Send(tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Searching %d notice(s)...", len(notices))))

	client, csrfToken, err := initClient()
	if err != nil {
		bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Connection error, please try again later"))
		log.Printf("initClient: %v", err)
		return
	}

	cache := &pdfCache{data: make(map[string][]byte)}
	results := make([]noticeResult, len(notices))
	var wg sync.WaitGroup

	for i, number := range notices {
		wg.Add(1)
		go func(i int, number string) {
			defer wg.Done()
			results[i] = findNotice(client, csrfToken, number, cache)
		}(i, number)
	}

	wg.Wait()

	var sb strings.Builder
	for _, r := range results {
		if r.text == "NOT FOUND" {
			sb.WriteString(fmt.Sprintf("❌ %s — not found\n\n", r.number))
		} else {
			sb.WriteString(fmt.Sprintf("✅ %s (week %s/%s)\n\n%s\n\n---\n\n", r.number, r.week, r.year, r.text))
		}
	}

	reply := tgbotapi.NewMessage(msg.Chat.ID, sb.String())
	if _, err := bot.Send(reply); err != nil {
		log.Printf("send error: %v", err)
	}
}

func main() {
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	if botToken == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN not set")
	}

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Bot started: @%s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}
		go handleMessage(bot, update.Message)
	}
}
