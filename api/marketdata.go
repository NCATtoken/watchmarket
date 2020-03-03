package api

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/trustwallet/blockatlas/coin"
	"github.com/trustwallet/blockatlas/pkg/blockatlas"
	"github.com/trustwallet/blockatlas/pkg/ginutils"
	"github.com/trustwallet/blockatlas/pkg/logger"
	"github.com/trustwallet/watchmarket/market"
	"github.com/trustwallet/watchmarket/pkg/watchmarket"
	"github.com/trustwallet/watchmarket/services/assets"
	"github.com/trustwallet/watchmarket/storage"
	"math/big"
	"net/http"
	"strconv"
	"strings"
)

const (
	defaultMaxChartItems = 64
)

type TickerRequest struct {
	Currency string `json:"currency"`
	Assets   []Coin `json:"assets"`
}

type Coin struct {
	Coin     uint                `json:"coin"`
	CoinType blockatlas.CoinType `json:"type"`
	TokenId  string              `json:"token_id,omitempty"`
}

func SetupMarketAPI(router gin.IRouter, db storage.Market) {
	router.Use(ginutils.TokenAuthMiddleware(viper.GetString("market.auth")))
	// Ticker
	router.POST("/ticker", getTickersHandler(db))
	// Charts
	router.GET("/charts", getChartsHandler())
	router.GET("/info", getCoinInfoHandler())
}

// @Summary Get ticker values for a specific market
// @Id get_tickers
// @Description Get the ticker values from many market and coin/token
// @Accept json
// @Produce json
// @Tags Market
// @Param tickers body api.TickerRequest true "Ticker"
// @Success 200 {object} watchmarket.Tickers
// @Router /v1/market/ticker [post]
func getTickersHandler(storage storage.Market) func(c *gin.Context) {
	if storage == nil {
		return nil
	}
	return func(c *gin.Context) {
		md := TickerRequest{Currency: watchmarket.DefaultCurrency}
		if err := c.BindJSON(&md); err != nil {
			ginutils.ErrorResponse(c).Message(err.Error()).Render()
			return
		}

		rate, err := storage.GetRate(strings.ToUpper(md.Currency))
		if err == watchmarket.ErrNotFound {
			ginutils.RenderError(c, http.StatusNotFound, fmt.Sprintf("Currency %s not found", md.Currency))
			return
		} else if err != nil {
			logger.Error(err, "Failed to retrieve rate", logger.Params{"currency": md.Currency})
			ginutils.RenderError(c, http.StatusInternalServerError, fmt.Sprintf("Failed to get rate for %s", md.Currency))
			return
		}

		tickers := make(watchmarket.Tickers, 0)
		for _, coinRequest := range md.Assets {
			exchangeRate := rate.Rate
			percentChange := rate.PercentChange24h

			coinObj, ok := coin.Coins[coinRequest.Coin]
			if !ok {
				continue
			}
			r, err := storage.GetTicker(coinObj.Symbol, strings.ToUpper(coinRequest.TokenId))
			if err != nil {
				if err == watchmarket.ErrNotFound {
					logger.Warn("Ticker not found", logger.Params{"coin": coinObj.Symbol, "token": coinRequest.TokenId})
				} else if err != nil {
					logger.Error(err, "Failed to retrieve ticker", logger.Params{"coin": coinObj.Symbol, "token": coinRequest.TokenId})
					ginutils.RenderError(c, http.StatusInternalServerError, "Failed to retrieve tickers")
					return
				}
				continue
			}
			if r.Price.Currency != watchmarket.DefaultCurrency {
				newRate, err := storage.GetRate(strings.ToUpper(r.Price.Currency))
				if err == nil {
					exchangeRate *= newRate.Rate
					percentChange = newRate.PercentChange24h
				} else {
					tickerRate, err := storage.GetTicker(strings.ToUpper(r.Price.Currency), "")
					if err == nil {
						exchangeRate *= tickerRate.Price.Value
						percentChange = big.NewFloat(tickerRate.Price.Change24h)
					}
				}
			}

			r.ApplyRate(md.Currency, exchangeRate, percentChange)
			r.SetCoinId(coinRequest.Coin)
			tickers = append(tickers, r)
		}

		ginutils.RenderSuccess(c, watchmarket.TickerResponse{Currency: md.Currency, Docs: tickers})
	}
}

// @Summary Get charts data for a specific coin
// @Id get_charts_data
// @Description Get the charts data from an market and coin/token
// @Accept json
// @Produce json
// @Tags Market
// @Param coin query int true "Coin ID" default(60)
// @Param token query string false "Token ID"
// @Param time_start query int false "Start timestamp" default(1574483028)
// @Param max_items query int false "Max number of items in result prices array" default(64)
// @Param currency query string false "The currency to show charts" default(USD)
// @Success 200 {object} watchmarket.ChartData
// @Router /v1/market/charts [get]
func getChartsHandler() func(c *gin.Context) {
	var charts = market.InitCharts()
	return func(c *gin.Context) {
		coinQuery := c.Query("coin")
		coinId, err := strconv.Atoi(coinQuery)
		if err != nil {
			ginutils.RenderError(c, http.StatusBadRequest, "Invalid coin")
			return
		}
		token := c.Query("token")

		timeStart, err := strconv.ParseInt(c.Query("time_start"), 10, 64)
		if err != nil {
			ginutils.RenderError(c, http.StatusBadRequest, "Invalid time_start")
			return
		}
		maxItems, err := strconv.Atoi(c.Query("max_items"))
		if err != nil || maxItems <= 0 {
			maxItems = defaultMaxChartItems
		}

		currency := c.DefaultQuery("currency", watchmarket.DefaultCurrency)

		chart, err := charts.GetChartData(uint(coinId), token, currency, timeStart, maxItems)
		if err != nil {
			logger.Error(err, "Failed to retrieve chart", logger.Params{"coin": coinId, "currency": currency})
			ginutils.RenderError(c, http.StatusInternalServerError, err.Error())
			return
		}
		ginutils.RenderSuccess(c, chart)
	}
}

// @Summary Get charts coin info data for a specific coin
// @Id get_charts_coin_info
// @Description Get the charts coin info data from an market and coin/contract
// @Accept json
// @Produce json
// @Tags Market
// @Param coin query int true "Coin ID" default(60)
// @Param token query string false "Token ID"
// @Param time_start query int false "Start timestamp" default(1574483028)
// @Param currency query string false "The currency to show coin info in" default(USD)
// @Success 200 {object} watchmarket.ChartCoinInfo
// @Router /v1/market/info [get]
func getCoinInfoHandler() func(c *gin.Context) {
	var charts = market.InitCharts()
	return func(c *gin.Context) {
		coinQuery := c.Query("coin")
		coinId, err := strconv.Atoi(coinQuery)
		if err != nil {
			ginutils.RenderError(c, http.StatusBadRequest, "Invalid coin")
			return
		}

		token := c.Query("token")
		currency := c.DefaultQuery("currency", watchmarket.DefaultCurrency)
		chart, err := charts.GetCoinInfo(uint(coinId), token, currency)
		if err != nil {
			logger.Error(err, "Failed to retrieve coin info", logger.Params{"coin": coinId, "currency": currency})
		}
		chart.Info, err = assets.GetCoinInfo(coinId, token)
		if err != nil {
			logger.Error(err, "Failed to retrieve coin info", logger.Params{"coin": coinId, "currency": currency})
			ginutils.RenderError(c, http.StatusInternalServerError, err.Error())
			return
		}
		ginutils.RenderSuccess(c, chart)
	}
}
