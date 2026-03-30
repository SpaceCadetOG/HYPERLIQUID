package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type TradeEvent struct {
	Symbol     string
	Side       string
	Price      float64
	Confluence float64
	Setup      string
	Reason     string
}

type Service interface {
	Sendf(format string, args ...any)
	SendTrade(event TradeEvent, isEntry bool)
	SendToChat(chatID, msg string)
	Stop()
}

type Telegram struct {
	enabled bool
	token   string
	chatID  string
	timeout time.Duration
	dedupe  time.Duration
	client  *http.Client

	mu       sync.Mutex
	lastSent map[string]time.Time

	msgCh chan outboundMessage
	stop  chan struct{}
	done  chan struct{}
}

type outboundMessage struct {
	chatID string
	text   string
}

type tgUpdateResp struct {
	OK     bool       `json:"ok"`
	Result []tgUpdate `json:"result"`
}

type tgUpdate struct {
	UpdateID int64      `json:"update_id"`
	Message  *tgMessage `json:"message"`
}

type tgMessage struct {
	MessageID int64  `json:"message_id"`
	Text      string `json:"text"`
	Chat      struct {
		ID int64 `json:"id"`
	} `json:"chat"`
}

func NewTelegramFromEnv() *Telegram {
	token := strings.TrimSpace(firstEnv("LIVE_TG_BOT_TOKEN", "TELEGRAM_BOT_TOKEN", "TG_BOT_TOKEN"))
	chatID := strings.TrimSpace(firstEnv("LIVE_TG_CHAT_ID", "TELEGRAM_CHAT_ID", "TG_CHAT_ID"))
	timeoutSec := envInt("LIVE_TG_TIMEOUT_SEC", 5)
	if timeoutSec <= 0 {
		timeoutSec = 5
	}
	dedupeSec := envInt("LIVE_TG_DEDUPE_SEC", 30)
	if dedupeSec < 0 {
		dedupeSec = 0
	}
	t := &Telegram{
		enabled:  token != "" && chatID != "",
		token:    token,
		chatID:   chatID,
		timeout:  time.Duration(timeoutSec) * time.Second,
		dedupe:   time.Duration(dedupeSec) * time.Second,
		client:   &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
		lastSent: map[string]time.Time{},
		msgCh:    make(chan outboundMessage, 256),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go t.runSender()
	return t
}

func (t *Telegram) Enabled() bool {
	return t != nil && t.enabled
}

func (t *Telegram) Sendf(format string, args ...any) {
	if t == nil || !t.enabled {
		return
	}
	msg := strings.TrimSpace(fmt.Sprintf(format, args...))
	if msg == "" {
		return
	}
	now := time.Now()
	if t.dedupe > 0 {
		t.mu.Lock()
		if at, ok := t.lastSent[msg]; ok && now.Sub(at) < t.dedupe {
			t.mu.Unlock()
			return
		}
		t.lastSent[msg] = now
		t.mu.Unlock()
	}
	t.enqueue(t.chatID, msg)
}

func (t *Telegram) SendTrade(event TradeEvent, isEntry bool) {
	if t == nil || !t.enabled {
		return
	}
	kind := "EXIT"
	if isEntry {
		kind = "ENTRY"
	}
	t.Sendf("%s %s %s\nsetup=%s conf=%.2f reason=%s price=%.6f",
		kind,
		strings.ToUpper(strings.TrimSpace(event.Symbol)),
		strings.ToUpper(strings.TrimSpace(event.Side)),
		strings.TrimSpace(event.Setup),
		event.Confluence,
		strings.TrimSpace(event.Reason),
		event.Price,
	)
}

func (t *Telegram) SendToChat(chatID, msg string) {
	if t == nil || !t.enabled {
		return
	}
	t.enqueue(chatID, msg)
}

func (t *Telegram) Stop() {
	if t == nil {
		return
	}
	select {
	case <-t.stop:
		return
	default:
		close(t.stop)
	}
	<-t.done
}

func (t *Telegram) Listen(ctx context.Context, handler func(chatID string, text string) string) {
	if t == nil || !t.enabled || handler == nil {
		return
	}
	var offset int64
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		updates, err := t.getUpdates(offset, 20)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			if u.Message == nil || strings.TrimSpace(u.Message.Text) == "" {
				continue
			}
			chatID := strconv.FormatInt(u.Message.Chat.ID, 10)
			if strings.TrimSpace(t.chatID) != "" && chatID != strings.TrimSpace(t.chatID) {
				continue
			}
			reply := handler(chatID, strings.TrimSpace(u.Message.Text))
			if strings.TrimSpace(reply) == "" {
				continue
			}
			t.SendToChat(chatID, strings.TrimSpace(reply))
		}
	}
}

func Pre(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return ""
	}
	return "<pre>" + msg + "</pre>"
}

func (t *Telegram) enqueue(chatID, msg string) {
	if t == nil || !t.enabled || strings.TrimSpace(msg) == "" {
		return
	}
	select {
	case t.msgCh <- outboundMessage{chatID: chatID, text: msg}:
	default:
	}
}

func (t *Telegram) runSender() {
	defer close(t.done)
	for {
		select {
		case <-t.stop:
			for {
				select {
				case msg := <-t.msgCh:
					_ = t.send(msg.chatID, msg.text)
				default:
					return
				}
			}
		case msg := <-t.msgCh:
			_ = t.send(msg.chatID, msg.text)
		}
	}
}

func (t *Telegram) send(chatID, msg string) error {
	if t == nil || !t.enabled {
		return nil
	}
	if strings.TrimSpace(chatID) == "" {
		chatID = t.chatID
	}
	form := url.Values{}
	form.Set("chat_id", chatID)
	form.Set("text", msg)
	form.Set("disable_web_page_preview", "true")
	form.Set("parse_mode", "HTML")
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.token)
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("telegram http %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (t *Telegram) getUpdates(offset int64, timeoutSec int) ([]tgUpdate, error) {
	if t == nil || !t.enabled {
		return nil, nil
	}
	if timeoutSec <= 0 {
		timeoutSec = 20
	}
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?timeout=%d&offset=%d", t.token, timeoutSec, offset)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("telegram getUpdates http %d: %s", resp.StatusCode, string(body))
	}
	var out tgUpdateResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, fmt.Errorf("telegram getUpdates response not ok")
	}
	return out.Result, nil
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func envInt(name string, def int) int {
	s := strings.TrimSpace(os.Getenv(name))
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
