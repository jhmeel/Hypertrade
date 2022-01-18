package exchange

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"trader/events"

	"trader/types"

	"github.com/adshao/go-binance/v2"
	"github.com/rs/zerolog/log"
)

var ZeroBalance = 0.00000000

type Binance struct {
	client *binance.Client
	pubsub events.PubSub
	test   bool
}

func NewBinance(key, secret string, pubsub events.PubSub, test bool) Binance {
	log.Trace().Str("type", "binance").Bool("test", test).Msg("Binance.Init")

	binance.UseTestnet = test
	client := binance.NewClient(key, secret)

	return Binance{client, pubsub, test}
}

func (b Binance) GetAccount() *binance.Account {
	svc := b.client.NewGetAccountService()
	account, err := svc.Do(context.Background())

	if err != nil {
		log.Error().Err(err).Msg("Binance.UserInfo")
	}

	return account
}

func (b Binance) PrintAccountInfo() {
	acc := b.GetAccount()

	fmt.Println("-------- Account Info --------")
	fmt.Println("Type:", acc.AccountType)
	fmt.Println("Can Trade:", acc.CanTrade)
	fmt.Println("Test Mode:", b.test)
	fmt.Println(b.StringifyBalance(acc.Balances))
	fmt.Println("------------------------------")
}

func (b Binance) StringifyBalance(userBalances []binance.Balance) string {
	header := "Balance:"

	if b.test {
		header = fmt.Sprintln("Test", header)
	}

	var balances = []string{header}

	for _, balance := range userBalances {
		amt, err := strconv.ParseFloat(balance.Free, 64)

		if err != nil {
			log.Error().Err(err).Msg("Binance.ParsingBalance")
		}

		var separator rune = '•'

		if amt > ZeroBalance {
			b := fmt.Sprintf("%c %v %v", separator, balance.Asset, balance.Free)
			balances = append(balances, b)
		}
	}

	return strings.Join(balances, "\n")
}

func (b Binance) getMinNotional(symbol string) float64 {
	var minNotional string

	info, err := b.client.NewExchangeInfoService().Symbol(symbol).Do(context.TODO())

	if err != nil {
		log.Error().Err(err).Msg("Binance.getMinNotional")
	}

	filters := info.Symbols[0].Filters

	for _, filter := range filters {
		if filter["filterType"] == "MIN_NOTIONAL" {
			minNotional = filter["minNotional"].(string)
		}
	}

	min, err := strconv.ParseFloat(minNotional, 64)

	if err != nil {
		log.Error().Err(err).Msg("Binance.getMinNotional.ParseFloat")
	}

	return min
}

func (b Binance) GetMinQuantity(symbol string, price float64) float64 {
	var min float64 = b.getMinNotional(symbol)

	quantity := (1 / price) * min

	log.Debug().Float64("min", min).Float64("price", price).Float64("quantity", quantity).Msg("Binance.GetMinQuantity")
	return quantity
}

func (b Binance) Trade(side binance.SideType, symbol string, price float64) {
	log.Info().Interface("side", side).Str("symbol", symbol).Float64("price", price).Msg(events.SignalTrade)

	quantity := fmt.Sprintf("%.4f", b.GetMinQuantity(symbol, price))

	order, err := b.client.NewCreateOrderService().
		Symbol(symbol).
		Side(side).
		Type(binance.OrderTypeMarket).
		Quantity(quantity).
		Do(context.Background())

	if err != nil {
		log.Error().Interface("side", side).Float64("price", price).Str("quantity", quantity).Err(err).Msg("Binance.Trade")
		b.pubsub.Publish(events.CriticalError, events.CriticalErrorPayload{err.Error()})
		return
	}

	log.Info().Interface("side", side).Float64("price", price).Str("quantity", quantity).Msg("Binance.Trade.Order")

	payload := events.NotifyTradePayload{order.OrderID, order.Side, order.Type, symbol, price, quantity}
	b.pubsub.Publish(events.NotifyTrade, payload)
}

func (b Binance) Kline(symbol string, interval string) {
	log.Info().Str("symbol", symbol).Str("interval", interval).Msg("Binance.Kline.Subscribe")

	wsKlineHandler := func(event *binance.WsKlineEvent) {
		close := event.Kline.IsFinal
		price, err := strconv.ParseFloat(event.Kline.Close, 64)

		kline := types.Kline{price, close}

		if err != nil {
			log.Error().Err(err).Msg("Binance.Kline.Parse")
		}

		b.pubsub.Publish(events.Kline, events.KlinePayload{kline, symbol})
	}

	errHandler := func(err error) {
		log.Error().Err(err).Msg("Binance.Kline")

		// Try to restart ws connection
		log.Warn().Msg("Binance.Kline.Recover")
		b.Kline(symbol, interval)
	}

	binance.WsKlineServe(symbol, interval, wsKlineHandler, errHandler)
}
