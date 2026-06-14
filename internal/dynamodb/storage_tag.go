package dynamodb

import "strings"

// tableNameFromARN extracts the table name from a DynamoDB table ARN or index ARN.
// Both "…:table/<name>" and "…:table/<name>/index/<idx>" are accepted; the
// latter resolves to the parent table, matching real AWS behaviour.
func tableNameFromARN(arn string) (string, bool) {
	const prefix = "arn:aws:dynamodb:us-east-1:000000000000:table/"
	if !strings.HasPrefix(arn, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(arn, prefix)
	if idx := strings.Index(rest, "/index/"); idx >= 0 {
		rest = rest[:idx]
	}
	if rest == "" {
		return "", false
	}
	return rest, true
}

func (s *Storage) TagResource(resourceARN string, tags map[string]string) error {
	name, ok := tableNameFromARN(resourceARN)
	if !ok {
		return ErrTableNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.tableExistsLocked(name) {
		return ErrTableNotFound
	}
	meta, err := s.readTableMeta(name)
	if err != nil {
		return err
	}
	if meta.Tags == nil {
		meta.Tags = make(map[string]string, len(tags))
	}
	newCount := 0
	for k := range tags {
		if _, exists := meta.Tags[k]; !exists {
			newCount++
		}
	}
	if len(meta.Tags)+newCount > tagMaxCount {
		return ErrTagLimitExceeded
	}
	for k, v := range tags {
		meta.Tags[k] = v
	}
	return s.writeTableMeta(name, meta)
}

func (s *Storage) UntagResource(resourceARN string, tagKeys []string) error {
	name, ok := tableNameFromARN(resourceARN)
	if !ok {
		return ErrTableNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.tableExistsLocked(name) {
		return ErrTableNotFound
	}
	meta, err := s.readTableMeta(name)
	if err != nil {
		return err
	}
	for _, k := range tagKeys {
		delete(meta.Tags, k)
	}
	return s.writeTableMeta(name, meta)
}

func (s *Storage) ListTagsOfResource(resourceARN string) (map[string]string, error) {
	name, ok := tableNameFromARN(resourceARN)
	if !ok {
		return nil, ErrTableNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.tableExistsLocked(name) {
		return nil, ErrTableNotFound
	}
	meta, err := s.readTableMeta(name)
	if err != nil {
		return nil, err
	}
	if meta.Tags == nil {
		return map[string]string{}, nil
	}
	return meta.Tags, nil
}

func (s *Storage) UpdateTimeToLive(tableName string, spec TTLSpec) (TTLSpec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.tableExistsLocked(tableName) {
		return TTLSpec{}, ErrTableNotFound
	}
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		return TTLSpec{}, err
	}
	meta.TTL = &spec
	if err := s.writeTableMeta(tableName, meta); err != nil {
		return TTLSpec{}, err
	}
	return spec, nil
}

func (s *Storage) DescribeTimeToLive(tableName string) (string, *TTLSpec, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.tableExistsLocked(tableName) {
		return "", nil, ErrTableNotFound
	}
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		return "", nil, err
	}
	if meta.TTL == nil || !meta.TTL.Enabled {
		return "DISABLED", meta.TTL, nil
	}
	return "ENABLED", meta.TTL, nil
}
