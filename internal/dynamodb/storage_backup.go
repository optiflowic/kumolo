package dynamodb

import "time"

const maxKinesisDestinations = 2

func (s *Storage) DescribeContinuousBackups(tableName string) (TableMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.tableExistsLocked(tableName) {
		return TableMetadata{}, ErrTableNotFound
	}
	return s.readTableMeta(tableName)
}

func (s *Storage) UpdateContinuousBackups(tableName string, enabled bool) (TableMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.tableExistsLocked(tableName) {
		return TableMetadata{}, ErrTableNotFound
	}
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		return TableMetadata{}, err
	}
	if enabled {
		now := time.Now().UTC()
		meta.PITR = &PITRStatus{Enabled: true, EnabledAt: &now}
	} else {
		meta.PITR = &PITRStatus{Enabled: false}
	}
	if err := s.writeTableMeta(tableName, meta); err != nil {
		return TableMetadata{}, err
	}
	return meta, nil
}

func (s *Storage) DescribeKinesisStreamingDestination(
	tableName string,
) ([]KinesisDestination, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.tableExistsLocked(tableName) {
		return nil, ErrTableNotFound
	}
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		return nil, err
	}
	return meta.KinesisDestinations, nil
}

func (s *Storage) EnableKinesisStreamingDestination(
	tableName, streamARN, precision string,
) (KinesisDestination, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.tableExistsLocked(tableName) {
		return KinesisDestination{}, ErrTableNotFound
	}
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		return KinesisDestination{}, err
	}
	// If the stream ARN already exists, update precision and set ACTIVE.
	for i, d := range meta.KinesisDestinations {
		if d.StreamARN == streamARN {
			if precision != "" {
				meta.KinesisDestinations[i].Precision = precision
			}
			meta.KinesisDestinations[i].Status = "ACTIVE"
			if err := s.writeTableMeta(tableName, meta); err != nil {
				return KinesisDestination{}, err
			}
			return meta.KinesisDestinations[i], nil
		}
	}
	if len(meta.KinesisDestinations) >= maxKinesisDestinations {
		return KinesisDestination{}, ErrKinesisLimitExceeded
	}
	if precision == "" {
		precision = "MILLISECOND"
	}
	dest := KinesisDestination{StreamARN: streamARN, Status: "ACTIVE", Precision: precision}
	meta.KinesisDestinations = append(meta.KinesisDestinations, dest)
	if err := s.writeTableMeta(tableName, meta); err != nil {
		return KinesisDestination{}, err
	}
	return dest, nil
}

func (s *Storage) DisableKinesisStreamingDestination(
	tableName, streamARN string,
) (KinesisDestination, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.tableExistsLocked(tableName) {
		return KinesisDestination{}, ErrTableNotFound
	}
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		return KinesisDestination{}, err
	}
	for i, d := range meta.KinesisDestinations {
		if d.StreamARN == streamARN {
			meta.KinesisDestinations[i].Status = "DISABLED"
			if err := s.writeTableMeta(tableName, meta); err != nil {
				return KinesisDestination{}, err
			}
			return meta.KinesisDestinations[i], nil
		}
	}
	return KinesisDestination{}, ErrKinesisDestinationNotFound
}
