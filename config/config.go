package config

import (
	"encoding/json"
	"net"
	"net/url"
	"os"
	"strconv"
)

type Config struct {
	Postgres postgresConfig `json:"postgres"`
	WebDAV   webDAVConfig   `json:"webdav"`
}

type postgresConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`
	Password string `json:"password"`
}

type webDAVConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg Config) DatabaseURL() string {
	pg := cfg.Postgres
	connection := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(pg.User, pg.Password),
		Host:   net.JoinHostPort(pg.Host, strconv.Itoa(pg.Port)),
		Path:   "/" + pg.Database,
	}
	return connection.String()
}

func (cfg Config) BlobURL() string {
	dav := cfg.WebDAV
	connection := url.URL{
		Scheme:   "webdav",
		User:     url.UserPassword(dav.User, dav.Password),
		Host:     net.JoinHostPort(dav.Host, strconv.Itoa(dav.Port)),
		Path:     "/",
		RawQuery: "metadata=skip",
	}
	return connection.String()
}
