package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// API prices and run usage (decision 0161). The picker ranks models by
// expected cost per accepted result at OpenRouter's public prices. model_prices
// is the last good copy of that list, and worker_usage keeps the tokens of each
// worker session by kind, so a run's cost can be computed after the session
// record is gone.
const modelPricesMigration = `
-- API prices and run usage (decision 0161).
CREATE TABLE IF NOT EXISTS model_prices (
  id TEXT PRIMARY KEY, prompt REAL NOT NULL, completion REAL NOT NULL,
  cache_read REAL NOT NULL DEFAULT 0, cache_write REAL NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS worker_usage (
  session_id TEXT PRIMARY KEY, usage TEXT NOT NULL, updated_at INTEGER NOT NULL);
`

// OpenRouterModelsURL is the public price list. It needs no key.
const OpenRouterModelsURL = "https://openrouter.ai/api/v1/models"

const (
	priceRefreshAfter = 24 * time.Hour
	priceCheckEvery   = time.Hour
	priceMaxBytes     = 16 << 20
)

// priceState is the cached price book.
type priceState struct {
	mu   sync.RWMutex
	book *protocol.PriceBook
}

// priceBook is the cached book, loaded from model_prices on first use. An
// unreadable table gives an empty book: costs are then unknown, never wrong.
func (s *Store) priceBook() *protocol.PriceBook {
	s.prices.mu.RLock()
	b := s.prices.book
	s.prices.mu.RUnlock()
	if b != nil {
		return b
	}
	list, _, err := s.storedPrices()
	if err != nil {
		s.logf("price book", "err", err)
		return protocol.NewPriceBook(nil)
	}
	b = protocol.NewPriceBook(list)
	s.prices.mu.Lock()
	s.prices.book = b
	s.prices.mu.Unlock()
	return b
}

func (s *Store) storedPrices() ([]protocol.ModelPrice, int64, error) {
	rows, err := s.db.Query(`SELECT id,prompt,completion,cache_read,cache_write,updated_at FROM model_prices`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var list []protocol.ModelPrice
	var updated int64
	for rows.Next() {
		var p protocol.ModelPrice
		var at int64
		if err := rows.Scan(&p.ID, &p.Prompt, &p.Completion, &p.CacheRead, &p.CacheWrite, &at); err != nil {
			return nil, 0, err
		}
		updated = max(updated, at)
		list = append(list, p)
	}
	return list, updated, rows.Err()
}

// ReplacePrices keeps a new price list in place of the old one and rebuilds the
// picker table, which reads it.
func (s *Store) ReplacePrices(list []protocol.ModelPrice) error {
	if len(list) == 0 {
		return reject("an empty price list")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM model_prices`); err != nil {
		return err
	}
	now := s.now().Unix()
	for _, p := range list {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO model_prices(id,prompt,completion,cache_read,cache_write,updated_at) VALUES(?,?,?,?,?,?)`,
			p.ID, p.Prompt, p.Completion, p.CacheRead, p.CacheWrite, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.prices.mu.Lock()
	s.prices.book = protocol.NewPriceBook(list)
	s.prices.mu.Unlock()
	return s.refreshPicker()
}

// parseOpenRouterPrices reads the /models list. OpenRouter writes each price
// as a string of dollars per token. A model with a missing, negative (a router)
// or unreadable prompt or completion price is skipped.
func parseOpenRouterPrices(r io.Reader) ([]protocol.ModelPrice, error) {
	var doc struct {
		Data []struct {
			ID      string `json:"id"`
			Pricing struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
				CacheRead  string `json:"input_cache_read"`
				CacheWrite string `json:"input_cache_write"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(r, priceMaxBytes)).Decode(&doc); err != nil {
		return nil, err
	}
	price := func(text string) (float64, bool) {
		v, err := strconv.ParseFloat(text, 64)
		return v, err == nil && v >= 0 && !math.IsInf(v, 0)
	}
	var out []protocol.ModelPrice
	for _, m := range doc.Data {
		prompt, ok1 := price(m.Pricing.Prompt)
		completion, ok2 := price(m.Pricing.Completion)
		if m.ID == "" || !ok1 || !ok2 {
			continue
		}
		p := protocol.ModelPrice{ID: m.ID, Prompt: prompt, Completion: completion}
		p.CacheRead, _ = price(m.Pricing.CacheRead)
		p.CacheWrite, _ = price(m.Pricing.CacheWrite)
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no prices in the list")
	}
	return out, nil
}

// refreshPrices fetches the list once. The request carries nothing about the
// owner: no key, no cookie, no identifying header.
func (g *Gateway) refreshPrices(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "agent-net")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	list, err := parseOpenRouterPrices(resp.Body)
	if err != nil {
		return err
	}
	return g.store.ReplacePrices(list)
}

// RunPrices keeps the price list fresh until ctx ends: once at start when the
// copy is more than a day old, and then each day. A failed fetch keeps the old
// prices and logs one line. url is OpenRouterModelsURL outside tests.
func (g *Gateway) RunPrices(ctx context.Context, url string) {
	check := func() {
		_, updated, err := g.store.storedPrices()
		if err == nil && g.store.now().Sub(time.Unix(updated, 0)) < priceRefreshAfter {
			return
		}
		if err := g.refreshPrices(ctx, url); err != nil {
			g.logLine("prices.refresh_failed", "err", err)
			return
		}
		g.logLine("prices.refreshed")
	}
	check()
	t := time.NewTicker(priceCheckEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			check()
		}
	}
}

// RunCost is a run's cost at API prices. known is false when the book is empty.
// A model with no listed price takes the middle price; priceKnown says it did
// not.
func (s *Store) RunCost(model string, t protocol.RunTokens) (cost float64, priceKnown, known bool) {
	p, listed, ok := s.priceBook().PriceOf(model)
	if !ok {
		return 0, false, false
	}
	return p.Cost(t), listed, true
}

// recordWorkerUsageTx keeps the session's last reported usage with its run. It
// runs in the transaction that records the run. A daemon that reported no
// usage by kind still wrote the run's tokens in its notice, and those stand in.
func recordWorkerUsageTx(tx *sql.Tx, session string, now int64) error {
	if session == "" {
		return nil
	}
	res, err := tx.Exec(`INSERT INTO worker_usage(session_id,usage,updated_at)
		SELECT id, json_extract(w,'$.usage'), ? FROM (SELECT id, CASE WHEN json_valid(worker) THEN worker ELSE '{}' END AS w FROM sessions WHERE id=?)
		WHERE json_type(w,'$.usage')='object'
		ON CONFLICT(session_id) DO UPDATE SET usage=excluded.usage, updated_at=excluded.updated_at`, now, session)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n > 0 {
		return err
	}
	return recordNoticeUsageTx(tx, session, now)
}

// workerUsage reads the stored usage of the given sessions, or of every session
// when none is given.
func (s *Store) workerUsage(sessions ...string) (map[string]protocol.RunTokens, error) {
	query, args := `SELECT session_id, usage FROM worker_usage`, []any(nil)
	if len(sessions) > 0 {
		query += ` WHERE session_id IN (?` + strings.Repeat(",?", len(sessions)-1) + `)`
		for _, id := range sessions {
			args = append(args, id)
		}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]protocol.RunTokens{}
	for rows.Next() {
		var session, raw string
		if err := rows.Scan(&session, &raw); err != nil {
			return nil, err
		}
		var t protocol.RunTokens
		if json.Unmarshal([]byte(raw), &t) == nil && !t.Zero() {
			out[session] = t
		}
	}
	return out, rows.Err()
}

// noticeUsageLine reads the worker and the "tokens:" line of one stored notice.
// A body kept with its line breaks escaped is read too.
func noticeUsageLine(body string) (session string, tokens protocol.RunTokens, ok bool) {
	if !strings.Contains(body, "\n") {
		body = strings.ReplaceAll(body, `\n`, "\n")
	}
	for _, line := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(line, "worker: "):
			session, _, _ = strings.Cut(strings.TrimPrefix(line, "worker: "), " ")
		case strings.HasPrefix(line, "tokens: "):
			tokens, ok = protocol.ParseTokenText(strings.TrimPrefix(line, "tokens: "))
		}
	}
	return session, tokens, ok
}

// noticeUsage reads the "tokens:" line of every stored worker notice, by worker
// session. Runs that ended before the gateway kept usage by kind have only
// this. A session's notices are cumulative, so the largest count of each kind
// stands.
func (s *Store) noticeUsage() (map[string]protocol.RunTokens, error) {
	rows, err := s.db.Query(`SELECT body FROM messages WHERE body LIKE '%[agent-net worker notice]%' AND body LIKE '%tokens: %'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]protocol.RunTokens{}
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		session, tokens, found := noticeUsageLine(body)
		if found && strings.HasPrefix(session, "s_") {
			out[session] = out[session].Max(tokens)
		}
	}
	return out, rows.Err()
}

// backfillNoticeUsage keeps the tokens of runs that ended before the daemon
// reported usage by kind, from the "tokens:" line of their stored notices
// (decision 0161). The gateway runs it when it opens, so the picker table and
// the worker log can price those runs. A session that has a recorded usage
// keeps it: a report by kind is always the better figure.
func (s *Store) backfillNoticeUsage() error {
	usage, err := s.noticeUsage()
	if err != nil || len(usage) == 0 {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().Unix()
	for session, t := range usage {
		raw, err := json.Marshal(t)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO worker_usage(session_id,usage,updated_at) VALUES(?,?,?)
			ON CONFLICT(session_id) DO NOTHING`, session, string(raw), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// recordNoticeUsageTx keeps the tokens of the session's stored notices as its
// usage. It stands in when a daemon reported no usage by kind, so a run is
// priced without waiting for the next gateway start.
func recordNoticeUsageTx(tx *sql.Tx, session string, now int64) error {
	rows, err := tx.Query(`SELECT body FROM messages
		WHERE body LIKE '[agent-net worker notice]%' AND body LIKE ?`, "%worker: "+session+" (%")
	if err != nil {
		return err
	}
	var tokens protocol.RunTokens
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			rows.Close()
			return err
		}
		if _, t, ok := noticeUsageLine(body); ok {
			tokens = tokens.Max(t)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || tokens.Zero() {
		return err
	}
	raw, err := json.Marshal(tokens)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO worker_usage(session_id,usage,updated_at) VALUES(?,?,?)
		ON CONFLICT(session_id) DO UPDATE SET usage=excluded.usage, updated_at=excluded.updated_at`, session, string(raw), now)
	return err
}
