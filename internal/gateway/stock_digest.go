package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const stockDigestTimeout = 15 * time.Second

var errInvalidStockDigest = errors.New("invalid stock digest request")

type StockDigestRequest struct {
	Symbols          []string `json:"symbols"`
	Days             int      `json:"days"`
	Limit            int      `json:"limit"`
	IncludeSentiment bool     `json:"include_sentiment"`
	Delivery         string   `json:"delivery"`
}

type StockDigestDiagnostic struct {
	Source  string `json:"source"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type StockDigestCandidate struct {
	Input   string              `json:"input"`
	Matches []StockSearchResult `json:"matches"`
}

type StockDigestItem struct {
	Symbol        string                    `json:"symbol"`
	Code          string                    `json:"code"`
	Name          string                    `json:"name"`
	Market        string                    `json:"market"`
	AssetType     string                    `json:"asset_type"`
	Quote         *StockQuote               `json:"quote,omitempty"`
	CryptoTickers []CryptoTicker            `json:"crypto_tickers,omitempty"`
	News          *StockNewsSentimentResult `json:"news,omitempty"`
	Social        *StockSentimentSnapshot   `json:"social_sentiment,omitempty"`
	Status        string                    `json:"status"`
}

type StockDigestResult struct {
	Status      string                    `json:"status"`
	Items       []StockDigestItem         `json:"items"`
	Candidates  []StockDigestCandidate    `json:"candidates,omitempty"`
	Diagnostics []StockDigestDiagnostic   `json:"diagnostics"`
	Delivery    NotificationPublishResult `json:"delivery"`
	FetchedAt   string                    `json:"fetched_at"`
}

type StockDigestService struct {
	stocks        *StockService
	crypto        *CryptoService
	news          *StockNewsSentimentService
	social        *StockSentimentService
	notifications *NotificationService
	store         *GatewayStore
}

func NewStockDigestService(
	stocks *StockService,
	crypto *CryptoService,
	news *StockNewsSentimentService,
	social *StockSentimentService,
	notifications *NotificationService,
	store *GatewayStore,
) *StockDigestService {
	return &StockDigestService{stocks: stocks, crypto: crypto, news: news, social: social, notifications: notifications, store: store}
}

func (s *StockDigestService) Latest(ctx context.Context, accountID int, callID string, request StockDigestRequest) (StockDigestResult, error) {
	request, err := normalizeStockDigestRequest(request)
	if err != nil {
		return StockDigestResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, stockDigestTimeout)
	defer cancel()
	identities, candidates, err := s.resolveDigestSymbols(ctx, accountID, request.Symbols)
	if err != nil {
		return StockDigestResult{}, err
	}
	result := StockDigestResult{
		Status: "ok", Items: []StockDigestItem{}, Candidates: candidates,
		Diagnostics: []StockDigestDiagnostic{}, FetchedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if len(candidates) > 0 {
		result.Status = "needs_clarification"
		return result, nil
	}
	if len(identities) == 0 {
		result.Status = "needs_input"
		result.Diagnostics = append(result.Diagnostics, StockDigestDiagnostic{Source: "watchlist", Status: "empty", Message: "未指定标的且账号自选股为空"})
		return result, nil
	}

	result.Items = make([]StockDigestItem, len(identities))
	stockSymbols, cryptoSymbols, usSymbols := make([]string, 0), make([]string, 0), make([]string, 0)
	indexBySymbol := make(map[string]int, len(identities))
	for index, identity := range identities {
		identity.Status = "ok"
		result.Items[index] = identity
		indexBySymbol[identity.Symbol] = index
		if identity.AssetType == "crypto" {
			cryptoSymbols = append(cryptoSymbols, identity.Symbol)
		} else {
			stockSymbols = append(stockSymbols, identity.Symbol)
			if isUSStockDigestSymbol(identity.Symbol) {
				usSymbols = append(usSymbols, identity.Symbol)
			}
		}
	}

	var mutex sync.Mutex
	var group sync.WaitGroup
	addDiagnostic := func(diagnostic StockDigestDiagnostic) {
		mutex.Lock()
		result.Diagnostics = append(result.Diagnostics, diagnostic)
		mutex.Unlock()
	}
	if len(stockSymbols) > 0 && s.stocks != nil {
		group.Add(1)
		go func() {
			defer group.Done()
			quotes, quoteErr := s.stocks.Quotes(ctx, stockSymbols)
			if quoteErr != nil {
				addDiagnostic(StockDigestDiagnostic{Source: "stock_quote", Status: "error", Message: quoteErr.Error()})
				return
			}
			mutex.Lock()
			for quoteIndex := range quotes {
				if index, ok := indexBySymbol[quotes[quoteIndex].Symbol]; ok {
					quote := quotes[quoteIndex]
					result.Items[index].Quote = &quote
					if result.Items[index].Name == "" || result.Items[index].Name == result.Items[index].Code {
						result.Items[index].Name = quote.Name
					}
					result.Items[index].Market = quote.Market
				}
			}
			mutex.Unlock()
		}()
	}
	if len(cryptoSymbols) > 0 && s.crypto != nil {
		group.Add(1)
		go func() {
			defer group.Done()
			tickers, tickerErr := s.crypto.Tickers(ctx, cryptoSymbols)
			if tickerErr != nil {
				addDiagnostic(StockDigestDiagnostic{Source: "crypto_quote", Status: "error", Message: tickerErr.Error()})
			}
			mutex.Lock()
			for _, ticker := range tickers.Tickers {
				if index, ok := indexBySymbol[ticker.Symbol]; ok {
					result.Items[index].CryptoTickers = append(result.Items[index].CryptoTickers, ticker)
				}
			}
			for _, diagnostic := range tickers.Diagnostics {
				if diagnostic.Status != "ok" {
					result.Diagnostics = append(result.Diagnostics, StockDigestDiagnostic{Source: "crypto_" + diagnostic.Exchange, Status: diagnostic.Status, Message: diagnostic.Message})
				}
			}
			mutex.Unlock()
		}()
	}
	if s.news != nil {
		for index := range result.Items {
			index := index
			group.Add(1)
			go func() {
				defer group.Done()
				item := identities[index]
				news, newsErr := s.news.Latest(ctx, item.Symbol, item.Name, request.Days, request.Limit)
				mutex.Lock()
				defer mutex.Unlock()
				if newsErr != nil {
					result.Diagnostics = append(result.Diagnostics, StockDigestDiagnostic{Source: "news:" + item.Symbol, Status: "error", Message: newsErr.Error()})
					return
				}
				result.Items[index].News = &news
				if news.Status != "ok" {
					result.Diagnostics = append(result.Diagnostics, StockDigestDiagnostic{Source: "news:" + item.Symbol, Status: news.Status, Message: news.Message})
				}
			}()
		}
	}
	if request.IncludeSentiment && len(usSymbols) > 0 && s.social != nil {
		group.Add(1)
		go func() {
			defer group.Done()
			sentiment, sentimentErr := s.social.Analyze(ctx, usSymbols)
			mutex.Lock()
			defer mutex.Unlock()
			if sentimentErr != nil {
				result.Diagnostics = append(result.Diagnostics, StockDigestDiagnostic{Source: "social_sentiment", Status: "error", Message: sentimentErr.Error()})
				return
			}
			for snapshotIndex := range sentiment.Snapshots {
				snapshot := sentiment.Snapshots[snapshotIndex]
				for itemIndex := range result.Items {
					if socialTickerForSymbol(result.Items[itemIndex].Symbol) == snapshot.Ticker {
						copySnapshot := snapshot
						result.Items[itemIndex].Social = &copySnapshot
					}
				}
			}
			if sentiment.Status != "ok" {
				result.Diagnostics = append(result.Diagnostics, StockDigestDiagnostic{Source: "social_sentiment", Status: sentiment.Status, Message: sentiment.Message})
			}
		}()
	}
	group.Wait()

	for index := range result.Items {
		item := &result.Items[index]
		if item.AssetType == "stock" && item.Quote == nil || item.AssetType == "crypto" && len(item.CryptoTickers) == 0 {
			item.Status = "partial"
		}
	}
	if len(result.Diagnostics) > 0 {
		result.Status = "partial"
	}
	sort.Slice(result.Diagnostics, func(i, j int) bool { return result.Diagnostics[i].Source < result.Diagnostics[j].Source })
	result.FetchedAt = time.Now().UTC().Format(time.RFC3339Nano)

	if request.Delivery != "never" {
		if s.notifications == nil {
			result.Diagnostics = append(result.Diagnostics, StockDigestDiagnostic{Source: "notification", Status: "unavailable", Message: "通知服务不可用"})
			result.Status = "partial"
		} else {
			payload, _ := json.Marshal(result.Items)
			digest := sha256.Sum256(payload)
			key := "stock-digest:" + strings.TrimSpace(callID) + ":" + hex.EncodeToString(digest[:8])
			delivery, publishErr := s.notifications.Publish(ctx, accountID, NotificationMessage{
				Event: "stock_digest", Title: "股票最新摘要", Body: formatStockDigestNotification(result), IdempotencyKey: key,
			})
			result.Delivery = delivery
			if publishErr != nil || delivery.Failed > 0 || request.Delivery == "required" && delivery.Sent == 0 && delivery.AlreadySent == 0 {
				message := "没有可用的股票摘要通知渠道"
				if publishErr != nil {
					message = publishErr.Error()
				} else if delivery.Failed > 0 {
					message = "部分或全部股票摘要通知发送失败"
				}
				result.Diagnostics = append(result.Diagnostics, StockDigestDiagnostic{Source: "notification", Status: "error", Message: message})
				result.Status = "partial"
			}
		}
	}
	return result, nil
}

func normalizeStockDigestRequest(request StockDigestRequest) (StockDigestRequest, error) {
	if len(request.Symbols) > 10 {
		return request, fmt.Errorf("%w: at most 10 symbols are allowed", errInvalidStockDigest)
	}
	if request.Days <= 0 {
		request.Days = 3
	}
	if request.Days > 30 {
		return request, fmt.Errorf("%w: days cannot exceed 30", errInvalidStockDigest)
	}
	if request.Limit <= 0 {
		request.Limit = 10
	}
	if request.Limit > 20 {
		return request, fmt.Errorf("%w: limit cannot exceed 20", errInvalidStockDigest)
	}
	request.Delivery = strings.ToLower(strings.TrimSpace(request.Delivery))
	if request.Delivery == "" {
		request.Delivery = "never"
	}
	if request.Delivery != "never" && request.Delivery != "configured" && request.Delivery != "required" {
		return request, fmt.Errorf("%w: unsupported delivery mode", errInvalidStockDigest)
	}
	return request, nil
}

func (s *StockDigestService) resolveDigestSymbols(ctx context.Context, accountID int, inputs []string) ([]StockDigestItem, []StockDigestCandidate, error) {
	if len(inputs) == 0 {
		if s.store == nil {
			return nil, nil, nil
		}
		watchlist, err := s.store.ListWatchlist(ctx, accountID)
		if err != nil {
			return nil, nil, err
		}
		items := make([]StockDigestItem, 0, len(watchlist))
		for _, item := range watchlist {
			items = append(items, StockDigestItem{Symbol: item.Symbol, Code: item.Code, Name: item.Name, Market: item.Market, AssetType: item.AssetType})
		}
		return items, nil, nil
	}
	items, candidates := make([]StockDigestItem, 0, len(inputs)), make([]StockDigestCandidate, 0)
	seen := make(map[string]bool)
	for _, raw := range inputs {
		input := strings.TrimSpace(raw)
		upper := strings.ToUpper(input)
		var item StockDigestItem
		switch {
		case cryptoSymbolPattern.MatchString(upper):
			item = StockDigestItem{Symbol: upper, Code: strings.Split(upper, "-")[0], Name: strings.Split(upper, "-")[0], Market: "Crypto", AssetType: "crypto"}
		case stockSymbolPattern.MatchString(upper):
			parts := strings.SplitN(upper, ".", 2)
			item = StockDigestItem{Symbol: upper, Code: parts[1], Name: parts[1], AssetType: "stock"}
		default:
			if s.stocks == nil {
				return nil, nil, fmt.Errorf("%w: stock search is unavailable", errInvalidStockDigest)
			}
			matches, err := s.stocks.Search(ctx, input)
			if err != nil {
				return nil, nil, err
			}
			matches = preferredStockMatches(input, matches)
			if len(matches) != 1 {
				candidates = append(candidates, StockDigestCandidate{Input: input, Matches: matches})
				continue
			}
			match := matches[0]
			item = StockDigestItem{Symbol: match.Symbol, Code: match.Code, Name: match.Name, Market: match.Market, AssetType: "stock"}
		}
		if !seen[item.Symbol] {
			seen[item.Symbol] = true
			items = append(items, item)
		}
	}
	return items, candidates, nil
}

func preferredStockMatches(input string, matches []StockSearchResult) []StockSearchResult {
	input = strings.TrimSpace(input)
	exact := make([]StockSearchResult, 0)
	for _, match := range matches {
		if strings.EqualFold(match.Code, input) || strings.EqualFold(match.Name, input) {
			exact = append(exact, match)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	if len(matches) > 5 {
		return matches[:5]
	}
	return matches
}

func isUSStockDigestSymbol(symbol string) bool {
	parts := strings.SplitN(symbol, ".", 2)
	return len(parts) == 2 && usMarketPrefixes[parts[0]]
}

func socialTickerForSymbol(symbol string) string {
	parts := strings.SplitN(symbol, ".", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return symbol
}

func formatStockDigestNotification(result StockDigestResult) string {
	var builder strings.Builder
	builder.WriteString("CodeGateway · 最新行情摘要")
	for _, item := range result.Items {
		fmt.Fprintf(&builder, "\n\n%s (%s)", item.Name, item.Symbol)
		if item.Quote != nil {
			fmt.Fprintf(&builder, "\n价格：%s，涨跌幅：%s", formatOptionalNumber(item.Quote.Price), formatOptionalPercent(item.Quote.ChangePercent))
		} else if len(item.CryptoTickers) > 0 {
			for _, ticker := range item.CryptoTickers {
				fmt.Fprintf(&builder, "\n%s：%s，24h：%s", strings.ToUpper(ticker.Exchange), formatOptionalNumber(ticker.Price), formatOptionalPercent(ticker.ChangePercent))
			}
		}
		if item.News != nil {
			fmt.Fprintf(&builder, "\n新闻舆情：%s，共 %d 条", item.News.SentimentLabel, len(item.News.Items))
			for index, news := range item.News.Items {
				if index >= 3 {
					break
				}
				fmt.Fprintf(&builder, "\n- %s（%s）", news.Title, news.Source)
			}
		}
	}
	fmt.Fprintf(&builder, "\n\n更新时间：%s\n仅供信息参考，不构成投资建议。", result.FetchedAt)
	return builder.String()
}

func formatOptionalNumber(value *float64) string {
	if value == nil {
		return "--"
	}
	return strconvFormatFloat(*value)
}

func formatOptionalPercent(value *float64) string {
	if value == nil {
		return "--"
	}
	return fmt.Sprintf("%+.2f%%", *value)
}

func strconvFormatFloat(value float64) string { return fmt.Sprintf("%.4f", value) }
