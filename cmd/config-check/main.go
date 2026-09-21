package main

import (
	"fmt"
	"os"

	"github.com/ah2909/e-commerce/internal/platform/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration invalid:", err)
		os.Exit(1)
	}
	fmt.Printf("configuration valid: environment=%s routing=%s kafka_brokers=%d database_max_open=%d metrics_enabled=%t\n", cfg.AppEnv, cfg.RoutingMode, len(cfg.KafkaBrokers), cfg.DatabaseMaxOpen, cfg.MetricsAddress != "")
}
