package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const binanceFuturesWSURL = "wss://fstream.binance.com/stream"

var supportedLiquidationSymbols = map[string]bool{
	"BTC-USDT-SWAP": true,
	"ETH-USDT-SWAP": true,
}

type CryptoLiquidation struct {
	ID                string  `json:"id"`
	Exchange          string  `json:"exchange"`
	Symbol            string  `json:"symbol"`
	Side              string  `json:"side"` // liquidated position: long | short
	Price             float64 `json:"price"`
	Quantity          float64 `json:"quantity"`
	Notional          float64 `json:"notional"`
	Currency          string  `json:"currency"`
	NotionalEstimated bool    `json:"notional_estimated,omitempty"`
	OccurredAt        string  `json:"occurred_at"`
	ReceivedAt        string  `json:"received_at"`
}

type CryptoLiquidationStreamEvent struct {
	Type        string             `json:"type"`
	Exchange    string             `json:"exchange"`
	State       string             `json:"state,omitempty"`
	Message     string             `json:"message,omitempty"`
	Liquidation *CryptoLiquidation `json:"liquidation,omitempty"`
}

type binanceForceOrderEnvelope struct {
	Data struct {
		EventTime int64 `json:"E"`
		Order     struct {
			Symbol       string `json:"s"`
			Side         string `json:"S"`
			Price        string `json:"p"`
			AveragePrice string `json:"ap"`
			Quantity     string `json:"q"`
			Filled       string `json:"z"`
			TradeTime    int64  `json:"T"`
		} `json:"o"`
	} `json:"data"`
}

type okxLiquidationEnvelope struct {
	Event string `json:"event"`
	Code  string `json:"code"`
	Msg   string `json:"msg"`
	Data  []struct {
		InstID  string `json:"instId"`
		Details []struct {
			Side  string `json:"side"`
			Size  string `json:"sz"`
			Price string `json:"bkPx"`
			Time  string `json:"ts"`
		} `json:"details"`
	} `json:"data"`
}

func normalizeLiquidationSymbols(raw []string) ([]string, error) {
	seen := make(map[string]bool)
	out := make([]string, 0, len(raw))
	for _, symbol := range raw {
		symbol = strings.ToUpper(strings.TrimSpace(symbol))
		if symbol == "BTC-USDT" || symbol == "ETH-USDT" {
			symbol += "-SWAP"
		}
		if !supportedLiquidationSymbols[symbol] {
			return nil, fmt.Errorf("%w: 清算图仅支持 BTC-USDT-SWAP 和 ETH-USDT-SWAP", errInvalidCryptoRequest)
		}
		if !seen[symbol] {
			seen[symbol] = true
			out = append(out, symbol)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: 需要 BTC 或 ETH 清算合约", errInvalidCryptoRequest)
	}
	return out, nil
}

func (s *CryptoService) StreamLiquidations(ctx context.Context, symbols []string) (<-chan CryptoLiquidationStreamEvent, error) {
	normalized, err := normalizeLiquidationSymbols(symbols)
	if err != nil {
		return nil, err
	}
	events := make(chan CryptoLiquidationStreamEvent, 128)
	go func() {
		defer close(events)
		providerEvents := make(chan CryptoLiquidationStreamEvent, 128)
		providerCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go s.runLiquidationProvider(providerCtx, "binance", normalized, providerEvents, s.streamBinanceLiquidations)
		go s.runLiquidationProvider(providerCtx, "okx", normalized, providerEvents, s.streamOKXLiquidations)
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-providerEvents:
				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return events, nil
}

type liquidationProviderStream func(context.Context, []string, chan<- CryptoLiquidationStreamEvent) error

func (s *CryptoService) runLiquidationProvider(ctx context.Context, exchange string, symbols []string, events chan<- CryptoLiquidationStreamEvent, stream liquidationProviderStream) {
	delay := time.Second
	for {
		if !sendLiquidationEvent(ctx, events, CryptoLiquidationStreamEvent{Type: "status", Exchange: exchange, State: "connecting"}) {
			return
		}
		err := stream(ctx, symbols, events)
		if ctx.Err() != nil {
			return
		}
		message := "连接中断，正在重试"
		if err != nil {
			message = err.Error()
		}
		if !sendLiquidationEvent(ctx, events, CryptoLiquidationStreamEvent{Type: "status", Exchange: exchange, State: "disconnected", Message: message}) {
			return
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(delay*2, 15*time.Second)
	}
}

func (s *CryptoService) streamBinanceLiquidations(ctx context.Context, symbols []string, events chan<- CryptoLiquidationStreamEvent) error {
	endpointURL := s.binanceLiquidationURL
	if endpointURL == "" {
		endpointURL = binanceFuturesWSURL
	}
	endpoint, err := url.Parse(endpointURL)
	if err != nil {
		return fmt.Errorf("Binance Futures 地址无效")
	}
	streams := make([]string, 0, len(symbols))
	allowed := make(map[string]string, len(symbols))
	for _, symbol := range symbols {
		providerSymbol := strings.TrimSuffix(strings.ReplaceAll(symbol, "-", ""), "SWAP")
		allowed[providerSymbol] = symbol
		streams = append(streams, strings.ToLower(providerSymbol)+"@forceOrder")
	}
	query := endpoint.Query()
	query.Set("streams", strings.Join(streams, "/"))
	endpoint.RawQuery = query.Encode()
	conn, cancelDial, err := s.dial(ctx, endpoint.String())
	if err != nil {
		return fmt.Errorf("Binance Futures 清算流连接失败")
	}
	defer cancelDial()
	defer conn.Close(websocket.StatusNormalClosure, "stream closed")
	conn.SetReadLimit(1 << 20)
	if !sendLiquidationEvent(ctx, events, CryptoLiquidationStreamEvent{Type: "status", Exchange: "binance", State: "connected"}) {
		return ctx.Err()
	}
	for {
		_, payload, readErr := conn.Read(ctx)
		if readErr != nil {
			return fmt.Errorf("Binance Futures 清算流中断")
		}
		item, ok := parseBinanceLiquidation(payload, allowed, time.Now().UTC())
		if ok && !sendLiquidationEvent(ctx, events, CryptoLiquidationStreamEvent{Type: "liquidation", Exchange: "binance", Liquidation: &item}) {
			return ctx.Err()
		}
	}
}

func (s *CryptoService) streamOKXLiquidations(ctx context.Context, symbols []string, events chan<- CryptoLiquidationStreamEvent) error {
	endpointURL := s.okxLiquidationURL
	if endpointURL == "" {
		endpointURL = "wss://ws.okx.com:8443/ws/v5/public"
	}
	conn, cancelDial, err := s.dial(ctx, endpointURL)
	if err != nil {
		return fmt.Errorf("OKX 清算流连接失败")
	}
	defer cancelDial()
	defer conn.Close(websocket.StatusNormalClosure, "stream closed")
	conn.SetReadLimit(1 << 20)
	if err := wsjson.Write(ctx, conn, map[string]any{"op": "subscribe", "args": []map[string]string{{"channel": "liquidation-orders", "instType": "SWAP"}}}); err != nil {
		return fmt.Errorf("OKX 清算流订阅失败")
	}
	allowed := make(map[string]bool, len(symbols))
	for _, symbol := range symbols {
		allowed[symbol] = true
	}
	if !sendLiquidationEvent(ctx, events, CryptoLiquidationStreamEvent{Type: "status", Exchange: "okx", State: "connected"}) {
		return ctx.Err()
	}
	for {
		_, payload, readErr := conn.Read(ctx)
		if readErr != nil {
			return fmt.Errorf("OKX 清算流中断")
		}
		items, parseErr := parseOKXLiquidations(payload, allowed, time.Now().UTC())
		if parseErr != nil {
			return parseErr
		}
		for i := range items {
			if !sendLiquidationEvent(ctx, events, CryptoLiquidationStreamEvent{Type: "liquidation", Exchange: "okx", Liquidation: &items[i]}) {
				return ctx.Err()
			}
		}
	}
}

func parseBinanceLiquidation(payload []byte, allowed map[string]string, received time.Time) (CryptoLiquidation, bool) {
	var envelope binanceForceOrderEnvelope
	if json.Unmarshal(payload, &envelope) != nil {
		return CryptoLiquidation{}, false
	}
	order := envelope.Data.Order
	symbol, ok := allowed[strings.ToUpper(order.Symbol)]
	if !ok {
		return CryptoLiquidation{}, false
	}
	price := firstPositiveDecimal(order.AveragePrice, order.Price)
	quantity := firstPositiveDecimal(order.Filled, order.Quantity)
	if price <= 0 || quantity <= 0 {
		return CryptoLiquidation{}, false
	}
	side := "short"
	if strings.EqualFold(order.Side, "SELL") {
		side = "long"
	}
	occurred := order.TradeTime
	if occurred <= 0 {
		occurred = envelope.Data.EventTime
	}
	item := CryptoLiquidation{Exchange: "binance", Symbol: symbol, Side: side, Price: price, Quantity: quantity, Notional: price * quantity, Currency: "USDT", OccurredAt: unixMilliString(occurred), ReceivedAt: received.Format(time.RFC3339Nano)}
	item.ID = liquidationFingerprint(item)
	return item, true
}

func parseOKXLiquidations(payload []byte, allowed map[string]bool, received time.Time) ([]CryptoLiquidation, error) {
	var envelope okxLiquidationEnvelope
	if json.Unmarshal(payload, &envelope) != nil {
		return nil, nil
	}
	if envelope.Event == "error" || (envelope.Code != "" && envelope.Code != "0") {
		return nil, fmt.Errorf("OKX 返回清算订阅错误")
	}
	var out []CryptoLiquidation
	for _, group := range envelope.Data {
		if !allowed[group.InstID] {
			continue
		}
		for _, detail := range group.Details {
			price := firstPositiveDecimal(detail.Price)
			quantity := firstPositiveDecimal(detail.Size)
			milliseconds, _ := strconv.ParseInt(detail.Time, 10, 64)
			if price <= 0 || quantity <= 0 || milliseconds <= 0 {
				continue
			}
			side := "short"
			if strings.EqualFold(detail.Side, "sell") {
				side = "long"
			}
			item := CryptoLiquidation{Exchange: "okx", Symbol: group.InstID, Side: side, Price: price, Quantity: quantity, Notional: price * quantity, Currency: "USDT", NotionalEstimated: true, OccurredAt: unixMilliString(milliseconds), ReceivedAt: received.Format(time.RFC3339Nano)}
			item.ID = liquidationFingerprint(item)
			out = append(out, item)
		}
	}
	return out, nil
}

func firstPositiveDecimal(values ...string) float64 {
	for _, value := range values {
		number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err == nil && number > 0 {
			return number
		}
	}
	return 0
}

func liquidationFingerprint(item CryptoLiquidation) string {
	raw := strings.Join([]string{item.Exchange, item.Symbol, item.Side, strconv.FormatFloat(item.Price, 'f', -1, 64), strconv.FormatFloat(item.Quantity, 'f', -1, 64), item.OccurredAt}, "|")
	sum := sha256.Sum256([]byte(raw))
	return item.Exchange + "_" + hex.EncodeToString(sum[:12])
}

func sendLiquidationEvent(ctx context.Context, events chan<- CryptoLiquidationStreamEvent, event CryptoLiquidationStreamEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case events <- event:
		return true
	}
}
