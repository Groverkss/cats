package config

import (
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Pool    PoolConfig    `toml:"pool"`
	Sandbox SandboxConfig `toml:"sandbox"`
}

type PoolConfig struct {
	PollInterval duration `toml:"poll_interval"`
	MaxRetries   int      `toml:"max_retries"`
}

type SandboxConfig struct {
	GPU     bool     `toml:"gpu"`
	Network bool     `toml:"network"`
	ExtraRO []string `toml:"extra_ro"`
	ExtraRW []string `toml:"extra_rw"`
}

// duration wraps time.Duration for TOML parsing.
type duration struct {
	time.Duration
}

func (d *duration) UnmarshalText(text []byte) error {
	var err error
	d.Duration, err = time.ParseDuration(string(text))
	return err
}

func Default() Config {
	return Config{
		Pool: PoolConfig{
			PollInterval: duration{5 * time.Second},
			MaxRetries:   2,
		},
		Sandbox: SandboxConfig{
			GPU:     false,
			Network: true,
		},
	}
}

func Load(workspace string) (Config, error) {
	cfg := Default()
	path := filepath.Join(workspace, "cats.toml")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}

	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
