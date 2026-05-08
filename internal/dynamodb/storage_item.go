package dynamodb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func (s *Storage) PutItem(
	tableName string,
	item map[string]any,
	cond *ConditionCheck,
) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrTableNotFound
		}
		return nil, err
	}
	key, err := itemKey(item, meta.KeySchema)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(tableName, key+".json")
	old, err := readJSON[map[string]any](s, path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if cond != nil && cond.Expr != "" {
		current := old
		if current == nil {
			current = map[string]any{}
		}
		ok, err := evalFilterExpr(cond.Expr, current, cond.Names, cond.Values)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrValidationException, err)
		}
		if !ok {
			return nil, ErrConditionalCheckFailed
		}
	}
	return old, s.writeJSON(path, item)
}

func (s *Storage) GetItem(tableName string, key map[string]any) (map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrTableNotFound
		}
		return nil, err
	}
	k, err := itemKey(key, meta.KeySchema)
	if err != nil {
		return nil, err
	}
	item, err := readJSON[map[string]any](s, filepath.Join(tableName, k+".json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return item, nil
}

func (s *Storage) DeleteItem(
	tableName string,
	key map[string]any,
	cond *ConditionCheck,
) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrTableNotFound
		}
		return nil, err
	}
	k, err := itemKey(key, meta.KeySchema)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(tableName, k+".json")
	old, err := readJSON[map[string]any](s, path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if cond != nil && cond.Expr != "" {
		current := old
		if current == nil {
			current = map[string]any{}
		}
		ok, err := evalFilterExpr(cond.Expr, current, cond.Names, cond.Values)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrValidationException, err)
		}
		if !ok {
			return nil, ErrConditionalCheckFailed
		}
	}
	if err := s.removeFile(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return old, nil
}

// UpdateItem reads an existing item (or seeds one from the key), applies the
// provided attribute updates, and writes the result back.
// A nil value in updates means remove the attribute.
// Returns (before, after, error): before is the item state prior to update,
// after is the item state following the update.
func (s *Storage) UpdateItem(
	tableName string,
	key map[string]any,
	updates map[string]any,
	cond *ConditionCheck,
) (map[string]any, map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, ErrTableNotFound
		}
		return nil, nil, err
	}
	k, err := itemKey(key, meta.KeySchema)
	if err != nil {
		return nil, nil, err
	}
	itemPath := filepath.Join(tableName, k+".json")
	item, err := readJSON[map[string]any](s, itemPath)
	var before map[string]any
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
		item = make(map[string]any, len(key))
		for kk, v := range key {
			item[kk] = v
		}
		// before stays nil: item did not exist prior to this update
	} else {
		before = make(map[string]any, len(item))
		for k, v := range item {
			before[k] = v
		}
	}
	if cond != nil && cond.Expr != "" {
		condItem := before
		if condItem == nil {
			condItem = map[string]any{}
		}
		ok, err := evalFilterExpr(cond.Expr, condItem, cond.Names, cond.Values)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrValidationException, err)
		}
		if !ok {
			return nil, nil, ErrConditionalCheckFailed
		}
	}
	for attr, val := range updates {
		switch op := val.(type) {
		case nil:
			delete(item, attr)
		case addOp:
			result, err := applyAddOp(item[attr], op.val)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %v", ErrValidationException, err)
			}
			item[attr] = result
		case deleteOp:
			result, err := applyDeleteOp(item[attr], op.val)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %v", ErrValidationException, err)
			}
			if result == nil {
				delete(item, attr)
			} else {
				item[attr] = result
			}
		default:
			item[attr] = val
		}
	}
	if err := s.writeJSON(itemPath, item); err != nil {
		return nil, nil, err
	}
	return before, item, nil
}

// BatchGetItems retrieves items by their primary keys from tableName.
// Items not found are omitted from the result (matching DynamoDB behavior).
func (s *Storage) BatchGetItems(tableName string, keys []map[string]any) ([]map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrTableNotFound
		}
		return nil, err
	}
	var items []map[string]any
	for _, key := range keys {
		k, err := itemKey(key, meta.KeySchema)
		if err != nil {
			return nil, err
		}
		item, err := readJSON[map[string]any](s, filepath.Join(tableName, k+".json"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// BatchWriteItems applies puts and deletes to tableName under a single lock.
func (s *Storage) BatchWriteItems(
	tableName string,
	puts []map[string]any,
	deletes []map[string]any,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrTableNotFound
		}
		return err
	}
	for _, item := range puts {
		key, err := itemKey(item, meta.KeySchema)
		if err != nil {
			return err
		}
		if err := s.writeJSON(filepath.Join(tableName, key+".json"), item); err != nil {
			return err
		}
	}
	for _, key := range deletes {
		k, err := itemKey(key, meta.KeySchema)
		if err != nil {
			return err
		}
		if err := s.removeFile(filepath.Join(tableName, k+".json")); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
	}
	return nil
}
