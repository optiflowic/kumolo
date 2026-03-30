package config

import (
	"flag"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterFlagsDefaults(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	build := RegisterFlags(fs)
	require.NoError(t, fs.Parse([]string{}))

	cfg := build()
	assert.Equal(t, "4566", cfg.Port)
	assert.Equal(t, "/tmp/kumolo", cfg.DataDir)
	assert.Equal(t, "info", cfg.LogLevel)
}

func TestRegisterFlagsExplicit(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	build := RegisterFlags(fs)
	require.NoError(t, fs.Parse([]string{
		"-port", "9000",
		"-data-dir", "/var/kumolo",
		"-log-level", "debug",
	}))

	cfg := build()
	assert.Equal(t, "9000", cfg.Port)
	assert.Equal(t, "/var/kumolo", cfg.DataDir)
	assert.Equal(t, "debug", cfg.LogLevel)
}

func TestRegisterFlagsEnvOverridesDefault(t *testing.T) {
	t.Setenv("KUMOLO_PORT", "8080")
	t.Setenv("KUMOLO_DATA_DIR", "/env/kumolo")
	t.Setenv("KUMOLO_LOG_LEVEL", "warn")

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	build := RegisterFlags(fs)
	require.NoError(t, fs.Parse([]string{}))

	cfg := build()
	assert.Equal(t, "8080", cfg.Port)
	assert.Equal(t, "/env/kumolo", cfg.DataDir)
	assert.Equal(t, "warn", cfg.LogLevel)
}

func TestRegisterFlagsFlagOverridesEnv(t *testing.T) {
	t.Setenv("KUMOLO_PORT", "8080")

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	build := RegisterFlags(fs)
	require.NoError(t, fs.Parse([]string{"-port", "9999"}))

	cfg := build()
	assert.Equal(t, "9999", cfg.Port)
}

func TestGetEnvFallback(t *testing.T) {
	require.NoError(t, os.Unsetenv("KUMOLO_TEST_KEY"))
	assert.Equal(t, "default", getEnv("KUMOLO_TEST_KEY", "default"))
}

func TestGetEnvSet(t *testing.T) {
	t.Setenv("KUMOLO_TEST_KEY", "value")
	assert.Equal(t, "value", getEnv("KUMOLO_TEST_KEY", "default"))
}
