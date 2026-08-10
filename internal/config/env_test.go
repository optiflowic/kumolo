package config

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadEnv(t *testing.T) {
	t.Run("returns built-in defaults when no env vars are set", func(t *testing.T) {
		require.NoError(t, os.Unsetenv("KUMOLO_PORT"))
		require.NoError(t, os.Unsetenv("KUMOLO_DATA_DIR"))
		require.NoError(t, os.Unsetenv("KUMOLO_LOG_LEVEL"))
		require.NoError(t, os.Unsetenv("KUMOLO_LIFECYCLE_INTERVAL"))
		require.NoError(t, os.Unsetenv("KUMOLO_CORS_ALLOW_ORIGIN"))
		require.NoError(t, os.Unsetenv("AWS_REGION"))
		require.NoError(t, os.Unsetenv("AWS_DEFAULT_REGION"))

		env := LoadEnv()
		assert.Equal(t, "5566", env.Port)
		assert.Equal(t, "", env.DataDir)
		assert.Equal(t, "info", env.LogLevel)
		assert.Equal(t, time.Minute, env.LifecycleInterval)
		assert.Equal(t, "", env.CORSAllowOrigin)
		assert.Equal(t, "", env.AWSRegion)
		assert.Equal(t, "", env.AWSDefaultRegion)
	})

	t.Run("reads values from environment variables", func(t *testing.T) {
		t.Setenv("KUMOLO_PORT", "8080")
		t.Setenv("KUMOLO_DATA_DIR", "/env/kumolo")
		t.Setenv("KUMOLO_LOG_LEVEL", "warn")
		t.Setenv("KUMOLO_CORS_ALLOW_ORIGIN", "http://localhost:5173")
		t.Setenv("AWS_REGION", "ap-northeast-1")
		t.Setenv("AWS_DEFAULT_REGION", "us-west-2")

		env := LoadEnv()
		assert.Equal(t, "8080", env.Port)
		assert.Equal(t, "/env/kumolo", env.DataDir)
		assert.Equal(t, "warn", env.LogLevel)
		assert.Equal(t, "http://localhost:5173", env.CORSAllowOrigin)
		assert.Equal(t, "ap-northeast-1", env.AWSRegion)
		assert.Equal(t, "us-west-2", env.AWSDefaultRegion)
	})

	t.Run("reads KUMOLO_LIFECYCLE_INTERVAL", func(t *testing.T) {
		t.Setenv("KUMOLO_LIFECYCLE_INTERVAL", "5m")
		env := loadEnv(func(_ ...string) error { return os.ErrNotExist })
		assert.Equal(t, 5*time.Minute, env.LifecycleInterval)
	})

	t.Run("uses default for invalid KUMOLO_LIFECYCLE_INTERVAL", func(t *testing.T) {
		t.Setenv("KUMOLO_LIFECYCLE_INTERVAL", "notaduration")
		env := loadEnv(func(_ ...string) error { return os.ErrNotExist })
		assert.Equal(t, time.Minute, env.LifecycleInterval)
	})

	t.Run("uses default for zero KUMOLO_LIFECYCLE_INTERVAL", func(t *testing.T) {
		t.Setenv("KUMOLO_LIFECYCLE_INTERVAL", "0s")
		env := loadEnv(func(_ ...string) error { return os.ErrNotExist })
		assert.Equal(t, time.Minute, env.LifecycleInterval)
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
