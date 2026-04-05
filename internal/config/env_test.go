package config

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadEnv(t *testing.T) {
	t.Run("returns built-in defaults when no env vars are set", func(t *testing.T) {
		require.NoError(t, os.Unsetenv("KUMOLO_PORT"))
		require.NoError(t, os.Unsetenv("KUMOLO_DATA_DIR"))
		require.NoError(t, os.Unsetenv("KUMOLO_LOG_LEVEL"))

		env := LoadEnv()
		assert.Equal(t, "5566", env.Port)
		assert.Equal(t, "", env.DataDir)
		assert.Equal(t, "info", env.LogLevel)
	})

	t.Run("reads values from environment variables", func(t *testing.T) {
		t.Setenv("KUMOLO_PORT", "8080")
		t.Setenv("KUMOLO_DATA_DIR", "/env/kumolo")
		t.Setenv("KUMOLO_LOG_LEVEL", "warn")

		env := LoadEnv()
		assert.Equal(t, "8080", env.Port)
		assert.Equal(t, "/env/kumolo", env.DataDir)
		assert.Equal(t, "warn", env.LogLevel)
	})

	t.Run("logs warning and continues when .env file cannot be parsed", func(t *testing.T) {
		env := loadEnv(func(_ ...string) error { return errors.New("parse error") })
		assert.Equal(t, "5566", env.Port)
	})
}

func TestGetEnv(t *testing.T) {
	t.Run("returns fallback when env var is not set", func(t *testing.T) {
		require.NoError(t, os.Unsetenv("KUMOLO_TEST_KEY"))
		assert.Equal(t, "default", getEnv("KUMOLO_TEST_KEY", "default"))
	})

	t.Run("returns env var value when set", func(t *testing.T) {
		t.Setenv("KUMOLO_TEST_KEY", "value")
		assert.Equal(t, "value", getEnv("KUMOLO_TEST_KEY", "default"))
	})
}
