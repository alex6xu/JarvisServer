package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	stockQuoteURL    = "https://push2.eastmoney.com/api/qt/ulist.np/get"
	stockSearchURL   = "https://searchapi.eastmoney.com/api/suggest/get"
	stockSearchToken = "D43BF722C8E33BDC906FB84D85E326E8"
	maxStockSymbols  = 30
)

var stockSymbolPattern = regexp.MustCompile(`^[0-9]{1,3}\.[A-Za-z0-9][A-Za-z0-9._-]{0,23}$`)

var errInvalidStockRequest = errors.New("invalid stock request")

type StockService struct {
	httpClient *http.Client
	quoteURL   string
	searchURL  string
}

type StockQuote struct {
	Symbol        string   `json:"symbol"`
	Code          string   `json:"code"`
	Name          string   `json:"name"`
	Market        string   `json:"market"`
	Price         *float64 `json:"price"`
	Change        *float64 `json:"change"`
	ChangePercent *float64 `json:"change_percent"`
	Open          *float64 `json:"open"`
	High          *float64 `json:"high"`
	Low           *float64 `json:"low"`
	PreviousClose *float64 `json:"previous_close"`
	Volume        *float64 `json:"volume"`
	Turnover      *float64 `json:"turnover"`
	UpdatedAt     string   `json:"updated_at,omitempty"`
}

type StockSearchResult struct {
	Symbol string `json:"symbol"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	Market string `json:"market"`
	Type   string `json:"type"`
}

type eastmoneyQuote struct {
	Price         json.RawMessage `json:"f2"`
	ChangePercent json.RawMessage `json:"f3"`
	Change        json.RawMessage `json:"f4"`
	Volume        json.RawMessage `json:"f5"`
	Turnover      json.RawMessage `json:"f6"`
	Code          string          `json:"f12"`
	Market        int             `json:"f13"`
	Name          string          `json:"f14"`
	High          json.RawMessage `json:"f15"`
	Low           json.RawMessage `json:"f16"`
	Open          json.RawMessage `json:"f17"`
	PreviousClose json.RawMessage `json:"f18"`
	UpdatedAt     json.RawMessage `json:"f124"`
}

func NewStockService() *StockService {
	return &StockService{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		quoteURL:   stockQuoteURL,
		searchURL:  stockSearchURL,
	}
}

func (s *StockService) Quotes(ctx context.Context, symbols []string) ([]StockQuote, error) {
	normalized, err := normalizeStockSymbols(symbols)
	if err != nil {
		return nil, err
	}
	values := url.Values{
		"fltt":   {"2"},
		"invt":   {"2"},
		"fields": {"f2,f3,f4,f5,f6,f12,f13,f14,f15,f16,f17,f18,f124"},
		"secids": {strings.Join(normalized, ",")},
	}
	var upstream struct {
		Code int `json:"rc"`
		Data *struct {
			Diff []eastmoneyQuote `json:"diff"`
		} `json:"data"`
	}
	if err := s.getJSON(ctx, s.quoteURL, values, &upstream); err != nil {
		return nil, err
	}
	if upstream.Code != 0 {
		return nil, fmt.Errorf("stock quote provider returned code %d", upstream.Code)
	}
	if upstream.Data == nil {
		return []StockQuote{}, nil
	}

	quotes := make([]StockQuote, 0, len(upstream.Data.Diff))
	for _, item := range upstream.Data.Diff {
		if strings.TrimSpace(item.Code) == "" {
			continue
		}
		quote := StockQuote{
			Symbol:        strconv.Itoa(item.Market) + "." + strings.ToUpper(item.Code),
			Code:          item.Code,
			Name:          item.Name,
			Market:        stockMarketLabel(item.Market),
			Price:         rawFloat(item.Price),
			Change:        rawFloat(item.Change),
			ChangePercent: rawFloat(item.ChangePercent),
			Open:          rawFloat(item.Open),
			High:          rawFloat(item.High),
			Low:           rawFloat(item.Low),
			PreviousClose: rawFloat(item.PreviousClose),
			Volume:        rawFloat(item.Volume),
			Turnover:      rawFloat(item.Turnover),
		}
		if seconds := rawInt64(item.UpdatedAt); seconds > 0 {
			quote.UpdatedAt = time.Unix(seconds, 0).UTC().Format(time.RFC3339)
		}
		quotes = append(quotes, quote)
	}
	return quotes, nil
}

func (s *StockService) Search(ctx context.Context, query string) ([]StockSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("%w: search query is required", errInvalidStockRequest)
	}
	if len([]rune(query)) > 40 {
		return nil, fmt.Errorf("%w: search query is too long", errInvalidStockRequest)
	}
	values := url.Values{
		"input": {query},
		"type":  {"14"},
		"token": {stockSearchToken},
		"count": {"12"},
	}
	var upstream struct {
		Table struct {
			Data []struct {
				Code             string `json:"Code"`
				Name             string `json:"Name"`
				QuoteID          string `json:"QuoteID"`
				SecurityTypeName string `json:"SecurityTypeName"`
				Classify         string `json:"Classify"`
			} `json:"Data"`
			Status  int    `json:"Status"`
			Message string `json:"Message"`
		} `json:"QuotationCodeTable"`
	}
	if err := s.getJSON(ctx, s.searchURL, values, &upstream); err != nil {
		return nil, err
	}
	if upstream.Table.Status != 0 {
		return nil, fmt.Errorf("stock search provider returned status %d", upstream.Table.Status)
	}

	results := make([]StockSearchResult, 0, len(upstream.Table.Data))
	seen := make(map[string]bool)
	for _, item := range upstream.Table.Data {
		symbol := strings.ToUpper(strings.TrimSpace(item.QuoteID))
		if !stockSymbolPattern.MatchString(symbol) || seen[symbol] {
			continue
		}
		seen[symbol] = true
		results = append(results, StockSearchResult{
			Symbol: symbol,
			Code:   item.Code,
			Name:   item.Name,
			Market: item.SecurityTypeName,
			Type:   item.Classify,
		})
	}
	return results, nil
}

func (s *StockService) getJSON(ctx context.Context, endpoint string, values url.Values, target any) error {
	requestURL, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid stock provider URL: %w", err)
	}
	requestURL.RawQuery = values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "CodeGateway/1.0")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("stock provider request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("stock provider returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(target); err != nil {
		return fmt.Errorf("decode stock provider response: %w", err)
	}
	return nil
}

func normalizeStockSymbols(symbols []string) ([]string, error) {
	if len(symbols) == 0 {
		return nil, fmt.Errorf("%w: at least one stock symbol is required", errInvalidStockRequest)
	}
	if len(symbols) > maxStockSymbols {
		return nil, fmt.Errorf("%w: at most %d stock symbols are allowed", errInvalidStockRequest, maxStockSymbols)
	}
	result := make([]string, 0, len(symbols))
	seen := make(map[string]bool)
	for _, symbol := range symbols {
		symbol = strings.ToUpper(strings.TrimSpace(symbol))
		if !stockSymbolPattern.MatchString(symbol) {
			return nil, fmt.Errorf("%w: invalid stock symbol %q", errInvalidStockRequest, symbol)
		}
		if !seen[symbol] {
			seen[symbol] = true
			result = append(result, symbol)
		}
	}
	return result, nil
}

func rawFloat(raw json.RawMessage) *float64 {
	value := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if value == "" || value == "-" || value == "null" {
		return nil
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil
	}
	return &number
}

func rawInt64(raw json.RawMessage) int64 {
	value := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	number, _ := strconv.ParseInt(value, 10, 64)
	return number
}

func stockMarketLabel(market int) string {
	switch market {
	case 0:
		return "深市"
	case 1:
		return "沪市"
	case 90:
		return "北交所"
	case 100:
		return "港股指数"
	case 105:
		return "NASDAQ"
	case 106:
		return "NYSE"
	case 107:
		return "AMEX"
	case 116:
		return "港股"
	default:
		return "市场 " + strconv.Itoa(market)
	}
}
