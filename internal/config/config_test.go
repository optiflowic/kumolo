package config

import (
	"flag"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterFlags(t *testing.T) {
	t.Run("uses env values as defaults", func(t *testing.T) {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		env := Env{Port: "5566", DataDir: "/tmp/kumolo", LogLevel: "info"}
		build := RegisterFlags(fs, env)
		require.NoError(t, fs.Parse([]string{}))

		cfg := build()
		assert.Equal(t, "5566", cfg.Port)
		assert.Equal(t, "/tmp/kumolo", cfg.DataDir)
		assert.Equal(t, "info", cfg.LogLevel)
	})

	t.Run("explicit flags override env defaults", func(t *testing.T) {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		build := RegisterFlags(fs, Env{Port: "8080", DataDir: "/tmp/kumolo", LogLevel: "info"})
		require.NoError(t, fs.Parse([]string{
			"-port", "9000",
			"-data-dir", "/var/kumolo",
			"-log-level", "debug",
		}))

		cfg := build()
		assert.Equal(t, "9000", cfg.Port)
		assert.Equal(t, "/var/kumolo", cfg.DataDir)
		assert.Equal(t, "debug", cfg.LogLevel)
	})

	t.Run("flag value takes precedence over env default", func(t *testing.T) {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		build := RegisterFlags(fs, Env{Port: "8080", DataDir: "/tmp/kumolo", LogLevel: "info"})
		require.NoError(t, fs.Parse([]string{"-port", "9999"}))

		cfg := build()
		assert.Equal(t, "9999", cfg.Port)
	})
}
