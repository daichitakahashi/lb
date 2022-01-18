package config

import (
	"encoding/json"
	"io"
)

type Config struct {
	Listen            string          `json:"listen"`
	Algorithm         string          `json:"algorithm"`
	PersistenceMethod string          `json:"persistenceMethod"`
	Backends          []BackendConfig `json:"backends"`
}

func Load(r io.Reader) (*Config, error) {
	var c Config
	err := json.NewDecoder(r).Decode(&c)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

type BackendConfig struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}
