package main

/*
#cgo LDFLAGS: -L. -lprocess -lpthread -lstdc++
#include "process.h"
*/
import "C"

import (
	"encoding/json"
	"log"
	"os"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

var (
	currentSymbol string
	symbolMu      sync.RWMutex
)

// TradeMessage from ingestion service
type TradeMessage struct {
	Symbol string  `json:"symbol"`
	Price  float64 `json:"price"`
	Time   int64   `json:"time"`
}

// ProcessedMessage published after C++ processing
type ProcessedMessage struct {
	Symbol        string  `json:"symbol"`
	Price         float64 `json:"price"`
	MovingAverage float64 `json:"moving_average"`
	High          float64 `json:"high"`
	Low           float64 `json:"low"`
	Time          int64   `json:"time"`
}

func main() {
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://localhost:4222"
	}

	log.Println("Processing service starting...")

	// Connect to NATS with retry
	var nc *nats.Conn
	var err error
	for i := 0; i < 10; i++ {
		nc, err = nats.Connect(natsURL)
		if err == nil {
			break
		}
		log.Printf("NATS connection failed, retrying in 2s... (%v)", err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer nc.Close()
	log.Println("Connected to NATS")

	// Subscribe to symbol change for processor reset
	nc.Subscribe("control.symbol", func(msg *nats.Msg) {
		var req struct {
			Symbol string `json:"symbol"`
		}
		if err := json.Unmarshal(msg.Data, &req); err != nil {
			return
		}
		symbolMu.Lock()
		currentSymbol = req.Symbol
		symbolMu.Unlock()
		C.reset_processor()
		log.Printf("Processor reset for symbol change to %s", req.Symbol)
	})

	// Subscribe to raw trades
	nc.Subscribe("trades.raw", func(msg *nats.Msg) {
		var trade TradeMessage
		if err := json.Unmarshal(msg.Data, &trade); err != nil {
			return
		}

		// Ignore trades from old symbol after a symbol change
		symbolMu.RLock()
		sym := currentSymbol
		symbolMu.RUnlock()
		if sym != "" && trade.Symbol != sym {
			return
		}

		// Process through C++
		C.add_price(C.double(trade.Price))

		// Get stats
		processed := ProcessedMessage{
			Symbol:        trade.Symbol,
			Price:         trade.Price,
			MovingAverage: float64(C.get_moving_average()),
			High:          float64(C.get_high()),
			Low:           float64(C.get_low()),
			Time:          trade.Time,
		}

		data, _ := json.Marshal(processed)
		nc.Publish("trades.processed", data)
	})

	log.Println("Processing service running, subscribed to trades.raw")

	// Keep running
	select {}
}
