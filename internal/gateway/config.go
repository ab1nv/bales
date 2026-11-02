package gateway

import (
	"fmt"
	"time"

	"github.com/ab1nv/bales/internal/coalesce"
	"github.com/ab1nv/bales/internal/cost"
)

// Config configures the gateway. Durations and costs are in milliseconds where
// noted by the field name.
type Config struct {
	Addr            string
	Replicas        []string
	Policy          string
	TenantSalt      string
	BlockBytes      int
	BlockTokens     int
	DirectoryBlocks int
	TokensPerByte   float64
	MaxBodyBytes    int64
	QMaxMS          float64
	DStream         time.Duration
	DBatch          time.Duration
	Eta             float64
	Cost            cost.Model
	Coalesce        coalesce.Config
	DecodeTokens    int
	MetricsInterval time.Duration
	KVMaxTokens     int
}

// DefaultConfig returns a working local configuration.
func DefaultConfig() Config {
	return Config{
		Addr:            "127.0.0.1:8080",
		Policy:          "bales_ect",
		TenantSalt:      "change-me",
		BlockBytes:      2048,
		BlockTokens:     512,
		DirectoryBlocks: 4096,
		TokensPerByte:   0.25,
		MaxBodyBytes:    32 << 20,
		QMaxMS:          60000,
		DStream:         300 * time.Millisecond,
		DBatch:          5 * time.Second,
		Eta:             1,
		Cost:            cost.DefaultModel(),
		Coalesce: coalesce.Config{
			EpsilonMS:      1,
			DeltaMaxMS:     250,
			DefaultSlackMS: 2000,
		},
		DecodeTokens:    64,
		MetricsInterval: time.Second,
		KVMaxTokens:     65536,
	}
}

// Validate checks required fields and fills small defaults.
func (c *Config) Validate() error {
	if len(c.Replicas) == 0 {
		return fmt.Errorf("gateway: at least one replica is required")
	}
	if c.Policy == "" {
		c.Policy = "bales_ect"
	}
	if c.TenantSalt == "" {
		c.TenantSalt = "default"
	}
	if c.BlockBytes <= 0 {
		c.BlockBytes = 2048
	}
	if c.BlockTokens <= 0 {
		c.BlockTokens = 512
	}
	if c.DirectoryBlocks <= 0 {
		c.DirectoryBlocks = 1024
	}
	if c.TokensPerByte <= 0 {
		c.TokensPerByte = 0.25
	}
	if c.MaxBodyBytes <= 0 {
		c.MaxBodyBytes = 32 << 20
	}
	if c.QMaxMS <= 0 {
		c.QMaxMS = 60000
	}
	if c.DStream <= 0 {
		c.DStream = 300 * time.Millisecond
	}
	if c.DBatch <= 0 {
		c.DBatch = 5 * time.Second
	}
	if c.DecodeTokens <= 0 {
		c.DecodeTokens = 64
	}
	if c.KVMaxTokens <= 0 {
		c.KVMaxTokens = 65536
	}
	if !c.Cost.Valid() {
		return fmt.Errorf("gateway: invalid cost model")
	}
	return nil
}
