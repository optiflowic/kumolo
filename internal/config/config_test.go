package config

import (
	"flag"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterFlags(t *testing.T) {
	t.Run("uses env values as defaults", func(t *testing.T) {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		env := Env{
			Port:              "5566",
			DataDir:           "",
			LogLevel:          "info",
			LifecycleInterval: time.Minute,
			CORSAllowOrigin:   "http://localhost:5173",
		}
		build := RegisterFlags(fs, env)
		require.NoError(t, fs.Parse([]string{}))

		cfg := build()
		assert.Equal(t, "5566", cfg.Port)
		assert.Equal(t, "", cfg.DataDir)
		assert.Equal(t, "info", cfg.LogLevel)
		assert.Equal(t, time.Minute, cfg.LifecycleInterval)
		assert.Equal(t, "http://localhost:5173", cfg.CORSAllowOrigin)
	})

	t.Run("explicit flags override env defaults", func(t *testing.T) {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		build := RegisterFlags(
			fs,
			Env{Port: "8080", DataDir: "", LogLevel: "info", LifecycleInterval: time.Minute},
		)
		require.NoError(t, fs.Parse([]string{
			"-port", "9000",
			"-data-dir", "/var/kumolo",
			"-log-level", "debug",
			"-cors-allow-origin", "*",
		}))

		cfg := build()
		assert.Equal(t, "9000", cfg.Port)
		assert.Equal(t, "/var/kumolo", cfg.DataDir)
		assert.Equal(t, "debug", cfg.LogLevel)
		assert.Equal(t, "*", cfg.CORSAllowOrigin)
	})

	t.Run("flag value takes precedence over env default", func(t *testing.T) {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		build := RegisterFlags(
			fs,
			Env{Port: "8080", DataDir: "", LogLevel: "info", LifecycleInterval: time.Minute},
		)
		require.NoError(t, fs.Parse([]string{"-port", "9999"}))

		cfg := build()
		assert.Equal(t, "9999", cfg.Port)
	})
}
