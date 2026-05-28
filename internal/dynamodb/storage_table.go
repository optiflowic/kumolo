package dynamodb

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

func newStreamLabel() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000000000")
}

func (s *Storage) CreateTable(meta TableMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tableExistsLocked(meta.Name) {
		return ErrTableAlreadyExists
	}
	meta.Status = "ACTIVE"
	meta.CreatedAt = time.Now().UTC()
	if meta.StreamSpec != nil && meta.StreamSpec.StreamEnabled && meta.StreamLabel == "" {
		meta.StreamLabel = newStreamLabel()
	}
	if err := s.mkdirFn(meta.Name, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := s.writeTableMeta(meta.Name, meta); err != nil {
		if removeErr := s.removeFile(meta.Name); removeErr != nil {
			slog.Warn(
				"failed to clean up table dir after meta write failure",
				"table", meta.Name,
				"err", removeErr,
			)
		}
		return err
	}
	if meta.StreamSpec != nil && meta.StreamSpec.StreamEnabled {
		s.ensureStreamBuffer(meta.Name, meta.StreamLabel)
	}
	return nil
}

func (s *Storage) DeleteTable(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.tableExistsLocked(name) {
		return ErrTableNotFound
	}
	entries, err := s.readDir(name)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		if err := s.removeFile(filepath.Join(name, e.Name())); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := s.removeFile(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := s.removeFile(name + ".table.json"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s.deleteStreamBuffer(name)
	return nil
}

func (s *Storage) DescribeTable(name string) (TableMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.tableExistsLocked(name) {
		return TableMetadata{}, ErrTableNotFound
	}
	return s.readTableMeta(name)
}

func (s *Storage) ListTables() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := s.readDir(".")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := s.statFn(e.Name() + ".table.json"); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("stat table metadata %s: %w", e.Name()+".table.json", err)
		}
		names = append(names, e.Name())
	}
	return names, nil
}

// UpdateTableInput holds the optional fields that can be changed via UpdateTable.
type UpdateTableInput struct {
	BillingMode           string
	ProvisionedThroughput *ProvisionedThroughput
	AttributeDefinitions  []AttributeDefinition
	GSICreates            []GlobalSecondaryIndex
	GSIUpdates            map[string]*ProvisionedThroughput // indexName → new throughput
	GSIDeletes            []string                          // indexNames to remove
	StreamSpec            *StreamSpecification
}

func (s *Storage) UpdateTable(tableName string, in UpdateTableInput) (TableMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.tableExistsLocked(tableName) {
		return TableMetadata{}, ErrTableNotFound
	}
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		return TableMetadata{}, err
	}
	if in.BillingMode != "" && in.BillingMode != meta.BillingMode {
		meta.BillingMode = in.BillingMode
		now := time.Now().UTC()
		meta.BillingModeUpdatedAt = &now
	}
	if in.ProvisionedThroughput != nil {
		meta.ProvisionedThroughput = in.ProvisionedThroughput
	}
	// Merge new AttributeDefinitions (deduplicate by AttributeName)
	existing := make(map[string]struct{}, len(meta.AttributeDefinitions))
	for _, a := range meta.AttributeDefinitions {
		existing[a.AttributeName] = struct{}{}
	}
	for _, a := range in.AttributeDefinitions {
		if _, ok := existing[a.AttributeName]; !ok {
			meta.AttributeDefinitions = append(meta.AttributeDefinitions, a)
			existing[a.AttributeName] = struct{}{}
		}
	}
	// GSI deletes
	deleteSet := make(map[string]struct{}, len(in.GSIDeletes))
	for _, name := range in.GSIDeletes {
		deleteSet[name] = struct{}{}
	}
	// GSI updates and deletes applied to existing list
	filtered := meta.GlobalSecondaryIndexes[:0:0]
	for _, gsi := range meta.GlobalSecondaryIndexes {
		if _, del := deleteSet[gsi.IndexName]; del {
			continue
		}
		if pt, ok := in.GSIUpdates[gsi.IndexName]; ok {
			gsi.ProvisionedThroughput = pt
		}
		filtered = append(filtered, gsi)
	}
	// GSI creates
	filtered = append(filtered, in.GSICreates...)
	meta.GlobalSecondaryIndexes = filtered

	if in.StreamSpec != nil {
		wasEnabled := meta.StreamSpec != nil && meta.StreamSpec.StreamEnabled
		nowEnabled := in.StreamSpec.StreamEnabled
		if nowEnabled && !wasEnabled {
			// Enabling streaming: assign a new stream label.
			meta.StreamLabel = newStreamLabel()
		}
		meta.StreamSpec = in.StreamSpec
	}

	if err := s.writeTableMeta(tableName, meta); err != nil {
		return TableMetadata{}, err
	}
	if meta.StreamSpec != nil && meta.StreamSpec.StreamEnabled {
		s.ensureStreamBuffer(tableName, meta.StreamLabel)
	} else {
		s.deleteStreamBuffer(tableName)
	}
	return meta, nil
}
