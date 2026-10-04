package models

import "time"

// FutureContract is a catalogue-listed future with its broker-reported expiry.
// Expiration is never derived from the ticker or a presumed monthly schedule.
type FutureContract struct {
	Symbol     string    `json:"symbol"`
	Name       string    `json:"name"`
	Expiration time.Time `json:"expiration"`
	Decimals   int       `json:"decimals"`
}
