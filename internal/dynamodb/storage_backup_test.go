package dynamodb

import (
	"errors"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDescribeContinuousBackups(t *testing.T) {
	t.Run("returns DISABLED by default", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		meta, err := s.DescribeContinuousBackups("test-table")
		require.NoError(t, err)
		assert.Nil(t, meta.PITR)
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.DescribeContinuousBackups("no-such-table")
		assert.ErrorIs(t, err, ErrTableNotFound)
	})
}

func TestUpdateContinuousBackups(t *testing.T) {
	t.Run("enables PITR and records EnabledAt", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		meta, err := s.UpdateContinuousBackups("test-table", true)
		require.NoError(t, err)
		require.NotNil(t, meta.PITR)
		assert.True(t, meta.PITR.Enabled)
		assert.NotNil(t, meta.PITR.EnabledAt)
	})

	t.Run("disables PITR", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, err := s.UpdateContinuousBackups("test-table", true)
		require.NoError(t, err)
		meta, err := s.UpdateContinuousBackups("test-table", false)
		require.NoError(t, err)
		require.NotNil(t, meta.PITR)
		assert.False(t, meta.PITR.Enabled)
	})

	t.Run("persists state across reads", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, err := s.UpdateContinuousBackups("test-table", true)
		require.NoError(t, err)
		meta, err := s.DescribeContinuousBackups("test-table")
		require.NoError(t, err)
		require.NotNil(t, meta.PITR)
		assert.True(t, meta.PITR.Enabled)
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.UpdateContinuousBackups("no-such-table", true)
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns error when readTableMeta fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("read failure") }
		_, err := s.UpdateContinuousBackups("test-table", true)
		assert.Error(t, err)
	})

	t.Run("returns error when writeTableMeta fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
			return nil, errors.New("write failure")
		}
		_, err := s.UpdateContinuousBackups("test-table", true)
		assert.Error(t, err)
	})
}

func TestDescribeKinesisStreamingDestination(t *testing.T) {
	t.Run("returns empty slice by default", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		dests, err := s.DescribeKinesisStreamingDestination("test-table")
		require.NoError(t, err)
		assert.Empty(t, dests)
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.DescribeKinesisStreamingDestination("no-such-table")
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns error when readTableMeta fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("read failure") }
		_, err := s.DescribeKinesisStreamingDestination("test-table")
		assert.Error(t, err)
	})
}

func TestEnableKinesisStreamingDestination(t *testing.T) {
	const streamARN = "arn:aws:kinesis:us-east-1:000000000000:stream/my-stream"

	t.Run("enables destination with ACTIVE status", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		dest, _, err := s.EnableKinesisStreamingDestination("test-table", streamARN, "MILLISECOND")
		require.NoError(t, err)
		assert.Equal(t, streamARN, dest.StreamARN)
		assert.Equal(t, "ACTIVE", dest.Status)
		assert.Equal(t, "MILLISECOND", dest.Precision)
	})

	t.Run("stores MILLISECOND precision", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		dest, _, err := s.EnableKinesisStreamingDestination("test-table", streamARN, "MILLISECOND")
		require.NoError(t, err)
		assert.Equal(t, "MILLISECOND", dest.Precision)
	})

	t.Run(
		"re-enabling ACTIVE destination returns wasActive=true and updates precision",
		func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateTable(testMeta))
			_, _, err := s.EnableKinesisStreamingDestination("test-table", streamARN, "MILLISECOND")
			require.NoError(t, err)
			dest, wasActive, err := s.EnableKinesisStreamingDestination(
				"test-table",
				streamARN,
				"MICROSECOND",
			)
			require.NoError(t, err)
			assert.True(t, wasActive)
			assert.Equal(t, "ACTIVE", dest.Status)
			assert.Equal(t, "MICROSECOND", dest.Precision)
		},
	)

	t.Run(
		"re-enabling DISABLED destination returns wasActive=false",
		func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateTable(testMeta))
			_, _, err := s.EnableKinesisStreamingDestination("test-table", streamARN, "MILLISECOND")
			require.NoError(t, err)
			_, err = s.DisableKinesisStreamingDestination("test-table", streamARN)
			require.NoError(t, err)
			_, wasActive, err := s.EnableKinesisStreamingDestination(
				"test-table",
				streamARN,
				"MILLISECOND",
			)
			require.NoError(t, err)
			assert.False(t, wasActive)
		},
	)

	t.Run("new destination returns wasActive=false", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, wasActive, err := s.EnableKinesisStreamingDestination(
			"test-table",
			streamARN,
			"MILLISECOND",
		)
		require.NoError(t, err)
		assert.False(t, wasActive)
	})

	t.Run("returns ErrKinesisLimitExceeded beyond 2 destinations", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, _, err := s.EnableKinesisStreamingDestination(
			"test-table",
			"arn:aws:kinesis:us-east-1:000000000000:stream/s1",
			"MILLISECOND",
		)
		require.NoError(t, err)
		_, _, err = s.EnableKinesisStreamingDestination(
			"test-table",
			"arn:aws:kinesis:us-east-1:000000000000:stream/s2",
			"MILLISECOND",
		)
		require.NoError(t, err)
		_, _, err = s.EnableKinesisStreamingDestination(
			"test-table",
			"arn:aws:kinesis:us-east-1:000000000000:stream/s3",
			"MILLISECOND",
		)
		assert.ErrorIs(t, err, ErrKinesisLimitExceeded)
	})

	t.Run("persists destination across reads", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, _, err := s.EnableKinesisStreamingDestination("test-table", streamARN, "MILLISECOND")
		require.NoError(t, err)
		dests, err := s.DescribeKinesisStreamingDestination("test-table")
		require.NoError(t, err)
		require.Len(t, dests, 1)
		assert.Equal(t, streamARN, dests[0].StreamARN)
		assert.Equal(t, "ACTIVE", dests[0].Status)
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		_, _, err := s.EnableKinesisStreamingDestination("no-such-table", streamARN, "MILLISECOND")
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns error when readTableMeta fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("read failure") }
		_, _, err := s.EnableKinesisStreamingDestination("test-table", streamARN, "MILLISECOND")
		assert.Error(t, err)
	})

	t.Run("returns error when writeTableMeta fails on new destination", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
			return nil, errors.New("write failure")
		}
		_, _, err := s.EnableKinesisStreamingDestination("test-table", streamARN, "MILLISECOND")
		assert.Error(t, err)
	})

	t.Run("returns error when writeTableMeta fails on re-enable", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, _, err := s.EnableKinesisStreamingDestination("test-table", streamARN, "MILLISECOND")
		require.NoError(t, err)
		s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
			return nil, errors.New("write failure")
		}
		_, _, err = s.EnableKinesisStreamingDestination("test-table", streamARN, "MICROSECOND")
		assert.Error(t, err)
	})
}

func TestDisableKinesisStreamingDestination(t *testing.T) {
	const streamARN = "arn:aws:kinesis:us-east-1:000000000000:stream/my-stream"

	t.Run("sets status to DISABLED", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, _, err := s.EnableKinesisStreamingDestination("test-table", streamARN, "MILLISECOND")
		require.NoError(t, err)
		dest, err := s.DisableKinesisStreamingDestination("test-table", streamARN)
		require.NoError(t, err)
		assert.Equal(t, "DISABLED", dest.Status)
	})

	t.Run("persists DISABLED status", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, _, err := s.EnableKinesisStreamingDestination("test-table", streamARN, "MILLISECOND")
		require.NoError(t, err)
		_, err = s.DisableKinesisStreamingDestination("test-table", streamARN)
		require.NoError(t, err)
		dests, err := s.DescribeKinesisStreamingDestination("test-table")
		require.NoError(t, err)
		require.Len(t, dests, 1)
		assert.Equal(t, "DISABLED", dests[0].Status)
	})

	t.Run("returns ErrKinesisDestinationNotFound for unknown stream", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, err := s.DisableKinesisStreamingDestination("test-table", streamARN)
		assert.ErrorIs(t, err, ErrKinesisDestinationNotFound)
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.DisableKinesisStreamingDestination("no-such-table", streamARN)
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns error when readTableMeta fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("read failure") }
		_, err := s.DisableKinesisStreamingDestination("test-table", streamARN)
		assert.Error(t, err)
	})

	t.Run("returns error when writeTableMeta fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, _, err := s.EnableKinesisStreamingDestination("test-table", streamARN, "MILLISECOND")
		require.NoError(t, err)
		s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
			return nil, errors.New("write failure")
		}
		_, err = s.DisableKinesisStreamingDestination("test-table", streamARN)
		assert.Error(t, err)
	})
}
