// Package rates puts amounts into hryvnias at the National Bank of Ukraine's
// official rate, for a client who pays in hryvnias. A day's rate is read from
// the bank's public API the first time it's needed and kept: it never
// changes once set. When the bank can't be reached, the latest rate kept
// stands in, with its own date.
package rates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pmaxis/helpic/internal/money"
)

// UAH is the currency the rates are in.
const UAH = "UAH"

const (
	// fetchTimeout bounds a call to the bank, which may run while a report
	// is being opened.
	fetchTimeout = 5 * time.Second
	// backoff is how long to leave the bank alone after a failed call, so a
	// bank that is down doesn't slow every report down.
	backoff   = 10 * time.Minute
	dayLayout = "2006-01-02"
)

// Rate is the official rate of a currency on a day.
type Rate struct {
	Currency string
	// On is the day the rate was set for; it can be before the day asked
	// for, when the bank had nothing newer.
	On time.Time
	// UAH is the hryvnias for one unit of Currency, as the bank gives it:
	// "45.0256".
	UAH string
}

// Convert is cents of the rate's currency in hryvnia kopiyky, rounded half up.
func (r Rate) Convert(cents int64) (int64, error) {
	rate, ok := new(big.Rat).SetString(r.UAH)
	if !ok {
		return 0, fmt.Errorf("rate %q is not a number", r.UAH)
	}
	v := new(big.Rat).Mul(new(big.Rat).SetInt64(cents), rate)
	// Half up, for the positive amounts prices are.
	v.Add(v, big.NewRat(1, 2))
	return new(big.Int).Quo(v.Num(), v.Denom()).Int64(), nil
}

// Service finds the rate for a day.
type Service struct {
	repo *Repository
	bank *Client
	// zone is where a day is a day.
	zone *time.Location
	// now is time.Now, replaceable in tests.
	now func() time.Time

	mu sync.Mutex
	// failedAt is when the bank last couldn't be reached.
	failedAt time.Time
}

// NewService creates a Service. zone is the app's time zone; nil is UTC.
func NewService(repo *Repository, bank *Client, zone *time.Location) *Service {
	if zone == nil {
		zone = time.UTC
	}
	return &Service{repo: repo, bank: bank, zone: zone, now: time.Now}
}

// On returns the official rate of currency on the day at falls on. A day
// missing from what is kept is asked of the bank; when the bank has no rate
// for it yet (a day ahead) or can't be reached, the latest earlier rate kept
// is used. ok is false when there is none, and for hryvnias themselves.
func (s *Service) On(ctx context.Context, currency string, at time.Time) (Rate, bool, error) {
	if currency == UAH || !money.ValidCurrency(currency) {
		return Rate{}, false, nil
	}
	y, m, d := at.In(s.zone).Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	kept, ok, err := s.repo.Latest(ctx, currency, day)
	if err != nil {
		return Rate{}, false, err
	}
	if ok && kept.On.Equal(day) {
		return kept, true, nil
	}
	if !s.fetch(ctx, day) {
		return kept, ok, nil
	}
	return s.repo.Latest(ctx, currency, day)
}

// Run keeps today's rates, so the first report opened in a day doesn't wait
// for the bank. For jobs.Registry.Every.
func (s *Service) Run(ctx context.Context) error {
	_, _, err := s.On(ctx, "USD", s.now())
	return err
}

// fetch asks the bank for the day's rates and keeps them, and reports
// whether it got any. After a failure it waits out the backoff.
func (s *Service) fetch(ctx context.Context, day time.Time) bool {
	s.mu.Lock()
	resting := !s.failedAt.IsZero() && s.now().Sub(s.failedAt) < backoff
	s.mu.Unlock()
	if resting {
		return false
	}
	list, err := s.bank.Rates(ctx, day)
	if err == nil && len(list) > 0 {
		err = s.repo.Save(ctx, list)
	}
	if err != nil {
		slog.Warn("could not get the NBU's exchange rates", "day", day.Format(dayLayout), "error", err)
		s.mu.Lock()
		s.failedAt = s.now()
		s.mu.Unlock()
		return false
	}
	return len(list) > 0
}

// --- the bank's API ------------------------------------------------------------

// Client reads the official rates from the National Bank of Ukraine.
type Client struct {
	base string
	http *http.Client
}

// NewClient creates a Client; baseURL is https://bank.gov.ua, or a fake one
// in tests.
func NewClient(baseURL string) *Client {
	return &Client{base: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: fetchTimeout}}
}

// nbuRate is one currency in the bank's answer.
type nbuRate struct {
	CC   string      `json:"cc"`
	Rate json.Number `json:"rate"`
	// ExchangeDate is "22.10.2026".
	ExchangeDate string `json:"exchangedate"`
}

// Rates returns the bank's rates for the currencies Helpic bills in, on the
// day; none for a day the bank hasn't set them for yet.
func (c *Client) Rates(ctx context.Context, day time.Time) ([]Rate, error) {
	url := c.base + "/NBUStatService/v1/statdirectory/exchange?json&date=" + day.Format("20060102")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("nbu request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nbu: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nbu: status %d", resp.StatusCode)
	}
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	var all []nbuRate
	if err := dec.Decode(&all); err != nil {
		return nil, fmt.Errorf("nbu: decode: %w", err)
	}
	var out []Rate
	for _, r := range all {
		if r.CC == UAH || !slices.Contains(money.Currencies, r.CC) {
			continue
		}
		on, err := time.Parse("02.01.2006", r.ExchangeDate)
		if err != nil {
			return nil, fmt.Errorf("nbu: date %q: %w", r.ExchangeDate, err)
		}
		if v, ok := new(big.Rat).SetString(r.Rate.String()); !ok || v.Sign() <= 0 {
			return nil, fmt.Errorf("nbu: rate %q for %s", r.Rate, r.CC)
		}
		out = append(out, Rate{Currency: r.CC, On: on, UAH: r.Rate.String()})
	}
	return out, nil
}

// --- repository ----------------------------------------------------------------

// Repository keeps the rates in PostgreSQL.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a rates Repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Latest returns the newest kept rate of currency on or before day; ok is
// false when none is kept.
func (r *Repository) Latest(ctx context.Context, currency string, day time.Time) (Rate, bool, error) {
	rate := Rate{Currency: currency}
	err := r.db.QueryRow(ctx, `
		SELECT rate_on, uah::text FROM exchange_rates
		WHERE currency = $1 AND rate_on <= $2
		ORDER BY rate_on DESC LIMIT 1`, currency, day).Scan(&rate.On, &rate.UAH)
	if errors.Is(err, pgx.ErrNoRows) {
		return Rate{}, false, nil
	}
	if err != nil {
		return Rate{}, false, fmt.Errorf("get exchange rate: %w", err)
	}
	// NUMERIC(20, 6) pads to six decimals; the bank's own digits read better.
	if strings.Contains(rate.UAH, ".") {
		rate.UAH = strings.TrimRight(strings.TrimRight(rate.UAH, "0"), ".")
	}
	return rate, true, nil
}

// Save keeps rates; a day's rate already kept stays as it is.
func (r *Repository) Save(ctx context.Context, list []Rate) error {
	batch := &pgx.Batch{}
	for _, rate := range list {
		batch.Queue(`
			INSERT INTO exchange_rates (currency, rate_on, uah) VALUES ($1, $2, $3::numeric)
			ON CONFLICT (currency, rate_on) DO NOTHING`, rate.Currency, rate.On, rate.UAH)
	}
	if err := r.db.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("save exchange rates: %w", err)
	}
	return nil
}
