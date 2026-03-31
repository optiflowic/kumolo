package config

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadEnvDefaults(t *testing.T) {
	require.NoError(t, os.Unsetenv("KUMOLO_PORT"))
	require.NoError(t, os.Unsetenv("KUMOLO_DATA_DIR"))
	require.NoError(t, os.Unsetenv("KUMOLO_LOG_LEVEL"))

	env := LoadEnv()
	assert.Equal(t, "5566", env.Port)
	assert.Equal(t, "/tmp/kumolo", env.DataDir)
	assert.Equal(t, "info", env.LogLevel)
}

func TestLoadEnvFromEnvironment(t *testing.T) {
	t.Setenv("KUMOLO_PORT", "8080")
	t.Setenv("KUMOLO_DATA_DIR", "/env/kumolo")
	t.Setenv("KUMOLO_LOG_LEVEL", "warn")

	env := LoadEnv()
	assert.Equal(t, "8080", env.Port)
	assert.Equal(t, "/env/kumolo", env.DataDir)
	assert.Equal(t, "warn", env.LogLevel)
}

func TestLoadEnvDotEnvLoadError(t *testing.T) {
	orig := godotenvLoad
	t.Cleanup(func() { godotenvLoad = orig })
	godotenvLoad = func(_ ...string) error { return errors.New("parse error") }

	// Should not panic; warn is logged and defaults are returned.
	env := LoadEnv()
	assert.Equal(t, "5566", env.Port)
}

func TestGetEnvFallback(t *testing.T) {
	require.NoError(t, os.Unsetenv("KUMOLO_TEST_KEY"))
	assert.Equal(t, "default", getEnv("KUMOLO_TEST_KEY", "default"))
}

func TestGetEnvSet(t *testing.T) {
	t.Setenv("KUMOLO_TEST_KEY", "value")
	assert.Equal(t, "value", getEnv("KUMOLO_TEST_KEY", "default"))
}
