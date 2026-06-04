package dynamodb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// applyNestedSet writes val at the document path described by segs within item.
// segs must contain ≥2 resolved segments (no #nameRef placeholders); segs[0].attr must be non-empty.
// Returns a ValidationException-compatible error when an intermediate map parent is missing.
func applyNestedSet(item map[string]any, segs []projSegment, val any) error {
	parent, ok := item[segs[0].attr]
	if !ok {
		return fmt.Errorf(
			"the document path provided in the update expression is invalid for update",
		)
	}
	return setAtDynamoValue(parent, segs[1:], val)
}

// setAtDynamoValue navigates into a DynamoDB typed value following segs and sets the leaf.
// Maps and slices are modified in place via Go reference semantics.
func setAtDynamoValue(dynVal any, segs []projSegment, val any) error {
	seg := segs[0]
	rest := segs[1:]
	isLeaf := len(rest) == 0
	// All kumolo-stored DynamoDB values are map[string]any; the assertion always holds.
	m := dynVal.(map[string]any)
	if seg.attr != "" {
		mRaw, ok := m["M"]
		if !ok {
			return fmt.Errorf(
				"the document path provided in the update expression is invalid for update",
			)
		}
		// kumolo always writes M as map[string]any; the assertion always holds.
		mMap := mRaw.(map[string]any)
		if isLeaf {
			mMap[seg.attr] = val
			return nil
		}
		child, childOk := mMap[seg.attr]
		if !childOk {
			return fmt.Errorf(
				"the document path provided in the update expression is invalid for update",
			)
		}
		return setAtDynamoValue(child, rest, val)
	}
	// List index step.
	lRaw, ok := m["L"]
	if !ok {
		return fmt.Errorf(
			"the document path provided in the update expression is invalid for update",
		)
	}
	// kumolo always writes L as []any; the assertion always holds.
	lSlice := lRaw.([]any)
	if isLeaf {
		if seg.index < len(lSlice) {
			lSlice[seg.index] = val
		} else {
			// AWS appends when index is beyond the current end.
			m["L"] = append(lSlice, val)
		}
		return nil
	}
	if seg.index >= len(lSlice) {
		return fmt.Errorf(
			"the document path provided in the update expression is invalid for update",
		)
	}
	return setAtDynamoValue(lSlice[seg.index], rest, val)
}

// applyNestedRemove removes the attribute at the document path described by segs within item.
// segs must contain ≥2 resolved segments; segs[0].attr must be non-empty.
// Missing intermediate nodes are treated as a no-op (AWS: "If the attributes don't exist, nothing happens").
func applyNestedRemove(item map[string]any, segs []projSegment) error {
	parent, ok := item[segs[0].attr]
	if !ok {
		return nil // no-op: missing top-level attribute
	}
	return removeAtDynamoValue(parent, segs[1:])
}

// removeAtDynamoValue navigates into a DynamoDB typed value following segs and removes the leaf.
func removeAtDynamoValue(dynVal any, segs []projSegment) error {
	seg := segs[0]
	rest := segs[1:]
	isLeaf := len(rest) == 0
	// All kumolo-stored DynamoDB values are map[string]any; the assertion always holds.
	m := dynVal.(map[string]any)
	if seg.attr != "" {
		mRaw, ok := m["M"]
		if !ok {
			return nil
		}
		// kumolo always writes M as map[string]any; the assertion always holds.
		mMap := mRaw.(map[string]any)
		if isLeaf {
			delete(mMap, seg.attr)
			return nil
		}
		child, ok := mMap[seg.attr]
		if !ok {
			return nil
		}
		return removeAtDynamoValue(child, rest)
	}
	// List index step: remove element and shift remaining elements.
	lRaw, ok := m["L"]
	if !ok {
		return nil
	}
	// kumolo always writes L as []any; the assertion always holds.
	lSlice := lRaw.([]any)
	if seg.index >= len(lSlice) {
		return nil
	}
	if isLeaf {
		newSlice := make([]any, 0, len(lSlice)-1)
		newSlice = append(newSlice, lSlice[:seg.index]...)
		newSlice = append(newSlice, lSlice[seg.index+1:]...)
		m["L"] = newSlice
		return nil
	}
	return removeAtDynamoValue(lSlice[seg.index], rest)
}

// isTTLExpired reports whether item's TTL attribute has a past Unix-second timestamp; non-numeric/missing attrs return false.
func isTTLExpired(item map[string]any, ttl *TTLSpec) bool {
	if ttl == nil || !ttl.Enabled || ttl.AttributeName == "" {
		return false
	}
	attrVal, ok := item[ttl.AttributeName]
	if !ok {
		return false
	}
	nMap, ok := attrVal.(map[string]any)
	if !ok {
		return false
	}
	nStr, ok := nMap["N"].(string)
	if !ok {
		return false
	}
	expiry, err := strconv.ParseFloat(nStr, 64)
	if err != nil {
		return false
	}
	return float64(time.Now().Unix()) >= expiry
}

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
	if err := s.writeJSON(path, item); err != nil {
		return nil, err
	}
	if meta.StreamSpec != nil && meta.StreamSpec.StreamEnabled {
		eventName := "INSERT"
		if old != nil {
			eventName = "MODIFY"
		}
		keys := extractKeys(item, meta.KeySchema)
		s.emitStreamRecord(tableName, eventName, meta.StreamSpec.StreamViewType, keys, old, item)
	}
	return old, nil
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
	if isTTLExpired(item, meta.TTL) {
		return nil, nil
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
	if old != nil && meta.StreamSpec != nil && meta.StreamSpec.StreamEnabled {
		keys := extractKeys(old, meta.KeySchema)
		s.emitStreamRecord(tableName, "REMOVE", meta.StreamSpec.StreamViewType, keys, old, nil)
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
		case ifNotExistsOp:
			item[attr] = op.resolve(item)
		case listAppendOp:
			result, err := applyListAppendOp(item, op)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %v", ErrValidationException, err)
			}
			item[attr] = result
		case nestedSetOp:
			var resolved any
			switch v := op.val.(type) {
			case ifNotExistsOp:
				resolved = v.resolve(item)
			case listAppendOp:
				var err error
				resolved, err = applyListAppendOp(item, v)
				if err != nil {
					return nil, nil, fmt.Errorf("%w: %v", ErrValidationException, err)
				}
			default:
				resolved = op.val
			}
			if err := applyNestedSet(item, op.segs, resolved); err != nil {
				return nil, nil, fmt.Errorf("%w: %v", ErrValidationException, err)
			}
		case nestedRemoveOp:
			if err := applyNestedRemove(item, op.segs); err != nil { // unreachable: applyNestedRemove always returns nil
				return nil, nil, fmt.Errorf("%w: %v", ErrValidationException, err)
			}
		default:
			item[attr] = val
		}
	}
	if err := s.writeJSON(itemPath, item); err != nil {
		return nil, nil, err
	}
	if meta.StreamSpec != nil && meta.StreamSpec.StreamEnabled {
		eventName := "INSERT"
		if before != nil {
			eventName = "MODIFY"
		}
		keys := extractKeys(item, meta.KeySchema)
		s.emitStreamRecord(tableName, eventName, meta.StreamSpec.StreamViewType, keys, before, item)
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
		if isTTLExpired(item, meta.TTL) {
			continue
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
	streamEnabled := meta.StreamSpec != nil && meta.StreamSpec.StreamEnabled
	for _, item := range puts {
		key, err := itemKey(item, meta.KeySchema)
		if err != nil {
			return err
		}
		path := filepath.Join(tableName, key+".json")
		var old map[string]any
		if streamEnabled {
			old, err = readJSON[map[string]any](s, path)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if err := s.writeJSON(path, item); err != nil {
			return err
		}
		if streamEnabled {
			eventName := "INSERT"
			if old != nil {
				eventName = "MODIFY"
			}
			keys := extractKeys(item, meta.KeySchema)
			s.emitStreamRecord(
				tableName,
				eventName,
				meta.StreamSpec.StreamViewType,
				keys,
				old,
				item,
			)
		}
	}
	for _, key := range deletes {
		k, err := itemKey(key, meta.KeySchema)
		if err != nil {
			return err
		}
		path := filepath.Join(tableName, k+".json")
		var old map[string]any
		if streamEnabled {
			old, err = readJSON[map[string]any](s, path)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if err := s.removeFile(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if streamEnabled && old != nil {
			keys := extractKeys(old, meta.KeySchema)
			s.emitStreamRecord(tableName, "REMOVE", meta.StreamSpec.StreamViewType, keys, old, nil)
		}
	}
	return nil
}
