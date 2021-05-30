/*
Copyright © 2021 Bhargav SNV bhargavsnv100@gmail.com

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Gituser143/cryptgo/pkg/utils"
	"github.com/gorilla/websocket"
	gecko "github.com/superoo7/go-gecko/v3"
	geckoTypes "github.com/superoo7/go-gecko/v3/types"
)

// API Documentation can be found at https://docs.coincap.io/

// CoinData Holds data pertaining to a single coin.
// This is used to serve per coin details.
// It additionally holds a map of favourite coins.
type CoinData struct {
	Type          string
	PriceHistory  []float64
	MinPrice      float64
	MaxPrice      float64
	CoinAssetData CoinAsset
	Price         string
	Favourites    map[string]float64
}

// CoinAsset holds Asset data for a single coin
type CoinAsset struct {
	Data      Asset `json:"data"`
	TimeStamp uint  `json:"timestamp"`
}

// GetFavouritePrices gets coin prices for coins specified by favourites.
// This data is returned on the dataChannel.
func GetFavouritePrices(ctx context.Context, favourites map[string]bool, dataChannel chan CoinData) error {

	// Init Client
	geckoClient := gecko.NewClient(nil)

	// Set Parameters
	vsCurrency := "usd"
	order := geckoTypes.OrderTypeObject.MarketCapDesc
	page := 1
	sparkline := true
	priceChangePercentage := []string{}

	return utils.LoopTick(ctx, time.Duration(10)*time.Second, func(errChan chan error) {

		var finalErr error

		favouriteData := make(map[string]float64)

		defer func() {
			if finalErr != nil {
				errChan <- finalErr
			}
		}()

		IDs := []string{}

		for id := range favourites {
			IDs = append(IDs, id)
		}

		perPage := len(IDs)

		coinDataPointer, err := geckoClient.CoinsMarket(vsCurrency, IDs, order, perPage, page, sparkline, priceChangePercentage)
		if err != nil {
			finalErr = err
			return
		}

		for _, val := range *coinDataPointer {
			symbol := strings.ToUpper(val.Symbol)
			favouriteData[symbol] = val.CurrentPrice
		}

		// Aggregate data
		coinData := CoinData{
			Type:       "FAVOURITES",
			Favourites: favouriteData,
		}

		// Send data
		select {
		case <-ctx.Done():
			finalErr = ctx.Err()
			return
		case dataChannel <- coinData:
		}

	})
}

// GetCoinHistory gets price history of a coin specified by id, for an interval
// received through the interval channel.
func GetCoinHistory(ctx context.Context, id string, intervalChannel chan string, dataChannel chan CoinData) error {
	method := "GET"

	// Init Client
	client := &http.Client{}

	// Set Default Interval to 1 day
	i := "d1"

	return utils.LoopTick(ctx, time.Duration(3)*time.Second, func(errChan chan error) {
		var finalErr error = nil

		defer func() {
			if finalErr != nil {
				errChan <- finalErr
			}
		}()

		select {
		case <-ctx.Done():
			finalErr = ctx.Err()
			return
		case interval := <-intervalChannel:
			// Update interval
			i = interval
		default:
			break
		}

		url := fmt.Sprintf("https://api.coincap.io/v2/assets/%s/history?interval=%s", id, i)
		data := CoinHistory{}

		// Create Request
		req, err := http.NewRequestWithContext(ctx, method, url, nil)
		if err != nil {
			finalErr = err
			return
		}

		// Send Request
		res, err := client.Do(req)
		if err != nil {
			finalErr = err
			return
		}
		defer res.Body.Close()

		// Read response
		err = json.NewDecoder(res.Body).Decode(&data)
		if err != nil {
			finalErr = err
			return
		}

		// Aggregate price history
		price := []float64{}
		for _, v := range data.Data {
			p, err := strconv.ParseFloat(v.Price, 64)
			if err != nil {
				finalErr = err
				return
			}

			price = append(price, p)
		}

		// Set max and min
		min := utils.MinFloat64(price...)
		max := utils.MaxFloat64(price...)

		// Clean price for graphs
		for i, val := range price {
			price[i] = val - min
		}

		// Aggregate data
		coinData := CoinData{
			Type:         "HISTORY",
			PriceHistory: price,
			MinPrice:     min,
			MaxPrice:     max,
		}

		// Send Data
		select {
		case <-ctx.Done():
			finalErr = ctx.Err()
			return
		case dataChannel <- coinData:
		}
	})
}

// GetCoinAsset fetches asset data for a coin specified by id
// and sends the data on dataChannel
func GetCoinAsset(ctx context.Context, id string, dataChannel chan CoinData) error {
	url := fmt.Sprintf("https://api.coincap.io/v2/assets/%s/", id)
	method := "GET"

	// Init client
	client := &http.Client{}

	// Create Request
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return err
	}

	return utils.LoopTick(ctx, time.Duration(3)*time.Second, func(errChan chan error) {
		data := CoinAsset{}
		var finalErr error = nil

		defer func() {
			if finalErr != nil {
				errChan <- finalErr
			}
		}()

		// Send Request
		res, err := client.Do(req)
		if err != nil {
			finalErr = err
			return
		}
		defer res.Body.Close()

		// Read response
		err = json.NewDecoder(res.Body).Decode(&data)
		if err != nil {
			finalErr = err
			return
		}

		// Aggregate data
		coinData := CoinData{
			Type:          "ASSET",
			CoinAssetData: data,
		}

		// Send data
		select {
		case <-ctx.Done():
			finalErr = ctx.Err()
			return
		case dataChannel <- coinData:
		}
	})
}

// GetLivePrice uses a websocket to stream realtime prices of a coin specified
// by id. The prices are sent on the dataChannel
func GetLivePrice(ctx context.Context, id string, dataChannel chan string) error {
	url := fmt.Sprintf("wss://ws.coincap.io/prices?assets=%s", id)
	c, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return err
	}
	defer c.Close()

	msg := make(map[string]string)

	return utils.LoopTick(ctx, time.Duration(100*time.Millisecond), func(errChan chan error) {
		var finalErr error = nil

		// Defer panic recovery for closed websocket
		defer func() {
			if e := recover(); e != nil {
				finalErr = fmt.Errorf("socket read error")
			}
		}()

		defer func() {
			if finalErr != nil {
				errChan <- finalErr
			}
		}()

		err = c.ReadJSON(&msg)
		if err != nil {
			finalErr = err
			return
		}

		select {
		case <-ctx.Done():
			finalErr = ctx.Err()
			return
		case dataChannel <- msg[id]:
		}
	})
}
