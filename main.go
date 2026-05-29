package main

import (
	"fmt"
	"log"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"sync"
	"time"

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

	sendLongMessage(bot, msg.Chat.ID, sb.String())

	for _, r := range results {
		log.Printf("=== %s ===\n%s\n", r.number, r.text)
	}

	filePath, err := saveResultsToDocx(results)
	if err != nil {
		log.Printf("docx error: %v", err)
	} else {
		doc := tgbotapi.NewDocument(msg.Chat.ID, tgbotapi.FilePath(filePath))
		doc.Caption = "NtM notices — " + time.Now().Format("02 January 2006")
		bot.Send(doc)
		os.Remove(filePath)
	}
}

func sendLongMessage(bot *tgbotapi.BotAPI, chatID int64, text string) {
	const maxLen = 4096
	parts := splitMessage(text, maxLen)
	for _, part := range parts {
		bot.Send(tgbotapi.NewMessage(chatID, part))
	}
}

func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	var parts []string
	for len(text) > maxLen {
		cut := strings.LastIndex(text[:maxLen], "\n---\n")
		if cut == -1 {
			cut = maxLen
		} else {
			cut += len("\n---\n")
		}
		parts = append(parts, strings.TrimSpace(text[:cut]))
		text = strings.TrimSpace(text[cut:])
	}
	if text != "" {
		parts = append(parts, text)
	}
	return parts
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
